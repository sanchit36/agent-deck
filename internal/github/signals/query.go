package signals

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/github"
)

// reviewSearch is GitHub search syntax for the open Pull Requests that ask
// the user for a review by name: user-review-requested leaves out requests
// made to a team the user is in.
const reviewSearch = "is:pr is:open archived:false user-review-requested:@me"

// itemFields asks for an Issue's state and a Pull Request's state, review
// decision and overall check state at its head commit (never the full
// statusCheckRollup contexts).
const itemFields = `__typename ... on Issue { state } ... on PullRequest { state reviewDecision commits(last: 1) { nodes { commit { statusCheckRollup { state } } } } }`

// slot is where one Target's answer sits in the reply: data[repo][item].
type slot struct {
	repo, item string
	target     Target
}

// buildQuery is one host's poll: every linked item of the host, grouped by
// repository under aliases (r0, r1, ... and i0, i1, ...), plus the review
// search. One GraphQL request, whatever the number of items.
func buildQuery(targets []Target) (string, []slot) {
	byRepo := map[github.Repo][]Target{}
	for _, t := range targets {
		byRepo[t.Repo] = append(byRepo[t.Repo], t)
	}
	repos := make([]github.Repo, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	slices.SortFunc(repos, func(a, b github.Repo) int { return strings.Compare(a.String(), b.String()) })

	var b strings.Builder
	var slots []slot
	b.WriteString("query {")
	for ri, repo := range repos {
		ts := byRepo[repo]
		slices.SortFunc(ts, func(a, b Target) int {
			if a.Number != b.Number {
				return a.Number - b.Number
			}
			return boolInt(a.PR) - boolInt(b.PR)
		})
		ra := fmt.Sprintf("r%d", ri)
		fmt.Fprintf(&b, " %s: repository(owner: %s, name: %s) {", ra, quote(repo.Owner), quote(repo.Name))
		for ii, t := range ts {
			ia := fmt.Sprintf("i%d", ii)
			fmt.Fprintf(&b, " %s: issueOrPullRequest(number: %d) { %s }", ia, t.Number, itemFields)
			slots = append(slots, slot{repo: ra, item: ia, target: t})
		}
		b.WriteString(" }")
	}
	fmt.Fprintf(&b, ` reviews: search(query: %s, type: ISSUE, first: 100) { nodes { ... on PullRequest { number title repository { name owner { login } } } } }`, quote(reviewSearch))
	b.WriteString(" }")
	return b.String(), slots
}

// quote is s as a GraphQL string literal (JSON's escaping is GraphQL's).
func quote(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}

type itemReply struct {
	Typename       string `json:"__typename"`
	State          string `json:"state"`
	ReviewDecision string `json:"reviewDecision"`
	Commits        struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

type pollReply struct {
	Data map[string]json.RawMessage `json:"data"`
}

// parseReply reads one host's poll reply: each slot's Item (Found false
// when GitHub returned no such repository or item) and the review requests.
// A reply with no data at all is an error.
func parseReply(host string, out []byte, slots []slot) (map[Target]Item, HostReviews, error) {
	var reply pollReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return nil, HostReviews{}, fmt.Errorf("parsing the attention signal reply from %s: %w", host, err)
	}
	if reply.Data == nil {
		return nil, HostReviews{}, fmt.Errorf("parsing the attention signal reply from %s: no data", host)
	}
	repos := map[string]map[string]*itemReply{}
	for _, s := range slots {
		if _, done := repos[s.repo]; done {
			continue
		}
		var items map[string]*itemReply
		if raw := reply.Data[s.repo]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, HostReviews{}, fmt.Errorf("parsing %s of the attention signal reply from %s: %w", s.repo, host, err)
			}
		}
		repos[s.repo] = items
	}
	got := make(map[Target]Item, len(slots))
	for _, s := range slots {
		got[s.target] = toItem(repos[s.repo][s.item])
	}

	var reviews HostReviews
	if raw := reply.Data["reviews"]; len(raw) > 0 && string(raw) != "null" {
		var search struct {
			Nodes []struct {
				Number     int    `json:"number"`
				Title      string `json:"title"`
				Repository struct {
					Name  string `json:"name"`
					Owner struct {
						Login string `json:"login"`
					} `json:"owner"`
				} `json:"repository"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(raw, &search); err != nil {
			return nil, HostReviews{}, fmt.Errorf("parsing the review search from %s: %w", host, err)
		}
		for _, n := range search.Nodes {
			if n.Number == 0 {
				continue // not a Pull Request
			}
			reviews.Requests = append(reviews.Requests, ReviewRequest{
				Repo:   github.Repo{Host: host, Owner: n.Repository.Owner.Login, Name: n.Repository.Name},
				Number: n.Number,
				Title:  n.Title,
			})
		}
	}
	return got, reviews, nil
}

func toItem(r *itemReply) Item {
	if r == nil || r.Typename == "" {
		return Item{}
	}
	it := Item{Found: true, PR: r.Typename == "PullRequest"}
	switch r.State {
	case "CLOSED":
		it.State = StateClosed
	case "MERGED":
		it.State = StateMerged
	}
	if !it.PR {
		return it
	}
	switch r.ReviewDecision {
	case "APPROVED":
		it.Review = ReviewApproved
	case "CHANGES_REQUESTED":
		it.Review = ReviewChanges
	case "REVIEW_REQUIRED":
		it.Review = ReviewRequired
	}
	if n := r.Commits.Nodes; len(n) > 0 && n[len(n)-1].Commit.StatusCheckRollup != nil {
		switch n[len(n)-1].Commit.StatusCheckRollup.State {
		case "SUCCESS":
			it.Checks = ChecksPassing
		case "FAILURE", "ERROR":
			it.Checks = ChecksFailing
		case "PENDING", "EXPECTED":
			it.Checks = ChecksRunning
		}
	}
	return it
}
