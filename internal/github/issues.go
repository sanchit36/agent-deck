package github

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ListPageSize is how many items a list fetch asks for: one GraphQL page,
// which costs one rate-limit point whatever its size (spec §5).
const ListPageSize = 100

// IssueListQuery is the lean GraphQL query of a Project's Issues list: the
// ListPageSize most recently updated open Issues and their total, with
// only what a list row shows.
const IssueListQuery = `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){` +
	`hasIssuesEnabled issues(first:100,states:OPEN,orderBy:{field:UPDATED_AT,direction:DESC}){` +
	`totalCount nodes{number title url state updatedAt author{login} labels(first:10){nodes{name}}}}}}`

// Issue is one row of an Issues list.
type Issue struct {
	Number int
	Title  string
	URL    string
	// Author is the login that opened the Issue; "ghost" for a deleted
	// account, as GitHub shows it.
	Author    string
	Labels    []string
	UpdatedAt time.Time
	Closed    bool
}

// IssueList is one fetch of a Project's Issues list.
type IssueList struct {
	// Issues are the fetched rows, most recently updated first; at most
	// ListPageSize.
	Issues []Issue
	// Total is how many Issues the list has on GitHub: more than
	// len(Issues) when the page is full.
	Total int
}

// ghostLogin is what GitHub shows for a deleted account (a null author).
const ghostLogin = "ghost"

type gqlActor struct {
	Login string `json:"login"`
}

type gqlLabels struct {
	Nodes []struct {
		Name string `json:"name"`
	} `json:"nodes"`
}

func (a *gqlActor) login() string {
	if a == nil || a.Login == "" {
		return ghostLogin
	}
	return a.Login
}

func (l gqlLabels) names() []string {
	if len(l.Nodes) == 0 {
		return nil
	}
	out := make([]string, 0, len(l.Nodes))
	for _, n := range l.Nodes {
		out = append(out, n.Name)
	}
	return out
}

// IssueList fetches repo's Issues list in one GraphQL request (spec §1,
// §5). A repository with Issues turned off returns ErrIssuesDisabled.
func (c *Client) IssueList(ctx context.Context, repo Repo) (IssueList, error) {
	var data struct {
		Repository *struct {
			HasIssuesEnabled bool `json:"hasIssuesEnabled"`
			Issues           struct {
				TotalCount int        `json:"totalCount"`
				Nodes      []gqlIssue `json:"nodes"`
			} `json:"issues"`
		} `json:"repository"`
	}
	if err := c.RepoGraphQL(ctx, repo, IssueListQuery, &data); err != nil {
		return IssueList{}, err
	}
	r := data.Repository
	if r == nil {
		return IssueList{}, fmt.Errorf("listing issues of %s: the reply has no repository", repo)
	}
	if !r.HasIssuesEnabled {
		return IssueList{}, ErrIssuesDisabled
	}
	out := IssueList{Total: r.Issues.TotalCount, Issues: make([]Issue, 0, len(r.Issues.Nodes))}
	for _, n := range r.Issues.Nodes {
		out.Issues = append(out.Issues, n.issue())
	}
	return out, nil
}

// gqlIssue is an Issue list row as the list and search queries return it.
type gqlIssue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	State     string    `json:"state"`
	UpdatedAt time.Time `json:"updatedAt"`
	Author    *gqlActor `json:"author"`
	Labels    gqlLabels `json:"labels"`
}

func (n gqlIssue) issue() Issue {
	return Issue{
		Number:    n.Number,
		Title:     n.Title,
		URL:       n.URL,
		Author:    n.Author.login(),
		Labels:    n.Labels.names(),
		UpdatedAt: n.UpdatedAt,
		Closed:    n.State == "CLOSED",
	}
}

// RepoGraphQL runs a GraphQL query against repo's host with $owner and
// $name bound to repo, and decodes the reply's data into data. It is one
// gh call; the list and detail queries go through it.
func (c *Client) RepoGraphQL(ctx context.Context, repo Repo, query string, data any) error {
	return c.repoGraphQL(ctx, repo, query, data)
}

// repoGraphQL is RepoGraphQL with more gh api arguments after the
// $owner/$name binding (e.g. -F number=N for a typed variable).
func (c *Client) repoGraphQL(ctx context.Context, repo Repo, query string, data any, extra ...string) error {
	args := []string{"graphql", "-f", "query=" + query, "-f", "owner=" + repo.Owner, "-f", "name=" + repo.Name}
	out, err := c.API(ctx, repo.HostOrDefault(), Call{Args: append(args, extra...)})
	if err != nil {
		return err
	}
	var reply struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return fmt.Errorf("decoding the GraphQL reply for %s: %w", repo, err)
	}
	if len(reply.Data) == 0 || string(reply.Data) == "null" {
		return fmt.Errorf("decoding the GraphQL reply for %s: no data", repo)
	}
	if err := json.Unmarshal(reply.Data, data); err != nil {
		return fmt.Errorf("decoding the GraphQL data for %s: %w", repo, err)
	}
	return nil
}
