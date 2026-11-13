package gitstate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// git runs a git command in dir with a fixed identity and no user or system
// config, failing the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", name)
	git(t, dir, "commit", "-q", "-m", name)
}

// newRepo makes a repo on branch main with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, "base.txt")
	return dir
}

// newClonePair makes a bare origin and a clone of it tracking origin/main.
func newClonePair(t *testing.T) (origin, clone string) {
	t.Helper()
	seed := newRepo(t)
	origin = filepath.Join(t.TempDir(), "origin.git")
	git(t, seed, "clone", "-q", "--bare", seed, origin)
	clone = filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	return origin, clone
}

func fetch(t *testing.T, path string) State {
	t.Helper()
	return Fetch(context.Background(), path)
}

func TestFetch_RealRepos(t *testing.T) {
	t.Run("in sync with upstream, clean", func(t *testing.T) {
		_, clone := newClonePair(t)
		st := fetch(t, clone)
		if st.Err != nil || st.NotRepo || st.JJ {
			t.Fatalf("unexpected state %+v", st)
		}
		if st.Branch != "main" || st.Upstream != "origin/main" || !st.HasCounts || st.Ahead != 0 || st.Behind != 0 || st.Dirty {
			t.Fatalf("got %+v, want main tracking origin/main ↑0 ↓0 clean", st)
		}
		if st.Repo != clone {
			t.Fatalf("Repo = %q, want %q", st.Repo, clone)
		}
	})

	t.Run("ahead and behind", func(t *testing.T) {
		origin, clone := newClonePair(t)
		other := filepath.Join(t.TempDir(), "other")
		git(t, filepath.Dir(other), "clone", "-q", origin, other)
		commit(t, other, "theirs.txt")
		git(t, other, "push", "-q", "origin", "main")

		commit(t, clone, "mine1.txt")
		commit(t, clone, "mine2.txt")
		git(t, clone, "fetch", "-q")

		st := fetch(t, clone)
		if st.Ahead != 2 || st.Behind != 1 || !st.HasCounts {
			t.Fatalf("got ↑%d ↓%d (counts=%v), want ↑2 ↓1", st.Ahead, st.Behind, st.HasCounts)
		}
	})

	t.Run("untracked file counts as dirty", func(t *testing.T) {
		dir := newRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if st := fetch(t, dir); !st.Dirty {
			t.Fatalf("untracked file must count as dirty, got %+v", st)
		}
	})

	t.Run("modified tracked file is dirty", func(t *testing.T) {
		dir := newRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
		if st := fetch(t, dir); !st.Dirty {
			t.Fatalf("modified file must be dirty, got %+v", st)
		}
	})

	t.Run("no upstream", func(t *testing.T) {
		dir := newRepo(t)
		st := fetch(t, dir)
		if st.Err != nil || st.Upstream != "" || st.HasCounts || st.Dirty || st.Branch != "main" {
			t.Fatalf("got %+v, want main, no upstream, clean", st)
		}
	})

	t.Run("subdirectory resolves the repo top level", func(t *testing.T) {
		dir := newRepo(t)
		sub := filepath.Join(dir, "a", "b")
		if err := os.MkdirAll(sub, 0o750); err != nil {
			t.Fatal(err)
		}
		st := fetch(t, sub)
		if st.NotRepo || st.Repo != dir {
			t.Fatalf("got %+v, want Repo %q", st, dir)
		}
	})

	t.Run("linked worktree", func(t *testing.T) {
		dir := newRepo(t)
		wt := filepath.Join(t.TempDir(), "wt")
		git(t, dir, "worktree", "add", "-q", "-b", "feature/x", wt)
		st := fetch(t, wt)
		if st.Err != nil || st.Branch != "feature/x" || st.Repo != wt || st.Dirty {
			t.Fatalf("got %+v, want feature/x at %q, clean", st, wt)
		}
	})

	t.Run("detached head", func(t *testing.T) {
		dir := newRepo(t)
		git(t, dir, "checkout", "-q", "--detach")
		if st := fetch(t, dir); st.Branch != "(detached)" {
			t.Fatalf("Branch = %q, want (detached)", st.Branch)
		}
	})

	t.Run("not a repo runs no git", func(t *testing.T) {
		st := fetch(t, t.TempDir())
		if !st.NotRepo || st.Err != nil {
			t.Fatalf("got %+v, want NotRepo", st)
		}
	})

	t.Run("jj workspace", func(t *testing.T) {
		dir := newRepo(t)
		if err := os.Mkdir(filepath.Join(dir, ".jj"), 0o750); err != nil {
			t.Fatal(err)
		}
		if st := fetch(t, dir); !st.JJ {
			t.Fatalf("a checkout with .jj must be a jj workspace, got %+v", st)
		}
	})

	t.Run("call error is unavailable", func(t *testing.T) {
		dir := newRepo(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if st := Fetch(ctx, dir); st.Err == nil {
			t.Fatalf("a cancelled call must report an error, got %+v", st)
		}
	})
}

func TestParseStatusV2(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want State
	}{
		{
			name: "upstream with counts and a change",
			in:   "# branch.oid abc\n# branch.head feature/x\n# branch.upstream origin/feature/x\n# branch.ab +3 -0\n1 .M N... 100644 100644 100644 a b f.go\n",
			want: State{Branch: "feature/x", Upstream: "origin/feature/x", HasCounts: true, Ahead: 3, Dirty: true},
		},
		{
			name: "upstream gone has no counts",
			in:   "# branch.oid abc\n# branch.head main\n# branch.upstream origin/main\n",
			want: State{Branch: "main", Upstream: "origin/main"},
		},
		{
			name: "untracked only",
			in:   "# branch.oid (initial)\n# branch.head main\n? new.txt\n",
			want: State{Branch: "main", Dirty: true},
		},
		{
			name: "clean, no upstream",
			in:   "# branch.oid abc\n# branch.head main\n",
			want: State{Branch: "main"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseStatusV2(tc.in); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCache(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	c := NewCache(10 * time.Second)

	if _, ok := c.Get("/p"); ok {
		t.Fatal("empty cache must miss")
	}
	if !c.Begin("/p", t0) {
		t.Fatal("first Begin must start a fetch")
	}
	if c.Begin("/p", t0.Add(time.Second)) {
		t.Fatal("Begin must not start a second fetch while one is in flight")
	}
	c.Put("/p", State{Branch: "main"}, t0.Add(2*time.Second))
	if st, ok := c.Get("/p"); !ok || st.Branch != "main" {
		t.Fatalf("Get after Put = %+v, %v", st, ok)
	}
	if c.Begin("/p", t0.Add(11*time.Second)) {
		t.Fatal("Begin within the TTL must not refetch")
	}
	if !c.Begin("/p", t0.Add(13*time.Second)) {
		t.Fatal("Begin after the TTL must refetch")
	}
	if st, ok := c.Get("/p"); !ok || st.Branch != "main" {
		t.Fatal("a refetch in flight keeps serving the old state")
	}

	c.Invalidate("/p")
	if _, ok := c.Get("/p"); ok {
		t.Fatal("Invalidate must drop the entry")
	}
	if !c.Begin("/p", t0.Add(14*time.Second)) {
		t.Fatal("Begin after Invalidate must start a fetch")
	}

	if !c.Begin("/q", t0) {
		t.Fatal("paths are keyed independently")
	}
	if !c.BeginForce("/q", t0) {
		t.Fatal("BeginForce starts a fetch even while one is in flight")
	}
}
