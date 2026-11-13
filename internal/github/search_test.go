package github_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

// searchArgs is the one gh call a search of repo makes for search string q.
func searchArgs(repo github.Repo, query, q string) []string {
	return []string{"api", "--hostname", repo.HostOrDefault(), "graphql", "-f", "query=" + query, "-f", "q=" + q}
}

// TestSearchQueryScoped: a filter runs scoped to the Project and the
// list's kind, newest update first; the filter cannot widen the scope or
// switch the kind, and a filter that sorts keeps its own order.
func TestSearchQueryScoped(t *testing.T) {
	for _, c := range []struct {
		kind, filter, want string
	}{
		{"issue", "is:closed label:bug", "repo:cli/cli is:issue is:closed label:bug sort:updated-desc"},
		{"pr", "is:open draft:true", "repo:cli/cli is:pr is:open draft:true sort:updated-desc"},
		{"issue", "is:open crash", "repo:cli/cli is:issue is:open crash sort:updated-desc"},
		{"issue", "repo:other/x org:other user:me is:open", "repo:cli/cli is:issue is:open sort:updated-desc"},
		{"issue", "is:pr is:open type:pr", "repo:cli/cli is:issue is:open sort:updated-desc"},
		{"pr", "is:merged sort:created-asc", "repo:cli/cli is:pr is:merged sort:created-asc"},
		{"issue", `label:"good first issue"`, `repo:cli/cli is:issue label:"good first issue" sort:updated-desc`},
	} {
		if got := github.SearchQuery(cliRepo, c.kind, c.filter); got != c.want {
			t.Errorf("SearchQuery(%s, %q) = %q, want %q", c.kind, c.filter, got, c.want)
		}
	}
}

// TestSearchIssuesParsesRecordedReply: the recorded search becomes rows
// like the default list's, with GitHub's match count as the total, in one
// gh call.
func TestSearchIssuesParsesRecordedReply(t *testing.T) {
	q := "repo:cli/cli is:issue is:closed label:bug sort:updated-desc"
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.IssueSearch), searchArgs(cliRepo, github.IssueSearchQuery, q)...)
	got, err := github.NewClient(fake).SearchIssues(context.Background(), cliRepo, "is:closed label:bug")
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 2002 || len(got.Issues) != 4 {
		t.Fatalf("Total %d, %d issues; want 2002 and 4", got.Total, len(got.Issues))
	}
	first := got.Issues[0]
	if first.Number != 14053 || first.Author != "MumuTW" || !first.Closed || len(first.Labels) != 3 ||
		first.URL != "https://github.com/cli/cli/issues/14053" {
		t.Fatalf("first issue = %+v", first)
	}
	if n := len(fake.Calls()); n != 1 {
		t.Fatalf("%d gh calls, want 1", n)
	}
}

// TestSearchPRsParsesRecordedReply: PR search rows carry the same state as
// the default list's: merged state, checks, review and a fork's head repo.
func TestSearchPRsParsesRecordedReply(t *testing.T) {
	q := "repo:cli/cli is:pr is:merged sort:updated-desc"
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.PRSearch), searchArgs(cliRepo, github.PRSearchQuery, q)...)
	got, err := github.NewClient(fake).SearchPRs(context.Background(), cliRepo, "is:merged")
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 3359 || len(got.PRs) != 4 {
		t.Fatalf("Total %d, %d PRs; want 3359 and 4", got.Total, len(got.PRs))
	}
	if p := got.PRs[0]; p.Number != 14972 || p.State != github.PRMerged || p.Checks != github.ChecksPassing || p.Review != github.ReviewApproved {
		t.Fatalf("first PR = %+v", p)
	}
	fork := got.PRs[1]
	if fork.HeadRepo.Owner != "mateusmarquezini" || fork.Checks != github.ChecksNone {
		t.Fatalf("fork PR = %+v", fork)
	}
}

// TestSearchListQueriesLean: the search queries ask for exactly the row
// fields of the default list queries, never the check contexts.
func TestSearchListQueriesLean(t *testing.T) {
	for _, q := range []string{github.IssueSearchQuery, github.PRSearchQuery} {
		for _, want := range []string{"first:100", "issueCount", "type:ISSUE"} {
			if !strings.Contains(q, want) {
				t.Fatalf("query lacks %q: %s", want, q)
			}
		}
		for _, banned := range []string{"contexts", "checkSuites", "checkRuns"} {
			if strings.Contains(q, banned) {
				t.Fatalf("query asks for %q: %s", banned, q)
			}
		}
	}
}

// TestSearchRejectedQuery: a query GitHub rejects is a *QueryError with
// GitHub's message, still classified as the gh failure it was.
func TestSearchRejectedQuery(t *testing.T) {
	fake := githubtest.NewFake().ServeFunc(githubtest.MustLoad(t, githubtest.SearchRejected), func([]string) bool { return true })
	_, err := github.NewClient(fake).SearchIssues(context.Background(), cliRepo, "is:open (bug")
	var qe *github.QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("err = %v, want a *QueryError", err)
	}
	if qe.Message != "The search query is invalid: unbalanced parentheses." {
		t.Fatalf("message = %q", qe.Message)
	}
	if github.ClassOf(err) != github.ClassFailed {
		t.Fatalf("class = %q, want failed", github.ClassOf(err))
	}
}

// TestSearchFailuresAreNotRejections: auth, network and not-found failures
// of a search keep their class and are never a rejected query.
func TestSearchFailuresAreNotRejections(t *testing.T) {
	for _, c := range []struct {
		fixture string
		class   github.Class
	}{
		{githubtest.BadToken, github.ClassBadToken},
		{githubtest.NotFound, github.ClassNotFound},
		{githubtest.ServerError, github.ClassServer},
		{githubtest.Failed, github.ClassFailed},
	} {
		t.Run(c.fixture, func(t *testing.T) {
			fake := githubtest.NewFake().ServeFunc(githubtest.MustLoad(t, c.fixture), func([]string) bool { return true })
			_, err := github.NewClient(fake).SearchPRs(context.Background(), cliRepo, "is:merged")
			var qe *github.QueryError
			if errors.As(err, &qe) {
				t.Fatalf("%s read as a rejected query: %v", c.fixture, err)
			}
			if github.ClassOf(err) != c.class {
				t.Fatalf("class = %q, want %q", github.ClassOf(err), c.class)
			}
		})
	}
}
