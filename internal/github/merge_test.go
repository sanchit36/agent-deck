package github_test

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/ghexec"
	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

// mergeInfoArgs is the one gh call a merge dialog's refetch makes.
func mergeInfoArgs(repo github.Repo, number int) []string {
	return []string{"api", "--hostname", repo.HostOrDefault(), "graphql",
		"-f", "query=" + github.MergeInfoQuery, "-f", "owner=" + repo.Owner, "-f", "name=" + repo.Name,
		"-F", "number=" + strconv.Itoa(number)}
}

func mergeInfo(t *testing.T, fixture string, number int) github.MergeInfo {
	t.Helper()
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, fixture), mergeInfoArgs(cliRepo, number)...)
	info, err := github.NewClient(fake).MergeInfo(context.Background(), cliRepo, number)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// TestMergeInfoParsesRecordedPR: the recorded cli/cli PR #15048 becomes a
// MergeInfo with its head SHA, branches, check counts, review and merge
// state, and the repository's merge settings.
func TestMergeInfoParsesRecordedPR(t *testing.T) {
	info := mergeInfo(t, githubtest.MergeInfoBlockedReview, 15048)
	if info.Number != 15048 || info.Title != "chore(deps): Bump golang.org/x/term from 0.47.0 to 0.48.0" ||
		info.URL != "https://github.com/cli/cli/pull/15048" || info.State != github.PROpen || info.Draft {
		t.Fatalf("info = %+v", info)
	}
	if info.HeadSHA != "f2b0c5d5e51351be27ca04e8b27f6be552d8de1e" || info.BaseBranch != "trunk" ||
		info.HeadBranch != "dependabot/go_modules/golang.org/x/term-0.48.0" || info.CrossRepo ||
		info.HeadRepo != (github.Repo{Host: "github.com", Owner: "cli", Name: "cli"}) {
		t.Fatalf("head/base = %+v", info)
	}
	if info.Checks != github.ChecksPassing || info.CheckCounts.Passed == 0 || info.CheckCounts.Failed != 0 ||
		info.Review != github.ReviewRequired || info.MergeState != github.MergeBlocked {
		t.Fatalf("checks %v %+v review %v merge %v", info.Checks, info.CheckCounts, info.Review, info.MergeState)
	}
	want := []github.MergeMethod{github.MethodMerge, github.MethodSquash, github.MethodRebase}
	if !slices.Equal(info.Methods, want) || info.DefaultMethod != github.MethodMerge {
		t.Fatalf("methods %v default %v", info.Methods, info.DefaultMethod)
	}
	if !info.DeleteBranchOnMerge || !info.AutoMergeAllowed || info.AutoMerge || info.MergeQueue || info.InMergeQueue {
		t.Fatalf("settings %+v", info)
	}
	if got := info.CommitTitle(github.MethodSquash); got != "chore(deps): Bump golang.org/x/term from 0.47.0 to 0.48.0 (#15048)" {
		t.Fatalf("squash title %q", got)
	}
	if got := info.CommitTitle(github.MethodRebase); got != "" {
		t.Fatalf("a rebase has no commit title, got %q", got)
	}
}

// TestMergeInfoAllowedMethodsOnly: the chooser offers only the methods the
// repository allows, and a viewer default that is not allowed falls back
// to the first allowed one.
func TestMergeInfoAllowedMethodsOnly(t *testing.T) {
	info := mergeInfo(t, githubtest.MergeInfoClean, 15048)
	if want := []github.MergeMethod{github.MethodSquash, github.MethodRebase}; !slices.Equal(info.Methods, want) {
		t.Fatalf("methods %v, want %v", info.Methods, want)
	}
	if info.DefaultMethod != github.MethodSquash {
		t.Fatalf("default %v", info.DefaultMethod)
	}
}

// TestMergeInfoForkPR: a PR from a fork is cross-repository, with the
// fork as its head repository.
func TestMergeInfoForkPR(t *testing.T) {
	info := mergeInfo(t, githubtest.MergeInfoClosedFork, 15046)
	if !info.CrossRepo || info.HeadRepo.Owner != "compiler-segfault" || info.State != github.PRClosed {
		t.Fatalf("info = %+v", info)
	}
}

