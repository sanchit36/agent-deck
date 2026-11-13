// Package signals is the Attention signal model (spec #18 §15): the live
// state of the Issues and Pull Requests sessions are linked to, the review
// requests waiting for the user, and the transitions between two polls
// that deserve a notification. The poller (poller.go) fills it; the TUI
// only reads Snapshots, so its render path does no I/O.
package signals

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/github"
)

// Target is one linked item to watch: an Issue, or a Pull Request when PR.
type Target struct {
	Repo   github.Repo
	Number int
	PR     bool
}

// Label is the item's row badge label: "#12" or "PR#34".
func (t Target) Label() string {
	if t.PR {
		return "PR#" + strconv.Itoa(t.Number)
	}
	return "#" + strconv.Itoa(t.Number)
}

// normal spells the default host out, so github.com/o/r is one target.
func (t Target) normal() Target {
	t.Repo.Host = t.Repo.HostOrDefault()
	return t
}

// State is an item's open/closed/merged state.
type State int

const (
	StateOpen State = iota
	StateClosed
	StateMerged
)

// Checks is a Pull Request's overall check state at its head commit.
type Checks int

const (
	ChecksNone Checks = iota
	ChecksPassing
	ChecksFailing
	ChecksRunning
)

// Review is a Pull Request's review decision.
type Review int

const (
	ReviewNone Review = iota
	ReviewRequired
	ReviewApproved
	ReviewChanges
)

// Item is the polled state of one Target. Found is false when GitHub did
// not return it (deleted, not visible).
type Item struct {
	Found  bool
	PR     bool
	State  State
	Checks Checks
	Review Review
}

// Badge is the item's live glyphs (spec #18 §15): ◆ merged or ✕ closed for
// a PR, else ✗ failing checks then ± changes requested or ✔ approved; ✓
// for a closed Issue. Empty when nothing needs saying.
func (it Item) Badge() string {
	if !it.Found {
		return ""
	}
	if !it.PR {
		if it.State == StateClosed {
			return "✓"
		}
		return ""
	}
	switch it.State {
	case StateMerged:
		return "◆"
	case StateClosed:
		return "✕"
	}
	var s string
	if it.Checks == ChecksFailing {
		s += "✗"
	}
	switch it.Review {
	case ReviewChanges:
		s += "±"
	case ReviewApproved:
		s += "✔"
	}
	return s
}

// ReviewRequest is an open Pull Request that asks the user for a review
// directly (team requests do not count).
type ReviewRequest struct {
	Repo   github.Repo
	Number int
	Title  string
}

// HostReviews is one host's review search result.
type HostReviews struct {
	Requests []ReviewRequest
}

// Snapshot is the outcome of the polls so far. It is immutable once
// published: the poller builds a new one for every change.
type Snapshot struct {
	// Items is the last known state of each Target, kept through failed
	// polls.
	Items map[Target]Item
	// Reviews is, per host, the last successful review search.
	Reviews map[string]HostReviews
	// Warn holds the hosts whose last poll failed in a way that will not
	// clear by itself (not logged in, bad token, SAML, gh missing): their
	// badges show ⚠.
	Warn map[string]bool
}

// Item returns t's polled state.
func (s *Snapshot) Item(t Target) (Item, bool) {
	if s == nil {
		return Item{}, false
	}
	it, ok := s.Items[t.normal()]
	return it, ok
}

// Badge is t's glyphs: ⚠ while t's host has a login problem, else the
// item's Badge, empty when unknown.
func (s *Snapshot) Badge(t Target) string {
	if s == nil {
		return ""
	}
	t = t.normal()
	if s.Warn[t.Repo.Host] {
		return "⚠"
	}
	return s.Items[t].Badge()
}

// ReviewCount is the number of review requests waiting for the user on
// every host.
func (s *Snapshot) ReviewCount() int {
	if s == nil {
		return 0
	}
	n := 0
	for _, r := range s.Reviews {
		n += len(r.Requests)
	}
	return n
}

// ReviewCountFor is the share of the review requests that are in repo,
// matched as github.SameRepo does (case-insensitively).
func (s *Snapshot) ReviewCountFor(repo github.Repo) int {
	if s == nil {
		return 0
	}
	n := 0
	for host, hr := range s.Reviews {
		if !strings.EqualFold(host, repo.HostOrDefault()) {
			continue
		}
		for _, r := range hr.Requests {
			if github.SameRepo(r.Repo, repo) {
				n++
			}
		}
	}
	return n
}

