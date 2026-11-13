package github_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

// fakeLocal serves checkouts by path and counts reads per path.
type fakeLocal struct {
	mu        sync.Mutex
	checkouts map[string]github.Local
	reads     map[string]int
}

func newFakeLocal(checkouts map[string]github.Local) *fakeLocal {
	return &fakeLocal{checkouts: checkouts, reads: map[string]int{}}
}

func (f *fakeLocal) read(_ context.Context, path string) (github.Local, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads[path]++
	l, ok := f.checkouts[path]
	if !ok {
		return github.Local{}, fmt.Errorf("reading %s: %w", path, github.ErrNotGit)
	}
	return l, nil
}

func (f *fakeLocal) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.reads {
		n += c
	}
	return n
}

func local(root string, remotes ...string) github.Local {
	l := github.Local{Root: root}
	for i := 0; i+1 < len(remotes); i += 2 {
		l.Remotes = append(l.Remotes, github.Remote{Name: remotes[i], URL: remotes[i+1]})
	}
	return l
}

var authStatus = func(host string) []string { return []string{"auth", "status", "--hostname", host} }

func TestResolverResolvesAPath(t *testing.T) {
	loc := newFakeLocal(map[string]github.Local{
		"/src/widgets": local("/src/widgets", "origin", "git@github.com:me/widgets.git", "upstream", "https://github.com/acme/widgets"),
		// A worktree reports its main checkout as Root.
		"/wt/widgets-feature": local("/src/widgets", "origin", "git@github.com:me/widgets.git"),
	})
	fake := githubtest.NewFake()
	r := github.NewResolver(github.NewClient(fake), loc.read)

	res, err := r.Resolve(context.Background(), "/src/widgets")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found() || res.Repo.String() != "github.com/me/widgets" || res.Root != "/src/widgets" {
		t.Fatalf("Resolve = %+v, want github.com/me/widgets at /src/widgets", res)
	}
	wt, err := r.Resolve(context.Background(), "/wt/widgets-feature")
	if err != nil {
		t.Fatal(err)
	}
	if wt.Repo != res.Repo || wt.Root != "/src/widgets" {
		t.Fatalf("worktree resolved to %+v, want the main repo's Project %+v", wt, res)
	}
	if n := len(fake.Calls()); n != 0 {
		t.Fatalf("github.com remotes made %d gh calls, want none", n)
	}
}

func TestResolverNotGitAndOtherForge(t *testing.T) {
	loc := newFakeLocal(map[string]github.Local{
		"/src/lab": local("/src/lab", "origin", "git@gitlab.com:acme/widgets.git"),
	})
	fake := githubtest.NewFake()
	r := github.NewResolver(github.NewClient(fake), loc.read)
	ctx := context.Background()

	res, err := r.Resolve(ctx, "/tmp/notes")
	if err != nil || res.Reason != github.ReasonNotGit {
		t.Fatalf("Resolve(non-git) = %+v, %v; want ReasonNotGit", res, err)
	}
	res, err = r.Resolve(ctx, "/src/lab")
	if err != nil || res.Reason != github.ReasonOtherForge || res.Host != "gitlab.com" {
		t.Fatalf("Resolve(gitlab) = %+v, %v; want ReasonOtherForge gitlab.com", res, err)
	}
	if n := len(fake.Calls()); n != 0 {
		t.Fatalf("a well-known other forge made %d gh calls, want none", n)
	}
}

