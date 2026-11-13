package gitstate

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/childenv"
)

// PRQuery names a pull request's head and where to look for local
// checkouts of it (spec #18 §8).
type PRQuery struct {
	// Root is the Project's local repo; its worktrees and branches that
	// track the PR head are checkouts. Empty: only Linked are read.
	Root string
	// Linked are the checkout paths of the sessions linked to the PR. They
	// are listed first, in order.
	Linked []string
	Number int
	// Branch is the PR's head branch (headRefName).
	Branch string
	// SHA is the PR's head commit (headRefOid).
	SHA string
	// CrossRepo is true when the head lives in another repository (a
	// fork); HeadOwner is that repository's owner.
	CrossRepo bool
	HeadOwner string
}

// CompareKind is how a checkout's HEAD relates to the PR head.
type CompareKind int

const (
	// Unknown: the comparison failed (Checkout.Err says why).
	Unknown CompareKind = iota
	// InSync: the checkout is at the PR head.
	InSync
	// Unpushed: the checkout has Ahead commits the PR head lacks.
	Unpushed
	// Behind: the PR head has Behind commits the checkout lacks.
	Behind
	// Diverged: both sides have commits of their own.
	Diverged
	// Newer: the PR head commit is not in the local repo (never fetched).
	Newer
)

// Compare is a checkout's HEAD against the PR head.
type Compare struct {
	Kind   CompareKind
	Ahead  int
	Behind int
}

// Checkout is one local checkout of a PR.
type Checkout struct {
	// Path is the worktree; empty for a branch checked out nowhere.
	Path string
	// Branch is the checked-out (or tracking) branch, or "(detached)".
	Branch string
	// Linked marks a linked session's checkout.
	Linked  bool
	Compare Compare
	Dirty   bool
	// JJ marks a Jujutsu checkout: v1 shows it as unavailable.
	JJ bool
	// Err is set when the checkout could not be read.
	Err error
}

// PRLocal is the local state of a PR: its checkouts, linked sessions'
// first, then worktrees, then branches checked out nowhere.
type PRLocal struct {
	Checkouts []Checkout
	// Err is set when the Project's local repo could not be read.
	Err error
}

// Warn reports whether a checkout has work the PR head lacks: unpushed
// commits or uncommitted changes (the merge dialog's ⚠, spec #18 §14).
func (l PRLocal) Warn() bool {
	for _, c := range l.Checkouts {
		if c.Dirty || c.Compare.Kind == Unpushed || c.Compare.Kind == Diverged {
			return true
		}
	}
	return false
}

// gitCallHook, when set, sees every git argv ReadPRLocal runs (tests).
var gitCallHook func(args []string)

// runGit runs git with args under CallTimeout and returns its stdout.
func runGit(ctx context.Context, args ...string) (string, error) {
	if gitCallHook != nil {
		gitCallHook(args)
	}
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	// #nosec G204 -- fixed git subcommands; paths and revisions are passed as separate argv entries
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = childenv.WithoutClaudeJobDir()
	out, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", err
	}
	return string(out), nil
}

// ReadPRLocal finds q's checkouts and compares each with the PR head. It
// only reads the local repositories: it never fetches, so a head that was
// never fetched reads as Newer.
func ReadPRLocal(ctx context.Context, q PRQuery) PRLocal {
	var l PRLocal
	seen := map[string]bool{}
	for _, p := range q.Linked {
		key := canonicalPath(p)
		if p == "" || seen[key] {
			continue
		}
		seen[key] = true
		c := readWorktreeCheckout(ctx, p, q.SHA)
		c.Linked = true
		l.Checkouts = append(l.Checkouts, c)
	}
	if q.Root == "" {
		return l
	}
	found, err := discoverPRCheckouts(ctx, q)
	if err != nil {
		l.Err = err
		return l
	}
	for _, d := range found {
		if d.path != "" {
			key := canonicalPath(d.path)
			if seen[key] {
				continue
			}
			seen[key] = true
			l.Checkouts = append(l.Checkouts, readWorktreeCheckout(ctx, d.path, q.SHA))
			continue
		}
		c := Checkout{Branch: d.branch}
		c.Compare, c.Err = compareHead(ctx, q.Root, "refs/heads/"+d.branch, q.SHA)
		l.Checkouts = append(l.Checkouts, c)
	}
	return l
}

func canonicalPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// readWorktreeCheckout reads the checkout at path: its branch and dirt
// (one git status) and its HEAD against sha.
func readWorktreeCheckout(ctx context.Context, path, sha string) Checkout {
	c := Checkout{Path: path}
	st := Fetch(ctx, path)
	switch {
	case st.JJ:
		c.JJ = true
		return c
	case st.Err != nil:
		c.Err = st.Err
		return c
	case st.NotRepo:
		c.Err = fmt.Errorf("reading checkout %s: not a git repository", path)
		return c
	}
	c.Branch, c.Dirty = st.Branch, st.Dirty
	c.Compare, c.Err = compareHead(ctx, path, "HEAD", sha)
	return c
}

