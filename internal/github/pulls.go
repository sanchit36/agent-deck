package github

import (
	"context"
	"fmt"
	"time"
)

// PRListQuery is the lean GraphQL query of a Project's PRs list: the
// ListPageSize most recently updated open PRs and their total, with only
// what a list row and Link auto-detect need. Checks come as the head
// commit's overall state alone (statusCheckRollup{state}); the rollup's
// contexts are never asked for in a list (spec §1).
const PRListQuery = `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){` +
	`pullRequests(first:100,states:OPEN,orderBy:{field:UPDATED_AT,direction:DESC}){` +
	`totalCount nodes{number title url state isDraft updatedAt author{login} ` +
	`headRefName baseRefName headRepository{name owner{login}} reviewDecision mergeStateStatus ` +
	`commits(last:1){nodes{commit{statusCheckRollup{state}}}}}}}}`

// PRState is a PR's state.
type PRState string

const (
	PROpen   PRState = "OPEN"
	PRClosed PRState = "CLOSED"
	PRMerged PRState = "MERGED"
)

// CheckState is the overall state of a PR's checks on its head commit.
type CheckState string

const (
	ChecksNone    CheckState = ""
	ChecksPassing CheckState = "passing"
	ChecksFailing CheckState = "failing"
	ChecksRunning CheckState = "running"
)

// CheckStateOf maps GitHub's StatusState (the rollup's state) onto a
// CheckState; "" (no rollup) is ChecksNone.
func CheckStateOf(s string) CheckState {
	switch s {
	case "SUCCESS":
		return ChecksPassing
	case "FAILURE", "ERROR":
		return ChecksFailing
	case "PENDING", "EXPECTED":
		return ChecksRunning
	}
	return ChecksNone
}

// ReviewDecision is GitHub's review decision for a PR.
type ReviewDecision string

const (
	ReviewNone             ReviewDecision = ""
	ReviewApproved         ReviewDecision = "APPROVED"
	ReviewChangesRequested ReviewDecision = "CHANGES_REQUESTED"
	ReviewRequired         ReviewDecision = "REVIEW_REQUIRED"
)

// MergeState is GitHub's mergeStateStatus for a PR.
type MergeState string

const (
	MergeBehind   MergeState = "BEHIND"
	MergeBlocked  MergeState = "BLOCKED"
	MergeClean    MergeState = "CLEAN"
	MergeDirty    MergeState = "DIRTY"
	MergeDraft    MergeState = "DRAFT"
	MergeHasHooks MergeState = "HAS_HOOKS"
	MergeUnknown  MergeState = "UNKNOWN"
	MergeUnstable MergeState = "UNSTABLE"
)

// PullRequest is one row of a PRs list.
type PullRequest struct {
	Number int
	Title  string
	URL    string
	// Author is the login that opened the PR; "ghost" for a deleted account.
	Author    string
	State     PRState
	Draft     bool
	UpdatedAt time.Time
	// HeadBranch is the branch the PR merges from, in HeadRepo; BaseBranch
	// the one it merges into.
	HeadBranch string
	BaseBranch string
	// HeadRepo is the repository holding the head branch, on the list's
	// host: the Project itself, or a fork. Zero when the fork was deleted.
	HeadRepo   Repo
	Checks     CheckState
	Review     ReviewDecision
	MergeState MergeState
}

// PRList is one fetch of a Project's PRs list.
type PRList struct {
	// PRs are the fetched rows, most recently updated first; at most
	// ListPageSize.
	PRs []PullRequest
	// Total is how many PRs the list has on GitHub.
	Total int
}

// PRList fetches repo's PRs list in one GraphQL request (spec §1, §5).
func (c *Client) PRList(ctx context.Context, repo Repo) (PRList, error) {
	var data struct {
		Repository *struct {
			PullRequests struct {
				TotalCount int `json:"totalCount"`
				Nodes      []struct {
					Number         int       `json:"number"`
					Title          string    `json:"title"`
					URL            string    `json:"url"`
					State          string    `json:"state"`
					IsDraft        bool      `json:"isDraft"`
					UpdatedAt      time.Time `json:"updatedAt"`
					Author         *gqlActor `json:"author"`
					HeadRefName    string    `json:"headRefName"`
					BaseRefName    string    `json:"baseRefName"`
					HeadRepository *struct {
						Name  string   `json:"name"`
						Owner gqlActor `json:"owner"`
					} `json:"headRepository"`
					ReviewDecision *string `json:"reviewDecision"`
					MergeState     string  `json:"mergeStateStatus"`
					Commits        struct {
						Nodes []struct {
							Commit struct {
								Rollup *struct {
									State string `json:"state"`
								} `json:"statusCheckRollup"`
							} `json:"commit"`
						} `json:"nodes"`
					} `json:"commits"`
				} `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	}
	if err := c.RepoGraphQL(ctx, repo, PRListQuery, &data); err != nil {
		return PRList{}, err
	}
	r := data.Repository
	if r == nil {
		return PRList{}, fmt.Errorf("listing pull requests of %s: the reply has no repository", repo)
	}
	out := PRList{Total: r.PullRequests.TotalCount, PRs: make([]PullRequest, 0, len(r.PullRequests.Nodes))}
	for _, n := range r.PullRequests.Nodes {
		p := PullRequest{
			Number:     n.Number,
			Title:      n.Title,
			URL:        n.URL,
			Author:     n.Author.login(),
			State:      PRState(n.State),
			Draft:      n.IsDraft,
			UpdatedAt:  n.UpdatedAt,
			HeadBranch: n.HeadRefName,
			BaseBranch: n.BaseRefName,
			MergeState: MergeState(n.MergeState),
		}
		if h := n.HeadRepository; h != nil && h.Owner.Login != "" && h.Name != "" {
			p.HeadRepo = Repo{Host: repo.HostOrDefault(), Owner: h.Owner.Login, Name: h.Name}
		}
		if n.ReviewDecision != nil {
			p.Review = ReviewDecision(*n.ReviewDecision)
		}
		if c := n.Commits.Nodes; len(c) > 0 && c[len(c)-1].Commit.Rollup != nil {
			p.Checks = CheckStateOf(c[len(c)-1].Commit.Rollup.State)
		}
		out.PRs = append(out.PRs, p)
	}
	return out, nil
}
