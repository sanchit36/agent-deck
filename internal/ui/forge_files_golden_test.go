package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/testutil"
)

// TestForgeFilesGolden freezes the Files tab over the recorded diff of
// cli/cli PR #14927 at dual (list beside the diff), stacked and single
// widths (narrow: the header carries the position), the narrow list
// opened with f, and navDiff's a.go (hunks, a tab, a long line cut).
func TestForgeFilesGolden(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		home func(t *testing.T, w, h int) *Home
		keys string
	}{
		{name: "120x30", w: 120, h: 30},
		{name: "70x24", w: 70, h: 24},
		{name: "45x20", w: 45, h: 20},
		{name: "70x24_list", w: 70, h: 24, keys: "fjjj"},
		{name: "120x30_version", w: 120, h: 30, keys: "]]]]]]]]]]]"},
		{name: "120x30_nav", w: 120, h: 30, home: navHome},
	}
	for _, tc := range cases {
		name := "forge_files_" + tc.name
		t.Run(name, func(t *testing.T) {
			var h *Home
			if tc.home != nil {
				h = tc.home(t, tc.w, tc.h)
			} else {
				h, _ = filesPRHome(t, tc.w, tc.h)
				h = openFiles(t, h)
			}
			for _, r := range tc.keys {
				h = drive(t, h, runeKey(r))
			}
			frame := h.View()
			if got := len(frameLines(frame)); got != tc.h {
				t.Fatalf("frame has %d rows, want %d", got, tc.h)
			}
			assertFrameGolden(t, name+".golden", stripAnsi(frame))
		})
	}
}

// TestPerf_ForgeFilesKeyFrame: with a 1500-line file expanded on screen,
// a key and the frame it draws stay inside the 16 ms key-frame budget,
// whether the key scrolls the diff or switches to the big file (a cold
// row model).
func TestPerf_ForgeFilesKeyFrame(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/small.go b/small.go\n--- a/small.go\n+++ b/small.go\n@@ -1 +1 @@\n-a\n+b\n")
	b.WriteString("diff --git a/big.go b/big.go\n--- a/big.go\n+++ b/big.go\n@@ -1,750 +1,750 @@\n")
	for i := 0; i < 750; i++ {
		fmt.Fprintf(&b, "-\told line %d %s\n", i, strings.Repeat("x", i%160))
	}
	for i := 0; i < 750; i++ {
		fmt.Fprintf(&b, "+\tnew line %d %s\n", i, strings.Repeat("y", i%160))
	}
	h, _ := diffHome(t, 160, 50, b.String())
	h = openFiles(t, h)
	h.selectForgeFile(1)
	h = drive(t, h, overlayEnter())
	d, _ := h.forgeFilesDiff()
	if d.Files[1].Changed() != 1500 || len(h.forgeFilesRows().rows) < 1500 {
		t.Fatalf("the big file is not expanded: %d changed, %d rows", d.Files[1].Changed(), len(h.forgeFilesRows().rows))
	}
	budget := 16 * time.Millisecond * time.Duration(testutil.BudgetMultiplier(t)*1000) / 1000

	scroll := testutil.TrimmedMean(func() {
		h = drive(t, h, runeKey('j'))
		_ = h.View()
	})
	if scroll > budget {
		t.Fatalf("scrolling a 1500-line diff: %v per key frame, budget %v", scroll, budget)
	}
	switchFile := testutil.TrimmedMean(func() {
		h = drive(t, h, runeKey('['))
		_ = h.View()
		h = drive(t, h, runeKey(']'))
		_ = h.View()
	})
	if switchFile/2 > budget {
		t.Fatalf("switching to a 1500-line diff: %v per key frame, budget %v", switchFile/2, budget)
	}
}

// TestForgeFilesKeepsFileOnNewHead: when a push brings a new head, the
// old diff stays on screen until the new one arrives, which keeps the
// selected file.
func TestForgeFilesKeepsFileOnNewHead(t *testing.T) {
	h := navHome(t, 120, 30)
	h = drive(t, h, runeKey(']'))
	h = drive(t, h, runeKey(']'))
	o := &h.forge.overlay
	old := o.files.sha
	dk := freshness.DetailKey(widgetsRepo, 14927)
	e, _ := h.forge.fresh.Get(dk)
	d := e.Value.(github.PRDetail)
	d.HeadSHA = "0000000000000000000000000000000000000002"
	// The new head's diff fails: the old one stays, with the failure in
	// the footer.
	h.forge.fetchers[freshness.Diff] = func(context.Context, *github.Client, freshness.Key) (any, error) {
		return nil, errors.New("offline")
	}
	h = drive(t, h, forgeFetchedMsg{key: dk, value: d})
	if frame := filesFrame(h); o.files.sha != old || !strings.Contains(frame, "file 3/6") || !strings.Contains(frame, "⚠ stale: ⚠ offline") {
		t.Fatalf("the old diff left the screen before the new one arrived (sha %q):\n%s", o.files.sha, filesFrame(h))
	}
	// It arrives without go.sum: logo.png is still selected.
	next := github.ParseDiff(strings.Replace(navDiff, "diff --git a/go.sum b/go.sum\n--- a/go.sum\n+++ b/go.sum\n@@ -1,1 +1,2 @@\n x v1 h1:a\n+y v1 h1:b\n", "", 1))
	h = drive(t, h, forgeFetchedMsg{key: freshness.DiffKey(widgetsRepo, 14927, d.HeadSHA), value: next})
	if o.files.sha != d.HeadSHA || !strings.Contains(filesFrame(h), "img/logo.png") || !strings.Contains(filesFrame(h), "file 2/5") {
		t.Fatalf("the new diff did not keep the selected file:\n%s", filesFrame(h))
	}
}
