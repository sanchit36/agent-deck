package ui

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

// serveDiff answers the diff request of me/widgets#number with f.
func serveDiff(fake *githubtest.Fake, number int, f githubtest.Fixture) {
	fake.Serve(f, github.PRDiffArgs(widgetsRepo, number)...)
}

// diffCalls counts the diff requests made for number.
func diffCalls(fake *githubtest.Fake, number int) int {
	n := 0
	want := github.PRDiffArgs(widgetsRepo, number)
	for _, c := range fake.Calls() {
		if slices.Equal(c.Args, want) {
			n++
		}
	}
	return n
}

// filesPRHome is overlayPRHome with the recorded diff of #14927 served.
func filesPRHome(t *testing.T, w, h int) (*Home, *githubtest.Fake) {
	t.Helper()
	home, fake := overlayPRHome(t, w, h)
	serveDiff(fake, 14927, githubtest.MustLoad(t, githubtest.PRDiff))
	return home, fake
}

// diffHome is overlayPRHome with #14927's diff answered by text.
func diffHome(t *testing.T, w, h int, text string) (*Home, *githubtest.Fake) {
	t.Helper()
	home, fake := overlayPRHome(t, w, h)
	serveDiff(fake, 14927, githubtest.Fixture{Result: github.Result{Stdout: []byte(text)}})
	return home, fake
}

// openFiles presses d on the list and runs the fetches.
func openFiles(t *testing.T, h *Home) *Home {
	t.Helper()
	return drive(t, h, runeKey('d'))
}

func filesFrame(h *Home) string { return stripAnsi(h.View()) }

// TestForgeFilesOpensFromList: d on a PR row opens the overlay on Files
// with a loading line, then the file list beside the first file's diff
// under its header, with line numbers; d on an Issue row does nothing.
func TestForgeFilesOpensFromList(t *testing.T) {
	h, fake := filesPRHome(t, 120, 30)
	model, cmd := h.Update(runeKey('d'))
	h = model.(*Home)
	if !h.forgeOverlayVisible() || h.forge.overlay.tab != forgeOverlayFiles {
		t.Fatal("d did not open the overlay on Files")
	}
	if frame := filesFrame(h); !strings.Contains(frame, "loading…") {
		t.Fatalf("no loading line before the diff arrives:\n%s", frame)
	}
	h = runCmds(t, h, cmd)
	if n := diffCalls(fake, 14927); n != 1 {
		t.Fatalf("%d diff requests, want 1", n)
	}
	frame := filesFrame(h)
	for _, want := range []string{
		"[ Files ]",
		"Files 21", "+4835 −1",
		"ng-up-identites/SKILL.md +79 −0", "M pkg/cmd/version/version.go +5 −1",
		"A .github/skills/setting-up-identites/SKILL.md", "file 1/21",
		"@@ -0,0 +1,79 @@",
		"  1 + ---",
		"  2 + name: setting-up-identites",
		"⚠ large PR: 4,836 changed lines in 21 files",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("Files tab lacks %q:\n%s", want, frame)
		}
	}

	is, _ := overlayIssueHome(t, 120, 30)
	is = drive(t, is, runeKey('d'))
	if is.forgeOverlayVisible() {
		t.Fatal("d opened the overlay on an Issue")
	}
}

// TestForgeFilesSingleColumnFetchesHead: in the single column no detail
// pane is open, so the Files tab fetches the PR detail itself for the
// head SHA, then the diff.
func TestForgeFilesSingleColumnFetchesHead(t *testing.T) {
	h, fake := filesPRHome(t, 45, 20)
	h = openFiles(t, h)
	if n := diffCalls(fake, 14927); n != 1 {
		t.Fatalf("%d diff requests, want 1", n)
	}
	if frame := filesFrame(h); !strings.Contains(frame, "file 1/21") {
		t.Fatalf("no diff in the single column:\n%s", frame)
	}
}

// TestForgeFilesDiffCachedByHead: the diff is fetched once per head SHA:
// r, switching tabs and reopening never refetch it; a new head does.
func TestForgeFilesDiffCachedByHead(t *testing.T) {
	h, fake := filesPRHome(t, 120, 30)
	h = openFiles(t, h)
	h = drive(t, h, runeKey('r'))
	h = drive(t, h, runeKey('c'))
	h = drive(t, h, runeKey('d'))
	h = drive(t, h, overlayEsc())
	h = openFiles(t, h)
	for i := 0; i < 5; i++ {
		h = runCmds(t, h, h.forgeOnTick())
	}
	if n := diffCalls(fake, 14927); n != 1 {
		t.Fatalf("%d diff requests for one head SHA, want 1", n)
	}
	if n := detailCalls(fake, 14927); n < 2 {
		t.Fatalf("%d detail requests: r did not refresh the head", n)
	}
	k := freshness.DiffKey(widgetsRepo, 14927, "d43c4e3ad3c34332c803a568f85f116e3ee58f19")
	if e, ok := h.forge.fresh.Get(k); !ok || !e.HasData() {
		t.Fatal("the diff is not cached under the head SHA")
	}

	// A push: the detail now reports a new head.
	dk := freshness.DetailKey(widgetsRepo, 14927)
	e, _ := h.forge.fresh.Get(dk)
	d := e.Value.(github.PRDetail)
	d.HeadSHA = "0000000000000000000000000000000000000001"
	_ = drive(t, h, forgeFetchedMsg{key: dk, value: d})
	if n := diffCalls(fake, 14927); n != 2 {
		t.Fatalf("%d diff requests after a new head, want 2", n)
	}
}

