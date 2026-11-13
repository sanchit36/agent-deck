package gitstate

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// prRepo is a bare origin, a clone of it (the Project's local repo) and a
// PR branch feature/42-x pushed to origin from the clone; head is that
// branch's tip.
type prRepo struct {
	origin, clone, head string
}

func newPRRepo(t *testing.T) prRepo {
	t.Helper()
	origin, clone := newClonePair(t)
	git(t, clone, "checkout", "-q", "-b", "feature/42-x")
	commit(t, clone, "pr1.txt")
	git(t, clone, "push", "-q", "-u", "origin", "feature/42-x")
	git(t, clone, "checkout", "-q", "main")
	return prRepo{origin: origin, clone: clone, head: git(t, clone, "rev-parse", "feature/42-x")}
}

// worktree checks the PR branch out in a new worktree of the clone.
func (r prRepo) worktree(t *testing.T) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, r.clone, "worktree", "add", "-q", wt, "feature/42-x")
	return realPath(t, wt)
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (r prRepo) query(head string, linked ...string) PRQuery {
	return PRQuery{Root: r.clone, Linked: linked, Number: 42, Branch: "feature/42-x", SHA: head}
}

func readPR(t *testing.T, q PRQuery) PRLocal {
	t.Helper()
	return ReadPRLocal(context.Background(), q)
}

// only returns the single checkout of l, failing otherwise.
func only(t *testing.T, l PRLocal) Checkout {
	t.Helper()
	if l.Err != nil || len(l.Checkouts) != 1 {
		t.Fatalf("want one checkout, got %+v", l)
	}
	return l.Checkouts[0]
}

