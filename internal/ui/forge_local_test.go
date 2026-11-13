package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/gitstate"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

// prLocalReader is a fake local-state reader: it answers with value and
// records each query.
type prLocalReader struct {
	mu      sync.Mutex
	value   gitstate.PRLocal
	queries []gitstate.PRQuery
}

func (r *prLocalReader) read(_ context.Context, q gitstate.PRQuery) gitstate.PRLocal {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, q)
	return r.value
}

func (r *prLocalReader) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.queries)
}

// localPRsHome is detailPRsHome with r reading the local state: the PRs
// tab on PR #14927, its detail open.
func localPRsHome(t *testing.T, w, h int, r *prLocalReader, instances ...*session.Instance) *Home {
	t.Helper()
	fake := githubtest.NewFake()
	servePRList(fake, githubtest.MustLoad(t, githubtest.PRList))
	serveDetail(fake, 14927, githubtest.MustLoad(t, githubtest.PRDetail))
	home := forgeHome(t, w, h, false, fake, widgetsCheckout)
	home.forge.fetchers = defaultForgeFetchers()
	home.forge.now = func() time.Time { return prsT0 }
	home.forge.interval = time.Minute
	home.forge.detailDelay = -1
	home.forge.localRead = r.read
	setForgeInstances(home, instances)
	home = drive(t, home, altKeyMsg('p'))
	return drive(t, home, runeKey('j'))
}

// localLine is the frame's local row, from its key on.
func localLine(t *testing.T, h *Home) string {
	t.Helper()
	for _, l := range frameLines(h.View()) {
		if i := strings.Index(l, "local     "); i >= 0 {
			return strings.TrimRight(l[i:], " │")
		}
	}
	t.Fatalf("no local row:\n%s", stripAnsi(h.View()))
	return ""
}

const prBranch = "feature/42-x"

