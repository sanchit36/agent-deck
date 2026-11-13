package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
)

// Local is what resolution needs from a checkout: the root of its main
// repository (a worktree's main checkout) and its remotes.
type Local struct {
	Root    string
	Remotes []Remote
}

// ErrNotGit is wrapped by a LocalReader error when the path is not inside a
// git repository (or does not exist).
var ErrNotGit = errors.New("not a git repository")

// LocalReader reads the checkout that holds path. ReadLocal is the real one;
// tests pass a fake.
type LocalReader func(ctx context.Context, path string) (Local, error)

// Project is a group's GitHub repository and the main checkout it was
// resolved from.
type Project struct {
	Repo Repo
	Root string
}

// Group is what a group resolves to: its Projects in order (first is the
// default), or, when there are none, the empty state to show.
type Group struct {
	Projects []Project
	Empty    Resolution
}

// Resolver turns paths into Projects (spec §2). It is lazy (nothing runs
// until Resolve, ResolveGroup or Parent is called) and caches per path,
// per host and per repository in memory. Call it from a tea.Cmd, never
// from View(); View() may use Cached, which never runs git or gh. Safe for
// concurrent use.
type Resolver struct {
	client *Client
	local  LocalReader

	mu      sync.Mutex
	paths   map[string]Resolution
	hosts   map[string]HostKind
	parents map[Repo]parentEntry
}

type parentEntry struct {
	parent Repo
	ok     bool
}

// NewResolver returns a Resolver that asks gh through client (nil = real
// gh) and reads checkouts with local (nil = ReadLocal).
func NewResolver(client *Client, local LocalReader) *Resolver {
	if client == nil {
		client = NewClient(nil)
	}
	if local == nil {
		local = ReadLocal
	}
	return &Resolver{
		client:  client,
		local:   local,
		paths:   map[string]Resolution{},
		hosts:   map[string]HostKind{},
		parents: map[Repo]parentEntry{},
	}
}

// Cached returns the cached resolution of path without running anything.
func (r *Resolver) Cached(path string) (Resolution, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.paths[cleanPath(path)]
	return res, ok
}

// Invalidate forgets path's resolution, so the next Resolve reads it again.
func (r *Resolver) Invalidate(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.paths, cleanPath(path))
}

// InvalidateHosts forgets which hosts gh has a login for, so the next
// Resolve of an uncached path asks `gh auth status` again (after a `gh auth
// login`). Path resolutions stay cached; Invalidate them to redo them.
func (r *Resolver) InvalidateHosts() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.hosts)
}

// Reset forgets everything: paths, host logins and parent repositories.
func (r *Resolver) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.paths)
	clear(r.hosts)
	clear(r.parents)
}

// Resolve returns path's Project, or the Reason it has none. The error is
// for a resolution that could not finish (gh missing, git failing, ctx
// cancelled); it is not cached.
func (r *Resolver) Resolve(ctx context.Context, path string) (Resolution, error) {
	path = cleanPath(path)
	if path == "" {
		return Resolution{Reason: ReasonNoPath}, nil
	}
	if res, ok := r.Cached(path); ok {
		return res, nil
	}
	loc, err := r.local(ctx, path)
	if errors.Is(err, ErrNotGit) {
		res := Resolution{Reason: ReasonNotGit}
		r.store(path, res)
		return res, nil
	}
	if err != nil {
		return Resolution{}, fmt.Errorf("resolving the GitHub project of %s: %w", path, err)
	}
	var kindErr error
	res := PickRemote(loc.Remotes, func(host string) HostKind {
		k, err := r.hostKind(ctx, host)
		if err != nil && kindErr == nil {
			kindErr = err
		}
		return k
	})
	if kindErr != nil {
		return Resolution{}, fmt.Errorf("resolving the GitHub project of %s: %w", path, kindErr)
	}
	res.Root = loc.Root
	r.store(path, res)
	return res, nil
}