func TestReadPRLocal_Comparisons(t *testing.T) {
	t.Run("in sync", func(t *testing.T) {
		r := newPRRepo(t)
		wt := r.worktree(t)
		c := only(t, readPR(t, r.query(r.head)))
		if c.Path != wt || c.Branch != "feature/42-x" || c.Compare != (Compare{Kind: InSync}) || c.Dirty || c.Err != nil {
			t.Fatalf("got %+v, want feature/42-x at %s in sync", c, wt)
		}
	})

	t.Run("unpushed commits", func(t *testing.T) {
		r := newPRRepo(t)
		wt := r.worktree(t)
		commit(t, wt, "local1.txt")
		commit(t, wt, "local2.txt")
		c := only(t, readPR(t, r.query(r.head)))
		if c.Compare != (Compare{Kind: Unpushed, Ahead: 2}) {
			t.Fatalf("got %+v, want ↑2 unpushed", c.Compare)
		}
	})

	t.Run("behind the PR head", func(t *testing.T) {
		r := newPRRepo(t)
		wt := r.worktree(t)
		commit(t, wt, "more1.txt")
		commit(t, wt, "more2.txt")
		head := git(t, wt, "rev-parse", "HEAD")
		git(t, wt, "reset", "-q", "--hard", "HEAD~2")
		c := only(t, readPR(t, r.query(head)))
		if c.Compare != (Compare{Kind: Behind, Behind: 2}) {
			t.Fatalf("got %+v, want ↓2 behind", c.Compare)
		}
	})

	t.Run("diverged", func(t *testing.T) {
		r := newPRRepo(t)
		wt := r.worktree(t)
		commit(t, wt, "theirs.txt")
		head := git(t, wt, "rev-parse", "HEAD")
		git(t, wt, "reset", "-q", "--hard", "HEAD~1")
		commit(t, wt, "mine.txt")
		c := only(t, readPR(t, r.query(head)))
		if c.Compare != (Compare{Kind: Diverged, Ahead: 1, Behind: 1}) {
			t.Fatalf("got %+v, want ↑1 ↓1", c.Compare)
		}
	})

	t.Run("PR head not in the local repo", func(t *testing.T) {
		r := newPRRepo(t)
		r.worktree(t)
		other := filepath.Join(t.TempDir(), "other")
		git(t, filepath.Dir(other), "clone", "-q", "-b", "feature/42-x", r.origin, other)
		commit(t, other, "pushed-elsewhere.txt")
		git(t, other, "push", "-q", "origin", "feature/42-x")
		head := git(t, other, "rev-parse", "HEAD")
		c := only(t, readPR(t, r.query(head)))
		if c.Compare != (Compare{Kind: Newer}) {
			t.Fatalf("got %+v, want PR has newer commits", c.Compare)
		}
	})

	t.Run("dirty worktree", func(t *testing.T) {
		r := newPRRepo(t)
		wt := r.worktree(t)
		if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if c := only(t, readPR(t, r.query(r.head))); !c.Dirty || c.Compare.Kind != InSync {
			t.Fatalf("got %+v, want dirty in sync", c)
		}
	})

	t.Run("no checkout", func(t *testing.T) {
		r := newPRRepo(t)
		git(t, r.clone, "branch", "-q", "-D", "feature/42-x")
		l := readPR(t, r.query(r.head))
		if l.Err != nil || len(l.Checkouts) != 0 {
			t.Fatalf("got %+v, want no checkouts", l)
		}
	})

	t.Run("branch without a worktree", func(t *testing.T) {
		r := newPRRepo(t)
		c := only(t, readPR(t, r.query(r.head)))
		if c.Path != "" || c.Branch != "feature/42-x" || c.Compare.Kind != InSync {
			t.Fatalf("got %+v, want the feature/42-x branch in sync, no path", c)
		}
	})

	t.Run("root is not a repository", func(t *testing.T) {
		l := ReadPRLocal(context.Background(), PRQuery{Root: t.TempDir(), Number: 42, Branch: "feature/42-x", SHA: "abc"})
		if l.Err == nil {
			t.Fatalf("got %+v, want an error (unavailable)", l)
		}
	})

	t.Run("jj checkout is unavailable", func(t *testing.T) {
		r := newPRRepo(t)
		wt := r.worktree(t)
		if err := os.Mkdir(filepath.Join(wt, ".jj"), 0o750); err != nil {
			t.Fatal(err)
		}
		c := only(t, readPR(t, r.query(r.head, wt)))
		if !c.JJ {
			t.Fatalf("got %+v, want a jj checkout", c)
		}
	})

	t.Run("missing linked checkout is unavailable", func(t *testing.T) {
		r := newPRRepo(t)
		gone := filepath.Join(t.TempDir(), "gone")
		l := readPR(t, r.query(r.head, gone))
		if len(l.Checkouts) != 2 || l.Checkouts[0].Path != gone || l.Checkouts[0].Err == nil {
			t.Fatalf("got %+v, want the linked checkout first, unavailable", l)
		}
	})
}

