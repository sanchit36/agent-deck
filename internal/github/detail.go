package github

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// DetailQuery is the GraphQL query of one Issue or Pull Request (they share
// GitHub's numbering, so one query serves both): what the detail pane shows
// (spec §3, §7) and a short timeline. The PR's check contexts are read for
// their counts only; the body and the comments are the overlay's (§9).
const DetailQuery = `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){` +
	`issueOrPullRequest(number:$number){__typename ` +
	`...on Issue{number title url issueState:state createdAt updatedAt author{login} labels(first:20){nodes{name}} ` +
	`assignees(first:10){nodes{login}} comments{totalCount} ` +
	`timelineItems(last:10,itemTypes:[ISSUE_COMMENT,CLOSED_EVENT,REOPENED_EVENT,CROSS_REFERENCED_EVENT]){nodes{__typename ` +
	`...on IssueComment{createdAt author{login}} ...on ClosedEvent{createdAt actor{login}} ` +
	`...on ReopenedEvent{createdAt actor{login}} ...on CrossReferencedEvent{createdAt actor{login}}}}} ` +
	`...on PullRequest{number title url prState:state isDraft createdAt updatedAt author{login} labels(first:20){nodes{name}} ` +
	`assignees(first:10){nodes{login}} comments{totalCount} headRefName baseRefName headRefOid headRepository{name owner{login}} ` +
	`reviewDecision mergeStateStatus additions deletions changedFiles ` +
	`commits(last:1){nodes{commit{statusCheckRollup{state contexts(first:100){nodes{__typename ` +
	`...on CheckRun{conclusion status completedAt} ...on StatusContext{state createdAt}}}}}}} ` +
	`timelineItems(last:10,itemTypes:[ISSUE_COMMENT,PULL_REQUEST_REVIEW,PULL_REQUEST_COMMIT,HEAD_REF_FORCE_PUSHED_EVENT,` +
	`CLOSED_EVENT,REOPENED_EVENT,MERGED_EVENT,READY_FOR_REVIEW_EVENT]){nodes{__typename ` +
	`...on IssueComment{createdAt author{login}} ...on PullRequestReview{reviewState:state submittedAt author{login}} ` +
	`...on PullRequestCommit{commit{committedDate author{user{login}}}} ...on HeadRefForcePushedEvent{createdAt actor{login}} ` +
	`...on ClosedEvent{createdAt actor{login}} ...on ReopenedEvent{createdAt actor{login}} ` +
	`...on MergedEvent{createdAt actor{login}} ...on ReadyForReviewEvent{createdAt actor{login}}}}}}}}`

// Detail is one fetched Issue (IssueDetail) or Pull Request (PRDetail).
// Version is its GitHub updatedAt, so the Freshness cache can tell when a
// list row reports a newer one.
type Detail interface {
	Version() time.Time
	isDetail()
}

// EventKind is what a timeline event records.
type EventKind string

const (
	EventComment    EventKind = "comment"
	EventReview     EventKind = "review"
	EventPush       EventKind = "push"
	EventForcePush  EventKind = "force_push"
	EventChecks     EventKind = "checks"
	EventClosed     EventKind = "closed"
	EventReopened   EventKind = "reopened"
	EventMerged     EventKind = "merged"
	EventReady      EventKind = "ready"
	EventReferenced EventKind = "referenced"
)

// Event is one entry of a detail's short timeline.
type Event struct {
	Kind EventKind
	At   time.Time
	// Actor is who did it; "ghost" when GitHub names no one.
	Actor string
	// Count is the commits of a push.
	Count int
	// Review is a review's state (APPROVED, CHANGES_REQUESTED, COMMENTED,
	// DISMISSED).
	Review string
	// Checks is the overall check state of a checks event.
	Checks CheckState
}

// IssueDetail is an Issue as the detail pane shows it.
type IssueDetail struct {
	Number    int
	Title     string
	URL       string
	Author    string
	Closed    bool
	CreatedAt time.Time
	UpdatedAt time.Time
	Labels    []string
	Assignees []string
	// Comments is how many comments the Issue has.
	Comments int
	// Timeline is the newest events first.
	Timeline []Event
}

func (d IssueDetail) Version() time.Time { return d.UpdatedAt }
func (IssueDetail) isDetail()            {}

// CheckCounts counts a PR head commit's checks by outcome. Neutral and
// skipped checks count as skipped; cancelled, timed out and errored ones
// as failed.
type CheckCounts struct {
	Passed, Failed, Running, Skipped int
}

