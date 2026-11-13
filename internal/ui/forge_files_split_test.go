package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// twoHunkDiff is one file with two 40-line hunks, each a changed block.
var twoHunkDiff = func() string {
	var b strings.Builder
	b.WriteString("diff --git a/long.go b/long.go\n--- a/long.go\n+++ b/long.go\n")
	for _, start := range []int{1, 100} {
		fmt.Fprintf(&b, "@@ -%d,40 +%d,40 @@\n", start, start)
		for i := 0; i < 20; i++ {
			fmt.Fprintf(&b, "-old %d\n", start+i)
		}
		for i := 0; i < 20; i++ {
			fmt.Fprintf(&b, "+new %d\n", start+i)
		}
		for i := 20; i < 40; i++ {
			fmt.Fprintf(&b, " same %d\n", start+i)
		}
	}
	return b.String()
}()

// diffTop is the first diff row under the file header (wide layout).
func diffTop(t *testing.T, h *Home) string {
	t.Helper()
	lines := frameLines(h.View())
	for i, l := range lines {
		if strings.Contains(l, "file 1/1") && i+1 < len(lines) {
			return lines[i+1]
		}
	}
	t.Fatalf("no file header:\n%s", filesFrame(h))
	return ""
}

// TestForgeFilesSplitPairsSides: s shows old | new side by side, a
// removed line beside the added line it pairs with, each side with its
// own line number; s again goes back to unified.
func TestForgeFilesSplitPairsSides(t *testing.T) {
	h, _ := diffHome(t, 120, 30, twoHunkDiff)
	h = openFiles(t, h)
	h = drive(t, h, runeKey('s'))
	frame := filesFrame(h)
	var pair string
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "old 1 ") {
			pair = l
			break
		}
	}
	if !strings.Contains(pair, "new 1 ") || strings.Index(pair, "old 1 ") > strings.Index(pair, "new 1 ") {
		t.Fatalf("old 1 is not on the left beside new 1:\n%s", frame)
	}
	if strings.Contains(frame, "split view needs") {
		t.Fatalf("120 columns said split is unavailable:\n%s", frame)
	}
	h = drive(t, h, runeKey('s'))
	for _, l := range strings.Split(filesFrame(h), "\n") {
		if strings.Contains(l, "old 1 ") && strings.Contains(l, "new 1 ") {
			t.Fatalf("s did not go back to unified:\n%s", filesFrame(h))
		}
	}
}

// TestForgeFilesSplitNarrow: under 120 columns s keeps the unified view
// and says why; widening the terminal then shows the split.
func TestForgeFilesSplitNarrow(t *testing.T) {
	h, _ := diffHome(t, 100, 30, twoHunkDiff)
	h = openFiles(t, h)
	h = drive(t, h, runeKey('s'))
	frame := filesFrame(h)
	if !strings.Contains(frame, "split view needs 120+ columns") {
		t.Fatalf("no reason for the unified view at 100 columns:\n%s", frame)
	}
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "old 1 ") && strings.Contains(l, "new 1 ") {
			t.Fatalf("split shown at 100 columns:\n%s", frame)
		}
	}
	h = drive(t, h, tea.WindowSizeMsg{Width: 130, Height: 30})
	frame = filesFrame(h)
	if strings.Contains(frame, "split view needs") {
		t.Fatalf("the reason stayed at 130 columns:\n%s", frame)
	}
	found := false
	for _, l := range strings.Split(frame, "\n") {
		found = found || (strings.Contains(l, "old 1 ") && strings.Contains(l, "new 1 "))
	}
	if !found {
		t.Fatalf("no split at 130 columns:\n%s", frame)
	}
}

// TestForgeFilesSplitHunks: n and N move by the split rows' own hunks, and
// toggling keeps the hunk at the top.
func TestForgeFilesSplitHunks(t *testing.T) {
	h, _ := diffHome(t, 120, 30, twoHunkDiff)
	h = openFiles(t, h)
	h = drive(t, h, runeKey('n'))
	if top := diffTop(t, h); !strings.Contains(top, "@@ -100,40 +100,40 @@") {
		t.Fatalf("n did not bring the second hunk up in unified: %q", top)
	}
	h = drive(t, h, runeKey('s'))
	if top := diffTop(t, h); !strings.Contains(top, "@@ -100,40 +100,40 @@") {
		t.Fatalf("s lost the hunk at the top: %q", top)
	}
	h = drive(t, h, runeKey('N'))
	if top := diffTop(t, h); !strings.Contains(top, "@@ -1,40 +1,40 @@") {
		t.Fatalf("N did not go back to the first hunk in split: %q", top)
	}
	h = drive(t, h, runeKey('n'))
	if top := diffTop(t, h); !strings.Contains(top, "@@ -100,40 +100,40 @@") {
		t.Fatalf("n did not bring the second hunk up in split: %q", top)
	}
}

// TestForgeFilesSplitPan: l pans both sides far enough to show the end of
// the longest line, which a split side cuts sooner than unified.
func TestForgeFilesSplitPan(t *testing.T) {
	h := navHome(t, 120, 30)
	h = drive(t, h, runeKey('s'))
	if strings.Contains(filesFrame(h), "END") {
		t.Fatalf("the long line is not cut in split:\n%s", filesFrame(h))
	}
	for i := 0; i < 300; i++ {
		h = drive(t, h, runeKey('l'))
	}
	if !strings.Contains(filesFrame(h), "END") {
		t.Fatalf("panning to the end does not show the long line's end:\n%s", filesFrame(h))
	}
}