// TestGuardMerge is the merge-guard table of spec §14 over each
// mergeStateStatus, plus the PR states, auto-merge and the merge queue.
// No row ever offers an admin bypass: a blocked PR is blocked.
func TestGuardMerge(t *testing.T) {
	open := github.MergeInfo{State: github.PROpen, Methods: []github.MergeMethod{github.MethodSquash},
		DefaultMethod: github.MethodSquash, AutoMergeAllowed: true, Checks: github.ChecksPassing,
		Review: github.ReviewApproved, MergeState: github.MergeClean, BaseBranch: "main"}
	with := func(f func(*github.MergeInfo)) github.MergeInfo {
		i := open
		f(&i)
		return i
	}
	cases := []struct {
		name    string
		info    github.MergeInfo
		action  github.MergeAction
		blocked string
		warning string
		refetch bool
	}{
		{name: "clean merges", info: open, action: github.ActionMerge},
		{name: "has hooks merges", info: with(func(i *github.MergeInfo) { i.MergeState = github.MergeHasHooks }), action: github.ActionMerge},
		{name: "unstable merges with a warning", info: with(func(i *github.MergeInfo) {
			i.MergeState, i.Checks = github.MergeUnstable, github.ChecksFailing
		}), action: github.ActionMerge, warning: "some checks are failing"},
		{name: "draft is blocked", info: with(func(i *github.MergeInfo) { i.MergeState, i.Draft = github.MergeDraft, true }),
			blocked: "draft: mark it ready on GitHub"},
		{name: "dirty is blocked and suggests a session", info: with(func(i *github.MergeInfo) { i.MergeState = github.MergeDirty }),
			blocked: "conflicts with main: start a session (n) to resolve them"},
		{name: "behind is blocked", info: with(func(i *github.MergeInfo) { i.MergeState = github.MergeBehind }),
			blocked: "behind main: update the branch first"},
		{name: "blocked on review enables auto-merge", info: with(func(i *github.MergeInfo) {
			i.MergeState, i.Review = github.MergeBlocked, github.ReviewRequired
		}), action: github.ActionAutoMerge},
		{name: "blocked on running checks enables auto-merge", info: with(func(i *github.MergeInfo) {
			i.MergeState, i.Checks = github.MergeBlocked, github.ChecksRunning
		}), action: github.ActionAutoMerge},
		{name: "blocked on review without auto-merge is blocked", info: with(func(i *github.MergeInfo) {
			i.MergeState, i.Review, i.AutoMergeAllowed = github.MergeBlocked, github.ReviewRequired, false
		}), blocked: "waiting on checks or review; auto-merge is off for this repository"},
		{name: "blocked by other rules is blocked", info: with(func(i *github.MergeInfo) { i.MergeState = github.MergeBlocked }),
			blocked: "blocked by branch protection"},
		{name: "unknown refetches", info: with(func(i *github.MergeInfo) { i.MergeState = github.MergeUnknown }),
			blocked: "GitHub is still checking whether it can merge: try again", refetch: true},
		{name: "auto-merge on disables it", info: with(func(i *github.MergeInfo) {
			i.AutoMerge, i.MergeState, i.Review = true, github.MergeBlocked, github.ReviewRequired
		}), action: github.ActionDisableAuto},
		{name: "merge queue adds to it", info: with(func(i *github.MergeInfo) {
			i.MergeQueue, i.MergeState, i.Checks = true, github.MergeBlocked, github.ChecksRunning
		}), action: github.ActionQueue},
		{name: "merge queue still refuses conflicts", info: with(func(i *github.MergeInfo) {
			i.MergeQueue, i.MergeState = true, github.MergeDirty
		}), blocked: "conflicts with main: start a session (n) to resolve them"},
		{name: "in the merge queue is blocked", info: with(func(i *github.MergeInfo) { i.MergeQueue, i.InMergeQueue = true, true }),
			blocked: "already in the merge queue"},
		{name: "merged is blocked", info: with(func(i *github.MergeInfo) { i.State = github.PRMerged }), blocked: "already merged"},
		{name: "closed is blocked", info: with(func(i *github.MergeInfo) { i.State = github.PRClosed }), blocked: "closed"},
		{name: "no allowed method is blocked", info: with(func(i *github.MergeInfo) { i.Methods, i.DefaultMethod = nil, "" }),
			blocked: "the repository allows no merge method"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := github.GuardMerge(c.info)
			if g.Action != c.action || g.Blocked != c.blocked || g.Warning != c.warning || g.Refetch != c.refetch {
				t.Fatalf("GuardMerge = %+v, want action %q blocked %q warning %q refetch %v",
					g, c.action, c.blocked, c.warning, c.refetch)
			}
			if (g.Action == "") == (g.Blocked == "") {
				t.Fatalf("a guard either allows an action or says why not: %+v", g)
			}
			if strings.Contains(g.Blocked+g.Warning, "admin") {
				t.Fatalf("guard text offers an admin bypass: %+v", g)
			}
		})
	}
}

