package github_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

var cliRepo = github.Repo{Host: "github.com", Owner: "cli", Name: "cli"}

// issueListArgs is the one gh call an Issues list fetch makes for repo.
func issueListArgs(repo github.Repo) []string {
	return []string{"api", "--hostname", repo.HostOrDefault(), "graphql",
		"-f", "query=" + github.IssueListQuery, "-f", "owner=" + repo.Owner, "-f", "name=" + repo.Name}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestIssueListParsesRecordedReply: the recorded cli/cli reply becomes six
// rows, newest update first, with the total GitHub counts; a deleted
// author reads as ghost.
func TestIssueListParsesRecordedReply(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.IssueList), issueListArgs(cliRepo)...)
	got, err := github.NewClient(fake).IssueList(context.Background(), cliRepo)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1075 || len(got.Issues) != 6 {
		t.Fatalf("Total %d, %d issues; want 1075 and 6", got.Total, len(got.Issues))
	}
	first := got.Issues[0]
	want := github.Issue{
		Number:    14822,
		Title:     "gh run view --job --log-failed fails on required shared-workflow metadata 404",
		URL:       "https://github.com/cli/cli/issues/14822",
		Author:    "henrymodisett",
		Labels:    []string{"bug", "priority-3", "gh-run"},
		UpdatedAt: mustTime(t, "2026-11-13T13:44:15Z"),
	}
	if first.Number != want.Number || first.Title != want.Title || first.URL != want.URL ||
		first.Author != want.Author || !slices.Equal(first.Labels, want.Labels) ||
		!first.UpdatedAt.Equal(want.UpdatedAt) || first.Closed {
		t.Fatalf("first issue = %+v, want %+v", first, want)
	}
	if !strings.Contains(got.Issues[2].Title, "repository’s open-PR limit") {
		t.Fatalf("title with a non-ASCII quote = %q", got.Issues[2].Title)
	}
	if last := got.Issues[5]; last.Number != 14558 || last.Author != "ghost" {
		t.Fatalf("deleted author = %+v, want #14558 by ghost", last)
	}
	for i := 1; i < len(got.Issues); i++ {
		if got.Issues[i].UpdatedAt.After(got.Issues[i-1].UpdatedAt) {
			t.Fatalf("issue %d updated after issue %d: not newest first", i, i-1)
		}
	}
}

// TestIssueListOneRequest: a fetch is exactly one gh call, the lean GraphQL
// query for the 100 most recently updated open Issues, never
// statusCheckRollup.
func TestIssueListOneRequest(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.IssueList), issueListArgs(cliRepo)...)
	if _, err := github.NewClient(fake).IssueList(context.Background(), cliRepo); err != nil {
		t.Fatal(err)
	}
	if n := len(fake.Calls()); n != 1 {
		t.Fatalf("%d gh calls, want 1", n)
	}
	for _, want := range []string{"first:100", "states:OPEN", "orderBy:{field:UPDATED_AT,direction:DESC}", "totalCount"} {
		if !strings.Contains(github.IssueListQuery, want) {
			t.Fatalf("query lacks %q: %s", want, github.IssueListQuery)
		}
	}
	if strings.Contains(github.IssueListQuery, "statusCheckRollup") {
		t.Fatal("the list query asks for statusCheckRollup")
	}
}

// TestIssueListGHE: a GHE Project asks its own host.
func TestIssueListGHE(t *testing.T) {
	ghe := github.Repo{Host: "ghe.example.com", Owner: "cli", Name: "cli"}
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.IssueList), issueListArgs(ghe)...)
	if _, err := github.NewClient(fake).IssueList(context.Background(), ghe); err != nil {
		t.Fatal(err)
	}
}

func TestIssueListFailures(t *testing.T) {
	cases := []struct {
		fixture string
		is      error
		class   github.Class
	}{
		{fixture: githubtest.IssueListDisabled, is: github.ErrIssuesDisabled},
		{fixture: githubtest.NotFound, class: github.ClassNotFound},
		{fixture: githubtest.BadToken, class: github.ClassBadToken},
		{fixture: githubtest.GhMissing, class: github.ClassGhMissing},
		{fixture: githubtest.OK, class: ""}, // stdout that is not the query's reply
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, c.fixture), issueListArgs(cliRepo)...)
			got, err := github.NewClient(fake).IssueList(context.Background(), cliRepo)
			if err == nil {
				t.Fatalf("no error; got %+v", got)
			}
			if c.is != nil && !errors.Is(err, c.is) {
				t.Fatalf("err = %v, want %v", err, c.is)
			}
			if c.class != "" && github.ClassOf(err) != c.class {
				t.Fatalf("class = %q, want %q (%v)", github.ClassOf(err), c.class, err)
			}
		})
	}
}