func TestResolverAsksGhAboutGHEHosts(t *testing.T) {
	loc := newFakeLocal(map[string]github.Local{
		"/src/corp":   local("/src/corp", "origin", "https://ghe.example.com/corp/widgets.git"),
		"/src/corp2":  local("/src/corp2", "origin", "git@ghe.example.com:corp/gadgets.git"),
		"/src/random": local("/src/random", "origin", "https://git.example.invalid/acme/widgets.git"),
	})
	fake := githubtest.NewFake().
		Serve(githubtest.MustLoad(t, githubtest.AuthStatusOffline), authStatus("ghe.example.com")...).
		Serve(githubtest.MustLoad(t, githubtest.AuthStatusNoHost), authStatus("git.example.invalid")...)
	r := github.NewResolver(github.NewClient(fake), loc.read)
	ctx := context.Background()

	for _, tt := range []struct{ path, want string }{
		{"/src/corp", "ghe.example.com/corp/widgets"},
		{"/src/corp2", "ghe.example.com/corp/gadgets"},
	} {
		res, err := r.Resolve(ctx, tt.path)
		if err != nil || !res.Found() || res.Repo.String() != tt.want {
			t.Fatalf("Resolve(%s) = %+v, %v; want %s (gh has a login for the host)", tt.path, res, err, tt.want)
		}
	}
	res, err := r.Resolve(ctx, "/src/random")
	if err != nil || res.Reason != github.ReasonUnknownHost || res.Host != "git.example.invalid" {
		t.Fatalf("Resolve(unknown host) = %+v, %v; want ReasonUnknownHost", res, err)
	}
	if n := len(fake.Calls()); n != 2 {
		t.Fatalf("gh calls = %d (%v), want one auth status per host", n, fake.Calls())
	}
}

func TestResolverGhMissingIsAnErrorNotAReason(t *testing.T) {
	loc := newFakeLocal(map[string]github.Local{
		"/src/corp": local("/src/corp", "origin", "https://ghe.example.com/corp/widgets.git"),
	})
	fake := githubtest.NewFake().ServeFunc(githubtest.MustLoad(t, githubtest.GhMissing), func([]string) bool { return true })
	r := github.NewResolver(github.NewClient(fake), loc.read)
	_, err := r.Resolve(context.Background(), "/src/corp")
	if github.ClassOf(err) != github.ClassGhMissing {
		t.Fatalf("err = %v, want gh missing", err)
	}
	if _, ok := r.Cached("/src/corp"); ok {
		t.Fatal("a failed resolution was cached")
	}
}

