package github_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

// conversationArgs is the one gh call a Conversation fetch makes.
func conversationArgs(repo github.Repo, number int) []string {
	return []string{"api", "--hostname", repo.HostOrDefault(), "graphql",
		"-f", "query=" + github.ConversationQuery, "-f", "owner=" + repo.Owner, "-f", "name=" + repo.Name,
		"-F", "number=" + strconv.Itoa(number)}
}

// TestConversationParsesRecordedIssue: cli/cli Issue #326 has 108
// comments; the Conversation holds the first 100, oldest first, counts the
// other 8, keeps minimized comments with their reason and names deleted
// accounts ghost.
func TestConversationParsesRecordedIssue(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.IssueConversation), conversationArgs(cliRepo, 326)...)
	c, err := github.NewClient(fake).Conversation(context.Background(), cliRepo, 326)
	if err != nil {
		t.Fatal(err)
	}
	if c.Number != 326 || c.Author != "ncmans" || c.Bot || c.Edited || c.Body == "" ||
		!c.CreatedAt.Equal(mustTime(t, "2020-02-07T04:29:50Z")) || !c.Version().Equal(mustTime(t, "2026-10-15T21:18:12Z")) {
		t.Fatalf("conversation header = %+v", c)
	}
	if len(c.Comments) != github.ConversationLimit || c.More != 8 || c.InlineComments != 0 {
		t.Fatalf("%d comments, %d more, %d inline; want 100, 8, 0", len(c.Comments), c.More, c.InlineComments)
	}
	if first, last := c.Comments[0], c.Comments[99]; first.Author != "eXamadeus" || first.Edited ||
		last.Author != "dotnvo" || !last.At.Equal(mustTime(t, "2026-02-24T20:53:34Z")) {
		t.Fatalf("first %+v, last %+v", first, last)
	}
	minimized := 0
	for _, cm := range c.Comments {
		if cm.Minimized {
			minimized++
		}
		if cm.Review != "" {
			t.Fatalf("an Issue has no reviews: %+v", cm)
		}
	}
	if minimized != 28 {
		t.Fatalf("%d minimized comments, want 28", minimized)
	}
	if cm := c.Comments[17]; !cm.Minimized || cm.MinimizedReason != "spam" {
		t.Fatalf("comment 17 = %+v, want minimized as spam", cm)
	}
	if cm := c.Comments[79]; !cm.Minimized || cm.MinimizedReason != "off-topic" {
		t.Fatalf("comment 79 = %+v, want minimized as off-topic", cm)
	}
	if cm := c.Comments[16]; cm.Author != "ghost" || !cm.Edited {
		t.Fatalf("comment 16 = %+v, want an edited comment by a deleted account", cm)
	}
}

// TestConversationParsesRecordedPR: cli/cli PR #8361 interleaves its
// comments with the review summaries by time, drops the empty COMMENTED
// review (it only holds an inline comment) and counts every inline review
// comment.
func TestConversationParsesRecordedPR(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.PRConversation), conversationArgs(cliRepo, 8361)...)
	c, err := github.NewClient(fake).Conversation(context.Background(), cliRepo, 8361)
	if err != nil {
		t.Fatal(err)
	}
	if c.Number != 8361 || c.Author != "mateusmarquezini" || !c.Edited || c.More != 0 || c.InlineComments != 4 {
		t.Fatalf("conversation = %+v", c)
	}
	want := []struct {
		author, review, at string
		minimized          bool
	}{
		{"dmgardiner25", "CHANGES_REQUESTED", "2023-11-27T18:59:57Z", false},
		{"mateusmarquezini", "", "2023-11-27T20:34:16Z", false},
		{"dmgardiner25", "APPROVED", "2023-11-28T19:49:33Z", false},
		{"cwinchesterii69-lang", "", "2026-11-13T04:29:28Z", true},
	}
	if len(c.Comments) != len(want) {
		t.Fatalf("%d comments, want %d: %+v", len(c.Comments), len(want), c.Comments)
	}
	for i, w := range want {
		got := c.Comments[i]
		if got.Author != w.author || got.Review != w.review || !got.At.Equal(mustTime(t, w.at)) || got.Minimized != w.minimized {
			t.Fatalf("comment %d = %+v, want %+v", i, got, w)
		}
	}
	if r := c.Comments[0]; r.Body != "Looks good, just a few small requests before approving" {
		t.Fatalf("review body = %q", r.Body)
	}
	if !c.Comments[1].Edited || c.Comments[3].MinimizedReason != "spam" {
		t.Fatalf("edited %v, reason %q", c.Comments[1].Edited, c.Comments[3].MinimizedReason)
	}
}

