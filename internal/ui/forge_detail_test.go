package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

// serveDetail answers the detail query of me/widgets#number with f.
func serveDetail(fake *githubtest.Fake, number int, f githubtest.Fixture) {
	fake.Serve(f, "api", "--hostname", "github.com", "graphql",
		"-f", "query="+github.DetailQuery, "-f", "owner=me", "-f", "name=widgets",
		"-F", fmt.Sprintf("number=%d", number))
}

// detailCalls counts the detail requests made for number.
func detailCalls(fake *githubtest.Fake, number int) int {
	n := 0
	want := fmt.Sprintf("number=%d", number)
	for _, c := range fake.Calls() {
		if slices.Contains(c.Args, "query="+github.DetailQuery) && slices.Contains(c.Args, want) {
			n++
		}
	}
	return n
}

// linkedTo is a session of the widgets group linked to me/widgets#number.
func linkedTo(id, title string, kind session.LinkKind, number int) *session.Instance {
	inst := &session.Instance{ID: id, Title: title, Tool: "claude", Status: session.StatusStopped,
		GroupPath: "widgets", ProjectPath: "/src/widgets"}
	inst.SetLink(kind, session.Link{Host: "github.com", Owner: "me", Name: "widgets", Number: number})
	return inst
}

// detailIssuesHome is issuesHome with the Issue #14822 detail served and
// no debounce, with instances as its sessions. The first row, #14822, is
// selected.
func detailIssuesHome(t *testing.T, w, h int, instances ...*session.Instance) (*Home, *githubtest.Fake) {
	t.Helper()
	fake := githubtest.NewFake()
	serveIssueList(fake, githubtest.MustLoad(t, githubtest.IssueList))
	serveDetail(fake, 14822, githubtest.MustLoad(t, githubtest.IssueDetail))
	home := forgeHome(t, w, h, false, fake, widgetsCheckout)
	home.forge.fetchers = defaultForgeFetchers()
	home.forge.now = func() time.Time { return issuesT0 }
	home.forge.interval = time.Minute
	home.forge.detailDelay = -1
	setForgeInstances(home, instances)
	return drive(t, home, altKeyMsg('i')), fake
}

// detailPRsHome is the PRs tab with the PR #14927 detail served and no
// debounce, on its row (the second).
func detailPRsHome(t *testing.T, w, h int, instances ...*session.Instance) (*Home, *githubtest.Fake) {
	t.Helper()
	fake := githubtest.NewFake()
	servePRList(fake, githubtest.MustLoad(t, githubtest.PRList))
	serveDetail(fake, 14927, githubtest.MustLoad(t, githubtest.PRDetail))
	home := forgeHome(t, w, h, false, fake, widgetsCheckout)
	home.forge.fetchers = defaultForgeFetchers()
	home.forge.now = func() time.Time { return prsT0 }
	home.forge.interval = time.Minute
	home.forge.detailDelay = -1
	setForgeInstances(home, instances)
	home = drive(t, home, altKeyMsg('p'))
	return drive(t, home, runeKey('j')), fake
}

// TestForgeDetailIssuePane: the dual layout's right pane is the selected
// Issue's detail, titled ISSUE #n: the title, the key/value block and the
// timeline newest first, with linked sessions and the attach key.
func TestForgeDetailIssuePane(t *testing.T) {
	h, fake := detailIssuesHome(t, 120, 30, linkedTo("s1", "triage run", session.LinkIssue, 14822))
	if n := detailCalls(fake, 14822); n != 1 {
		t.Fatalf("%d detail requests, want 1", n)
	}
	frame := stripAnsi(h.View())
	for _, want := range []string{
		"ISSUE #14822",
		"gh run view --job --log-failed fails on required shared-workflow",
		"state     ○ open",
		"author    henrymodisett",
		"labels    bug, priority-3, gh-run",
		"assignee  none",
		"comments  5",
		"sessions  ◉ triage run",
		"15m  henrymodisett commented",
		"2w   cli-triage commented",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame lacks %q:\n%s", want, frame)
		}
	}
	if strings.Index(frame, "15m  henrymodisett") > strings.Index(frame, "2w   cli-triage") {
		t.Fatalf("timeline not newest first:\n%s", frame)
	}
}