// TestForgeFilesFailure: a failed diff fetch with nothing cached says
// what failed in the Files tab.
func TestForgeFilesFailure(t *testing.T) {
	h, _ := overlayPRHome(t, 120, 30) // no diff served: gh exits 1
	h = openFiles(t, h)
	if frame := filesFrame(h); !strings.Contains(frame, "⚠ gh failed") {
		t.Fatalf("failure not shown:\n%s", frame)
	}
}

// TestForgeFilesHintFooterFits: at 120 columns the Files tab's hint footer
// fits whole by leaving out the hints every tab shares (o, y, r, j/k: they
// are in the help), so the Files keys, m merge and esc close all show;
// wider, nothing is left out.
func TestForgeFilesHintFooterFits(t *testing.T) {
	h, _ := filesPRHome(t, 120, 30)
	h = openFiles(t, h)
	lines := frameLines(h.View())
	footer := strings.TrimRight(lines[len(lines)-1], " ")
	if strings.Contains(footer, "…") {
		t.Fatalf("footer cut: %q", footer)
	}
	for _, want := range []string{"n/N hunk", "|/\\ pager", "S new session", "m merge", "esc close"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer lacks %q: %q", want, footer)
		}
	}

	wide, _ := filesPRHome(t, 200, 30)
	wide = openFiles(t, wide)
	lines = frameLines(wide.View())
	if footer := lines[len(lines)-1]; !strings.Contains(footer, "o open · y copy url · r refresh · esc close") {
		t.Fatalf("wide footer left hints out: %q", footer)
	}
}

// TestForgeFilesDropsSupersededDiff: after a push, the old head's diff
// stays on screen while the new head's loads, then is dropped from the
// cache once the new head's diff is cached.
func TestForgeFilesDropsSupersededDiff(t *testing.T) {
	h, fake := filesPRHome(t, 120, 30)
	h = openFiles(t, h)
	oldKey := freshness.DiffKey(widgetsRepo, 14927, "d43c4e3ad3c34332c803a568f85f116e3ee58f19")
	if e, ok := h.forge.fresh.Get(oldKey); !ok || !e.HasData() {
		t.Fatal("the first head's diff is not cached")
	}

	dk := freshness.DetailKey(widgetsRepo, 14927)
	e, _ := h.forge.fresh.Get(dk)
	d := e.Value.(github.PRDetail)
	d.HeadSHA = "0000000000000000000000000000000000000001"
	model, cmd := h.Update(forgeFetchedMsg{key: dk, value: d})
	h = model.(*Home)
	if e, ok := h.forge.fresh.Get(oldKey); !ok || !e.HasData() {
		t.Fatal("the old head's diff was dropped while the new head's loads")
	}
	h = runCmds(t, h, cmd)
	if n := diffCalls(fake, 14927); n != 2 {
		t.Fatalf("%d diff requests, want 2", n)
	}
	newKey := freshness.DiffKey(widgetsRepo, 14927, d.HeadSHA)
	if e, ok := h.forge.fresh.Get(newKey); !ok || !e.HasData() || h.forge.overlay.files.sha != d.HeadSHA {
		t.Fatal("the new head's diff is not cached and on screen")
	}
	if _, ok := h.forge.fresh.Get(oldKey); ok {
		t.Fatal("the superseded head's diff is still cached")
	}
	// The fake's detail still reports the first head, so r would move the
	// head back; ticks and reopening keep the new head.
	h = drive(t, h, runeKey('c'))
	h = drive(t, h, runeKey('d'))
	for i := 0; i < 3; i++ {
		h = runCmds(t, h, h.forgeOnTick())
	}
	if n := diffCalls(fake, 14927); n != 2 {
		t.Fatalf("%d diff requests, want 2: the new head's diff was refetched", n)
	}
}

// TestForgeFilesCapsCachedDiffs: diffs of many PRs viewed in turn are
// bounded: the least recently viewed are dropped, the recent ones and the
// one on screen stay.
func TestForgeFilesCapsCachedDiffs(t *testing.T) {
	h, _ := filesPRHome(t, 120, 30)
	h = openFiles(t, h)
	first := freshness.DiffKey(widgetsRepo, 14927, "d43c4e3ad3c34332c803a568f85f116e3ee58f19")
	const prs = 30
	keys := make([]freshness.Key, 0, prs)
	now := h.forgeNow()
	for i := 1; i <= prs; i++ {
		n, sha := 3000+i, strconv.Itoa(i)
		h.forge.fresh.Done(freshness.DetailKey(widgetsRepo, n), github.PRDetail{HeadSHA: sha}, nil, now)
		k := freshness.DiffKey(widgetsRepo, n, sha)
		h.forge.fresh.Done(k, github.Diff{}, nil, now)
		keys = append(keys, k)
		h.forge.overlay.row.number = n
		_ = h.forgeFilesFetch()
		if h.forge.overlay.files.sha != sha {
			t.Fatalf("PR %d's diff not on screen", n)
		}
	}
	cached := 0
	for _, k := range append([]freshness.Key{first}, keys...) {
		if _, ok := h.forge.fresh.Get(k); ok {
			cached++
		}
	}
	if cached < 8 || cached > 16 {
		t.Fatalf("%d diffs cached after viewing %d PRs, want 8 to 16", cached, prs+1)
	}
	if _, ok := h.forge.fresh.Get(first); ok {
		t.Fatal("the least recently viewed diff is still cached")
	}
	for _, k := range keys[prs-8:] {
		if _, ok := h.forge.fresh.Get(k); !ok {
			t.Fatalf("recently viewed diff of #%d was dropped", k.Number)
		}
	}
}
