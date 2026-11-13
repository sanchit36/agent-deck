package signals

import (
	"slices"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
)

var (
	widgets = github.Repo{Host: "github.com", Owner: "acme", Name: "widgets"}
	pr34    = Target{Repo: widgets, Number: 34, PR: true}
	issue12 = Target{Repo: widgets, Number: 12}
)

func openPR(c Checks, r Review) Item {
	return Item{Found: true, PR: true, State: StateOpen, Checks: c, Review: r}
}

// TestTransitions is the transition table of spec #18 §15: a notification
// only when a condition starts to hold, never for a first sighting.
func TestTransitions(t *testing.T) {
	cases := []struct {
		name       string
		prev, next Item
		havePrev   bool
		want       []EventKind
	}{
		{"first sighting failing", Item{}, openPR(ChecksFailing, ReviewChanges), false, nil},
		{"unchanged failing", openPR(ChecksFailing, ReviewNone), openPR(ChecksFailing, ReviewNone), true, nil},
		{"passing to failing", openPR(ChecksPassing, ReviewNone), openPR(ChecksFailing, ReviewNone), true, []EventKind{EventChecksFailing}},
		{"running to failing", openPR(ChecksRunning, ReviewNone), openPR(ChecksFailing, ReviewNone), true, []EventKind{EventChecksFailing}},
		{"failing to passing", openPR(ChecksFailing, ReviewNone), openPR(ChecksPassing, ReviewNone), true, nil},
		{"required to changes", openPR(ChecksPassing, ReviewRequired), openPR(ChecksPassing, ReviewChanges), true, []EventKind{EventChangesRequested}},
		{"changes to approved", openPR(ChecksPassing, ReviewChanges), openPR(ChecksPassing, ReviewApproved), true, []EventKind{EventApproved}},
		{"approved stays", openPR(ChecksPassing, ReviewApproved), openPR(ChecksRunning, ReviewApproved), true, nil},
		{"checks and review at once", openPR(ChecksRunning, ReviewNone), openPR(ChecksFailing, ReviewApproved), true, []EventKind{EventChecksFailing, EventApproved}},
		{"merged", openPR(ChecksPassing, ReviewApproved), Item{Found: true, PR: true, State: StateMerged, Review: ReviewApproved}, true, []EventKind{EventMerged}},
		{"closed", openPR(ChecksFailing, ReviewNone), Item{Found: true, PR: true, State: StateClosed, Checks: ChecksFailing}, true, []EventKind{EventClosed}},
		{"merged stays", Item{Found: true, PR: true, State: StateMerged}, Item{Found: true, PR: true, State: StateMerged}, true, nil},
		{"previous not found", Item{}, openPR(ChecksFailing, ReviewNone), true, nil},
		{"was not a PR", Item{Found: true, State: StateOpen}, openPR(ChecksFailing, ReviewNone), true, nil},
		{"issue closed", Item{Found: true, State: StateOpen}, Item{Found: true, State: StateClosed}, true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Transitions(c.prev, c.next, c.havePrev)
			if !slices.Equal(got, c.want) {
				t.Fatalf("Transitions = %v, want %v", got, c.want)
			}
		})
	}
}

// TestDiff: events between two snapshots, review requests included; none
// against no previous snapshot (first load, or after a restart).
func TestDiff(t *testing.T) {
	rr := ReviewRequest{Repo: widgets, Number: 77, Title: "Add widgets"}
	rr2 := ReviewRequest{Repo: widgets, Number: 78, Title: "More widgets"}
	next := &Snapshot{
		Items:   map[Target]Item{pr34: openPR(ChecksFailing, ReviewNone), issue12: {Found: true, State: StateClosed}},
		Reviews: map[string]HostReviews{"github.com": {Requests: []ReviewRequest{rr, rr2}}},
	}
	if got := Diff(nil, next); len(got) != 0 {
		t.Fatalf("Diff(nil, next) = %v, want no events on first load", got)
	}
	prev := &Snapshot{
		Items:   map[Target]Item{pr34: openPR(ChecksPassing, ReviewNone), issue12: {Found: true, State: StateOpen}},
		Reviews: map[string]HostReviews{"github.com": {Requests: []ReviewRequest{rr}}},
	}
	got := Diff(prev, next)
	want := []Event{
		{Kind: EventChecksFailing, Target: pr34},
		{Kind: EventReviewRequested, Review: rr2},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Diff = %+v, want %+v", got, want)
	}
	// A host whose review search was not known before (it failed, or the
	// host is new) is a first load for its requests.
	prev.Reviews = map[string]HostReviews{}
	if got := Diff(prev, next); !slices.Equal(got, want[:1]) {
		t.Fatalf("Diff with unknown previous reviews = %+v, want %+v", got, want[:1])
	}
}