// PRDetail is a Pull Request as the detail pane shows it.
type PRDetail struct {
	Number    int
	Title     string
	URL       string
	Author    string
	State     PRState
	Draft     bool
	CreatedAt time.Time
	UpdatedAt time.Time
	Labels    []string
	Assignees []string
	Comments  int
	// HeadBranch in HeadRepo merges into BaseBranch; HeadSHA is the head
	// commit (the diff key and the local-state comparison).
	HeadBranch string
	BaseBranch string
	HeadSHA    string
	// HeadRepo is zero when the fork was deleted.
	HeadRepo     Repo
	Checks       CheckState
	CheckCounts  CheckCounts
	Review       ReviewDecision
	MergeState   MergeState
	Additions    int
	Deletions    int
	ChangedFiles int
	// Timeline is the newest events first: reviews, the checks, pushes,
	// comments and state changes.
	Timeline []Event
}

func (d PRDetail) Version() time.Time { return d.UpdatedAt }
func (PRDetail) isDetail()            {}

type gqlTimelineNode struct {
	Typename    string    `json:"__typename"`
	CreatedAt   time.Time `json:"createdAt"`
	SubmittedAt time.Time `json:"submittedAt"`
	Author      *gqlActor `json:"author"`
	Actor       *gqlActor `json:"actor"`
	ReviewState string    `json:"reviewState"`
	Commit      *struct {
		CommittedDate time.Time `json:"committedDate"`
		Author        *struct {
			User *gqlActor `json:"user"`
		} `json:"author"`
	} `json:"commit"`
}

type gqlDetail struct {
	Typename   string    `json:"__typename"`
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	IssueState string    `json:"issueState"`
	PRState    string    `json:"prState"`
	IsDraft    bool      `json:"isDraft"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Author     *gqlActor `json:"author"`
	Labels     gqlLabels `json:"labels"`
	Assignees  struct {
		Nodes []gqlActor `json:"nodes"`
	} `json:"assignees"`
	Comments struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
	HeadRefName    string `json:"headRefName"`
	BaseRefName    string `json:"baseRefName"`
	HeadRefOid     string `json:"headRefOid"`
	HeadRepository *struct {
		Name  string   `json:"name"`
		Owner gqlActor `json:"owner"`
	} `json:"headRepository"`
	ReviewDecision *string `json:"reviewDecision"`
	MergeState     string  `json:"mergeStateStatus"`
	Additions      int     `json:"additions"`
	Deletions      int     `json:"deletions"`
	ChangedFiles   int     `json:"changedFiles"`
	Commits        struct {
		Nodes []struct {
			Commit struct {
				Rollup *struct {
					State    string `json:"state"`
					Contexts struct {
						Nodes []struct {
							Typename    string    `json:"__typename"`
							Conclusion  string    `json:"conclusion"`
							Status      string    `json:"status"`
							CompletedAt time.Time `json:"completedAt"`
							State       string    `json:"state"`
							CreatedAt   time.Time `json:"createdAt"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	Timeline struct {
		Nodes []gqlTimelineNode `json:"nodes"`
	} `json:"timelineItems"`
}

// Detail fetches Issue or Pull Request number of repo in one GraphQL
// request (spec §7): an IssueDetail or a PRDetail. A number that is
// neither is a ClassNotFound error.
func (c *Client) Detail(ctx context.Context, repo Repo, number int) (Detail, error) {
	var data struct {
		Repository *struct {
			Item *gqlDetail `json:"issueOrPullRequest"`
		} `json:"repository"`
	}
	if err := c.repoGraphQL(ctx, repo, DetailQuery, &data, "-F", "number="+strconv.Itoa(number)); err != nil {
		return nil, err
	}
	if data.Repository == nil || data.Repository.Item == nil {
		return nil, &Error{Class: ClassNotFound, Host: repo.HostOrDefault(),
			Message: fmt.Sprintf("no issue or pull request #%d in %s", number, repo)}
	}
	n := data.Repository.Item
	labels := n.Labels.names()
	assignees := make([]string, 0, len(n.Assignees.Nodes))
	for _, a := range n.Assignees.Nodes {
		assignees = append(assignees, a.login())
	}
	if n.Typename != "PullRequest" {
		return IssueDetail{
			Number: n.Number, Title: n.Title, URL: n.URL, Author: n.Author.login(), Closed: n.IssueState == "CLOSED",
			CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, Labels: labels, Assignees: assignees,
			Comments: n.Comments.TotalCount, Timeline: timelineEvents(n.Timeline.Nodes, nil),
		}, nil
	}
	d := PRDetail{
		Number: n.Number, Title: n.Title, URL: n.URL, Author: n.Author.login(), State: PRState(n.PRState),
		Draft: n.IsDraft, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, Labels: labels, Assignees: assignees,
		Comments: n.Comments.TotalCount, HeadBranch: n.HeadRefName, BaseBranch: n.BaseRefName, HeadSHA: n.HeadRefOid,
		MergeState: MergeState(n.MergeState), Additions: n.Additions, Deletions: n.Deletions, ChangedFiles: n.ChangedFiles,
	}
	if h := n.HeadRepository; h != nil && h.Owner.Login != "" && h.Name != "" {
		d.HeadRepo = Repo{Host: repo.HostOrDefault(), Owner: h.Owner.Login, Name: h.Name}
	}
	if n.ReviewDecision != nil {
		d.Review = ReviewDecision(*n.ReviewDecision)
	}
	var checks *Event
	if cs := n.Commits.Nodes; len(cs) > 0 && cs[len(cs)-1].Commit.Rollup != nil {
		r := cs[len(cs)-1].Commit.Rollup
		d.Checks = CheckStateOf(r.State)
		var last time.Time
		for _, x := range r.Contexts.Nodes {
			at := x.CompletedAt
			if x.Typename == "StatusContext" {
				at = x.CreatedAt
				d.CheckCounts.add(statusOutcome(x.State))
			} else {
				d.CheckCounts.add(checkRunOutcome(x.Status, x.Conclusion))
			}
			if at.After(last) {
				last = at
			}
		}
		if !last.IsZero() {
			checks = &Event{Kind: EventChecks, At: last, Checks: d.Checks}
		}
	}
	d.Timeline = timelineEvents(n.Timeline.Nodes, checks)
	return d, nil
}