// TestGuardMergeRecorded: the recorded PRs land where the table says:
// #15048 (blocked on review) offers auto-merge, the draft and the closed
// fork PR are blocked.
func TestGuardMergeRecorded(t *testing.T) {
	if g := github.GuardMerge(mergeInfo(t, githubtest.MergeInfoBlockedReview, 15048)); g.Action != github.ActionAutoMerge {
		t.Fatalf("#15048: %+v", g)
	}
	if g := github.GuardMerge(mergeInfo(t, githubtest.MergeInfoDraft, 14927)); g.Blocked != "draft: mark it ready on GitHub" {
		t.Fatalf("#14927: %+v", g)
	}
	if g := github.GuardMerge(mergeInfo(t, githubtest.MergeInfoClosedFork, 15046)); g.Blocked != "closed" {
		t.Fatalf("#15046: %+v", g)
	}
	if g := github.GuardMerge(mergeInfo(t, githubtest.MergeInfoClean, 15048)); g.Action != github.ActionMerge {
		t.Fatalf("clean: %+v", g)
	}
}

const shownSHA = "f2b0c5d5e51351be27ca04e8b27f6be552d8de1e"

// TestMergeArgs: each action is one `gh pr merge` call on the repository,
// with the merge timeout; every call that can merge is pinned to the shown
// head SHA, and none ever passes --admin.
func TestMergeArgs(t *testing.T) {
	cases := []struct {
		name string
		req  github.MergeRequest
		want []string
	}{
		{"squash", github.MergeRequest{Number: 15048, HeadSHA: shownSHA, Action: github.ActionMerge, Method: github.MethodSquash},
			[]string{"pr", "merge", "15048", "--squash", "--match-head-commit", shownSHA}},
		{"merge and delete the branch", github.MergeRequest{Number: 15048, HeadSHA: shownSHA, Action: github.ActionMerge,
			Method: github.MethodMerge, DeleteBranch: true},
			[]string{"pr", "merge", "15048", "--merge", "--match-head-commit", shownSHA, "--delete-branch"}},
		{"auto-merge", github.MergeRequest{Number: 15048, HeadSHA: shownSHA, Action: github.ActionAutoMerge, Method: github.MethodRebase},
			[]string{"pr", "merge", "15048", "--auto", "--rebase", "--match-head-commit", shownSHA}},
		{"merge queue", github.MergeRequest{Number: 15048, HeadSHA: shownSHA, Action: github.ActionQueue},
			[]string{"pr", "merge", "15048", "--auto", "--match-head-commit", shownSHA}},
		{"disable auto-merge", github.MergeRequest{Number: 15048, Action: github.ActionDisableAuto},
			[]string{"pr", "merge", "15048", "--disable-auto"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := append(c.want, "-R", "github.com/cli/cli")
			fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.OK), want...)
			if err := github.NewClient(fake).Merge(context.Background(), cliRepo, c.req); err != nil {
				t.Fatal(err)
			}
			calls := fake.Calls()
			if len(calls) != 1 || !slices.Equal(calls[0].Args, want) {
				t.Fatalf("calls %+v, want one with %q", calls, want)
			}
			if calls[0].Timeout != ghexec.MergeTimeout {
				t.Fatalf("timeout %v, want %v", calls[0].Timeout, ghexec.MergeTimeout)
			}
			if slices.Contains(calls[0].Args, "--admin") {
				t.Fatal("merge passed --admin")
			}
		})
	}
}

