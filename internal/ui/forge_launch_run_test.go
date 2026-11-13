package ui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Launch executor tests (spec #18 §13): a real git repository in a temp
// dir, gh served by the fake runner (its pr checkout makes the branch the
// way gh would), and the agent start replaced, so no tmux and no agent CLI
// run.

var launchRepo = github.Repo{Host: "github.com", Owner: "acme", Name: "widgets"}

func launchTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "widgets")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitMustUI(t, repo, "init", "-q", "-b", "main")
	gitMustUI(t, repo, "config", "user.email", "t@example.com")
	gitMustUI(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "seed"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitMustUI(t, repo, "add", ".")
	gitMustUI(t, repo, "commit", "-qm", "seed")
	return repo
}

// checkoutGh is a fake gh whose pr checkout creates head in the call's
// directory, as the real one does; every other call goes to fake.
type checkoutGh struct {
	t    *testing.T
	head string
	fake *githubtest.Fake
}

func (g checkoutGh) Run(ctx context.Context, call github.Call) (github.Result, error) {
	res, err := g.fake.Run(ctx, call)
	if err == nil && res.ExitCode == 0 && len(call.Args) > 1 && call.Args[0] == "pr" && call.Args[1] == "checkout" {
		gitMustUI(g.t, call.Dir, "checkout", "-q", "-b", g.head)
	}
	return res, err
}

func prCheckoutArgs(n string) []string {
	return []string{"pr", "checkout", n, "-R", launchRepo.String()}
}

// startRecorder replaces the agent start: it records the session and the
// first message and returns err; alive is what running reports after.
type startRecorder struct {
	calls   []string
	err     error
	alive   bool
	started *session.Instance
}

func (s *startRecorder) launcher(runner github.Runner) forgeLauncher {
	return forgeLauncher{
		client: github.NewClient(runner),
		start: func(inst *session.Instance, msg string) error {
			s.calls = append(s.calls, msg)
			s.started = inst
			return s.err
		},
		running: func(*session.Instance) bool { return s.alive },
	}
}

func issuePlan(repo string) forgeLaunchPlan {
	return forgeLaunchPlan{
		kind: session.LinkIssue, repo: launchRepo, number: 12, repoRoot: repo,
		worktree: true, branch: "feature/12-fix-crash", message: "work on #12",
		newInstance: func(path string) (*session.Instance, error) {
			return session.NewInstanceWithGroupAndTool("#12 Fix crash", path, "widgets", "claude"), nil
		},
	}
}

func prPlan(repo string) forgeLaunchPlan {
	return forgeLaunchPlan{
		kind: session.LinkPR, repo: launchRepo, number: 34, repoRoot: repo,
		worktree: true, branch: "widget-sizes", baseBranch: "main", message: "review PR#34",
		newInstance: func(path string) (*session.Instance, error) {
			return session.NewInstanceWithGroupAndTool("PR#34 Widget sizes", path, "widgets", "claude"), nil
		},
	}
}

func worktreeCount(t *testing.T, repo string) int {
	t.Helper()
	out := gitOutUI(t, repo, "worktree", "list", "--porcelain")
	return strings.Count(out, "worktree ")
}

func TestForgeLaunchIssueWorktree(t *testing.T) {
	repo := launchTestRepo(t)
	rec := &startRecorder{}
	res := rec.launcher(githubtest.NewFake()).launch(context.Background(), issuePlan(repo))
	if res.err != nil || res.sendErr != nil {
		t.Fatalf("launch: err %v, sendErr %v", res.err, res.sendErr)
	}
	inst := res.inst
	if inst.WorktreeBranch != "feature/12-fix-crash" || inst.WorktreeRepoRoot != repo || inst.ProjectPath != inst.WorktreePath || inst.WorktreePath == repo {
		t.Fatalf("worktree fields: branch %q root %q path %q wt %q", inst.WorktreeBranch, inst.WorktreeRepoRoot, inst.ProjectPath, inst.WorktreePath)
	}
	if got := gitOutUI(t, inst.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD"); strings.TrimSpace(got) != "feature/12-fix-crash" {
		t.Fatalf("worktree HEAD %q", got)
	}
	if !slices.Equal(rec.calls, []string{"work on #12"}) {
		t.Fatalf("start calls %q", rec.calls)
	}
	l := inst.LinkOf(session.LinkIssue)
	if l == nil || *l != (session.Link{Host: "github.com", Owner: "acme", Name: "widgets", Number: 12}) {
		t.Fatalf("issue link %+v", l)
	}
}