// TestForgePRLocalRow: the local row shows the first checkout's branch,
// its comparison with the PR head and dirt, then +n more; none without a
// checkout and unavailable when the state could not be read.
func TestForgePRLocalRow(t *testing.T) {
	co := func(c gitstate.Compare) gitstate.Checkout {
		return gitstate.Checkout{Path: "/wt/s1", Branch: prBranch, Compare: c}
	}
	tests := []struct {
		name  string
		value gitstate.PRLocal
		want  string
	}{
		{"none", gitstate.PRLocal{}, "local     none"},
		{"repo unreadable", gitstate.PRLocal{Err: fmt.Errorf("listing worktrees: boom")}, "local     unavailable"},
		{"in sync", gitstate.PRLocal{Checkouts: []gitstate.Checkout{co(gitstate.Compare{Kind: gitstate.InSync})}},
			"local     feature/42-x · in sync"},
		{"unpushed and dirty, more checkouts",
			gitstate.PRLocal{Checkouts: []gitstate.Checkout{
				{Branch: prBranch, Compare: gitstate.Compare{Kind: gitstate.Unpushed, Ahead: 2}, Dirty: true},
				co(gitstate.Compare{Kind: gitstate.InSync}),
				{Branch: "other", Compare: gitstate.Compare{Kind: gitstate.InSync}},
			}},
			"local     feature/42-x · ↑2 unpushed · dirty  +2 more"},
		{"behind", gitstate.PRLocal{Checkouts: []gitstate.Checkout{co(gitstate.Compare{Kind: gitstate.Behind, Behind: 3})}},
			"local     feature/42-x · ↓3 behind"},
		{"head never fetched", gitstate.PRLocal{Checkouts: []gitstate.Checkout{co(gitstate.Compare{Kind: gitstate.Newer})}},
			"local     feature/42-x · ↓ PR has newer commits"},
		{"diverged", gitstate.PRLocal{Checkouts: []gitstate.Checkout{co(gitstate.Compare{Kind: gitstate.Diverged, Ahead: 1, Behind: 2})}},
			"local     feature/42-x · ↑1 ↓2"},
		{"jj checkout", gitstate.PRLocal{Checkouts: []gitstate.Checkout{{Path: "/wt/s1", JJ: true}}}, "local     unavailable"},
		{"checkout unreadable", gitstate.PRLocal{Checkouts: []gitstate.Checkout{{Path: "/wt/s1", Err: fmt.Errorf("gone")}}},
			"local     unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := localPRsHome(t, 120, 30, &prLocalReader{value: tc.value})
			if got := localLine(t, h); got != tc.want {
				t.Fatalf("local row = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestForgePRLocalRowChecking: before the first read the row says so.
func TestForgePRLocalRowChecking(t *testing.T) {
	if got := stripAnsi(prLocalValue(gitstate.PRLocal{}, false)); got != "checking…" {
		t.Fatalf("got %q, want checking…", got)
	}
}

// TestForgePRLocalQuery: the read looks in the Project's local repo for
// checkouts of the PR head, with the linked sessions' checkouts first.
func TestForgePRLocalQuery(t *testing.T) {
	r := &prLocalReader{}
	s1 := worktreeSession("s1", "fix login", "/src/widgets", prBranch)
	s1.SetLink(session.LinkPR, session.Link{Host: "github.com", Owner: "me", Name: "widgets", Number: 14927})
	localPRsHome(t, 120, 30, r, s1,
		linkedTo("s2", "plain", session.LinkPR, 14927),
		linkedTo("s3", "issue of the same number", session.LinkIssue, 14927))
	if r.calls() != 1 {
		t.Fatalf("%d reads, want 1", r.calls())
	}
	q := r.queries[0]
	want := gitstate.PRQuery{
		Root: "/src/widgets", Linked: []string{"/wt/s1", "/src/widgets"}, Number: 14927,
		Branch: "williammartin-multiaccount-draft-08-evaluation", SHA: "d43c4e3ad3c34332c803a568f85f116e3ee58f19",
		// The recording is of cli/cli, so against me/widgets it is a fork PR.
		CrossRepo: true, HeadOwner: "cli",
	}
	if fmt.Sprint(q) != fmt.Sprint(want) {
		t.Fatalf("query = %+v\nwant    %+v", q, want)
	}
}

// TestForgePRLocalTestsNeverRead is a guard for the PR detail tests: the
// forgeHome helper answers local reads with none, never real git.
func TestForgePRLocalTestsNeverRead(t *testing.T) {
	h, _ := detailPRsHome(t, 120, 30)
	if got := localLine(t, h); got != "local     none" {
		t.Fatalf("local row = %q, want none", got)
	}
}

// TestForgePRLocalCachedByHeadSHA: the comparison is read once per PR head
// SHA: redrawing or the same head arriving again reads nothing; a new head
// SHA, a manual refresh, reopening the PR or an expired entry read again.
func TestForgePRLocalCachedByHeadSHA(t *testing.T) {
	r := &prLocalReader{value: gitstate.PRLocal{Checkouts: []gitstate.Checkout{{Branch: prBranch, Compare: gitstate.Compare{Kind: gitstate.InSync}}}}}
	h := localPRsHome(t, 120, 30, r)
	if r.calls() != 1 {
		t.Fatalf("%d reads on open, want 1", r.calls())
	}
	key := h.forge.detail
	cur := func() github.PRDetail {
		e, _ := h.forge.fresh.Get(key)
		return e.Value.(github.PRDetail)
	}
	deliver := func(d github.PRDetail) {
		model, cmd := h.Update(forgeFetchedMsg{key: key, value: d})
		h = runCmds(t, model.(*Home), cmd)
	}

	for range 3 {
		_ = h.View()
	}
	deliver(cur())
	if r.calls() != 1 {
		t.Fatalf("%d reads after redraws and the same head again, want 1", r.calls())
	}

	d := cur()
	d.HeadSHA = "1111111111111111111111111111111111111111"
	deliver(d)
	if r.calls() != 2 || r.queries[1].SHA != d.HeadSHA {
		t.Fatalf("new head SHA: %d reads (%+v), want a second for it", r.calls(), r.queries)
	}

	h = drive(t, h, runeKey('r'))
	if r.calls() != 3 {
		t.Fatalf("manual refresh: %d reads, want 3", r.calls())
	}

	h = drive(t, h, runeKey('k'))
	h = drive(t, h, runeKey('j'))
	if r.calls() != 4 {
		t.Fatalf("reopen: %d reads, want 4", r.calls())
	}

	now := prsT0.Add(gitstate.TTL + time.Second)
	h.forge.now = func() time.Time { return now }
	deliver(cur())
	if r.calls() != 5 {
		t.Fatalf("after the TTL: %d reads, want 5", r.calls())
	}
}

// TestForgePRLocalNotForIssues: Issue detail has no local row and reads
// nothing.
func TestForgePRLocalNotForIssues(t *testing.T) {
	h, _ := detailIssuesHome(t, 120, 30)
	r := &prLocalReader{}
	h.forge.localRead = r.read
	h = drive(t, h, runeKey('r'))
	if r.calls() != 0 || strings.Contains(stripAnsi(h.View()), "local     ") {
		t.Fatalf("issue detail read local state (%d) or shows a local row", r.calls())
	}
}

// TestForgePRLocalRowGolden freezes the PR detail with a local row at
// dual and single (full-screen) widths.
func TestForgePRLocalRowGolden(t *testing.T) {
	value := gitstate.PRLocal{Checkouts: []gitstate.Checkout{
		{Path: "/wt/s1", Branch: prBranch, Linked: true, Compare: gitstate.Compare{Kind: gitstate.Unpushed, Ahead: 2}, Dirty: true},
		{Branch: prBranch, Compare: gitstate.Compare{Kind: gitstate.InSync}},
	}}
	for _, sz := range [][2]int{{120, 30}, {45, 20}} {
		name := fmt.Sprintf("forge_detail_local_%dx%d", sz[0], sz[1])
		t.Run(name, func(t *testing.T) {
			h := localPRsHome(t, sz[0], sz[1], &prLocalReader{value: value})
			if h.getLayoutMode() == LayoutModeSingle {
				h = drive(t, h, tea.KeyMsg{Type: tea.KeyEnter})
			}
			assertFrameGolden(t, name+".golden", stripAnsi(h.View()))
		})
	}
}
