package ui

import (
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	tea "github.com/charmbracelet/bubbletea"
)

func leftClick(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
}

// forgeRowY is the screen row of list item i's first line (under the
// Project line), with the list scrolled to the top.
func forgeRowY(h *Home, i int) int {
	return h.getListContentStartY() + 1 + i*forgeRowHeight
}

// TestForgeListClickSelectsRow: a click on any line of a list item moves
// the forge cursor to it (spec #18 §4) and never opens it; a click under
// the last item does nothing.
func TestForgeListClickSelectsRow(t *testing.T) {
	for _, sz := range [][2]int{{120, 30}, {70, 24}, {45, 20}} {
		h, _ := issuesHome(t, sz[0], sz[1], githubtest.IssueList)
		for _, c := range []struct{ item, line int }{{2, 0}, {1, 1}, {0, 0}, {2, 2}} {
			if c.item >= h.forgeListPage() {
				continue // not on screen at this size
			}
			h.forge.now = func() time.Time { return issuesT0.Add(time.Duration(c.item+1) * time.Minute) }
			h = drive(t, h, leftClick(3, forgeRowY(h, c.item)+c.line))
			if got := h.forge.lists[tabIssues].cursor; got != c.item {
				t.Fatalf("%v: click on item %d line %d: cursor = %d", sz, c.item, c.line, got)
			}
			if h.forgeOverlayVisible() || h.forge.detailFull {
				t.Fatalf("%v: a single click opened the item", sz)
			}
		}
		count := h.forge.lists[tabIssues].count
		page := h.forgeListPage()
		if count < page {
			before := h.forge.lists[tabIssues].cursor
			h = drive(t, h, leftClick(3, forgeRowY(h, count)))
			if h.forge.lists[tabIssues].cursor != before {
				t.Fatalf("%v: a click under the last item moved the cursor", sz)
			}
		}
	}
}

// TestForgeListClickScrolled: with the list scrolled, a click maps through
// the viewport offset.
func TestForgeListClickScrolled(t *testing.T) {
	h, _ := issuesHome(t, 120, 14, githubtest.IssueList)
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEnd})
	l := h.forge.lists[tabIssues]
	if l.offset == 0 {
		t.Fatalf("list did not scroll: count %d page %d", l.count, h.forgeListPage())
	}
	h = drive(t, h, leftClick(3, forgeRowY(h, 0)))
	if got := h.forge.lists[tabIssues].cursor; got != l.offset {
		t.Fatalf("click on the first visible item: cursor = %d, want %d", got, l.offset)
	}
}

// TestForgeListDoubleClickOpens: a second click on the same item within the
// threshold is enter: the overlay in the dual layout, the full-screen
// detail first in the single column. A slow second click only selects.
func TestForgeListDoubleClickOpens(t *testing.T) {
	t.Run("dual", func(t *testing.T) {
		h, _ := overlayIssueHome(t, 120, 30)
		y := forgeRowY(h, 0)
		h = drive(t, h, leftClick(3, y))
		h = drive(t, h, leftClick(3, y+1))
		if !h.forgeOverlayVisible() {
			t.Fatal("double-click did not open the overlay")
		}
		if h.forge.overlay.row.number != 14822 {
			t.Fatalf("overlay opened #%d, want #14822", h.forge.overlay.row.number)
		}
	})
	t.Run("different items", func(t *testing.T) {
		h, _ := issuesHome(t, 120, 30, githubtest.IssueList)
		h = drive(t, h, leftClick(3, forgeRowY(h, 0)))
		h = drive(t, h, leftClick(3, forgeRowY(h, 1)))
		if h.forgeOverlayVisible() {
			t.Fatal("clicks on two items opened the overlay")
		}
	})
	t.Run("slow", func(t *testing.T) {
		h, _ := issuesHome(t, 120, 30, githubtest.IssueList)
		y := forgeRowY(h, 0)
		h = drive(t, h, leftClick(3, y))
		h.forge.now = func() time.Time { return issuesT0.Add(time.Second) }
		h = drive(t, h, leftClick(3, y))
		if h.forgeOverlayVisible() {
			t.Fatal("a slow second click opened the overlay")
		}
	})
	t.Run("single column", func(t *testing.T) {
		h, _ := issuesHome(t, 45, 20, githubtest.IssueList)
		y := forgeRowY(h, 0)
		h = drive(t, h, leftClick(3, y))
		h = drive(t, h, leftClick(3, y))
		if !h.forge.detailFull {
			t.Fatal("double-click did not open the full-screen detail")
		}
	})
}

// TestForgeOverlayTabClick: a click on an overlay tab switches to it; an
// Issue's overlay has no Files tab to click.
func TestForgeOverlayTabClick(t *testing.T) {
	h, _ := overlayPRHome(t, 120, 30)
	h = openOverlay(t, h)
	segs := forgeOverlayTabSegments(true, h.forge.overlay.tab)
	if len(segs) != 2 {
		t.Fatalf("%d tab segments, want 2", len(segs))
	}
	files := segs[1]
	h = drive(t, h, leftClick(files.x0+1, 1))
	if h.forge.overlay.tab != forgeOverlayFiles {
		t.Fatalf("click on Files: tab = %v", h.forge.overlay.tab)
	}
	conv := segs[0]
	h = drive(t, h, leftClick(conv.x0+1, 1))
	if h.forge.overlay.tab != forgeOverlayConversation {
		t.Fatalf("click on Conversation: tab = %v", h.forge.overlay.tab)
	}
	// A click elsewhere in the overlay does nothing (and keeps it open).
	h = drive(t, h, leftClick(files.x0+1, 5))
	if !h.forgeOverlayVisible() || h.forge.overlay.tab != forgeOverlayConversation {
		t.Fatal("a click on the body changed the overlay")
	}

	hi, _ := overlayIssueHome(t, 120, 30)
	hi = openOverlay(t, hi)
	hi = drive(t, hi, leftClick(files.x0+1, 1))
	if hi.forge.overlay.tab != forgeOverlayConversation {
		t.Fatal("an Issue's overlay switched to Files")
	}
}

// TestForgeDetailFullIgnoresClicks: the single column's full-screen detail
// covers the list and the tab strip, so a click there never selects the
// off-screen row under it, a double-click never opens the overlay and a
// click on the title row never switches tabs.
func TestForgeDetailFullIgnoresClicks(t *testing.T) {
	h, _ := issuesHome(t, 45, 20, githubtest.IssueList)
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	if !h.forgeDetailFull() {
		t.Fatal("enter did not open the full-screen detail")
	}
	before := h.forge.lists[tabIssues].cursor
	y := forgeRowY(h, 1)
	h = drive(t, h, leftClick(3, y))
	h = drive(t, h, leftClick(3, y))
	if got := h.forge.lists[tabIssues].cursor; got != before {
		t.Fatalf("a click on the detail moved the cursor to %d, want %d", got, before)
	}
	if h.forgeOverlayVisible() {
		t.Fatal("a double-click on the detail opened the overlay")
	}
	segs := h.forgeStripSegments(h.width)
	h = drive(t, h, leftClick(segs[tabPRs].x0, h.getListContentStartY()-2))
	if h.forge.tab != tabIssues || !h.forgeDetailFull() {
		t.Fatalf("a click on the detail's title row switched to %v", h.forge.tab)
	}
}