// EventKind is a transition worth a notification.
type EventKind int

const (
	EventChecksFailing EventKind = iota + 1
	EventChangesRequested
	EventApproved
	EventMerged
	EventClosed
	EventReviewRequested
)

// Event is one transition: of Target, or a new Review request.
type Event struct {
	Kind   EventKind
	Target Target
	Review ReviewRequest
}

// Glyph is the event's mark on the notification bar: the badge glyph of
// the new state, ⇠ for a review request.
func (e Event) Glyph() string {
	switch e.Kind {
	case EventChecksFailing:
		return "✗"
	case EventChangesRequested:
		return "±"
	case EventApproved:
		return "✔"
	case EventMerged:
		return "◆"
	case EventClosed:
		return "✕"
	case EventReviewRequested:
		return "⇠"
	}
	return ""
}

// Text is the event's one-line description.
func (e Event) Text() string {
	label := e.Target.Label()
	switch e.Kind {
	case EventChecksFailing:
		return label + " checks failing"
	case EventChangesRequested:
		return label + " changes requested"
	case EventApproved:
		return label + " approved"
	case EventMerged:
		return label + " merged"
	case EventClosed:
		return label + " closed"
	case EventReviewRequested:
		r := e.Review
		return "review requested: " + r.Repo.Owner + "/" + r.Repo.Name + "#" + strconv.Itoa(r.Number) + " " + r.Title
	}
	return label
}

// Transitions is what changed from prev to next for one item that deserves
// a notification: checks going to failing, the review going to changes
// requested or approved, the PR merged or closed. Nothing for a first
// sighting (havePrev false, or prev not found), and nothing for Issues.
func Transitions(prev, next Item, havePrev bool) []EventKind {
	if !havePrev || !prev.Found || !next.Found || !prev.PR || !next.PR {
		return nil
	}
	if next.State != prev.State {
		switch next.State {
		case StateMerged:
			return []EventKind{EventMerged}
		case StateClosed:
			return []EventKind{EventClosed}
		}
	}
	if next.State != StateOpen {
		return nil
	}
	var out []EventKind
	if next.Checks == ChecksFailing && prev.Checks != ChecksFailing {
		out = append(out, EventChecksFailing)
	}
	if next.Review != prev.Review {
		switch next.Review {
		case ReviewChanges:
			out = append(out, EventChangesRequested)
		case ReviewApproved:
			out = append(out, EventApproved)
		}
	}
	return out
}

// Diff is the events from prev to next: item transitions in target order,
// then new review requests host by host. A
// nil prev (first load, a restart) has none, and so has a host whose
// review search prev did not know.
func Diff(prev, next *Snapshot) []Event {
	if prev == nil || next == nil {
		return nil
	}
	var out []Event
	for _, t := range sortedTargets(next.Items) {
		p, ok := prev.Items[t]
		for _, k := range Transitions(p, next.Items[t], ok) {
			out = append(out, Event{Kind: k, Target: t})
		}
	}
	for _, host := range sortedHosts(next.Reviews) {
		before, ok := prev.Reviews[host]
		if !ok {
			continue
		}
		seen := map[ReviewRequest]bool{}
		for _, r := range before.Requests {
			seen[key(r)] = true
		}
		for _, r := range next.Reviews[host].Requests {
			if !seen[key(r)] {
				out = append(out, Event{Kind: EventReviewRequested, Review: r})
			}
		}
	}
	return out
}

// key identifies a review request regardless of its title.
func key(r ReviewRequest) ReviewRequest { return ReviewRequest{Repo: r.Repo, Number: r.Number} }

func sortedTargets(m map[Target]Item) []Target {
	ts := make([]Target, 0, len(m))
	for t := range m {
		ts = append(ts, t)
	}
	slices.SortFunc(ts, func(a, b Target) int {
		return cmp.Or(
			cmp.Compare(a.Repo.String(), b.Repo.String()),
			cmp.Compare(a.Number, b.Number),
			cmp.Compare(boolInt(a.PR), boolInt(b.PR)),
		)
	})
	return ts
}

func sortedHosts[V any](m map[string]V) []string {
	hs := make([]string, 0, len(m))
	for h := range m {
		hs = append(hs, h)
	}
	slices.Sort(hs)
	return hs
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