// compareHead compares rev with sha in the repository at dir: whether sha
// is there at all, then the commits on each side.
func compareHead(ctx context.Context, dir, rev, sha string) (Compare, error) {
	if sha == "" {
		return Compare{}, errors.New("comparing with the PR head: no head commit")
	}
	if _, err := runGit(ctx, "-C", dir, "rev-parse", "--verify", "--quiet", sha+"^{commit}"); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return Compare{Kind: Newer}, nil
		}
		return Compare{}, fmt.Errorf("looking up PR head %s in %s: %w", sha, dir, err)
	}
	out, err := runGit(ctx, "-C", dir, "rev-list", "--left-right", "--count", rev+"..."+sha)
	if err != nil {
		return Compare{}, fmt.Errorf("counting commits between %s and the PR head in %s: %w", rev, dir, err)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return Compare{}, fmt.Errorf("counting commits between %s and the PR head in %s: unexpected output %q", rev, dir, out)
	}
	ahead, errA := strconv.Atoi(fields[0])
	behind, errB := strconv.Atoi(fields[1])
	if errA != nil || errB != nil {
		return Compare{}, fmt.Errorf("counting commits between %s and the PR head in %s: unexpected output %q", rev, dir, out)
	}
	c := Compare{Ahead: ahead, Behind: behind}
	switch {
	case ahead == 0 && behind == 0:
		c.Kind = InSync
	case behind == 0:
		c.Kind = Unpushed
	case ahead == 0:
		c.Kind = Behind
	default:
		c.Kind = Diverged
	}
	return c, nil
}

// discovered is a checkout found in the Project's local repo: a worktree
// (path set) or a branch checked out nowhere.
type discovered struct {
	path   string
	branch string
}

// discoverPRCheckouts lists the worktrees and branches of q.Root that
// track the PR head: worktrees first, in git's order, then branches.
func discoverPRCheckouts(ctx context.Context, q PRQuery) ([]discovered, error) {
	wtOut, err := runGit(ctx, "-C", q.Root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("listing worktrees of %s: %w", q.Root, err)
	}
	refOut, err := runGit(ctx, "-C", q.Root, "for-each-ref", "--format=%(refname)", "refs/heads")
	if err != nil {
		return nil, fmt.Errorf("listing branches of %s: %w", q.Root, err)
	}
	// Exit 1 means no matching key: no branch tracks anything.
	cfgOut, err := runGit(ctx, "-C", q.Root, "config", "--get-regexp", `^(branch\..*\.(remote|merge)|remote\..*\.url)$`)
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("reading branch config of %s: %w", q.Root, err)
		}
	}
	cfg := parseTrackingConfig(cfgOut)

	tracking := map[string]bool{}
	var branches []string
	for _, line := range strings.Split(refOut, "\n") {
		name, ok := strings.CutPrefix(strings.TrimSpace(line), "refs/heads/")
		if ok && cfg.tracks(name, q) {
			tracking[name] = true
			branches = append(branches, name)
		}
	}

	var out []discovered
	checkedOut := map[string]bool{}
	for _, wt := range parseWorktrees(wtOut) {
		if wt.branch != "" {
			checkedOut[wt.branch] = true
		}
		if (wt.branch != "" && tracking[wt.branch]) || (wt.detached && wt.head == q.SHA && q.SHA != "") {
			out = append(out, discovered{path: wt.path})
		}
	}
	for _, b := range branches {
		if !checkedOut[b] {
			out = append(out, discovered{branch: b})
		}
	}
	return out, nil
}

type worktree struct {
	path, head, branch string
	detached           bool
}

// parseWorktrees parses `git worktree list --porcelain`. Bare entries are
// left out.
func parseWorktrees(out string) []worktree {
	var list []worktree
	var cur *worktree
	bare := false
	flush := func() {
		if cur != nil && !bare {
			list = append(list, *cur)
		}
		cur, bare = nil, false
	}
	for _, line := range strings.Split(out, "\n") {
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			cur = &worktree{path: val}
		case "HEAD":
			if cur != nil {
				cur.head = val
			}
		case "branch":
			if cur != nil {
				cur.branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "detached":
			if cur != nil {
				cur.detached = true
			}
		case "bare":
			bare = true
		}
	}
	flush()
	return list
}

// trackingConfig is the branches' upstream config and the remotes' URLs.
type trackingConfig struct {
	remote map[string]string // branch -> remote name or URL
	merge  map[string]string // branch -> upstream ref on the remote
	url    map[string]string // remote name -> URL
}

func parseTrackingConfig(out string) trackingConfig {
	c := trackingConfig{remote: map[string]string{}, merge: map[string]string{}, url: map[string]string{}}
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(key, "branch.") && strings.HasSuffix(key, ".remote"):
			c.remote[strings.TrimSuffix(strings.TrimPrefix(key, "branch."), ".remote")] = val
		case strings.HasPrefix(key, "branch.") && strings.HasSuffix(key, ".merge"):
			c.merge[strings.TrimSuffix(strings.TrimPrefix(key, "branch."), ".merge")] = val
		case strings.HasPrefix(key, "remote.") && strings.HasSuffix(key, ".url"):
			c.url[strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")] = val
		}
	}
	return c
}

// tracks reports whether branch tracks the PR head: its upstream is the
// PR's pull ref, or the head branch on the head repository (any remote
// for a same-repo PR, a remote of the fork's owner for a cross-repo one),
// or, for a same-repo PR, it has no upstream and the head branch's name.
func (c trackingConfig) tracks(branch string, q PRQuery) bool {
	merge, hasMerge := c.merge[branch]
	if !hasMerge {
		return !q.CrossRepo && branch == q.Branch
	}
	if q.Number > 0 && merge == fmt.Sprintf("refs/pull/%d/head", q.Number) {
		return true
	}
	if merge != "refs/heads/"+q.Branch {
		return false
	}
	if !q.CrossRepo {
		return true
	}
	url := c.remote[branch]
	if u, ok := c.url[url]; ok {
		url = u
	}
	return q.HeadOwner != "" && strings.EqualFold(remoteOwner(url), q.HeadOwner)
}

// remoteOwner is the owner segment of a remote URL
// (git@host:owner/repo.git, https://host/owner/repo): the second-to-last
// path segment.
func remoteOwner(url string) string {
	s := strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	s = strings.ReplaceAll(s, ":", "/")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}