// TestForgeDetailPRPane: a PR's detail has its branches, checks, review,
// merge state and diff size, and a timeline of the review, the checks and
// the push.
func TestForgeDetailPRPane(t *testing.T) {
	h, _ := detailPRsHome(t, 120, 30)
	frame := stripAnsi(h.View())
	for _, want := range []string{
		"PR #14927",
		"state     ◌ draft",
		// The recording is of cli/cli, so against me/widgets its head
		// repository reads as a fork.
		"branch    cli:williammartin-multiaccount-draft-08-evaluation",
		"into      williammartin-multiaccount-draft-07-explain",
		"checks    ✗ 1 failed · 11 passed · 13 skipped",
		"review    ? review required",
		"merge     ⊘ blocked",
		"diff      +4835 −1 · 21 files",
		"sessions  none",
		"copilot-pull-request-reviewer reviewed",
		"williammartin pushed 4 commits",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame lacks %q:\n%s", want, frame)
		}
	}
}

// TestForgeDetailCachedThenFresh: going back to a row shows its cached
// detail at once, and every open fetches it fresh.
func TestForgeDetailCachedThenFresh(t *testing.T) {
	h, fake := detailIssuesHome(t, 120, 30)
	h = drive(t, h, runeKey('j'))
	if h.forge.detail.Number != 8954 {
		t.Fatalf("open detail = %+v, want #8954", h.forge.detail)
	}
	// Back on #14822: before any command runs the cached copy is drawn.
	model, cmd := h.Update(runeKey('k'))
	h = model.(*Home)
	if frame := stripAnsi(h.View()); !strings.Contains(frame, "comments  5") {
		t.Fatalf("cached detail not shown at once:\n%s", frame)
	}
	_ = runCmds(t, h, cmd)
	if n := detailCalls(fake, 14822); n != 2 {
		t.Fatalf("%d detail requests for #14822, want 2 (one per open)", n)
	}
}

// TestForgeDetailDebounced: moving over rows quickly fetches only the row
// the cursor stops on.
func TestForgeDetailDebounced(t *testing.T) {
	h, fake := detailIssuesHome(t, 120, 30)
	h.forge.detailDelay = 0 // the default debounce
	model, first := h.Update(runeKey('j'))
	h = model.(*Home)
	model, second := h.Update(runeKey('j'))
	h = model.(*Home)
	h = runCmds(t, h, first)
	h = runCmds(t, h, second)
	if n := detailCalls(fake, 8954); n != 0 {
		t.Fatalf("passed-over row fetched %d times", n)
	}
	if h.forge.detail.Number != 14874 {
		t.Fatalf("open detail = %+v, want #14874", h.forge.detail)
	}
	if n := detailCalls(fake, 14874); n != 1 {
		t.Fatalf("the row the cursor stopped on was fetched %d times, want 1", n)
	}
}

// TestForgeDetailPolledWhenUpdated: while a detail is on screen, a list
// row reporting a newer updatedAt makes the next tick refetch it.
func TestForgeDetailPolledWhenUpdated(t *testing.T) {
	h, fake := detailIssuesHome(t, 120, 30)
	k, _ := h.forgeListKey()
	e, _ := h.forge.fresh.Get(k)
	l := e.Value.(github.IssueList)
	l.Issues = slices.Clone(l.Issues)
	l.Issues[0].UpdatedAt = l.Issues[0].UpdatedAt.Add(time.Minute)
	h = drive(t, h, forgeFetchedMsg{key: k, value: l})
	_ = runCmds(t, h, h.forgeOnTick())
	if n := detailCalls(fake, 14822); n != 2 {
		t.Fatalf("%d detail requests, want 2 (the open and the poll)", n)
	}
}

// TestForgeDetailFailureWithoutCache: a failed detail fetch with nothing
// cached keeps the row's title and says what failed.
func TestForgeDetailFailureWithoutCache(t *testing.T) {
	h, _ := detailIssuesHome(t, 120, 30)
	h = drive(t, h, runeKey('j')) // #8954 has no fixture: gh exits 1
	frame := stripAnsi(h.View())
	if !strings.Contains(frame, "ISSUE #8954") || !strings.Contains(frame, "⚠ gh failed") {
		t.Fatalf("failure not shown:\n%s", frame)
	}
}

// TestForgeDetailSingleColumn: the single column shows the list only; the
// first enter opens the detail full screen, esc returns to the list.
func TestForgeDetailSingleColumn(t *testing.T) {
	h, fake := detailIssuesHome(t, 45, 20)
	if n := detailCalls(fake, 14822); n != 0 {
		t.Fatalf("single column fetched a detail nobody sees (%d)", n)
	}
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	frame := stripAnsi(h.View())
	if !strings.Contains(frame, "ISSUE #14822") || !strings.Contains(frame, "comments  5") {
		t.Fatalf("enter did not open the full-screen detail:\n%s", frame)
	}
	if n := detailCalls(fake, 14822); n != 1 {
		t.Fatalf("%d detail requests, want 1", n)
	}
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEsc})
	frame = stripAnsi(h.View())
	if strings.Contains(frame, "ISSUE #14822") || !strings.Contains(frame, "me/widgets ▾") {
		t.Fatalf("esc did not return to the list:\n%s", frame)
	}
	if !h.forgeTabActive() {
		t.Fatal("esc left the Issues tab")
	}
}

