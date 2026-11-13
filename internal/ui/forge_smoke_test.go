package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// End-to-end smoke tests (spec #18 §17.3): the real gh runner runs a fake
// gh script on PATH that serves the recorded fixtures, one test per user
// flow (open a tab, launch; merge is internal/github's
// TestMergeSmokeFakeGh). The agent start is replaced, so no tmux and no
// agent CLI run.

// Routes of the fake gh, by the arguments that tell each call apart.
func issueListRoute() githubtest.ScriptRoute {
	return githubtest.ScriptRoute{Fixture: githubtest.IssueList, Args: []string{"query=" + github.IssueListQuery, "owner=me", "name=widgets"}}
}

func prListRoute() githubtest.ScriptRoute {
	return githubtest.ScriptRoute{Fixture: githubtest.PRList, Args: []string{"query=" + github.PRListQuery, "owner=me", "name=widgets"}}
}

func detailRoute(fixture string, number string) githubtest.ScriptRoute {
	return githubtest.ScriptRoute{Fixture: fixture, Args: []string{"query=" + github.DetailQuery, "number=" + number}}
}

func itemTextRoute(fixture string) githubtest.ScriptRoute {
	return githubtest.ScriptRoute{Fixture: fixture, Args: []string{"query=" + github.ItemTextQuery}}
}

// smokeHome is a gate-on Home whose gh is the one on PATH (github.NewClient
// with the real runner) and whose checkouts are local.
func smokeHome(t *testing.T, local map[string]github.Local) *Home {
	t.Helper()
	home := forgeFrameHome(t, 120, 40, false, gateOn)
	home.forge.client = github.NewClient(nil)
	home.forge.resolver = github.NewResolver(home.forge.client, forgeLocal(local))
	home.forge.fetchers = defaultForgeFetchers()
	home.forge.now = func() time.Time { return issuesT0 }
	home.forge.interval = time.Minute
	home.forge.detailDelay = -1
	return home
}

// callsWith are the fake gh's calls holding every one of args.
func callsWith(gh *githubtest.FakeGhScript, args ...string) []githubtest.ScriptCall {
	var out []githubtest.ScriptCall
	for _, c := range gh.Calls() {
		if !slices.ContainsFunc(args, func(a string) bool { return !slices.Contains(c.Args, a) }) {
			out = append(out, c)
		}
	}
	return out
}

// TestForgeOpenTabSmokeFakeGh: alt+i opens the Issues tab, whose list and
// selected row's detail come from gh on PATH with the gh child env.
func TestForgeOpenTabSmokeFakeGh(t *testing.T) {
	gh := githubtest.InstallFakeGhScript(t, issueListRoute(), detailRoute(githubtest.IssueDetail, "14822"))
	h := launchDrive(t, smokeHome(t, widgetsCheckout), altKeyMsg('i'))
	frame := squash(stripAnsi(h.View()))
	for _, want := range []string{"ghrunview--job--log-failedfails", "#14822", "#8954"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the Issues tab lacks %q:\n%s", want, stripAnsi(h.View()))
		}
	}
	list := callsWith(gh, "query="+github.IssueListQuery)
	if len(list) != 1 || list[0].Env != "111" {
		t.Fatalf("issue list calls %+v, want one with the gh child env", list)
	}
	if len(callsWith(gh, "query="+github.DetailQuery, "number=14822")) != 1 {
		t.Fatalf("no detail fetch for the selected row in %+v", gh.Calls())
	}
}

// TestForgeLaunchIssueSmokeFakeGh: n then enter on an Issue fills the
// template from gh on PATH and starts the session in a new worktree on the
// issue branch, Linked to the Issue.
func TestForgeLaunchIssueSmokeFakeGh(t *testing.T) {
	gh := githubtest.InstallFakeGhScript(t, issueListRoute(), detailRoute(githubtest.IssueDetail, "14822"), itemTextRoute(githubtest.ItemTextIssue))
	repo, local := launchRepoCheckout(t)
	h := launchDrive(t, smokeHome(t, local), altKeyMsg('i'))
	var sent []string
	h.forge.launcher = forgeLauncher{
		start:   func(_ *session.Instance, m string) error { sent = append(sent, m); return nil },
		running: func(*session.Instance) bool { return true },
	}
	h = launchDrive(t, h, runeKey('n'))
	h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyCtrlS})
	if h.newDialog.IsVisible() {
		t.Fatalf("dialog still open: %s", h.newDialog.validationErr)
	}
	inst := findLaunched(t, h, session.LinkIssue, 14822)
	if inst.WorktreeRepoRoot != repo || inst.WorktreeBranch != "feature/14822-gh-run-view-job-log-failed-fails-on" {
		t.Fatalf("worktree %q branch %q", inst.WorktreeRepoRoot, inst.WorktreeBranch)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "Pressing n on an empty list crashes.") || !strings.Contains(sent[0], "@alice (2026-11-10)") {
		t.Fatalf("first message %q", sent)
	}
	if len(callsWith(gh, "query="+github.ItemTextQuery, "comments=true")) != 1 {
		t.Fatalf("no item text fetch in %+v", gh.Calls())
	}
}

// TestForgeLaunchPRSmokeFakeGh: n then enter on a PR runs gh pr checkout
// (on PATH) inside a new worktree detached at the PR base, and Links the
// session to the PR.
func TestForgeLaunchPRSmokeFakeGh(t *testing.T) {
	head := "williammartin-multiaccount-draft-08-evaluation"
	gh := githubtest.InstallFakeGhScript(t, prListRoute(), detailRoute(githubtest.PRDetail, "14927"), itemTextRoute(githubtest.ItemTextPR),
		githubtest.ScriptRoute{Fixture: githubtest.OK, Args: []string{"pr", "checkout", "14927"}, Then: "git checkout -q -b " + head})
	repo, local := launchRepoCheckout(t)
	gitMustUI(t, repo, "update-ref", "refs/remotes/origin/williammartin-multiaccount-draft-07-explain", "HEAD")
	h := launchDrive(t, smokeHome(t, local), altKeyMsg('p'))
	h = launchDrive(t, h, runeKey('j'))
	h.forge.launcher = forgeLauncher{
		start:   func(*session.Instance, string) error { return nil },
		running: func(*session.Instance) bool { return true },
	}
	h = launchDrive(t, h, runeKey('n'))
	h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyCtrlS})
	if h.newDialog.IsVisible() {
		t.Fatalf("dialog still open: %s", h.newDialog.validationErr)
	}
	inst := findLaunched(t, h, session.LinkPR, 14927)
	if inst.WorktreeBranch != head || inst.WorktreePath == repo {
		t.Fatalf("branch %q worktree %q", inst.WorktreeBranch, inst.WorktreePath)
	}
	co := callsWith(gh, "pr", "checkout", "14927")
	if len(co) != 1 || co[0].Dir != inst.WorktreePath || co[0].Env != "111" || !slices.Contains(co[0].Args, "github.com/me/widgets") {
		t.Fatalf("gh pr checkout calls %+v, want one in %s", co, inst.WorktreePath)
	}
}