// ResolveGroup resolves a group: a DefaultPath that resolves to a GitHub
// repository is the sole Project; otherwise the session paths (each
// session's ProjectPath, never AdditionalPaths, sessions directly in the
// group only) give the Projects, one per repository, in order. With none,
// Empty carries the most telling reason among the paths.
func (r *Resolver) ResolveGroup(ctx context.Context, defaultPath string, sessionPaths []string) (Group, error) {
	empty := Resolution{Reason: ReasonNoPath}
	consider := func(res Resolution) {
		if reasonRank(res.Reason) > reasonRank(empty.Reason) {
			empty = res
		}
	}
	if defaultPath != "" {
		res, err := r.Resolve(ctx, defaultPath)
		if err != nil {
			return Group{}, err
		}
		if res.Found() {
			return Group{Projects: []Project{{Repo: res.Repo, Root: res.Root}}}, nil
		}
		consider(res)
	}
	var g Group
	seen := map[Repo]bool{}
	for _, p := range sessionPaths {
		if p == "" {
			continue
		}
		res, err := r.Resolve(ctx, p)
		if err != nil {
			return Group{}, err
		}
		if !res.Found() {
			consider(res)
			continue
		}
		if !seen[res.Repo] {
			seen[res.Repo] = true
			g.Projects = append(g.Projects, Project{Repo: res.Repo, Root: res.Root})
		}
	}
	if len(g.Projects) == 0 {
		g.Empty = empty
	}
	return g, nil
}

// reasonRank orders reasons by how much they tell the user: a remote on the
// wrong host beats "no GitHub remote", which beats "not a git repository".
func reasonRank(r Reason) int {
	switch r {
	case ReasonOtherForge, ReasonUnknownHost, ReasonNotFound:
		return 3
	case ReasonNoGitHubRemote:
		return 2
	case ReasonNotGit:
		return 1
	}
	return 0
}

// Parent returns the Parent Repository of repo from GitHub's fork metadata
// (`gh api repos/OWNER/NAME`), never from a local remote. ok is false when
// repo is not a fork. A repository GitHub cannot find is a ClassNotFound
// error (see NotFound). Answers are cached per repository.
func (r *Resolver) Parent(ctx context.Context, repo Repo) (Repo, bool, error) {
	key := Repo{Host: repo.HostOrDefault(), Owner: repo.Owner, Name: repo.Name}
	r.mu.Lock()
	e, cached := r.parents[key]
	r.mu.Unlock()
	if cached {
		return e.parent, e.ok, nil
	}
	out, err := r.client.API(ctx, key.Host, Call{Args: []string{"repos/" + key.Owner + "/" + key.Name}})
	if err != nil {
		return Repo{}, false, fmt.Errorf("looking up the parent repository of %s: %w", key, err)
	}
	var body struct {
		Fork   bool `json:"fork"`
		Parent *struct {
			Name  string `json:"name"`
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"parent"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return Repo{}, false, fmt.Errorf("parsing the repository metadata of %s: %w", key, err)
	}
	if body.Fork && body.Parent != nil && body.Parent.Owner.Login != "" && body.Parent.Name != "" {
		e = parentEntry{parent: Repo{Host: key.Host, Owner: body.Parent.Owner.Login, Name: body.Parent.Name}, ok: true}
	}
	r.mu.Lock()
	r.parents[key] = e
	r.mu.Unlock()
	return e.parent, e.ok, nil
}

// NotFound turns a ClassNotFound error from a call on repo into the
// "repository not found" empty state.
func NotFound(repo Repo, err error) (Resolution, bool) {
	if ClassOf(err) != ClassNotFound {
		return Resolution{}, false
	}
	return Resolution{Repo: repo, Reason: ReasonNotFound}, true
}

// hostKind says whether host is GitHub: github.com is; a well-known other
// forge is not; any other host is GitHub when `gh auth status` has a login
// for it (spec §2: "any host gh knows"), even one whose check failed now.
// gh missing or ctx cancelled is an error; every other answer is cached.
func (r *Resolver) hostKind(ctx context.Context, host string) (HostKind, error) {
	switch {
	case host == DefaultHost:
		return HostGitHub, nil
	case IsOtherForge(host):
		return HostOtherForge, nil
	}
	r.mu.Lock()
	k, ok := r.hosts[host]
	r.mu.Unlock()
	if ok {
		return k, nil
	}
	err := r.client.AuthStatus(ctx, host)
	switch ClassOf(err) {
	case "":
		if err != nil {
			return HostUnknown, err
		}
		k = HostGitHub
	case ClassNotLoggedIn:
		k = HostUnknown
	case ClassGhMissing:
		return HostUnknown, err
	default:
		k = HostGitHub
	}
	r.mu.Lock()
	r.hosts[host] = k
	r.mu.Unlock()
	return k, nil
}

func (r *Resolver) store(path string, res Resolution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths[path] = res
}

func cleanPath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}
