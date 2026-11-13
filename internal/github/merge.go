package github

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/ghexec"
)

// MergeInfoQuery is the GraphQL query the merge dialog refetches when it
// opens (spec §14): the PR's head SHA, branches, checks, review and
// mergeStateStatus, its auto-merge and merge queue state, GitHub's default
// commit titles, and the repository's merge settings.
const MergeInfoQuery = `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){` +
	`mergeCommitAllowed squashMergeAllowed rebaseMergeAllowed autoMergeAllowed deleteBranchOnMerge viewerDefaultMergeMethod ` +
	`pullRequest(number:$number){number title url state isDraft headRefName baseRefName headRefOid isCrossRepository ` +
	`headRepository{name owner{login}} reviewDecision mergeStateStatus isMergeQueueEnabled isInMergeQueue ` +
	`autoMergeRequest{mergeMethod} mergeTitle:viewerMergeHeadlineText(mergeType:MERGE) ` +
	`squashTitle:viewerMergeHeadlineText(mergeType:SQUASH) ` +
	`commits(last:1){nodes{commit{statusCheckRollup{state contexts(first:100){nodes{__typename ` +
	`...on CheckRun{conclusion status} ...on StatusContext{state}}}}}}}}}}`

// MergeMethod is how a PR is merged: GitHub's PullRequestMergeMethod.
type MergeMethod string

const (
	MethodMerge  MergeMethod = "MERGE"
	MethodSquash MergeMethod = "SQUASH"
	MethodRebase MergeMethod = "REBASE"
)

// Word is the method as the dialog and the toast name it: merge, squash or
// rebase.
func (m MergeMethod) Word() string {
	switch m {
	case MethodMerge:
		return "merge"
	case MethodSquash:
		return "squash"
	case MethodRebase:
		return "rebase"
	}
	return ""
}

// flag is the method's `gh pr merge` flag.
func (m MergeMethod) flag() string {
	if w := m.Word(); w != "" {
		return "--" + w
	}
	return ""
}

// MergeInfo is what the merge dialog shows and decides on, fetched fresh
// when it opens.
type MergeInfo struct {
	Number int
	Title  string
	URL    string
	State  PRState
	Draft  bool
	// HeadBranch merges into BaseBranch; HeadSHA is the head commit the
	// merge is pinned to (--match-head-commit).
	HeadBranch string
	BaseBranch string
	HeadSHA    string
	// HeadRepo is zero when the fork was deleted; CrossRepo is true for a
	// PR from a fork.
	HeadRepo    Repo
	CrossRepo   bool
	Checks      CheckState
	CheckCounts CheckCounts
	Review      ReviewDecision
	MergeState  MergeState
	// Methods are the methods the repository allows, in the order merge,
	// squash, rebase; DefaultMethod is the viewer's default when it is
	// allowed, else the first allowed one.
	Methods       []MergeMethod
	DefaultMethod MergeMethod
	// DeleteBranchOnMerge is the repository's "automatically delete head
	// branches" setting; AutoMergeAllowed its "allow auto-merge".
	DeleteBranchOnMerge bool
	AutoMergeAllowed    bool
	// AutoMerge is true when auto-merge is on for the PR.
	AutoMerge bool
	// MergeQueue is true when the base branch has a merge queue;
	// InMergeQueue when the PR is in it.
	MergeQueue   bool
	InMergeQueue bool

	mergeTitle  string
	squashTitle string
}

// CommitTitle is GitHub's default commit title for merging with method;
// empty for a rebase, which makes no merge commit.
func (i MergeInfo) CommitTitle(method MergeMethod) string {
	switch method {
	case MethodMerge:
		return i.mergeTitle
	case MethodSquash:
		return i.squashTitle
	}
	return ""
}