// TestForgeLaunchIssueReusesBranch: an existing branch is checked out, not
// recreated.
func TestForgeLaunchIssueReusesBranch(t *testing.T) {
	repo := launchTestRepo(t)
	gitMustUI(t, repo, "branch", "feature/12-fix-crash")
	rec := &startRecorder{}
	res := rec.launcher(githubtest.NewFake()).launch(context.Background(), issuePlan(repo))
	if res.err != nil {
		t.Fatal(res.err)
	}
	if res.inst.WorktreeBranch != "feature/12-fix-crash" {
		t.Fatalf("branch %q", res.inst.WorktreeBranch)
	}
}

// TestForgeLaunchFailedStartLeavesNothing: a start that fails before the
// session runs leaves no session, no Link and no worktree it created.
func TestForgeLaunchFailedStartLeavesNothing(t *testing.T) {
	repo := launchTestRepo(t)
	rec := &startRecorder{err: errors.New("tmux exploded")}
	res := rec.launcher(githubtest.NewFake()).launch(context.Background(), issuePlan(repo))
	if res.err == nil || !strings.Contains(res.err.Error(), "tmux exploded") {
		t.Fatalf("err %v", res.err)
	}
	if res.inst != nil {
		t.Fatalf("a failed launch returned session %q", res.inst.ID)
	}
	if n := worktreeCount(t, repo); n != 1 {
		t.Fatalf("%d worktrees after a failed launch, want only the main one", n)
	}
	if rec.started == nil || rec.started.LinkOf(session.LinkIssue) != nil {
		t.Fatal("the Link was written before the session started")
	}
	if _, err := os.Stat(rec.started.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree dir %s still there: %v", rec.started.WorktreePath, err)
	}
}

// TestForgeLaunchFailureKeepsReusedWorktree: a worktree the launch did not
// create is never removed.
func TestForgeLaunchFailureKeepsReusedWorktree(t *testing.T) {
	repo := launchTestRepo(t)
	existing := filepath.Join(filepath.Dir(repo), "existing")
	gitMustUI(t, repo, "worktree", "add", "-q", "-b", "feature/12-fix-crash", existing)
	rec := &startRecorder{err: errors.New("boom")}
	res := rec.launcher(githubtest.NewFake()).launch(context.Background(), issuePlan(repo))
	if res.err == nil {
		t.Fatal("want the start error")
	}
	if rec.started.ProjectPath != existing {
		t.Fatalf("session path %q, want the existing worktree %q", rec.started.ProjectPath, existing)
	}
	if _, err := os.Stat(existing); err != nil {
		t.Fatalf("the reused worktree was removed: %v", err)
	}
}

// TestForgeLaunchFailedSendKeepsSession: a session that started but did
// not take the first message is kept, Linked, with the send error.
func TestForgeLaunchFailedSendKeepsSession(t *testing.T) {
	repo := launchTestRepo(t)
	rec := &startRecorder{err: errors.New("prompt never appeared"), alive: true}
	res := rec.launcher(githubtest.NewFake()).launch(context.Background(), issuePlan(repo))
	if res.err != nil || res.sendErr == nil || res.inst == nil {
		t.Fatalf("err %v, sendErr %v, inst %v", res.err, res.sendErr, res.inst)
	}
	if res.inst.LinkOf(session.LinkIssue) == nil {
		t.Fatal("a session kept after a failed send is still Linked")
	}
	if _, err := os.Stat(res.inst.WorktreePath); err != nil {
		t.Fatalf("worktree removed: %v", err)
	}
}

