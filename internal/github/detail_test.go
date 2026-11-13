package github_test

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

// detailArgs is the one gh call a detail fetch makes for repo's number.
func detailArgs(repo github.Repo, number int) []string {
	return []string{"api", "--hostname", repo.HostOrDefault(), "graphql",
		"-f", "query=" + github.DetailQuery, "-f", "owner=" + repo.Owner, "-f", "name=" + repo.Name,
		"-F", "number=" + strconv.Itoa(number)}
}

// TestDetailParsesRecordedIssue: the recorded cli/cli Issue #14822 becomes
// an IssueDetail with its labels, comment count and a newest-first
// timeline of its comments.
func TestDetailParsesRecordedIssue(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.IssueDetail), detailArgs(cliRepo, 14822)...)
	got, err := github.NewClient(fake).Detail(context.Background(), cliRepo, 14822)
	if err != nil {
		t.Fatal(err)
	}
	is, ok := got.(github.IssueDetail)
	if !ok {
		t.Fatalf("Detail = %T, want github.IssueDetail", got)
	}
	if is.Number != 14822 || is.Title != "gh run view --job --log-failed fails on required shared-workflow metadata 404" ||
		is.URL != "https://github.com/cli/cli/issues/14822" || is.Author != "henrymodisett" || is.Closed {
		t.Fatalf("issue = %+v", is)
	}
	if !slices.Equal(is.Labels, []string{"bug", "priority-3", "gh-run"}) || len(is.Assignees) != 0 || is.Comments != 5 {
		t.Fatalf("labels %v, assignees %v, comments %d", is.Labels, is.Assignees, is.Comments)
	}
	if !is.CreatedAt.Equal(mustTime(t, "2026-10-26T04:50:39Z")) || !got.Version().Equal(mustTime(t, "2026-11-13T13:44:15Z")) {
		t.Fatalf("created %v, version %v", is.CreatedAt, got.Version())
	}
	if len(is.Timeline) != 5 {
		t.Fatalf("timeline has %d events, want 5: %+v", len(is.Timeline), is.Timeline)
	}
	first, last := is.Timeline[0], is.Timeline[4]
	if first.Kind != github.EventComment || first.Actor != "henrymodisett" || !first.At.Equal(mustTime(t, "2026-11-13T13:44:15Z")) {
		t.Fatalf("newest event = %+v", first)
	}
	if last.Actor != "cli-triage" || !last.At.Equal(mustTime(t, "2026-10-26T04:55:38Z")) {
		t.Fatalf("oldest event = %+v", last)
	}
}

// TestDetailParsesRecordedPR: the recorded cli/cli PR #14927 (a draft
// with one failing check) becomes a PRDetail with its branches, head SHA,
// check counts, review and merge state, diff size, and a newest-first
// timeline: the review, the checks, then its four commits as one push.
func TestDetailParsesRecordedPR(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.PRDetail), detailArgs(cliRepo, 14927)...)
	got, err := github.NewClient(fake).Detail(context.Background(), cliRepo, 14927)
	if err != nil {
		t.Fatal(err)
	}
	pr, ok := got.(github.PRDetail)
	if !ok {
		t.Fatalf("Detail = %T, want github.PRDetail", got)
	}
	if pr.Number != 14927 || pr.Title != "Add guided multi-account setup and evaluation" || pr.Author != "williammartin" ||
		pr.State != github.PROpen || !pr.Draft {
		t.Fatalf("pr = %+v", pr)
	}
	if pr.HeadBranch != "williammartin-multiaccount-draft-08-evaluation" || pr.BaseBranch != "williammartin-multiaccount-draft-07-explain" ||
		pr.HeadSHA != "d43c4e3ad3c34332c803a568f85f116e3ee58f19" || pr.HeadRepo != cliRepo {
		t.Fatalf("branches %q -> %q at %q in %v", pr.HeadBranch, pr.BaseBranch, pr.HeadSHA, pr.HeadRepo)
	}
	if pr.Checks != github.ChecksFailing || pr.CheckCounts != (github.CheckCounts{Passed: 11, Failed: 1, Skipped: 13}) {
		t.Fatalf("checks %q %+v", pr.Checks, pr.CheckCounts)
	}
	if pr.Review != github.ReviewRequired || pr.MergeState != github.MergeBlocked {
		t.Fatalf("review %q merge %q", pr.Review, pr.MergeState)
	}
	if pr.Additions != 4835 || pr.Deletions != 1 || pr.ChangedFiles != 21 || pr.Comments != 0 {
		t.Fatalf("diff +%d -%d %d files, %d comments", pr.Additions, pr.Deletions, pr.ChangedFiles, pr.Comments)
	}
	if !got.Version().Equal(mustTime(t, "2026-11-13T12:06:21Z")) {
		t.Fatalf("version %v", got.Version())
	}
	kinds := make([]github.EventKind, 0, len(pr.Timeline))
	for _, e := range pr.Timeline {
		kinds = append(kinds, e.Kind)
	}
	if want := []github.EventKind{github.EventChecks, github.EventReview, github.EventPush}; !slices.Equal(kinds, want) {
		t.Fatalf("timeline kinds %v, want %v: %+v", kinds, want, pr.Timeline)
	}
	review, push := pr.Timeline[1], pr.Timeline[2]
	if review.Actor != "copilot-pull-request-reviewer" || review.Review != "COMMENTED" {
		t.Fatalf("review event = %+v", review)
	}
	if push.Actor != "williammartin" || push.Count != 4 || !push.At.Equal(mustTime(t, "2026-11-03T21:19:35Z")) {
		t.Fatalf("push event = %+v", push)
	}
	if checks := pr.Timeline[0]; checks.Checks != github.ChecksFailing || checks.At.IsZero() {
		t.Fatalf("checks event = %+v", checks)
	}
}