func TestResolverIsLazyAndCached(t *testing.T) {
	loc := newFakeLocal(map[string]github.Local{
		"/src/widgets": local("/src/widgets", "origin", "https://github.com/acme/widgets"),
		"/src/corp":    local("/src/corp", "origin", "https://ghe.example.com/corp/widgets.git"),
	})
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.AuthStatusOK), authStatus("ghe.example.com")...)
	r := github.NewResolver(github.NewClient(fake), loc.read)

	if _, ok := r.Cached("/src/widgets"); ok {
		t.Fatal("Cached reported a resolution before anything resolved")
	}
	if loc.total() != 0 || len(fake.Calls()) != 0 {
		t.Fatal("NewResolver or Cached did work: they must never run git or gh")
	}
	ctx := context.Background()
	for range 3 {
		for _, p := range []string{"/src/widgets", "/src/corp"} {
			if _, err := r.Resolve(ctx, p); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := loc.total(); got != 2 {
		t.Fatalf("git reads = %d, want 1 per path", got)
	}
	if got := len(fake.Calls()); got != 1 {
		t.Fatalf("gh calls = %d, want 1 (auth status, then cached)", got)
	}
	res, ok := r.Cached("/src/widgets")
	if !ok || res.Repo.String() != "github.com/acme/widgets" {
		t.Fatalf("Cached = %+v, %v", res, ok)
	}

	r.Invalidate("/src/widgets")
	if _, ok := r.Cached("/src/widgets"); ok {
		t.Fatal("Invalidate left the path cached")
	}
	if _, err := r.Resolve(ctx, "/src/widgets"); err != nil {
		t.Fatal(err)
	}
	if got := loc.total(); got != 3 {
		t.Fatalf("git reads after Invalidate = %d, want 3", got)
	}
}

// TestResolverInvalidateHosts: a GHE host gh did not know stays "unknown"
// until InvalidateHosts, which makes the next Resolve ask gh again (after
// `gh auth login`); paths stay cached until invalidated.
func TestResolverInvalidateHosts(t *testing.T) {
	loc := newFakeLocal(map[string]github.Local{
		"/src/corp": local("/src/corp", "origin", "https://ghe.example.com/corp/widgets.git"),
	})
	loggedIn := false
	fake := githubtest.NewFake().
		ServeFunc(githubtest.MustLoad(t, githubtest.AuthStatusNoHost), func(args []string) bool {
			return !loggedIn && fmt.Sprint(args) == fmt.Sprint(authStatus("ghe.example.com"))
		}).
		ServeFunc(githubtest.MustLoad(t, githubtest.AuthStatusOK), func(args []string) bool {
			return loggedIn && fmt.Sprint(args) == fmt.Sprint(authStatus("ghe.example.com"))
		})
	r := github.NewResolver(github.NewClient(fake), loc.read)
	ctx := context.Background()

	res, err := r.Resolve(ctx, "/src/corp")
	if err != nil || res.Reason != github.ReasonUnknownHost {
		t.Fatalf("Resolve before login = %+v, %v; want ReasonUnknownHost", res, err)
	}
	loggedIn = true
	r.Invalidate("/src/corp")
	if res, _ := r.Resolve(ctx, "/src/corp"); res.Found() {
		t.Fatal("Invalidate alone forgot the host answer")
	}
	r.InvalidateHosts()
	if _, ok := r.Cached("/src/corp"); !ok {
		t.Fatal("InvalidateHosts forgot a path")
	}
	r.Invalidate("/src/corp")
	res, err = r.Resolve(ctx, "/src/corp")
	if err != nil || !res.Found() || res.Repo.String() != "ghe.example.com/corp/widgets" {
		t.Fatalf("Resolve after login = %+v, %v; want ghe.example.com/corp/widgets", res, err)
	}
}

func TestResolveGroup(t *testing.T) {
	loc := newFakeLocal(map[string]github.Local{
		"/src/widgets":     local("/src/widgets", "origin", "git@github.com:me/widgets.git"),
		"/wt/widgets-a":    local("/src/widgets", "origin", "git@github.com:me/widgets.git"),
		"/src/gadgets":     local("/src/gadgets", "origin", "https://github.com/acme/gadgets"),
		"/src/lab":         local("/src/lab", "origin", "git@gitlab.com:acme/lab.git"),
		"/src/no-remote":   local("/src/no-remote"),
		"/src/upstream":    local("/src/upstream", "upstream", "https://github.com/acme/widgets"),
		"/src/parent-only": local("/src/parent-only", "origin", "https://github.com/acme/parent"),
	})
	tests := []struct {
		name         string
		defaultPath  string
		sessionPaths []string
		want         []string // HOST/OWNER/REPO in order
		reason       github.Reason
	}{
		{"DefaultPath on GitHub is the sole Project", "/src/parent-only", []string{"/src/widgets", "/src/gadgets"}, []string{"github.com/acme/parent"}, ""},
		{"DefaultPath not GitHub: session repos", "/src/lab", []string{"/src/widgets", "/src/gadgets"}, []string{"github.com/me/widgets", "github.com/acme/gadgets"}, ""},
		{"DefaultPath not git: session repos", "/tmp/notes", []string{"/src/gadgets"}, []string{"github.com/acme/gadgets"}, ""},
		{"worktrees and repeats collapse to one Project", "", []string{"/src/widgets", "/wt/widgets-a", "/src/widgets"}, []string{"github.com/me/widgets"}, ""},
		{"sessions skip paths with no Project", "", []string{"/tmp/notes", "/src/lab", "/src/gadgets"}, []string{"github.com/acme/gadgets"}, ""},
		{"no paths", "", nil, nil, github.ReasonNoPath},
		{"empty paths are no paths", "", []string{""}, nil, github.ReasonNoPath},
		{"not git everywhere", "/tmp/a", []string{"/tmp/b"}, nil, github.ReasonNotGit},
		{"the most telling reason wins", "/tmp/a", []string{"/src/no-remote", "/src/lab", "/src/upstream"}, nil, github.ReasonOtherForge},
		{"upstream-only repo", "", []string{"/src/upstream"}, nil, github.ReasonNoGitHubRemote},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := github.NewResolver(github.NewClient(githubtest.NewFake()), loc.read)
			g, err := r.ResolveGroup(context.Background(), tt.defaultPath, tt.sessionPaths)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, p := range g.Projects {
				got = append(got, p.Repo.String())
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("Projects = %v, want %v", got, tt.want)
			}
			if len(tt.want) == 0 && (g.Empty.Reason != tt.reason || g.Empty.Message() == "") {
				t.Fatalf("Empty = %+v, want reason %s with a message", g.Empty, tt.reason)
			}
			if len(tt.want) > 0 && g.Empty.Reason != "" {
				t.Fatalf("Empty = %+v with Projects present", g.Empty)
			}
		})
	}
}