type gqlMergeInfo struct {
	MergeCommitAllowed  bool    `json:"mergeCommitAllowed"`
	SquashMergeAllowed  bool    `json:"squashMergeAllowed"`
	RebaseMergeAllowed  bool    `json:"rebaseMergeAllowed"`
	AutoMergeAllowed    bool    `json:"autoMergeAllowed"`
	DeleteBranchOnMerge bool    `json:"deleteBranchOnMerge"`
	ViewerDefault       *string `json:"viewerDefaultMergeMethod"`
	PR                  *struct {
		Number         int    `json:"number"`
		Title          string `json:"title"`
		URL            string `json:"url"`
		State          string `json:"state"`
		IsDraft        bool   `json:"isDraft"`
		HeadRefName    string `json:"headRefName"`
		BaseRefName    string `json:"baseRefName"`
		HeadRefOid     string `json:"headRefOid"`
		CrossRepo      bool   `json:"isCrossRepository"`
		HeadRepository *struct {
			Name  string   `json:"name"`
			Owner gqlActor `json:"owner"`
		} `json:"headRepository"`
		ReviewDecision   *string   `json:"reviewDecision"`
		MergeState       string    `json:"mergeStateStatus"`
		MergeQueue       bool      `json:"isMergeQueueEnabled"`
		InMergeQueue     bool      `json:"isInMergeQueue"`
		AutoMergeRequest *struct{} `json:"autoMergeRequest"`
		MergeTitle       string    `json:"mergeTitle"`
		SquashTitle      string    `json:"squashTitle"`
		Commits          struct {
			Nodes []struct {
				Commit struct {
					Rollup *struct {
						State    string `json:"state"`
						Contexts struct {
							Nodes []struct {
								Typename   string `json:"__typename"`
								Conclusion string `json:"conclusion"`
								Status     string `json:"status"`
								State      string `json:"state"`
							} `json:"nodes"`
						} `json:"contexts"`
					} `json:"statusCheckRollup"`
				} `json:"commit"`
			} `json:"nodes"`
		} `json:"commits"`
	} `json:"pullRequest"`
}

// MergeInfo fetches what the merge dialog needs for PR number of repo in
// one GraphQL request. A number that is not a PR is a ClassNotFound error.
func (c *Client) MergeInfo(ctx context.Context, repo Repo, number int) (MergeInfo, error) {
	var data struct {
		Repository *gqlMergeInfo `json:"repository"`
	}
	if err := c.repoGraphQL(ctx, repo, MergeInfoQuery, &data, "-F", "number="+strconv.Itoa(number)); err != nil {
		return MergeInfo{}, err
	}
	r := data.Repository
	if r == nil || r.PR == nil {
		return MergeInfo{}, &Error{Class: ClassNotFound, Host: repo.HostOrDefault(),
			Message: fmt.Sprintf("no pull request #%d in %s", number, repo)}
	}
	p := r.PR
	info := MergeInfo{
		Number: p.Number, Title: p.Title, URL: p.URL, State: PRState(p.State), Draft: p.IsDraft,
		HeadBranch: p.HeadRefName, BaseBranch: p.BaseRefName, HeadSHA: p.HeadRefOid, CrossRepo: p.CrossRepo,
		MergeState: MergeState(p.MergeState), DeleteBranchOnMerge: r.DeleteBranchOnMerge,
		AutoMergeAllowed: r.AutoMergeAllowed, AutoMerge: p.AutoMergeRequest != nil,
		MergeQueue: p.MergeQueue, InMergeQueue: p.InMergeQueue,
		mergeTitle: p.MergeTitle, squashTitle: p.SquashTitle,
	}
	if h := p.HeadRepository; h != nil && h.Owner.Login != "" && h.Name != "" {
		info.HeadRepo = Repo{Host: repo.HostOrDefault(), Owner: h.Owner.Login, Name: h.Name}
	}
	if p.ReviewDecision != nil {
		info.Review = ReviewDecision(*p.ReviewDecision)
	}
	if cs := p.Commits.Nodes; len(cs) > 0 && cs[len(cs)-1].Commit.Rollup != nil {
		roll := cs[len(cs)-1].Commit.Rollup
		info.Checks = CheckStateOf(roll.State)
		for _, x := range roll.Contexts.Nodes {
			info.CheckCounts.addContext(x.Typename, x.Status, x.Conclusion, x.State)
		}
	}
	for _, m := range []struct {
		method  MergeMethod
		allowed bool
	}{{MethodMerge, r.MergeCommitAllowed}, {MethodSquash, r.SquashMergeAllowed}, {MethodRebase, r.RebaseMergeAllowed}} {
		if m.allowed {
			info.Methods = append(info.Methods, m.method)
		}
	}
	if len(info.Methods) > 0 {
		info.DefaultMethod = info.Methods[0]
		if r.ViewerDefault != nil {
			for _, m := range info.Methods {
				if m == MergeMethod(*r.ViewerDefault) {
					info.DefaultMethod = m
				}
			}
		}
	}
	return info, nil
}