func TestEventText(t *testing.T) {
	cases := []struct {
		e           Event
		want, glyph string
	}{
		{Event{Kind: EventChecksFailing, Target: pr34}, "PR#34 checks failing", "✗"},
		{Event{Kind: EventChangesRequested, Target: pr34}, "PR#34 changes requested", "±"},
		{Event{Kind: EventApproved, Target: pr34}, "PR#34 approved", "✔"},
		{Event{Kind: EventMerged, Target: pr34}, "PR#34 merged", "◆"},
		{Event{Kind: EventClosed, Target: pr34}, "PR#34 closed", "✕"},
		{Event{Kind: EventReviewRequested, Review: ReviewRequest{Repo: widgets, Number: 77, Title: "Add widgets"}}, "review requested: acme/widgets#77 Add widgets", "⇠"},
	}
	for _, c := range cases {
		if got := c.e.Text(); got != c.want {
			t.Errorf("Text() = %q, want %q", got, c.want)
		}
		if got := c.e.Glyph(); got != c.glyph {
			t.Errorf("%s: Glyph() = %q, want %q", c.want, got, c.glyph)
		}
	}
}

// TestBadge is the glyph set of spec #18 §15.
func TestBadge(t *testing.T) {
	cases := []struct {
		name string
		it   Item
		want string
	}{
		{"not found", Item{}, ""},
		{"open passing", openPR(ChecksPassing, ReviewNone), ""},
		{"running required", openPR(ChecksRunning, ReviewRequired), ""},
		{"failing", openPR(ChecksFailing, ReviewNone), "✗"},
		{"changes", openPR(ChecksPassing, ReviewChanges), "±"},
		{"approved", openPR(ChecksPassing, ReviewApproved), "✔"},
		{"failing approved", openPR(ChecksFailing, ReviewApproved), "✗✔"},
		{"merged", Item{Found: true, PR: true, State: StateMerged, Checks: ChecksFailing}, "◆"},
		{"closed", Item{Found: true, PR: true, State: StateClosed}, "✕"},
		{"issue open", Item{Found: true, State: StateOpen}, ""},
		{"issue closed", Item{Found: true, State: StateClosed}, "✓"},
	}
	for _, c := range cases {
		if got := c.it.Badge(); got != c.want {
			t.Errorf("%s: Badge() = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestReviewCountForIgnoresCase: the on-screen Project comes from a git
// remote, whose spelling may differ in case from the repository GitHub's
// review search returns; the PRS chip still counts its requests.
func TestReviewCountForIgnoresCase(t *testing.T) {
	snap := &Snapshot{Reviews: map[string]HostReviews{
		"github.com": {Requests: []ReviewRequest{
			{Repo: github.Repo{Host: "github.com", Owner: "Acme", Name: "Widgets"}, Number: 81},
			{Repo: github.Repo{Host: "github.com", Owner: "acme", Name: "gadgets"}, Number: 82},
		}},
	}}
	for _, repo := range []github.Repo{
		{Owner: "acme", Name: "widgets"},
		{Host: "GitHub.com", Owner: "ACME", Name: "widgets"},
	} {
		if got := snap.ReviewCountFor(repo); got != 1 {
			t.Errorf("ReviewCountFor(%v) = %d, want 1", repo, got)
		}
	}
}