// TestMergeNeedsShownHead: a merge without the shown head SHA, or without
// a method where one is needed, never reaches gh.
func TestMergeNeedsShownHead(t *testing.T) {
	for _, req := range []github.MergeRequest{
		{Number: 1, Action: github.ActionMerge, Method: github.MethodSquash},
		{Number: 1, Action: github.ActionAutoMerge, Method: github.MethodSquash},
		{Number: 1, Action: github.ActionQueue},
		{Number: 1, HeadSHA: shownSHA, Action: github.ActionMerge},
		{Number: 1, HeadSHA: shownSHA, Action: "admin"},
	} {
		fake := githubtest.NewFake()
		if err := github.NewClient(fake).Merge(context.Background(), cliRepo, req); err == nil {
			t.Fatalf("%+v: no error", req)
		}
		if n := len(fake.Calls()); n != 0 {
			t.Fatalf("%+v: %d gh calls, want none", req, n)
		}
	}
}

// TestMergeFailures: gh's merge failures from the fixtures. A moved head
// is told apart (the dialog refreshes); the other refusals read as gh's
// first line, never with gh's --admin hint.
func TestMergeFailures(t *testing.T) {
	req := github.MergeRequest{Number: 15048, HeadSHA: shownSHA, Action: github.ActionMerge, Method: github.MethodMerge}
	cases := []struct {
		fixture string
		moved   bool
		text    string
	}{
		{githubtest.MergeHeadMoved, true, "Head branch was modified. Review and try the merge again. (mergePullRequest)"},
		{githubtest.MergeNotMergeable, false, "Pull request cli/cli#15048 is not mergeable: the base branch policy prohibits the merge."},
		{githubtest.MergeMethodNotAllowed, false, "Merge commits are not allowed on this repository. (mergePullRequest)"},
		{githubtest.Timeout, false, "gh did not answer within 1m0s: GitHub may still merge it"},
		{githubtest.NotLoggedIn, false, "not logged in"},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			fake := githubtest.NewFake().ServeFunc(githubtest.MustLoad(t, c.fixture), func([]string) bool { return true })
			err := github.NewClient(fake).Merge(context.Background(), cliRepo, req)
			if err == nil {
				t.Fatal("no error")
			}
			if got := github.MergeHeadMoved(err); got != c.moved {
				t.Fatalf("MergeHeadMoved = %v, want %v (%v)", got, c.moved, err)
			}
			text := github.MergeErrorText(err)
			if !strings.Contains(text, c.text) {
				t.Fatalf("MergeErrorText = %q, want it to hold %q", text, c.text)
			}
			if strings.Contains(text, "admin") || strings.HasPrefix(text, "X ") {
				t.Fatalf("MergeErrorText = %q", text)
			}
		})
	}
}

// TestMergeSmokeFakeGh: the merge runs the gh on PATH (a fake gh script
// serving the recorded fixtures) with the gh child env and the exact merge
// arguments, and a head that moved comes back as that error.
func TestMergeSmokeFakeGh(t *testing.T) {
	want := []string{"pr", "merge", "15048", "--squash", "--match-head-commit", shownSHA, "--delete-branch", "-R", "github.com/cli/cli"}
	req := github.MergeRequest{Number: 15048, HeadSHA: shownSHA, Action: github.ActionMerge, Method: github.MethodSquash, DeleteBranch: true}
	t.Run("merged", func(t *testing.T) {
		gh := githubtest.InstallFakeGhScript(t, githubtest.ScriptRoute{Fixture: githubtest.OK, Args: []string{"merge", "15048"}})
		if err := github.NewClient(nil).Merge(context.Background(), cliRepo, req); err != nil {
			t.Fatal(err)
		}
		calls := gh.Calls()
		if len(calls) != 1 || calls[0].Env != "111" || !slices.Equal(calls[0].Args, want) {
			t.Fatalf("gh saw %+v, want env 111 and %q", calls, want)
		}
	})
	t.Run("head moved", func(t *testing.T) {
		githubtest.InstallFakeGhScript(t, githubtest.ScriptRoute{Fixture: githubtest.MergeHeadMoved, Args: []string{"merge", "15048"}})
		err := github.NewClient(nil).Merge(context.Background(), cliRepo, req)
		if !github.MergeHeadMoved(err) {
			t.Fatalf("err %v, want the head-moved error", err)
		}
	})
}