type checkOutcome int

const (
	outcomePassed checkOutcome = iota
	outcomeFailed
	outcomeRunning
	outcomeSkipped
)

func (c *CheckCounts) add(o checkOutcome) {
	switch o {
	case outcomePassed:
		c.Passed++
	case outcomeFailed:
		c.Failed++
	case outcomeRunning:
		c.Running++
	default:
		c.Skipped++
	}
}

func checkRunOutcome(status, conclusion string) checkOutcome {
	if status != "COMPLETED" {
		return outcomeRunning
	}
	switch conclusion {
	case "SUCCESS":
		return outcomePassed
	case "NEUTRAL", "SKIPPED", "STALE":
		return outcomeSkipped
	}
	return outcomeFailed
}

func statusOutcome(state string) checkOutcome {
	switch state {
	case "SUCCESS":
		return outcomePassed
	case "PENDING", "EXPECTED":
		return outcomeRunning
	}
	return outcomeFailed
}

// timelineEvents turns timeline items (oldest first, as GitHub returns
// them) and the checks event, if any, into events newest first.
// Consecutive commits by one author are one push.
func timelineEvents(nodes []gqlTimelineNode, checks *Event) []Event {
	var out []Event
	for _, n := range nodes {
		var e Event
		switch n.Typename {
		case "IssueComment":
			e = Event{Kind: EventComment, At: n.CreatedAt, Actor: n.Author.login()}
		case "PullRequestReview":
			e = Event{Kind: EventReview, At: n.SubmittedAt, Actor: n.Author.login(), Review: n.ReviewState}
		case "PullRequestCommit":
			if n.Commit == nil {
				continue
			}
			var who *gqlActor
			if n.Commit.Author != nil {
				who = n.Commit.Author.User
			}
			e = Event{Kind: EventPush, At: n.Commit.CommittedDate, Actor: who.login(), Count: 1}
			if k := len(out) - 1; k >= 0 && out[k].Kind == EventPush && out[k].Actor == e.Actor {
				out[k].Count++
				if e.At.After(out[k].At) {
					out[k].At = e.At
				}
				continue
			}
		case "HeadRefForcePushedEvent":
			e = Event{Kind: EventForcePush, At: n.CreatedAt, Actor: n.Actor.login()}
		case "ClosedEvent":
			e = Event{Kind: EventClosed, At: n.CreatedAt, Actor: n.Actor.login()}
		case "ReopenedEvent":
			e = Event{Kind: EventReopened, At: n.CreatedAt, Actor: n.Actor.login()}
		case "MergedEvent":
			e = Event{Kind: EventMerged, At: n.CreatedAt, Actor: n.Actor.login()}
		case "ReadyForReviewEvent":
			e = Event{Kind: EventReady, At: n.CreatedAt, Actor: n.Actor.login()}
		case "CrossReferencedEvent":
			e = Event{Kind: EventReferenced, At: n.CreatedAt, Actor: n.Actor.login()}
		default:
			continue
		}
		out = append(out, e)
	}
	if checks != nil {
		out = append(out, *checks)
	}
	// Newest first; a stable sort keeps GitHub's order among equal times,
	// reversed.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}