// TestConversationNotFound: a number that is neither an Issue nor a PR is
// a not-found error, as for the detail.
func TestConversationNotFound(t *testing.T) {
	f := githubtest.Fixture{Name: "null-item", Result: github.Result{Stdout: []byte(`{"data":{"repository":{"issueOrPullRequest":null}}}`)}}
	fake := githubtest.NewFake().Serve(f, conversationArgs(cliRepo, 9)...)
	_, err := github.NewClient(fake).Conversation(context.Background(), cliRepo, 9)
	if github.ClassOf(err) != github.ClassNotFound {
		t.Fatalf("err = %v, want class not_found", err)
	}
}

// TestConversationMarksBots: cli/cli Issue #14822's first comment is by
// the cli-triage GitHub App; PR #14927's only review summary is by
// Copilot's, with four inline comments.
func TestConversationMarksBots(t *testing.T) {
	fake := githubtest.NewFake().
		Serve(githubtest.MustLoad(t, githubtest.IssueRowConversation), conversationArgs(cliRepo, 14822)...).
		Serve(githubtest.MustLoad(t, githubtest.PRRowConversation), conversationArgs(cliRepo, 14927)...)
	is, err := github.NewClient(fake).Conversation(context.Background(), cliRepo, 14822)
	if err != nil {
		t.Fatal(err)
	}
	if len(is.Comments) != 5 || !is.Comments[0].Bot || is.Comments[0].Author != "cli-triage" || is.Comments[2].Bot {
		t.Fatalf("issue comments = %+v", is.Comments)
	}
	pr, err := github.NewClient(fake).Conversation(context.Background(), cliRepo, 14927)
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.Comments) != 1 || !pr.Comments[0].Bot || pr.Comments[0].Review != "COMMENTED" || pr.InlineComments != 4 || !pr.Edited {
		t.Fatalf("pr conversation = %+v", pr)
	}
}

// TestConversationCounts: the inline review comment count leaves out the
// viewer's PENDING (draft) review, and "N more" counts only comments and
// reviews that would render: unfetched empty COMMENTED reviews hold only
// inline comments and are never shown.
func TestConversationCounts(t *testing.T) {
	const pr = `{"data":{"repository":{"issueOrPullRequest":{"__typename":"PullRequest","number":7,` +
		`"updatedAt":"2026-01-02T00:00:00Z","createdAt":"2026-01-01T00:00:00Z","body":"b","author":{"__typename":"User","login":"a"},` +
		`"comments":{"totalCount":%d,"nodes":[%s]},` +
		`"reviews":{"totalCount":%d,"nodes":[%s]},"verdicts":{"totalCount":%d}}}}}`
	const comment = `{"author":{"__typename":"User","login":"c"},"createdAt":"2026-01-01T01:00:00Z","body":"hi"}`
	review := func(state, body string, inline int) string {
		return fmt.Sprintf(`{"author":{"__typename":"User","login":"r"},"state":%q,"submittedAt":"2026-01-01T02:00:00Z","body":%q,"comments":{"totalCount":%d}}`,
			state, body, inline)
	}
	tests := []struct {
		name                  string
		comments, reviews     int
		commentNodes, reviewN []string
		verdicts              int
		wantShown, wantMore   int
		wantInline            int
	}{
		{
			name:       "pending review's inline comments are not counted",
			reviews:    3,
			reviewN:    []string{review("PENDING", "", 3), review("COMMENTED", "", 2), review("APPROVED", "ok", 1)},
			verdicts:   1,
			wantShown:  1,
			wantInline: 3,
		},
		{
			name:         "unfetched empty COMMENTED reviews are not more comments",
			comments:     3,
			commentNodes: []string{comment, comment},
			reviews:      5,
			reviewN:      []string{review("APPROVED", "", 0), review("COMMENTED", "", 1)},
			verdicts:     2,
			wantShown:    3,
			// One unfetched comment and one unfetched verdict; the other two
			// unfetched reviews are COMMENTED.
			wantMore:   2,
			wantInline: 1,
		},
		{
			name:       "every review fetched",
			reviews:    2,
			reviewN:    []string{review("CHANGES_REQUESTED", "fix", 2), review("COMMENTED", "note", 1)},
			verdicts:   1,
			wantShown:  2,
			wantInline: 3,
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := 100 + i
			body := fmt.Sprintf(pr, tt.comments, strings.Join(tt.commentNodes, ","), tt.reviews, strings.Join(tt.reviewN, ","), tt.verdicts)
			f := githubtest.Fixture{Name: "counts", Result: github.Result{Stdout: []byte(body)}}
			fake := githubtest.NewFake().Serve(f, conversationArgs(cliRepo, n)...)
			c, err := github.NewClient(fake).Conversation(context.Background(), cliRepo, n)
			if err != nil {
				t.Fatal(err)
			}
			if len(c.Comments) != tt.wantShown || c.More != tt.wantMore || c.InlineComments != tt.wantInline {
				t.Fatalf("%d shown, %d more, %d inline; want %d, %d, %d",
					len(c.Comments), c.More, c.InlineComments, tt.wantShown, tt.wantMore, tt.wantInline)
			}
		})
	}
}
