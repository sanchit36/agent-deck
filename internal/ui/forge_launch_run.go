package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/git"
	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
	"github.com/asheshgoplani/agent-deck/internal/vcsbackend"
)

// Launch from an Issue or PR (spec #18 §13), the part that runs off the UI
// goroutine: make the worktree (an Issue's branch, or a PR's detached at
// its base then gh pr checkout), start the session with its first message
// through StartWithMessage, and write the Link once it runs. A failure in
// the worktree, the checkout or the start leaves no session and removes a
// worktree made for this launch; a failed send keeps the session.

// forgeLaunchPlan is one launch, read from the dialog on the UI goroutine.
type forgeLaunchPlan struct {
	kind   session.LinkKind
	repo   github.Repo
	number int
	// repoRoot is the main worktree of the local repository.
	repoRoot string
	worktree bool
	// branch is an Issue's branch (created or reused), or the PR's head
	// branch, which names its worktree.
	branch string
	// baseBranch is the PR's base, where its worktree starts detached.
	baseBranch string
	// message is the first message; "" sends none.
	message string
	// newInstance builds the session, with the dialog's options, in path.
	newInstance func(path string) *session.Instance
}

// forgeLaunchResult is a launch's outcome: err when nothing was left
// behind, else the session (sendErr when its first message failed).
type forgeLaunchResult struct {
	inst         *session.Instance
	err          error
	sendErr      error
	setupWarning string
}

// forgeLauncher runs launches. Every field is injectable for tests; nil
// means the real thing.
type forgeLauncher struct {
	runner github.Runner
	// start starts inst and sends message: inst.StartWithMessage.
	start func(inst *session.Instance, message string) error
	// running reports whether inst's session exists after a failed start
	// (then only the send failed).
	running func(inst *session.Instance) bool
}

func (l forgeLauncher) startSession(inst *session.Instance, message string) error {
	if l.start != nil {
		return l.start(inst, message)
	}
	if err := tmux.IsTmuxAvailable(); err != nil {
		return fmt.Errorf("cannot create session: %w", err)
	}
	return inst.StartWithMessage(message)
}

func (l forgeLauncher) sessionRunning(inst *session.Instance) bool {
	if l.running != nil {
		return l.running(inst)
	}
	return inst.Exists()
}

// launch runs plan. See forgeLaunchResult.
func (l forgeLauncher) launch(ctx context.Context, p forgeLaunchPlan) forgeLaunchResult {
	dir, created, branch := p.repoRoot, "", ""
	var setupErr error
	fail := func(err error) forgeLaunchResult {
		if created != "" {
			if rmErr := git.RemoveWorktree(p.repoRoot, created, true); rmErr != nil {
				uiLog.Warn("forge_launch_worktree_cleanup_failed", slog.String("path", created), slog.String("error", rmErr.Error()))
				err = fmt.Errorf("%w (removing the worktree %s also failed: %v)", err, created, rmErr)
			}
		}
		return forgeLaunchResult{err: err}
	}

	switch {
	case p.kind == session.LinkIssue && p.worktree:
		if existing := forgeExistingWorktree(p.repoRoot, p.branch); existing != "" {
			dir = existing
			break
		}
		wt, err := forgeWorktreePath(p.repoRoot, p.branch)
		if err != nil {
			return fail(err)
		}
		backend, err := vcsbackend.Detect(p.repoRoot)
		if err != nil {
			return fail(fmt.Errorf("detecting the repository at %s: %w", p.repoRoot, err))
		}
		if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
			return fail(fmt.Errorf("creating the worktree parent directory: %w", err))
		}
		setupErr, err = createWorktreeWithSetupAndLog(backend, wt, p.branch, p.repoRoot)
		if err != nil {
			return fail(fmt.Errorf("creating the worktree for %s: %w", p.branch, err))
		}
		dir, created, branch = wt, wt, p.branch
	case p.kind == session.LinkPR && p.worktree:
		if existing := forgeExistingWorktree(p.repoRoot, p.branch); existing != "" {
			dir = existing
			break
		}
		name := p.branch
		if name == "" {
			name = "pr-" + strconv.Itoa(p.number)
		}
		wt, err := forgeWorktreePath(p.repoRoot, name)
		if err != nil {
			return fail(err)
		}
		if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
			return fail(fmt.Errorf("creating the worktree parent directory: %w", err))
		}
		if err := addDetachedWorktree(p.repoRoot, wt, p.baseBranch); err != nil {
			return fail(err)
		}
		dir, created = wt, wt
		if err := l.prCheckout(ctx, p, dir); err != nil {
			return fail(err)
		}
		setupErr = runForgeWorktreeSetup(p.repoRoot, wt)
	case p.kind == session.LinkPR:
		dirty, err := git.HasUncommittedChanges(p.repoRoot)
		if err != nil {
			return fail(fmt.Errorf("checking %s for uncommitted changes: %w", p.repoRoot, err))
		}
		if dirty {
			return fail(fmt.Errorf("%s has uncommitted changes: commit or stash them, or tick Worktree", p.repoRoot))
		}
		if err := l.prCheckout(ctx, p, dir); err != nil {
			return fail(err)
		}
	}
	if p.worktree && branch == "" {
		if b, err := git.GetCurrentBranch(dir); err == nil {
			branch = b
		}
	}

	inst := p.newInstance(dir)
	if p.worktree {
		inst.WorktreePath = dir
		inst.WorktreeRepoRoot = p.repoRoot
		inst.WorktreeBranch = branch
		inst.WorktreeType = "git"
	}
	var sendErr error
	if err := l.startSession(inst, p.message); err != nil {
		if !l.sessionRunning(inst) {
			return fail(err)
		}
		sendErr = err
	}
	inst.SetLink(p.kind, session.Link{
		Host: p.repo.HostOrDefault(), Owner: p.repo.Owner, Name: p.repo.Name, Number: p.number,
	})
	res := forgeLaunchResult{inst: inst, sendErr: sendErr}
	if setupErr != nil {
		res.setupWarning = formatSetupWarning(setupErr)
	}
	return res
}

