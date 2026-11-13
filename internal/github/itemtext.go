package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ItemTextQuery is the GraphQL query of the text a launch fills a Prompt
// Template with (spec #18 §12, §13): the body always, the comments and the
// PR's review threads only when the template uses them ($comments,
// $review).
const ItemTextQuery = `query($owner:String!,$name:String!,$number:Int!,$comments:Boolean!,$review:Boolean!){` +
	`repository(owner:$owner,name:$name){issueOrPullRequest(number:$number){__typename ` +
	`...on Issue{body comments(last:50) @include(if:$comments){nodes{author{login} body createdAt}}} ` +
	`...on PullRequest{body comments(last:50) @include(if:$comments){nodes{author{login} body createdAt}} ` +
	`reviewThreads(last:50) @include(if:$review){nodes{path line originalLine isResolved ` +
	`comments(first:20){nodes{author{login} body}}}}}}}}`

// TextComment is one comment as a launch's {comments} reads it.
type TextComment struct {
	Author    string
	Body      string
	CreatedAt time.Time
}

// ReviewThread is one inline review thread of a PR: its file and line and
// its comments in order.
type ReviewThread struct {
	Path     string
	Line     int
	Resolved bool
	Comments []TextComment
}

// ItemText is an Issue's or PR's body, comments and review threads.
type ItemText struct {
	Body     string
	Comments []TextComment
	Threads  []ReviewThread
}

// CommentsText is the {comments} value: each comment as "@author (date):"
// and its body, oldest first, separated by a blank line.
func (t ItemText) CommentsText() string {
	parts := make([]string, 0, len(t.Comments))
	for _, c := range t.Comments {
		parts = append(parts, fmt.Sprintf("@%s (%s):\n%s", c.Author, c.CreatedAt.UTC().Format("2006-01-02"), c.Body))
	}
	return strings.Join(parts, "\n\n")
}

// ReviewText is the {review-comments} value: each thread as file:line
// (marked resolved) and its comments indented below it.
func (t ItemText) ReviewText() string {
	parts := make([]string, 0, len(t.Threads))
	for _, th := range t.Threads {
		var b strings.Builder
		b.WriteString(th.Path)
		if th.Line > 0 {
			b.WriteString(":" + strconv.Itoa(th.Line))
		}
		if th.Resolved {
			b.WriteString(" (resolved)")
		}
		for _, c := range th.Comments {
			b.WriteString("\n  @" + c.Author + ": " + strings.ReplaceAll(c.Body, "\n", "\n    "))
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "\n\n")
}

type gqlTextComment struct {
	Author    *gqlActor `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

func (c gqlTextComment) textComment() TextComment {
	return TextComment{Author: c.Author.login(), Body: normalizeNewlines(c.Body), CreatedAt: c.CreatedAt}
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// ItemText fetches the body of Issue or PR number of repo, with its
// comments when comments is set and a PR's review threads when review is.
// One gh call.
func (c *Client) ItemText(ctx context.Context, repo Repo, number int, comments, review bool) (ItemText, error) {
	var data struct {
		Repository *struct {
			Item *struct {
				Body     string `json:"body"`
				Comments *struct {
					Nodes []gqlTextComment `json:"nodes"`
				} `json:"comments"`
				Threads *struct {
					Nodes []struct {
						Path         string `json:"path"`
						Line         *int   `json:"line"`
						OriginalLine *int   `json:"originalLine"`
						IsResolved   bool   `json:"isResolved"`
						Comments     struct {
							Nodes []gqlTextComment `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"issueOrPullRequest"`
		} `json:"repository"`
	}
	err := c.repoGraphQL(ctx, repo, ItemTextQuery, &data, "-F", "number="+strconv.Itoa(number),
		"-F", "comments="+strconv.FormatBool(comments), "-F", "review="+strconv.FormatBool(review))
	if err != nil {
		return ItemText{}, err
	}
	if data.Repository == nil || data.Repository.Item == nil {
		return ItemText{}, &Error{Class: ClassNotFound, Host: repo.HostOrDefault(),
			Message: fmt.Sprintf("no issue or pull request #%d in %s", number, repo)}
	}
	n := data.Repository.Item
	out := ItemText{Body: normalizeNewlines(n.Body)}
	if n.Comments != nil {
		for _, cm := range n.Comments.Nodes {
			out.Comments = append(out.Comments, cm.textComment())
		}
	}
	if n.Threads != nil {
		for _, th := range n.Threads.Nodes {
			rt := ReviewThread{Path: th.Path, Resolved: th.IsResolved}
			switch {
			case th.Line != nil:
				rt.Line = *th.Line
			case th.OriginalLine != nil:
				rt.Line = *th.OriginalLine
			}
			for _, cm := range th.Comments.Nodes {
				rt.Comments = append(rt.Comments, cm.textComment())
			}
			out.Threads = append(out.Threads, rt)
		}
	}
	return out, nil
}
