package github_test

import (
	"context"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

// prListArgs is the one gh call a PRs list fetch makes for repo.
func prListArgs(repo github.Repo) []string {
	return []string{"api", "--hostname", repo.HostOrDefault(), "graphql",
		"-f", "query=" + github.PRListQuery, "-f", "owner=" + repo.Owner, "-f", "name=" + repo.Name}
}

// TestPRListParsesRecordedReply: the recorded cli/cli reply becomes six
// rows, newest update first, with the overall check state, the review
// decision, the merge state and the head repository of each.
func TestPRListParsesRecordedReply(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.PRList), prListArgs(cliRepo)...)
	got, err := github.NewClient(fake).PRList(context.Background(), cliRepo)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 59 || len(got.PRs) != 6 {
		t.Fatalf("Total %d, %d PRs; want 59 and 6", got.Total, len(got.PRs))
	}
	first := got.PRs[0]
	want := github.PullRequest{
		Number:     15048,
		Title:      "chore(deps): Bump golang.org/x/term from 0.47.0 to 0.48.0",
		URL:        "https://github.com/cli/cli/pull/15048",
		Author:     "dependabot",
		State:      github.PROpen,
		UpdatedAt:  mustTime(t, "2026-11-13T14:29:44Z"),
		HeadBranch: "dependabot/go_modules/golang.org/x/term-0.48.0",
		BaseBranch: "trunk",
		HeadRepo:   github.Repo{Host: "github.com", Owner: "cli", Name: "cli"},
		Checks:     github.ChecksPassing,
		Review:     github.ReviewRequired,
		MergeState: github.MergeBlocked,
	}
	if !first.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("UpdatedAt = %v, want %v", first.UpdatedAt, want.UpdatedAt)
	}
	first.UpdatedAt = want.UpdatedAt
	if first != want {
		t.Fatalf("first PR =\n%+v\nwant\n%+v", first, want)
	}

	cases := []struct {
		i          int
		number     int
		draft      bool
		checks     github.CheckState
		review     github.ReviewDecision
		merge      github.MergeState
		headRepo   github.Repo
		headBranch string
	}{
		{1, 14927, true, github.ChecksFailing, github.ReviewRequired, github.MergeBlocked, cliRepo, "williammartin-multiaccount-draft-08-evaluation"},
		{2, 15046, false, github.ChecksPassing, github.ReviewRequired, github.MergeBlocked,
			github.Repo{Host: "github.com", Owner: "compiler-segfault", Name: "cli"}, "fix/agent-task-limit-preallocation"},
		{3, 13781, true, github.ChecksNone, github.ReviewRequired, github.MergeDirty,
			github.Repo{Host: "github.com", Owner: "platinummonkey", Name: "gh-cli"}, "feature/context-scoped-accounts"},
		{4, 13665, false, github.ChecksPassing, github.ReviewApproved, github.MergeBlocked, cliRepo, "use-actions-firewall"},
		{5, 13899, false, github.ChecksPassing, github.ReviewChangesRequested, github.MergeBlocked,
			github.Repo{Host: "github.com", Owner: "imkp1", Name: "cli"}, "fix/13804-gh-issue-create-errors-after-successful"},
	}
	for _, c := range cases {
		p := got.PRs[c.i]
		if p.Number != c.number || p.Draft != c.draft || p.Checks != c.checks || p.Review != c.review ||
			p.MergeState != c.merge || p.HeadRepo != c.headRepo || p.HeadBranch != c.headBranch {
			t.Fatalf("PR %d = %+v, want #%d draft=%v checks=%q review=%q merge=%q head %s:%s",
				c.i, p, c.number, c.draft, c.checks, c.review, c.merge, c.headRepo, c.headBranch)
		}
	}
	for i := 1; i < len(got.PRs); i++ {
		if got.PRs[i].UpdatedAt.After(got.PRs[i-1].UpdatedAt) {
			t.Fatalf("PR %d updated after PR %d: not newest first", i, i-1)
		}
	}
}

