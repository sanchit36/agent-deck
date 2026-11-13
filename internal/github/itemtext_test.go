package github_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

func itemTextArgs(repo github.Repo, number int, comments, review bool) []string {
	return []string{"api", "--hostname", repo.HostOrDefault(), "graphql",
		"-f", "query=" + github.ItemTextQuery, "-f", "owner=" + repo.Owner, "-f", "name=" + repo.Name,
		"-F", "number=" + strconv.Itoa(number),
		"-F", "comments=" + strconv.FormatBool(comments), "-F", "review=" + strconv.FormatBool(review)}
}

// TestItemTextIssue: one gh call returns the Issue's body and comments
// (oldest first); a deleted author reads "ghost", CRLF becomes LF.
func TestItemTextIssue(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.ItemTextIssue), itemTextArgs(widgets, 12, true, false)...)
	got, err := github.NewClient(fake).ItemText(context.Background(), widgets, 12, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "Pressing n on an empty list crashes.\n\nSteps: open the Issues tab, press n." {
		t.Fatalf("body %q", got.Body)
	}
	want := "@alice (2026-11-10):\nI can reproduce this on main.\n\n@ghost (2026-11-11):\nSame here."
	if c := got.CommentsText(); c != want {
		t.Fatalf("comments text\n%q\nwant\n%q", c, want)
	}
	if got.ReviewText() != "" {
		t.Fatalf("an Issue has no review comments: %q", got.ReviewText())
	}
}

// TestItemTextPRReviewThreads: review threads carry file:line (the
// original line when the thread is outdated) and every reply.
func TestItemTextPRReviewThreads(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.ItemTextPR), itemTextArgs(widgets, 34, true, true)...)
	got, err := github.NewClient(fake).ItemText(context.Background(), widgets, 34, true, true)
	if err != nil {
		t.Fatal(err)
	}
	want := "widget.go:42\n  @carol: Check for nil here.\n  @bob: Done.\n\nREADME.md:7 (resolved)\n  @carol: Typo."
	if r := got.ReviewText(); r != want {
		t.Fatalf("review text\n%q\nwant\n%q", r, want)
	}
	if !strings.HasPrefix(got.CommentsText(), "@bob (2026-11-12):\nLooks good overall.") {
		t.Fatalf("comments text %q", got.CommentsText())
	}
}

// TestItemTextFailureIsClassified: a failed gh call is a *github.Error.
func TestItemTextFailureIsClassified(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.Network), itemTextArgs(widgets, 12, false, false)...)
	_, err := github.NewClient(fake).ItemText(context.Background(), widgets, 12, false, false)
	if github.ClassOf(err) != github.ClassNetwork {
		t.Fatalf("err %v, class %q", err, github.ClassOf(err))
	}
}