// MergeAction is what confirming the merge dialog does.
type MergeAction string

const (
	// ActionMerge merges now with the chosen method.
	ActionMerge MergeAction = "merge"
	// ActionAutoMerge turns auto-merge on (gh pr merge --auto): GitHub
	// merges once the checks and reviews it waits on pass.
	ActionAutoMerge MergeAction = "auto"
	// ActionDisableAuto turns auto-merge off (gh pr merge --disable-auto).
	ActionDisableAuto MergeAction = "disable_auto"
	// ActionQueue adds the PR to its base branch's merge queue, which sets
	// the method.
	ActionQueue MergeAction = "queue"
)

// MergeGuard is the merge dialog's decision for a MergeInfo: either an
// Action it may take or why it is Blocked. Nothing ever bypasses a block
// (no --admin).
type MergeGuard struct {
	Action MergeAction
	// Blocked says why nothing can be done; empty when Action is set.
	Blocked string
	// Warning is shown in red next to an allowed Action (UNSTABLE).
	Warning string
	// Refetch is true while GitHub has not computed mergeability yet
	// (UNKNOWN): the dialog refetches once.
	Refetch bool
}

// GuardMerge applies the merge-guard table of spec §14 to info.
func GuardMerge(info MergeInfo) MergeGuard {
	blocked := func(why string) MergeGuard { return MergeGuard{Blocked: why} }
	switch {
	case info.State == PRMerged:
		return blocked("already merged")
	case info.State == PRClosed:
		return blocked("closed")
	case info.InMergeQueue:
		return blocked("already in the merge queue")
	case info.AutoMerge:
		return MergeGuard{Action: ActionDisableAuto}
	case info.Draft || info.MergeState == MergeDraft:
		return blocked("draft: mark it ready on GitHub")
	case info.MergeState == MergeDirty:
		return blocked("conflicts with " + info.BaseBranch + ": start a session (n) to resolve them")
	case info.MergeState == MergeUnknown || info.MergeState == "":
		g := blocked("GitHub is still checking whether it can merge: try again")
		g.Refetch = true
		return g
	case info.MergeQueue:
		return MergeGuard{Action: ActionQueue}
	case len(info.Methods) == 0:
		return blocked("the repository allows no merge method")
	}
	switch info.MergeState {
	case MergeClean, MergeHasHooks:
		return MergeGuard{Action: ActionMerge}
	case MergeUnstable:
		return MergeGuard{Action: ActionMerge, Warning: "some checks are failing"}
	case MergeBehind:
		return blocked("behind " + info.BaseBranch + ": update the branch first")
	case MergeBlocked:
		if !waitingOnChecksOrReview(info) {
			return blocked("blocked by branch protection")
		}
		if !info.AutoMergeAllowed {
			return blocked("waiting on checks or review; auto-merge is off for this repository")
		}
		return MergeGuard{Action: ActionAutoMerge}
	}
	return blocked("not mergeable (" + string(info.MergeState) + ")")
}

