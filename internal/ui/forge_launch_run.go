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
	"time"

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
// the worktree, the checkout or the start leaves no session and removes the
// worktree and branch made for this launch (never ones that were there); a
// failed send keeps the session.

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
	newInstance func(path string) (*session.Instance, error)
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
	// client runs gh pr checkout; nil runs the real gh.
	client *github.Client
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
	// branchesBefore are the local branches before this launch made its
	// worktree: a branch the worktree holds that is not among them was
	// made by this launch (by git worktree add, or gh pr checkout), and a
	// failure removes it with the worktree. nil (unread) removes none.
	var branchesBefore map[string]bool
	fail := func(err error) forgeLaunchResult {
		if created != "" {
			err = forgeLaunchCleanup(context.WithoutCancel(ctx), p.repoRoot, created, branchesBefore, err)
		}
		return forgeLaunchResult{err: err}
	}

	switch {
	case p.kind == session.LinkIssue && p.worktree:
		existing, inMain := forgeExistingWorktree(p.repoRoot, p.branch)
		if inMain {
			return fail(fmt.Errorf("%s is checked out in the main checkout %s: switch that checkout to another branch, or untick Worktree to work there", p.branch, p.repoRoot))
		}
		if existing != "" {
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
		branchesBefore = forgeLocalBranches(ctx, p.repoRoot)
		setupErr, err = createWorktreeWithSetupAndLog(backend, wt, p.branch, p.repoRoot)
		if err != nil {
			return fail(fmt.Errorf("creating the worktree for %s: %w", p.branch, err))
		}
		dir, created, branch = wt, wt, p.branch
	case p.kind == session.LinkPR && p.worktree:
		// A head the main checkout holds (a fork's main, say) gets a new
		// worktree: gh pr checkout names its branch there.
		if existing, _ := forgeExistingWorktree(p.repoRoot, p.branch); existing != "" {
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
		branchesBefore = forgeLocalBranches(ctx, p.repoRoot)
		if err := addDetachedWorktree(ctx, p.repoRoot, wt, p.baseBranch); err != nil {
			return fail(err)
		}
		dir, created = wt, wt
		if err := l.prCheckout(ctx, p, dir); err != nil {
			return fail(err)
		}
		setupErr = runForgeWorktreeSetup(p.repoRoot, wt)
	case p.kind == session.LinkPR:
		status, err := forgeGit(ctx, p.repoRoot, "status", "--porcelain")
		if err != nil {
			return fail(fmt.Errorf("checking %s for uncommitted changes: %w", p.repoRoot, err))
		}
		if strings.TrimSpace(string(status)) != "" {
			return fail(fmt.Errorf("%s has uncommitted changes: commit or stash them, or tick Worktree", p.repoRoot))
		}
		if err := l.prCheckout(ctx, p, dir); err != nil {
			return fail(err)
		}
	}
	if p.worktree && branch == "" {
		if b, err := forgeGit(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			branch = strings.TrimSpace(string(b))
		}
	}

	inst, err := p.newInstance(dir)
	if err != nil {
		return fail(err)
	}
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
	c := l.client
	if c == nil {
		c = github.NewClient(nil)
	}
	if _, err := c.Repo(ctx, p.repo, github.Call{Args: []string{"pr", "checkout", strconv.Itoa(p.number)}, Dir: dir}); err != nil {
		return fmt.Errorf("gh pr checkout %d: %w", p.number, err)
	}
	return nil
}

// forgeLocalBranches are repoRoot's local branch names; nil when git
// cannot list them.
func forgeLocalBranches(ctx context.Context, repoRoot string) map[string]bool {
	out, err := forgeGit(ctx, repoRoot, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		uiLog.Warn("forge_launch_list_branches_failed", slog.String("repo", repoRoot), slog.String("error", err.Error()))
		return nil
	}
	set := map[string]bool{}
	for _, b := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if b != "" {
			set[b] = true
		}
	}
	return set
}