// prCheckout runs gh pr checkout in dir.
func (l forgeLauncher) prCheckout(ctx context.Context, p forgeLaunchPlan, dir string) error {
	c := github.NewClient(l.runner)
	if _, err := c.Repo(ctx, p.repo, github.Call{Args: []string{"pr", "checkout", strconv.Itoa(p.number)}, Dir: dir}); err != nil {
		return fmt.Errorf("gh pr checkout %d: %w", p.number, err)
	}
	return nil
}

// forgeExistingWorktree is the worktree that has branch checked out, ""
// when none does (or branch is empty: a detached worktree has no branch).
func forgeExistingWorktree(repoRoot, branch string) string {
	if branch == "" {
		return ""
	}
	wt, _ := git.GetWorktreeForBranch(repoRoot, branch)
	return wt
}

// forgeWorktreePath is where a launch's worktree for branch goes, by the
// [worktree] settings of repoRoot.
func forgeWorktreePath(repoRoot, branch string) (string, error) {
	wt, _, _, errMsg := resolveWorktreeTarget(repoRoot, branch, true)
	if errMsg != "" {
		return "", errors.New(errMsg)
	}
	return wt, nil
}

// addDetachedWorktree adds a worktree at wt detached at the PR's base:
// the remote-tracking origin/<base>, else the local <base>, else HEAD.
func addDetachedWorktree(repoRoot, wt, base string) error {
	var points []string
	if base != "" {
		points = append(points, "origin/"+base, base)
	}
	points = append(points, "HEAD")
	start := "HEAD"
	for _, pt := range points {
		// #nosec G204 -- fixed git subcommand; pt is a ref name passed as one argument.
		if exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", "--quiet", pt+"^{commit}").Run() == nil {
			start = pt
			break
		}
	}
	// #nosec G204 -- fixed git subcommand with path and ref arguments.
	out, err := exec.Command("git", "-C", repoRoot, "worktree", "add", "--detach", wt, start).CombinedOutput()
	if err != nil {
		return fmt.Errorf("creating the worktree at %s: %s: %w", wt, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// runForgeWorktreeSetup runs the repository's worktree setup script (if
// trusted) in a PR worktree; its failure is a warning, not a launch error.
func runForgeWorktreeSetup(repoRoot, wt string) error {
	settings, err := session.GetWorktreeSettingsForDir(repoRoot)
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	if err := git.RunWorktreeSetupAfterCreate(repoRoot, wt, &buf, &buf, settings.SetupTimeout()); err != nil {
		return withSetupOutputTail(err, buf.String())
	}
	return nil
}