// MergeRequest is one confirmed merge dialog.
type MergeRequest struct {
	Number int
	// HeadSHA is the head commit the dialog showed: the merge happens only
	// if the PR head is still that commit (--match-head-commit).
	HeadSHA string
	Action  MergeAction
	// Method is the chosen method (ActionMerge, ActionAutoMerge).
	Method MergeMethod
	// DeleteBranch deletes the remote head branch after a direct merge.
	// gh with -R never touches local branches.
	DeleteBranch bool
}

// mergeArgs is req as `gh pr merge` arguments. Every action that can
// merge carries --match-head-commit; there is no way to pass --admin.
func mergeArgs(req MergeRequest) ([]string, error) {
	args := []string{"pr", "merge", strconv.Itoa(req.Number)}
	if req.Action == ActionDisableAuto {
		return append(args, "--disable-auto"), nil
	}
	if req.HeadSHA == "" {
		return nil, fmt.Errorf("merging #%d: no head commit to pin the merge to", req.Number)
	}
	needMethod := func() error {
		if req.Method.flag() == "" {
			return fmt.Errorf("merging #%d: no merge method", req.Number)
		}
		return nil
	}
	switch req.Action {
	case ActionMerge:
		if err := needMethod(); err != nil {
			return nil, err
		}
		args = append(args, req.Method.flag(), "--match-head-commit", req.HeadSHA)
		if req.DeleteBranch {
			args = append(args, "--delete-branch")
		}
	case ActionAutoMerge:
		if err := needMethod(); err != nil {
			return nil, err
		}
		args = append(args, "--auto", req.Method.flag(), "--match-head-commit", req.HeadSHA)
	case ActionQueue:
		// The merge queue sets the method.
		args = append(args, "--auto", "--match-head-commit", req.HeadSHA)
	default:
		return nil, fmt.Errorf("merging #%d: unknown merge action %q", req.Number, req.Action)
	}
	return args, nil
}

// Merge runs the confirmed merge dialog as one `gh pr merge` on repo,
// bounded by ghexec.MergeTimeout. A head that moved since the dialog read
// it is refused by GitHub (MergeHeadMoved).
func (c *Client) Merge(ctx context.Context, repo Repo, req MergeRequest) error {
	args, err := mergeArgs(req)
	if err != nil {
		return err
	}
	_, err = c.Repo(ctx, repo, Call{Args: args, Timeout: ghexec.MergeTimeout})
	return err
}

var headMovedMarkers = []string{"head branch was modified", "expected head", "expectedheadoid", "head sha"}

// MergeHeadMoved reports whether a Merge failed because the PR head is no
// longer the commit the dialog showed.
func MergeHeadMoved(err error) bool {
	var e *Error
	if !errors.As(err, &e) || e.Class != ClassFailed {
		return false
	}
	return containsAny(strings.ToLower(e.Message), headMovedMarkers)
}

// MergeErrorText is a failed Merge as the dialog says it: gh's first line
// without its ✗ mark or GraphQL prefix (gh's hints, among them --admin,
// are on the lines after it), or the failure class.
func MergeErrorText(err error) string {
	var e *Error
	if !errors.As(err, &e) {
		return err.Error()
	}
	if e.Class == ClassNetwork && errors.Is(e.Err, context.DeadlineExceeded) {
		return fmt.Sprintf("gh did not answer within %s: GitHub may still merge it", ghexec.MergeTimeout)
	}
	msg := strings.TrimSpace(e.Message)
	for _, p := range []string{"X ", "✗ ", "! ", "GraphQL: "} {
		msg = strings.TrimPrefix(msg, p)
	}
	if strings.Contains(msg, "--admin") {
		msg = ""
	}
	text := classText[e.Class]
	switch {
	case e.Class == ClassFailed && msg != "":
		return msg
	case msg != "":
		return text + ": " + msg
	}
	return text
}

// waitingOnChecksOrReview reports whether a BLOCKED PR has checks that have
// not passed or a review it still needs: what auto-merge waits for.
func waitingOnChecksOrReview(info MergeInfo) bool {
	return info.Checks == ChecksRunning || info.Checks == ChecksFailing ||
		info.Review == ReviewRequired || info.Review == ReviewChangesRequested
}