// TestDetailTimelineEvents: every timeline item the query asks for maps to
// its event; consecutive commits fold into one push and the list is
// newest first.
func TestDetailTimelineEvents(t *testing.T) {
	reply := `{"data":{"repository":{"issueOrPullRequest":{"__typename":"PullRequest","number":7,"title":"t","url":"u",` +
		`"prState":"MERGED","isDraft":false,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-09T00:00:00Z","author":null,` +
		`"labels":{"nodes":[]},"assignees":{"nodes":[{"login":"zoe"}]},"comments":{"totalCount":1},"headRefName":"h","baseRefName":"main",` +
		`"headRefOid":"abc","headRepository":null,"reviewDecision":null,"mergeStateStatus":"UNKNOWN","additions":1,"deletions":2,"changedFiles":3,` +
		`"commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]},"timelineItems":{"nodes":[` +
		`{"__typename":"PullRequestCommit","commit":{"committedDate":"2026-01-02T00:00:00Z","author":{"user":{"login":"amy"}}}},` +
		`{"__typename":"PullRequestCommit","commit":{"committedDate":"2026-01-02T01:00:00Z","author":{"user":{"login":"amy"}}}},` +
		`{"__typename":"IssueComment","createdAt":"2026-01-03T00:00:00Z","author":{"login":"bob"}},` +
		`{"__typename":"PullRequestCommit","commit":{"committedDate":"2026-01-04T00:00:00Z","author":{"user":{"login":"amy"}}}},` +
		`{"__typename":"HeadRefForcePushedEvent","createdAt":"2026-01-05T00:00:00Z","actor":{"login":"amy"}},` +
		`{"__typename":"ReadyForReviewEvent","createdAt":"2026-01-06T00:00:00Z","actor":{"login":"amy"}},` +
		`{"__typename":"PullRequestReview","reviewState":"CHANGES_REQUESTED","submittedAt":"2026-01-07T00:00:00Z","author":{"login":"cat"}},` +
		`{"__typename":"ClosedEvent","createdAt":"2026-01-08T00:00:00Z","actor":{"login":"amy"}},` +
		`{"__typename":"ReopenedEvent","createdAt":"2026-01-08T01:00:00Z","actor":{"login":"amy"}},` +
		`{"__typename":"MergedEvent","createdAt":"2026-01-09T00:00:00Z","actor":null}]}}}}}`
	repo := github.Repo{Host: "github.com", Owner: "me", Name: "widgets"}
	fake := githubtest.NewFake().Serve(githubtest.Fixture{Result: github.Result{Stdout: []byte(reply)}}, detailArgs(repo, 7)...)
	got, err := github.NewClient(fake).Detail(context.Background(), repo, 7)
	if err != nil {
		t.Fatal(err)
	}
	pr := got.(github.PRDetail)
	if pr.Author != "ghost" || pr.State != github.PRMerged || pr.HeadRepo != (github.Repo{}) || pr.Checks != github.ChecksNone ||
		!slices.Equal(pr.Assignees, []string{"zoe"}) {
		t.Fatalf("pr = %+v", pr)
	}
	type ev struct {
		kind  github.EventKind
		actor string
		count int
	}
	var evs []ev
	for _, e := range pr.Timeline {
		evs = append(evs, ev{e.Kind, e.Actor, e.Count})
	}
	want := []ev{
		{github.EventMerged, "ghost", 0},
		{github.EventReopened, "amy", 0},
		{github.EventClosed, "amy", 0},
		{github.EventReview, "cat", 0},
		{github.EventReady, "amy", 0},
		{github.EventForcePush, "amy", 0},
		{github.EventPush, "amy", 1},
		{github.EventComment, "bob", 0},
		{github.EventPush, "amy", 2},
	}
	if !slices.Equal(evs, want) {
		t.Fatalf("timeline\n%v\nwant\n%v", evs, want)
	}
}

// TestDetailNotFound: a number that is neither an Issue nor a PR (GitHub
// answers with data and a null item) is a not-found error.
func TestDetailNotFound(t *testing.T) {
	repo := github.Repo{Host: "github.com", Owner: "me", Name: "widgets"}
	reply := `{"data":{"repository":{"issueOrPullRequest":null}}}`
	fake := githubtest.NewFake().Serve(githubtest.Fixture{Result: github.Result{Stdout: []byte(reply)}}, detailArgs(repo, 9)...)
	_, err := github.NewClient(fake).Detail(context.Background(), repo, 9)
	if github.ClassOf(err) != github.ClassNotFound {
		t.Fatalf("err = %v (class %q), want not found", err, github.ClassOf(err))
	}
}
