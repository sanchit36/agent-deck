package github_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestReadLocalWithRealGit reads a real repository, its linked worktree and
// a plain directory, then resolves them end to end.
func TestReadLocalWithRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "widgets")
	git(t, tmp, "init", "-q", repo)
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, repo, "remote", "add", "upstream", "https://github.com/acme/widgets.git")
	git(t, repo, "remote", "add", "origin", "git@github.com:me/widgets.git")
	wt := filepath.Join(tmp, "widgets-feature")
	git(t, repo, "worktree", "add", "-q", "-b", "feature", wt)
	plain := filepath.Join(tmp, "notes")
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// Resolve symlinks (macOS /var -> /private/var) the way git reports paths.
	wantRoot, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{repo, wt, filepath.Join(repo, ".")} {
		loc, err := github.ReadLocal(ctx, p)
		if err != nil {
			t.Fatalf("ReadLocal(%s): %v", p, err)
		}
		if loc.Root != wantRoot {
			t.Fatalf("ReadLocal(%s).Root = %q, want the main checkout %q", p, loc.Root, wantRoot)
		}
		if len(loc.Remotes) != 2 {
			t.Fatalf("ReadLocal(%s).Remotes = %+v, want origin and upstream", p, loc.Remotes)
		}
	}
	for _, p := range []string{plain, filepath.Join(tmp, "missing")} {
		if _, err := github.ReadLocal(ctx, p); !errors.Is(err, github.ErrNotGit) {
			t.Fatalf("ReadLocal(%s) err = %v, want ErrNotGit", p, err)
		}
	}

	r := github.NewResolver(github.NewClient(githubtest.NewFake()), nil)
	g, err := r.ResolveGroup(ctx, plain, []string{wt, repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Projects) != 1 || g.Projects[0].Repo.String() != "github.com/me/widgets" || g.Projects[0].Root != wantRoot {
		t.Fatalf("ResolveGroup = %+v, want the fork me/widgets once, rooted at the main checkout", g)
	}
}
