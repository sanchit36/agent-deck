package github

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ConversationLimit is how many comments a Conversation holds (spec §9):
// the rest are counted, never fetched.
const ConversationLimit = 100

// ConversationQuery is the GraphQL query of an Issue's or Pull Request's
// Conversation (spec §9): the body, the first 100 issue-style comments and,
// for a PR, the first 100 reviews with their verdict, body and inline
// comment count. No timeline events.
const ConversationQuery = `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){` +
	`issueOrPullRequest(number:$number){__typename ` +
	`...on Issue{number updatedAt createdAt lastEditedAt body author{__typename login} ` +
	`comments(first:100){totalCount nodes{author{__typename login} createdAt lastEditedAt body isMinimized minimizedReason}}} ` +
	`...on PullRequest{number updatedAt createdAt lastEditedAt body author{__typename login} ` +
	`comments(first:100){totalCount nodes{author{__typename login} createdAt lastEditedAt body isMinimized minimizedReason}} ` +
	`reviews(first:100){totalCount nodes{author{__typename login} state submittedAt lastEditedAt body isMinimized minimizedReason ` +
	`comments{totalCount}}}}}}}`

// Comment is one entry of a Conversation: an issue-style comment, or a PR
// review summary (Review set).
type Comment struct {
	Author string
	// Bot marks an author that is a GitHub App ([bot]).
	Bot    bool
	At     time.Time
	Edited bool
	Body   string
	// Minimized comments are hidden on GitHub; MinimizedReason is GitHub's
	// lower-case reason (spam, off-topic, outdated, resolved, duplicate,
	// abuse), possibly empty.
	Minimized       bool
	MinimizedReason string
	// Review is a review summary's verdict (APPROVED, CHANGES_REQUESTED,
	// COMMENTED, DISMISSED); empty for an issue-style comment.
	Review string
}

// Conversation is an Issue's or Pull Request's body and comments, as the
// overlay's Conversation tab shows them.
type Conversation struct {
	Number    int
	UpdatedAt time.Time
	// The body comes first, by Author at CreatedAt.
	Author    string
	Bot       bool
	CreatedAt time.Time
	Edited    bool
	Body      string
	// Comments are the issue-style comments and review summaries, oldest
	// first, at most ConversationLimit of them.
	Comments []Comment
	// More counts the comments and review summaries not fetched.
	More int
	// InlineComments counts the inline review comments of the fetched
	// reviews; they are shown only as a count.
	InlineComments int
}

// Version is the item's updatedAt, for the Freshness cache.
func (c Conversation) Version() time.Time { return c.UpdatedAt }

type gqlAuthor struct {
	Typename string `json:"__typename"`
	Login    string `json:"login"`
}

func (a *gqlAuthor) who() (string, bool) {
	if a == nil || a.Login == "" {
		return ghostLogin, false
	}
	return a.Login, a.Typename == "Bot"
}

type gqlComment struct {
	Author          *gqlAuthor `json:"author"`
	CreatedAt       time.Time  `json:"createdAt"`
	SubmittedAt     time.Time  `json:"submittedAt"`
	LastEditedAt    *time.Time `json:"lastEditedAt"`
	Body            string     `json:"body"`
	IsMinimized     bool       `json:"isMinimized"`
	MinimizedReason string     `json:"minimizedReason"`
	State           string     `json:"state"`
	Comments        struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
}

type gqlCommentConn struct {
	TotalCount int          `json:"totalCount"`
	Nodes      []gqlComment `json:"nodes"`
}

func (c gqlComment) comment(at time.Time) Comment {
	who, bot := c.Author.who()
	return Comment{
		Author: who, Bot: bot, At: at, Edited: c.LastEditedAt != nil, Body: c.Body,
		Minimized: c.IsMinimized, MinimizedReason: strings.ToLower(c.MinimizedReason),
	}
}

// Conversation fetches the Conversation of Issue or Pull Request number of
// repo in one GraphQL request. A number that is neither is a ClassNotFound
// error.
func (c *Client) Conversation(ctx context.Context, repo Repo, number int) (Conversation, error) {
	var data struct {
		Repository *struct {
			Item *struct {
				Number       int            `json:"number"`
				UpdatedAt    time.Time      `json:"updatedAt"`
				CreatedAt    time.Time      `json:"createdAt"`
				LastEditedAt *time.Time     `json:"lastEditedAt"`
				Body         string         `json:"body"`
				Author       *gqlAuthor     `json:"author"`
				Comments     gqlCommentConn `json:"comments"`
				Reviews      gqlCommentConn `json:"reviews"`
			} `json:"issueOrPullRequest"`
		} `json:"repository"`
	}
	if err := c.repoGraphQL(ctx, repo, ConversationQuery, &data, "-F", "number="+strconv.Itoa(number)); err != nil {
		return Conversation{}, err
	}
	if data.Repository == nil || data.Repository.Item == nil {
		return Conversation{}, &Error{Class: ClassNotFound, Host: repo.HostOrDefault(),
			Message: fmt.Sprintf("no issue or pull request #%d in %s", number, repo)}
	}
	n := data.Repository.Item
	who, bot := n.Author.who()
	conv := Conversation{
		Number: n.Number, UpdatedAt: n.UpdatedAt, Author: who, Bot: bot, CreatedAt: n.CreatedAt,
		Edited: n.LastEditedAt != nil, Body: n.Body,
		More: max(0, n.Comments.TotalCount-len(n.Comments.Nodes)) + max(0, n.Reviews.TotalCount-len(n.Reviews.Nodes)),
	}
	for _, x := range n.Comments.Nodes {
		conv.Comments = append(conv.Comments, x.comment(x.CreatedAt))
	}
	for _, r := range n.Reviews.Nodes {
		conv.InlineComments += r.Comments.TotalCount
		// A pending review is the viewer's draft; a COMMENTED review with
		// no body only holds inline comments, which are counted above.
		if r.State == "PENDING" || (r.State == "COMMENTED" && strings.TrimSpace(r.Body) == "") {
			continue
		}
		cm := r.comment(r.SubmittedAt)
		cm.Review = r.State
		conv.Comments = append(conv.Comments, cm)
	}
	sort.SliceStable(conv.Comments, func(i, j int) bool { return conv.Comments[i].At.Before(conv.Comments[j].At) })
	if len(conv.Comments) > ConversationLimit {
		conv.More += len(conv.Comments) - ConversationLimit
		conv.Comments = conv.Comments[:ConversationLimit]
	}
	return conv, nil
}