func TestReadPRLocal_Checkouts(t *testing.T) {
	t.Run("linked checkout first, not repeated", func(t *testing.T) {
		r := newPRRepo(t)
		discovered := r.worktree(t)
		linked := filepath.Join(t.TempDir(), "linked")
		git(t, r.clone, "worktree", "add", "-q", "-b", "session-branch", linked, "feature/42-x")
		linked = realPath(t, linked)
		l := readPR(t, r.query(r.head, linked))
		var paths []string
		for _, c := range l.Checkouts {
			paths = append(paths, c.Path)
		}
		if !slices.Equal(paths, []string{linked, discovered}) || !l.Checkouts[0].Linked || l.Checkouts[1].Linked {
			t.Fatalf("checkouts %q (%+v), want linked %s then %s", paths, l.Checkouts, linked, discovered)
		}
		if c := only(t, readPR(t, r.query(r.head, discovered))); c.Path != discovered || !c.Linked {
			t.Fatalf("a linked checkout that also tracks the head is listed once, linked: %+v", c)
		}
	})

	t.Run("branch tracking the pull ref", func(t *testing.T) {
		r := newPRRepo(t)
		git(t, r.clone, "branch", "-q", "-D", "feature/42-x")
		git(t, r.clone, "branch", "-q", "alice-x", r.head)
		git(t, r.clone, "config", "branch.alice-x.remote", "origin")
		git(t, r.clone, "config", "branch.alice-x.merge", "refs/pull/42/head")
		c := only(t, readPR(t, PRQuery{Root: r.clone, Number: 42, Branch: "x", SHA: r.head, CrossRepo: true, HeadOwner: "alice"}))
		if c.Branch != "alice-x" || c.Compare.Kind != InSync {
			t.Fatalf("got %+v, want alice-x in sync", c)
		}
	})

	t.Run("cross-repo PR does not match a same-named branch of origin", func(t *testing.T) {
		r := newPRRepo(t)
		l := readPR(t, PRQuery{Root: r.clone, Number: 42, Branch: "main", SHA: r.head, CrossRepo: true, HeadOwner: "alice"})
		if len(l.Checkouts) != 0 {
			t.Fatalf("got %+v, want no checkouts: origin/main is not alice's main", l)
		}
	})

	t.Run("cross-repo PR matches a branch tracking the fork's remote", func(t *testing.T) {
		r := newPRRepo(t)
		git(t, r.clone, "remote", "add", "alice", "git@github.com:alice/widgets.git")
		git(t, r.clone, "branch", "-q", "alice-main", r.head)
		git(t, r.clone, "config", "branch.alice-main.remote", "alice")
		git(t, r.clone, "config", "branch.alice-main.merge", "refs/heads/main")
		c := only(t, readPR(t, PRQuery{Root: r.clone, Number: 42, Branch: "main", SHA: r.head, CrossRepo: true, HeadOwner: "alice"}))
		if c.Branch != "alice-main" {
			t.Fatalf("got %+v, want alice-main", c)
		}
	})

	t.Run("detached worktree at the head", func(t *testing.T) {
		r := newPRRepo(t)
		git(t, r.clone, "branch", "-q", "-D", "feature/42-x")
		wt := filepath.Join(t.TempDir(), "wt")
		git(t, r.clone, "worktree", "add", "-q", "--detach", wt, r.head)
		c := only(t, readPR(t, r.query(r.head)))
		if c.Path != realPath(t, wt) || c.Branch != "(detached)" || c.Compare.Kind != InSync {
			t.Fatalf("got %+v, want the detached worktree in sync", c)
		}
	})
}

// TestReadPRLocal_NeverFetches: the comparison only reads what the local
// repo already has; no git call fetches.
func TestReadPRLocal_NeverFetches(t *testing.T) {
	var calls [][]string
	prev := gitCallHook
	gitCallHook = func(args []string) { calls = append(calls, args) }
	t.Cleanup(func() { gitCallHook = prev })

	r := newPRRepo(t)
	wt := r.worktree(t)
	readPR(t, r.query("0123456789abcdef0123456789abcdef01234567", wt))
	if len(calls) == 0 {
		t.Fatal("no git calls recorded")
	}
	for _, args := range calls {
		for _, a := range args {
			if a == "fetch" || a == "pull" || a == "remote" || a == "ls-remote" {
				t.Fatalf("git call %q talks to a remote", strings.Join(args, " "))
			}
		}
	}
}

func TestPRLocalWarn(t *testing.T) {
	tests := []struct {
		name string
		l    PRLocal
		want bool
	}{
		{"none", PRLocal{}, false},
		{"in sync and clean", PRLocal{Checkouts: []Checkout{{Compare: Compare{Kind: InSync}}}}, false},
		{"unpushed", PRLocal{Checkouts: []Checkout{{Compare: Compare{Kind: InSync}}, {Compare: Compare{Kind: Unpushed, Ahead: 1}}}}, true},
		{"diverged", PRLocal{Checkouts: []Checkout{{Compare: Compare{Kind: Diverged, Ahead: 1, Behind: 1}}}}, true},
		{"dirty", PRLocal{Checkouts: []Checkout{{Compare: Compare{Kind: Behind, Behind: 1}, Dirty: true}}}, true},
		{"behind only", PRLocal{Checkouts: []Checkout{{Compare: Compare{Kind: Behind, Behind: 3}}}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.l.Warn(); got != tc.want {
				t.Fatalf("Warn() = %v, want %v", got, tc.want)
			}
		})
	}
}
