package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Filtered lists (spec #18 §6): a list under any filter but DefaultFilter
// is one GraphQL search, scoped to the Project with repo:OWNER/NAME (the
// host is the one gh api --hostname asks) and to the list's kind with
// is:issue or is:pr. Results come most recently updated first, like the
// default list, unless the filter sorts them itself. The search index can
// lag GitHub by a few minutes.

// IssueSearchQuery is the Issues search: the rows IssueListQuery returns,
// for at most ListPageSize matches, and how many match.
const IssueSearchQuery = `query($q:String!){search(query:$q,type:ISSUE,first:100){` +
	`issueCount nodes{... on Issue{number title url state updatedAt author{login} labels(first:10){nodes{name}}}}}}`

// PRSearchQuery is the PRs search: the rows PRListQuery returns, for at
// most ListPageSize matches, and how many match.
const PRSearchQuery = `query($q:String!){search(query:$q,type:ISSUE,first:100){` +
	`issueCount nodes{... on PullRequest{number title url state isDraft updatedAt author{login} ` +
	`headRefName baseRefName headRepository{name owner{login}} reviewDecision mergeStateStatus ` +
	`commits(last:1){nodes{commit{statusCheckRollup{state}}}}}}}}`

// QueryError is a filter GitHub rejected: the search ran and GitHub
// answered with a GraphQL error about the query. Message is GitHub's.
type QueryError struct {
	Message string
	// Err is the classified gh failure.
	Err error
}

func (e *QueryError) Error() string { return "GitHub rejected the query: " + e.Message }

func (e *QueryError) Unwrap() error { return e.Err }

// SearchQuery is the search string of repo's kind list ("issue" or "pr")
// under filter: the scope first, then the filter without anything that
// would widen or change the scope (repo:, org:, user:, is:issue, is:pr,
// type:), then sort:updated-desc unless the filter sorts.
func SearchQuery(repo Repo, kind, filter string) string {
	parts := []string{"repo:" + repo.Owner + "/" + repo.Name, "is:" + kind}
	sorted := false
	for _, tok := range filterTokens(filter) {
		key, value, _ := strings.Cut(strings.ToLower(tok), ":")
		if isQualifier(tok) {
			switch {
			case key == "repo" || key == "org" || key == "user" || key == "type":
				continue
			case key == "is" && (value == "issue" || value == "pr"):
				continue
			case key == "sort":
				sorted = true
			}
		}
		parts = append(parts, tok)
	}
	if !sorted {
		parts = append(parts, "sort:updated-desc")
	}
	return strings.Join(parts, " ")
}

// SearchIssues fetches repo's Issues matching filter in one GraphQL
// search. A query GitHub rejects returns a *QueryError.
func (c *Client) SearchIssues(ctx context.Context, repo Repo, filter string) (IssueList, error) {
	var data struct {
		Search struct {
			IssueCount int        `json:"issueCount"`
			Nodes      []gqlIssue `json:"nodes"`
		} `json:"search"`
	}
	if err := c.search(ctx, repo, IssueSearchQuery, SearchQuery(repo, "issue", filter), &data); err != nil {
		return IssueList{}, err
	}
	out := IssueList{Total: data.Search.IssueCount, Issues: make([]Issue, 0, len(data.Search.Nodes))}
	for _, n := range data.Search.Nodes {
		if n.Number == 0 {
			continue
		}
		out.Issues = append(out.Issues, n.issue())
	}
	return out, nil
}

// SearchPRs fetches repo's PRs matching filter in one GraphQL search. A
// query GitHub rejects returns a *QueryError.
func (c *Client) SearchPRs(ctx context.Context, repo Repo, filter string) (PRList, error) {
	var data struct {
		Search struct {
			IssueCount int     `json:"issueCount"`
			Nodes      []gqlPR `json:"nodes"`
		} `json:"search"`
	}
	if err := c.search(ctx, repo, PRSearchQuery, SearchQuery(repo, "pr", filter), &data); err != nil {
		return PRList{}, err
	}
	out := PRList{Total: data.Search.IssueCount, PRs: make([]PullRequest, 0, len(data.Search.Nodes))}
	for _, n := range data.Search.Nodes {
		if n.Number == 0 {
			continue
		}
		out.PRs = append(out.PRs, n.pr(repo))
	}
	return out, nil
}

// search runs one GraphQL search with $q bound to q and decodes its data.
func (c *Client) search(ctx context.Context, repo Repo, query, q string, data any) error {
	out, err := c.API(ctx, repo.HostOrDefault(), Call{Args: []string{"graphql", "-f", "query=" + query, "-f", "q=" + q}})
	if err != nil {
		if msg, ok := rejectedQuery(out, err); ok {
			return &QueryError{Message: msg, Err: err}
		}
		return err
	}
	var reply struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return fmt.Errorf("decoding the search reply for %s: %w", repo, err)
	}
	if len(reply.Data) == 0 || string(reply.Data) == "null" {
		return fmt.Errorf("decoding the search reply for %s: no data", repo)
	}
	if err := json.Unmarshal(reply.Data, data); err != nil {
		return fmt.Errorf("decoding the search data for %s: %w", repo, err)
	}
	return nil
}

// rejectedQuery reports whether a failed search is GitHub rejecting the
// query: gh exited with an unclassified failure and printed a GraphQL
// error that is not about access, existence or limits. The message is the
// first such error's.
func rejectedQuery(stdout []byte, err error) (string, bool) {
	var ge *Error
	if !errors.As(err, &ge) || ge.Class != ClassFailed {
		return "", false
	}
	var reply struct {
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(stdout, &reply) != nil {
		return "", false
	}
	for _, e := range reply.Errors {
		switch e.Type {
		case "NOT_FOUND", "FORBIDDEN", "RATE_LIMITED", "RATE_LIMIT", "INTERNAL":
			continue
		}
		if e.Message != "" {
			return e.Message, true
		}
	}
	return "", false
}