func TestResolveGroupProjectRoot(t *testing.T) {
	loc := newFakeLocal(map[string]github.Local{
		"/wt/widgets-a": local("/src/widgets", "origin", "git@github.com:me/widgets.git"),
	})
	r := github.NewResolver(github.NewClient(githubtest.NewFake()), loc.read)
	g, err := r.ResolveGroup(context.Background(), "", []string{"/wt/widgets-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Projects) != 1 || g.Projects[0].Root != "/src/widgets" {
		t.Fatalf("Projects = %+v, want the main checkout as Root", g.Projects)
	}
}

func TestParentRepository(t *testing.T) {
	fork := github.Repo{Host: "github.com", Owner: "me", Name: "widgets"}
	source := github.Repo{Host: "github.com", Owner: "acme", Name: "widgets"}
	gone := github.Repo{Host: "github.com", Owner: "acme", Name: "nope"}
	repoCall := func(host, owner, name string) []string {
		return []string{"api", "--hostname", host, "repos/" + owner + "/" + name}
	}
	fake := githubtest.NewFake().
		Serve(githubtest.MustLoad(t, githubtest.RepoFork), repoCall("github.com", "me", "widgets")...).
		Serve(githubtest.MustLoad(t, githubtest.RepoSource), repoCall("github.com", "acme", "widgets")...).
		Serve(githubtest.MustLoad(t, githubtest.RepoNotFound), repoCall("github.com", "acme", "nope")...)
	r := github.NewResolver(github.NewClient(fake), newFakeLocal(nil).read)
	ctx := context.Background()

	parent, ok, err := r.Parent(ctx, fork)
	if err != nil || !ok || parent != source {
		t.Fatalf("Parent(fork) = %+v, %v, %v; want %+v", parent, ok, err, source)
	}
	if _, ok, err := r.Parent(ctx, source); err != nil || ok {
		t.Fatalf("Parent(not a fork) = %v, %v; want no parent", ok, err)
	}
	_, _, err = r.Parent(ctx, gone)
	if github.ClassOf(err) != github.ClassNotFound {
		t.Fatalf("Parent(missing) err = %v, want not found", err)
	}
	empty, ok := github.NotFound(gone, err)
	if !ok || empty.Reason != github.ReasonNotFound || empty.Message() != "repository acme/nope not found" {
		t.Fatalf("NotFound = %+v, %v", empty, ok)
	}
	if _, ok := github.NotFound(gone, errors.New("other")); ok {
		t.Fatal("NotFound accepted an error that is not ClassNotFound")
	}

	before := len(fake.Calls())
	if _, _, err := r.Parent(ctx, fork); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Parent(ctx, source); err != nil {
		t.Fatal(err)
	}
	if got := len(fake.Calls()); got != before {
		t.Fatalf("Parent re-asked gh for a cached repo (%d calls, want %d)", got, before)
	}
}

func TestParentRepositoryOnGHE(t *testing.T) {
	fork := github.Repo{Host: "ghe.example.com", Owner: "me", Name: "widgets"}
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.RepoFork),
		"api", "--hostname", "ghe.example.com", "repos/me/widgets")
	r := github.NewResolver(github.NewClient(fake), newFakeLocal(nil).read)
	parent, ok, err := r.Parent(context.Background(), fork)
	if err != nil || !ok || parent.String() != "ghe.example.com/acme/widgets" {
		t.Fatalf("Parent = %+v, %v, %v; want the parent on the same host", parent, ok, err)
	}
}