// TestForgeDetailAttach: a with one linked session attaches to it; with
// several it opens a picker (j/k move, enter attaches, esc closes); with
// none it says so.
func TestForgeDetailAttach(t *testing.T) {
	var attached []string
	setup := func(t *testing.T, instances ...*session.Instance) *Home {
		h, _ := detailIssuesHome(t, 120, 30, instances...)
		attached = nil
		h.forge.attach = func(id string) tea.Cmd { attached = append(attached, id); return nil }
		return h
	}

	t.Run("one", func(t *testing.T) {
		h := setup(t, linkedTo("s1", "triage run", session.LinkIssue, 14822))
		h = drive(t, h, runeKey('a'))
		if !slices.Equal(attached, []string{"s1"}) || h.forge.sessionPick != nil {
			t.Fatalf("attached %v, picker %v", attached, h.forge.sessionPick)
		}
	})

	t.Run("several", func(t *testing.T) {
		h := setup(t,
			linkedTo("s1", "triage run", session.LinkIssue, 14822),
			linkedTo("s2", "repro", session.LinkIssue, 14822),
			linkedTo("s3", "other issue", session.LinkIssue, 8954))
		h = drive(t, h, runeKey('a'))
		if len(attached) != 0 || h.forge.sessionPick == nil {
			t.Fatalf("attached %v without a picker", attached)
		}
		pane := listPane(h)
		if !strings.Contains(pane, "triage run") || !strings.Contains(pane, "repro") || strings.Contains(pane, "other issue") {
			t.Fatalf("picker does not list the linked sessions only:\n%s", pane)
		}
		h = drive(t, h, runeKey('j'))
		h = drive(t, h, tea.KeyMsg{Type: tea.KeyEnter})
		if !slices.Equal(attached, []string{"s2"}) || h.forge.sessionPick != nil {
			t.Fatalf("attached %v, picker %v; want s2 and closed", attached, h.forge.sessionPick)
		}
		h = drive(t, h, runeKey('a'))
		h = drive(t, h, tea.KeyMsg{Type: tea.KeyEsc})
		if h.forge.sessionPick != nil || len(attached) != 1 || !h.forgeTabActive() {
			t.Fatalf("esc did not just close the picker: attached %v", attached)
		}
	})

	t.Run("none", func(t *testing.T) {
		h := setup(t)
		h = drive(t, h, runeKey('a'))
		if len(attached) != 0 || h.forge.sessionPick != nil {
			t.Fatalf("attached %v with no linked session", attached)
		}
		if h.err == nil || !strings.Contains(h.err.Error(), "no session linked to #14822") {
			t.Fatalf("message = %v", h.err)
		}
	})
}

// TestForgeDetailGolden freezes the Issue and PR detail panes at dual,
// stacked and single (full-screen, after enter) widths.
func TestForgeDetailGolden(t *testing.T) {
	for _, sz := range forgeFrameSizes {
		for _, kind := range []string{"issue", "pr"} {
			name := fmt.Sprintf("forge_detail_%dx%d_%s", sz[0], sz[1], kind)
			t.Run(name, func(t *testing.T) {
				var h *Home
				if kind == "issue" {
					h, _ = detailIssuesHome(t, sz[0], sz[1],
						linkedTo("s1", "triage run", session.LinkIssue, 14822),
						linkedTo("s2", "repro", session.LinkIssue, 14822))
				} else {
					h, _ = detailPRsHome(t, sz[0], sz[1], linkedTo("s1", "fix login", session.LinkPR, 14927))
				}
				if h.getLayoutMode() == LayoutModeSingle {
					h = drive(t, h, tea.KeyMsg{Type: tea.KeyEnter})
				}
				frame := h.View()
				if got := len(frameLines(frame)); got != sz[1] {
					t.Fatalf("frame has %d rows, want %d", got, sz[1])
				}
				assertFrameGolden(t, name+".golden", stripAnsi(frame))
			})
		}
	}
}

// TestForgeDetailKeyIsVisibleKey: the open detail is one of the visible
// keys, so r refreshes it with the list.
func TestForgeDetailKeyIsVisibleKey(t *testing.T) {
	h, _ := detailIssuesHome(t, 120, 30)
	want := freshness.DetailKey(widgetsRepo, 14822)
	if !slices.Contains(h.forgeVisibleKeys(), want) {
		t.Fatalf("visible keys %v lack %v", h.forgeVisibleKeys(), want)
	}
}