// TestPRListOneLeanRequest: a fetch is exactly one gh call, the lean
// GraphQL query for the 100 most recently updated open PRs. It asks for
// the overall check state of the head commit only: never the rollup's
// contexts, which make the payload balloon.
func TestPRListOneLeanRequest(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.PRList), prListArgs(cliRepo)...)
	if _, err := github.NewClient(fake).PRList(context.Background(), cliRepo); err != nil {
		t.Fatal(err)
	}
	if n := len(fake.Calls()); n != 1 {
		t.Fatalf("%d gh calls, want 1", n)
	}
	for _, want := range []string{"first:100", "states:OPEN", "orderBy:{field:UPDATED_AT,direction:DESC}",
		"totalCount", "commits(last:1)", "statusCheckRollup{state}", "reviewDecision", "mergeStateStatus"} {
		if !strings.Contains(github.PRListQuery, want) {
			t.Fatalf("query lacks %q: %s", want, github.PRListQuery)
		}
	}
	for _, banned := range []string{"contexts", "checkSuites", "checkRuns", "statusCheckRollup{state "} {
		if strings.Contains(github.PRListQuery, banned) {
			t.Fatalf("the list query asks for %q: %s", banned, github.PRListQuery)
		}
	}
	if n := strings.Count(github.PRListQuery, "statusCheckRollup"); n != 1 {
		t.Fatalf("statusCheckRollup appears %d times, want once (its state only)", n)
	}
}

// TestPRListPayloadSmall: the recorded reply holds about 500 bytes per PR
// (the untrimmed page of 100 was 30 KB), far below the 248 KB that gh pr
// list's full rollup costs.
func TestPRListPayloadSmall(t *testing.T) {
	f := githubtest.MustLoad(t, githubtest.PRList)
	per := len(f.Result.Stdout) / 6
	if per > 600 {
		t.Fatalf("%d bytes per PR in the recorded reply, want at most 600", per)
	}
}

// TestPRListGHE: a GHE Project asks its own host, and the head repository
// carries that host.
func TestPRListGHE(t *testing.T) {
	ghe := github.Repo{Host: "ghe.example.com", Owner: "cli", Name: "cli"}
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.PRList), prListArgs(ghe)...)
	got, err := github.NewClient(fake).PRList(context.Background(), ghe)
	if err != nil {
		t.Fatal(err)
	}
	if h := got.PRs[0].HeadRepo.Host; h != "ghe.example.com" {
		t.Fatalf("head repo host %q, want ghe.example.com", h)
	}
}

// TestPRListNullFields: a deleted author reads as ghost, a deleted head
// repository leaves HeadRepo zero, and a missing rollup or review decision
// reads as none.
func TestPRListNullFields(t *testing.T) {
	reply := `{"data":{"repository":{"pullRequests":{"totalCount":1,"nodes":[{"number":7,"title":"t",` +
		`"url":"u","state":"OPEN","isDraft":false,"updatedAt":"2026-11-13T10:00:00Z","author":null,` +
		`"headRefName":"x","baseRefName":"main","isCrossRepository":true,"headRepository":null,` +
		`"reviewDecision":null,"mergeStateStatus":"UNKNOWN","commits":{"nodes":[]}}]}}}}`
	fake := githubtest.NewFake().Serve(githubtest.Fixture{Result: github.Result{Stdout: []byte(reply)}}, prListArgs(cliRepo)...)
	got, err := github.NewClient(fake).PRList(context.Background(), cliRepo)
	if err != nil {
		t.Fatal(err)
	}
	p := got.PRs[0]
	if p.Author != "ghost" || p.HeadRepo != (github.Repo{}) || p.Checks != github.ChecksNone ||
		p.Review != github.ReviewNone || p.MergeState != github.MergeUnknown {
		t.Fatalf("PR = %+v", p)
	}
}

// TestCheckStateOf: GitHub's rollup states map onto the four glyph states.
func TestCheckStateOf(t *testing.T) {
	for in, want := range map[string]github.CheckState{
		"SUCCESS":  github.ChecksPassing,
		"FAILURE":  github.ChecksFailing,
		"ERROR":    github.ChecksFailing,
		"PENDING":  github.ChecksRunning,
		"EXPECTED": github.ChecksRunning,
		"":         github.ChecksNone,
	} {
		if got := github.CheckStateOf(in); got != want {
			t.Errorf("CheckStateOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPRListFailures(t *testing.T) {
	for _, c := range []struct {
		fixture string
		class   github.Class
	}{
		{githubtest.NotFound, github.ClassNotFound},
		{githubtest.BadToken, github.ClassBadToken},
		{githubtest.GhMissing, github.ClassGhMissing},
		{githubtest.OK, ""},
	} {
		t.Run(c.fixture, func(t *testing.T) {
			fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, c.fixture), prListArgs(cliRepo)...)
			got, err := github.NewClient(fake).PRList(context.Background(), cliRepo)
			if err == nil {
				t.Fatalf("no error; got %+v", got)
			}
			if c.class != "" && github.ClassOf(err) != c.class {
				t.Fatalf("class = %q, want %q (%v)", github.ClassOf(err), c.class, err)
			}
		})
	}
}