// forgeLaunchCleanup removes what a failed launch made: the worktree wt,
// and the branch it holds when that is not among before (this launch made
// it). A cleanup failure is added to err, which it returns.
func forgeLaunchCleanup(ctx context.Context, repoRoot, wt string, before map[string]bool, err error) error {
	made := ""
	if before != nil {
		if out, bErr := forgeGit(ctx, wt, "symbolic-ref", "--quiet", "--short", "HEAD"); bErr == nil {
			if b := strings.TrimSpace(string(out)); b != "" && !before[b] {
				made = b
			}
		}
	}
	if rmErr := git.RemoveWorktree(repoRoot, wt, true); rmErr != nil {
		uiLog.Warn("forge_launch_worktree_cleanup_failed", slog.String("path", wt), slog.String("error", rmErr.Error()))
		return fmt.Errorf("%w (removing the worktree %s also failed: %v)", err, wt, rmErr)
	}
	if made == "" {
		return err
	}
	if _, brErr := forgeGit(ctx, repoRoot, "branch", "-D", "--", made); brErr != nil {
		uiLog.Warn("forge_launch_branch_cleanup_failed", slog.String("branch", made), slog.String("error", brErr.Error()))
		return fmt.Errorf("%w (deleting the branch %s also failed: %v)", err, made, brErr)
	}
	return err
}

// forgeExistingWorktree is the linked worktree that has branch checked
// out, "" when none does (or branch is empty: a detached worktree has no
// branch). The main checkout is never one to reuse: inMain reports that it
// holds branch, and a session recording it as its worktree would have
// Delete and Finish act on the repository itself.
func forgeExistingWorktree(repoRoot, branch string) (linked string, inMain bool) {
	if branch == "" {
		return "", false
	}
	wts, err := git.ListWorktrees(repoRoot)
	if err != nil {
		return "", false
	}
	for i, wt := range wts {
		if wt.Branch != branch {
			continue
		}
		// git lists the main worktree (or a bare repository) first.
		if i == 0 || wt.Bare {
			return "", true
		}
		return wt.Path, false
	}
	return "", false
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

// forgeLaunchGitTimeout bounds each git call of a launch; worktree add
// checks out the whole tree, so it is generous.
const forgeLaunchGitTimeout = 2 * time.Minute

// forgeGit runs git in dir under ctx and forgeLaunchGitTimeout, with the
// child environment of github.GitEnv, and returns its stdout. A failure
// carries git's stderr.
func forgeGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, forgeLaunchGitTimeout)
	defer cancel()
	// #nosec G204 -- "git" is a fixed binary run with a literal argv (no
	// shell); dir is the value of -C and refs are single arguments.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = github.GitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return out, fmt.Errorf("running git %s: %w", args[0], ctxErr)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("running git %s: %s: %w", args[0], msg, err)
		}
		return out, fmt.Errorf("running git %s: %w", args[0], err)
	}
	return out, nil
}

// addDetachedWorktree adds a worktree at wt detached at the PR's base:
// the remote-tracking origin/<base>, else the local <base>. A base the
// repository does not have is an error, never a start at HEAD.
func addDetachedWorktree(ctx context.Context, repoRoot, wt, base string) error {
	if base == "" {
		return errors.New("the PR's base branch is not known: wait for its details to load and launch again")
	}
	start := ""
	for _, pt := range []string{"origin/" + base, base} {
		if _, err := forgeGit(ctx, repoRoot, "rev-parse", "--verify", "--quiet", pt+"^{commit}"); err == nil {
			start = pt
			break
		} else if ctx.Err() != nil {
			return fmt.Errorf("looking up the PR base %s: %w", base, err)
		}
	}
	if start == "" {
		return fmt.Errorf("the PR base %s is not in %s: run git fetch origin %s and launch again", base, repoRoot, base)
	}
	if _, err := forgeGit(ctx, repoRoot, "worktree", "add", "--detach", wt, start); err != nil {
		return fmt.Errorf("creating the worktree at %s: %w", wt, err)
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