// TestForgeLaunchPRWorktree: a worktree detached at the base, then gh pr
// checkout inside it; the session records gh's branch.
func TestForgeLaunchPRWorktree(t *testing.T) {
	repo := launchTestRepo(t)
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.OK), prCheckoutArgs("34")...)
	rec := &startRecorder{}
	res := rec.launcher(checkoutGh{t: t, head: "widget-sizes", fake: fake}).launch(context.Background(), prPlan(repo))
	if res.err != nil {
		t.Fatal(res.err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Dir != res.inst.WorktreePath || calls[0].Dir == repo {
		t.Fatalf("gh calls %+v, worktree %q", calls, res.inst.WorktreePath)
	}
	if res.inst.WorktreeBranch != "widget-sizes" || res.inst.ProjectPath != res.inst.WorktreePath {
		t.Fatalf("branch %q path %q", res.inst.WorktreeBranch, res.inst.ProjectPath)
	}
	if l := res.inst.LinkOf(session.LinkPR); l == nil || l.Number != 34 {
		t.Fatalf("PR link %+v", l)
	}
	if got := strings.TrimSpace(gitOutUI(t, repo, "rev-parse", "--abbrev-ref", "HEAD")); got != "main" {
		t.Fatalf("the main checkout moved to %q", got)
	}
}

// TestForgeLaunchPRCheckoutFailureRemovesWorktree: gh pr checkout failing
// removes the worktree made for it and never starts a session.
func TestForgeLaunchPRCheckoutFailureRemovesWorktree(t *testing.T) {
	repo := launchTestRepo(t)
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.Failed), prCheckoutArgs("34")...)
	rec := &startRecorder{}
	res := rec.launcher(fake).launch(context.Background(), prPlan(repo))
	if res.err == nil || res.inst != nil {
		t.Fatalf("err %v inst %v", res.err, res.inst)
	}
	if len(rec.calls) != 0 {
		t.Fatal("a session started after a failed checkout")
	}
	if n := worktreeCount(t, repo); n != 1 {
		t.Fatalf("%d worktrees, want 1", n)
	}
}

// TestForgeLaunchPRUntickedRefusesDirtyTree: without a worktree gh pr
// checkout runs in the repo itself, and only on a clean tree.
func TestForgeLaunchPRUntickedRefusesDirtyTree(t *testing.T) {
	repo := launchTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "seed"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.OK), prCheckoutArgs("34")...)
	rec := &startRecorder{}
	plan := prPlan(repo)
	plan.worktree = false
	res := rec.launcher(fake).launch(context.Background(), plan)
	if res.err == nil || !strings.Contains(res.err.Error(), "uncommitted changes") {
		t.Fatalf("err %v, want a dirty-tree refusal", res.err)
	}
	if len(fake.Calls()) != 0 || len(rec.calls) != 0 {
		t.Fatalf("gh calls %d, starts %d after a refusal", len(fake.Calls()), len(rec.calls))
	}
}

func TestForgeLaunchPRUntickedChecksOutInRepo(t *testing.T) {
	repo := launchTestRepo(t)
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.OK), prCheckoutArgs("34")...)
	rec := &startRecorder{}
	plan := prPlan(repo)
	plan.worktree = false
	res := rec.launcher(checkoutGh{t: t, head: "widget-sizes", fake: fake}).launch(context.Background(), plan)
	if res.err != nil {
		t.Fatal(res.err)
	}
	if calls := fake.Calls(); len(calls) != 1 || calls[0].Dir != repo {
		t.Fatalf("gh calls %+v", calls)
	}
	if res.inst.ProjectPath != repo || res.inst.WorktreePath != "" {
		t.Fatalf("path %q worktree %q", res.inst.ProjectPath, res.inst.WorktreePath)
	}
}

// TestForgeLaunchNeverUsesMainCheckout: a branch the main checkout holds
// is never "reused": a PR whose head is that branch (a fork's main) gets a
// new worktree and gh pr checkout, and an Issue branch checked out there
// is refused with the reason, so no session records the repository root
// as its worktree (a later Delete or Finish would act on it).
func TestForgeLaunchNeverUsesMainCheckout(t *testing.T) {
	t.Run("pr head is main", func(t *testing.T) {
		repo := launchTestRepo(t)
		fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.OK), prCheckoutArgs("34")...)
		rec := &startRecorder{}
		plan := prPlan(repo)
		plan.branch = "main"
		res := rec.launcher(checkoutGh{t: t, head: "fork-main", fake: fake}).launch(context.Background(), plan)
		if res.err != nil {
			t.Fatal(res.err)
		}
		if res.inst.WorktreePath == repo || res.inst.ProjectPath == repo {
			t.Fatalf("the session runs in the main checkout: worktree %q path %q", res.inst.WorktreePath, res.inst.ProjectPath)
		}
		if calls := fake.Calls(); len(calls) != 1 || calls[0].Dir != res.inst.WorktreePath {
			t.Fatalf("gh calls %+v, want pr checkout in the new worktree %q", calls, res.inst.WorktreePath)
		}
		if got := strings.TrimSpace(gitOutUI(t, repo, "rev-parse", "--abbrev-ref", "HEAD")); got != "main" {
			t.Fatalf("the main checkout moved to %q", got)
		}
	})
	t.Run("issue branch checked out in the main checkout", func(t *testing.T) {
		repo := launchTestRepo(t)
		gitMustUI(t, repo, "checkout", "-q", "-b", "feature/12-fix-crash")
		rec := &startRecorder{}
		res := rec.launcher(githubtest.NewFake()).launch(context.Background(), issuePlan(repo))
		if res.err == nil || !strings.Contains(res.err.Error(), "main checkout") {
			t.Fatalf("err %v, want a refusal naming the main checkout", res.err)
		}
		if res.inst != nil || len(rec.calls) != 0 {
			t.Fatalf("a session started in the main checkout: %+v", rec.started)
		}
		if n := worktreeCount(t, repo); n != 1 {
			t.Fatalf("%d worktrees, want only the main one", n)
		}
	})
}

// TestForgeLaunchPRMissingBase: a PR worktree starts detached at the PR's
// base; a base the repository does not have (or an unknown one) is an
// error naming it, never a silent start at HEAD.
func TestForgeLaunchPRMissingBase(t *testing.T) {
	for _, base := range []string{"release-9", ""} {
		t.Run("base "+base, func(t *testing.T) {
			repo := launchTestRepo(t)
			fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.OK), prCheckoutArgs("34")...)
			rec := &startRecorder{}
			plan := prPlan(repo)
			plan.baseBranch = base
			res := rec.launcher(checkoutGh{t: t, head: "widget-sizes", fake: fake}).launch(context.Background(), plan)
			if res.err == nil || !strings.Contains(res.err.Error(), "base") || (base != "" && !strings.Contains(res.err.Error(), base)) {
				t.Fatalf("err %v, want one naming the missing base %q", res.err, base)
			}
			if len(fake.Calls()) != 0 || len(rec.calls) != 0 {
				t.Fatalf("gh calls %d, starts %d after a missing base", len(fake.Calls()), len(rec.calls))
			}
			if n := worktreeCount(t, repo); n != 1 {
				t.Fatalf("%d worktrees, want only the main one", n)
			}
		})
	}
}

// TestForgeLaunchPRWorktreeAtBase: the worktree starts at the base's
// remote-tracking ref (origin/<base>), not at the main checkout's HEAD.
func TestForgeLaunchPRWorktreeAtBase(t *testing.T) {
	repo := launchTestRepo(t)
	base := strings.TrimSpace(gitOutUI(t, repo, "rev-parse", "HEAD"))
	gitMustUI(t, repo, "update-ref", "refs/remotes/origin/release-9", base)
	gitMustUI(t, repo, "commit", "-q", "--allow-empty", "-m", "ahead of the base")
	rec := &startRecorder{}
	plan := prPlan(repo)
	plan.baseBranch = "release-9"
	// The fake gh pr checkout changes nothing, so the start sees the
	// commit the worktree was made at.
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.OK), prCheckoutArgs("34")...)
	var at string
	l := rec.launcher(fake)
	l.start = func(inst *session.Instance, _ string) error {
		at = strings.TrimSpace(gitOutUI(t, inst.ProjectPath, "rev-parse", "HEAD"))
		return nil
	}
	if res := l.launch(context.Background(), plan); res.err != nil {
		t.Fatal(res.err)
	}
	if at != base {
		t.Fatalf("worktree HEAD %s, want the base %s", at, base)
	}
}
