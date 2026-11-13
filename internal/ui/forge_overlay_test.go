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

// serveConversation answers the Conversation query of me/widgets#number
// with f.
func serveConversation(fake *githubtest.Fake, number int, f githubtest.Fixture) {
	fake.Serve(f, "api", "--hostname", "github.com", "graphql",
		"-f", "query="+github.ConversationQuery, "-f", "owner=me", "-f", "name=widgets",
		"-F", fmt.Sprintf("number=%d", number))
}

// conversationCalls counts the Conversation requests made for number.
func conversationCalls(fake *githubtest.Fake, number int) int {
	n := 0
	want := fmt.Sprintf("number=%d", number)
	for _, c := range fake.Calls() {
		if slices.Contains(c.Args, "query="+github.ConversationQuery) && slices.Contains(c.Args, want) {
			n++
		}
	}
	return n
}

// overlayIssueHome is detailIssuesHome with the Conversation of #14822
// served: the recorded cli/cli Issue with a bot comment and a minimized
// one.
func overlayIssueHome(t *testing.T, w, h int, instances ...*session.Instance) (*Home, *githubtest.Fake) {
	t.Helper()
	home, fake := detailIssuesHome(t, w, h, instances...)
	serveConversation(fake, 14822, githubtest.MustLoad(t, githubtest.IssueRowConversation))
	return home, fake
}

// overlayPRHome is detailPRsHome with the Conversation of #14927 served.
func overlayPRHome(t *testing.T, w, h int, instances ...*session.Instance) (*Home, *githubtest.Fake) {
	t.Helper()
	home, fake := detailPRsHome(t, w, h, instances...)
	serveConversation(fake, 14927, githubtest.MustLoad(t, githubtest.PRRowConversation))
	return home, fake
}

// openOverlay presses enter (twice in the single column: the first opens
// the full-screen detail) and runs the fetches.
func openOverlay(t *testing.T, h *Home) *Home {
	t.Helper()
	if h.getLayoutMode() == LayoutModeSingle {
		h = drive(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	}
	return drive(t, h, tea.KeyMsg{Type: tea.KeyEnter})
}

func overlayEnter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func overlayEsc() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyEsc} }

// overlayBodyTop is the overlay's first body row (under the header, the
// tab row and the rule), trimmed.
func overlayBodyTop(h *Home) string {
	return strings.TrimSpace(frameLines(h.View())[forgeOverlayHeaderRows])
}

// TestForgeOverlayOpensWithCachedHeader: enter opens the overlay at once
// with the header from the cached row and a loading line; the fetch then
// fills in the body and the comments, oldest first, with their chrome.
func TestForgeOverlayOpensWithCachedHeader(t *testing.T) {
	h, fake := overlayIssueHome(t, 120, 30)
	model, cmd := h.Update(overlayEnter())
	h = model.(*Home)
	if !h.forgeOverlayVisible() || !h.hasModalVisible() {
		t.Fatal("enter did not open the overlay")
	}
	frame := stripAnsi(h.View())
	for _, want := range []string{"○ #14822", "gh run view --job --log-failed fails", "open", "Conversation", "loading…"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("opening frame lacks %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "Files") {
		t.Fatalf("an Issue has no Files tab:\n%s", frame)
	}
	h = runCmds(t, h, cmd)
	if n := conversationCalls(fake, 14822); n != 1 {
		t.Fatalf("%d Conversation requests, want 1", n)
	}
	frame = stripAnsi(h.View())
	if strings.Contains(frame, "loading…") {
		t.Fatalf("still loading after the fetch:\n%s", frame)
	}
	// The body by its author, then the bot's comment.
	if !strings.HasPrefix(overlayBodyTop(h), "henrymodisett · 2w") {
		t.Fatalf("body chrome = %q", overlayBodyTop(h))
	}
	all := strings.Join(h.forgeOverlayLines(), "\n")
	all = stripAnsi(all)
	for _, want := range []string{
		"cli-triage [bot] · 2w",
		"▸ duongynhi000005-oss · 2w · hidden as spam",
		"henrymodisett · 15m",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("Conversation lacks %q:\n%s", want, all)
		}
	}
	if strings.Index(all, "cli-triage [bot]") > strings.Index(all, "henrymodisett · 15m") {
		t.Fatal("comments not oldest first")
	}
}

// TestForgeOverlayKeepsScrollOnNewComments: a refetch that brings new
// comments keeps the scroll position and says how many arrived; reaching
// the bottom clears the note.
func TestForgeOverlayKeepsScrollOnNewComments(t *testing.T) {
	h, _ := overlayIssueHome(t, 120, 30)
	h = openOverlay(t, h)
	for range 6 {
		h = drive(t, h, runeKey('j'))
	}
	top := overlayBodyTop(h)
	k := freshness.ConversationKey(widgetsRepo, 14822)
	e, _ := h.forge.fresh.Get(k)
	c := e.Value.(github.Conversation)
	c.Comments = append(slices.Clone(c.Comments),
		github.Comment{Author: "octocat", At: issuesT0.Add(-time.Minute), Body: "first new"},
		github.Comment{Author: "hubot", Bot: true, At: issuesT0, Body: "second new"})
	c.UpdatedAt = issuesT0
	h = drive(t, h, forgeFetchedMsg{key: k, value: c})
	if got := overlayBodyTop(h); got != top {
		t.Fatalf("scroll moved on new comments: top %q, was %q", got, top)
	}
	if frame := stripAnsi(h.View()); !strings.Contains(frame, "2 new comments") {
		t.Fatalf("no new-comments footer:\n%s", frame)
	}
	h = drive(t, h, runeKey('G'))
	if frame := stripAnsi(h.View()); strings.Contains(frame, "new comments") || !strings.Contains(frame, "second new") {
		t.Fatalf("G did not reach the new comments and clear the note:\n%s", frame)
	}
}

// TestForgeOverlayRefetchedWhenRowUpdated: a list row reporting a newer
// updatedAt makes the next tick refetch the open Conversation.
func TestForgeOverlayRefetchedWhenRowUpdated(t *testing.T) {
	h, fake := overlayIssueHome(t, 120, 30)
	h = openOverlay(t, h)
	k, _ := h.forgeListKey()
	e, _ := h.forge.fresh.Get(k)
	l := e.Value.(github.IssueList)
	l.Issues = slices.Clone(l.Issues)
	l.Issues[0].UpdatedAt = l.Issues[0].UpdatedAt.Add(time.Hour)
	h = drive(t, h, forgeFetchedMsg{key: k, value: l})
	_ = runCmds(t, h, h.forgeOnTick())
	if n := conversationCalls(fake, 14822); n != 2 {
		t.Fatalf("%d Conversation requests, want 2 (the open and the poll)", n)
	}
}

// TestForgeOverlayScrollKeys: j/k, ctrl+d/u, space/pgup, g/G and the wheel
// scroll the body; h/l pan code.
func TestForgeOverlayScrollKeys(t *testing.T) {
	h, _ := overlayIssueHome(t, 120, 30)
	h = openOverlay(t, h)
	top := overlayBodyTop(h)
	offset := func() int { return h.forge.overlay.scroll[forgeOverlayConversation].offset }
	steps := []struct {
		key  tea.Msg
		want func(before int) bool
	}{
		{runeKey('j'), func(b int) bool { return offset() == b+1 }},
		{runeKey('k'), func(b int) bool { return offset() == b-1 }},
		{tea.KeyMsg{Type: tea.KeyCtrlD}, func(b int) bool { return offset() > b+1 }},
		{tea.KeyMsg{Type: tea.KeyCtrlU}, func(b int) bool { return offset() < b }},
		{tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, func(b int) bool { return offset() > b+10 }},
		{tea.KeyMsg{Type: tea.KeyPgUp}, func(b int) bool { return offset() < b }},
		{runeKey('G'), func(int) bool { return offset() > 0 && offset() == h.forgeOverlayMaxOffset() }},
		{runeKey('g'), func(int) bool { return offset() == 0 }},
		{tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress}, func(b int) bool { return offset() == b+3 }},
		{tea.KeyMsg{Type: tea.KeyHome}, func(int) bool { return offset() == 0 }},
	}
	for i, s := range steps {
		before := offset()
		h = drive(t, h, s.key)
		if !s.want(before) {
			t.Fatalf("step %d (%v): offset %d -> %d", i, s.key, before, offset())
		}
	}
	if overlayBodyTop(h) != top {
		t.Fatalf("back at the top the first row is %q, want %q", overlayBodyTop(h), top)
	}
	// A comment with a code line wider than the view pans.
	k := freshness.ConversationKey(widgetsRepo, 14822)
	e, _ := h.forge.fresh.Get(k)
	c := e.Value.(github.Conversation)
	c.Comments = append(slices.Clone(c.Comments), github.Comment{Author: "octocat", At: issuesT0, Body: "```\n" + strings.Repeat("x", 300) + "\n```"})
	c.UpdatedAt = issuesT0
	h = drive(t, h, forgeFetchedMsg{key: k, value: c})
	h = drive(t, h, runeKey('l'))
	if h.forge.overlay.scroll[forgeOverlayConversation].pan == 0 {
		t.Fatal("l did not pan")
	}
	h = drive(t, h, runeKey('h'))
	if h.forge.overlay.scroll[forgeOverlayConversation].pan != 0 {
		t.Fatal("h did not pan back")
	}
}

// TestForgeOverlayMinimized: a minimized comment is one line; enter
// expands the one on screen, e expands or collapses them all.
func TestForgeOverlayMinimized(t *testing.T) {
	h, _ := overlayIssueHome(t, 120, 30)
	h = openOverlay(t, h)
	text := func() string { return stripAnsi(strings.Join(h.forgeOverlayLines(), "\n")) }
	hidden := "hidden as spam"
	if !strings.Contains(text(), "▸ duongynhi000005-oss") {
		t.Fatalf("minimized comment not collapsed:\n%s", text())
	}
	collapsed := len(h.forgeOverlayLines())
	h = drive(t, h, runeKey('e'))
	if !strings.Contains(text(), "▾ duongynhi000005-oss · 2w · "+hidden) || len(h.forgeOverlayLines()) <= collapsed {
		t.Fatalf("e did not expand the minimized comment:\n%s", text())
	}
	h = drive(t, h, runeKey('e'))
	if len(h.forgeOverlayLines()) != collapsed {
		t.Fatal("a second e did not collapse it again")
	}
	// Scroll the collapsed line to the top and expand it with enter.
	for i, l := range h.forgeOverlayLines() {
		if strings.Contains(stripAnsi(l), "▸ duongynhi000005-oss") {
			h.forge.overlay.scroll[forgeOverlayConversation].offset = i
		}
	}
	h = drive(t, h, overlayEnter())
	if !strings.Contains(text(), "▾ duongynhi000005-oss") {
		t.Fatalf("enter did not expand the minimized comment on screen:\n%s", text())
	}
	if !h.forgeOverlayVisible() {
		t.Fatal("enter closed the overlay")
	}
}

// TestForgeOverlayTabs: a PR has Conversation | Files; c/d and 1/2 switch
// and each tab keeps its own scroll. An Issue has Conversation only.
func TestForgeOverlayTabs(t *testing.T) {
	h, _ := overlayPRHome(t, 120, 30)
	h = openOverlay(t, h)
	frame := stripAnsi(h.View())
	for _, want := range []string{"◌ #14927", "Add guided multi-account setup and evaluation", "draft", "Conversation", "Files",
		"williammartin · 1w (edited)", "copilot-pull-request-reviewer [bot] reviewed"} {
		if !strings.Contains(frame+stripAnsi(strings.Join(h.forgeOverlayLines(), "\n")), want) {
			t.Fatalf("PR overlay lacks %q:\n%s", want, frame)
		}
	}
	if all := stripAnsi(strings.Join(h.forgeOverlayLines(), "\n")); !strings.Contains(all, "4 inline review comments: open in browser") {
		t.Fatalf("no inline review comment count:\n%s", all)
	}
	h = drive(t, h, runeKey('j'))
	for _, s := range []struct {
		key  tea.KeyMsg
		want forgeOverlayTab
	}{{runeKey('d'), forgeOverlayFiles}, {runeKey('c'), forgeOverlayConversation}, {runeKey('2'), forgeOverlayFiles}, {runeKey('1'), forgeOverlayConversation}} {
		h = drive(t, h, s.key)
		if h.forge.overlay.tab != s.want {
			t.Fatalf("%q: tab %v, want %v", s.key.String(), h.forge.overlay.tab, s.want)
		}
	}
	if h.forge.overlay.scroll[forgeOverlayConversation].offset != 1 {
		t.Fatal("switching tabs lost the Conversation scroll")
	}

	is, _ := overlayIssueHome(t, 120, 30)
	is = openOverlay(t, is)
	is = drive(t, is, runeKey('d'))
	if is.forge.overlay.tab != forgeOverlayConversation {
		t.Fatal("d opened a Files tab on an Issue")
	}
}

// TestForgeOverlayClose: esc and q close the overlay back to the list;
// other overview keys are swallowed while it is open.
func TestForgeOverlayClose(t *testing.T) {
	for _, k := range []tea.KeyMsg{overlayEsc(), runeKey('q')} {
		h, _ := overlayIssueHome(t, 120, 30)
		h = openOverlay(t, h)
		h = drive(t, h, runeKey('n'))
		h = drive(t, h, runeKey('?'))
		if h.newDialog.IsVisible() || h.helpOverlay.IsVisible() || !h.forgeOverlayVisible() {
			t.Fatal("an overview key reached past the overlay")
		}
		h = drive(t, h, k)
		if h.forgeOverlayVisible() || !h.forgeTabActive() {
			t.Fatalf("%q did not close the overlay back to the list", k.String())
		}
		if frame := stripAnsi(h.View()); !strings.Contains(frame, "ISSUE #14822") {
			t.Fatalf("not back on the list and detail:\n%s", frame)
		}
	}
}

// TestForgeOverlaySingleColumn: in the single column the first enter
// opens the full-screen detail and the second the overlay; closing it
// returns to the detail.
func TestForgeOverlaySingleColumn(t *testing.T) {
	h, _ := overlayIssueHome(t, 45, 20)
	h = drive(t, h, overlayEnter())
	if h.forgeOverlayVisible() || !h.forgeDetailFull() {
		t.Fatal("first enter did not open the full-screen detail")
	}
	h = drive(t, h, overlayEnter())
	if !h.forgeOverlayVisible() {
		t.Fatal("second enter did not open the overlay")
	}
	h = drive(t, h, overlayEsc())
	if h.forgeOverlayVisible() || !h.forgeDetailFull() {
		t.Fatal("esc did not return to the full-screen detail")
	}
}

// TestForgeOverlayRowActions: o, y, a and r act on the overlay's item,
// even when a list refresh moves another row under the cursor.
func TestForgeOverlayRowActions(t *testing.T) {
	h, fake := overlayIssueHome(t, 120, 30, linkedTo("s1", "triage run", session.LinkIssue, 14822))
	var opened, copied, attached []string
	h.forge.openURL = func(u string) error { opened = append(opened, u); return nil }
	h.forge.copyURL = func(u string) error { copied = append(copied, u); return nil }
	h.forge.attach = func(id string) tea.Cmd { attached = append(attached, id); return nil }
	h = openOverlay(t, h)

	// The list refreshes with #14822 no longer first.
	k, _ := h.forgeListKey()
	e, _ := h.forge.fresh.Get(k)
	l := e.Value.(github.IssueList)
	l.Issues = append(slices.Clone(l.Issues[1:]), l.Issues[0])
	h = drive(t, h, forgeFetchedMsg{key: k, value: l})

	h = drive(t, h, runeKey('o'))
	h = drive(t, h, runeKey('y'))
	h = drive(t, h, runeKey('a'))
	want := "https://github.com/cli/cli/issues/14822"
	if !slices.Equal(opened, []string{want}) || !slices.Equal(copied, []string{want}) || !slices.Equal(attached, []string{"s1"}) {
		t.Fatalf("opened %v, copied %v, attached %v", opened, copied, attached)
	}
	h = drive(t, h, runeKey('r'))
	if n := conversationCalls(fake, 14822); n != 2 {
		t.Fatalf("%d Conversation requests after r, want 2", n)
	}
	if !h.forgeOverlayVisible() {
		t.Fatal("a row action closed the overlay")
	}
}

// TestForgeOverlayFailureWithoutCache: a failed fetch with nothing cached
// says what failed under the header.
func TestForgeOverlayFailureWithoutCache(t *testing.T) {
	h, _ := detailIssuesHome(t, 120, 30) // no Conversation served: gh exits 1
	h = openOverlay(t, h)
	frame := stripAnsi(h.View())
	if !strings.Contains(frame, "#14822") || !strings.Contains(frame, "⚠ gh failed") {
		t.Fatalf("failure not shown:\n%s", frame)
	}
}

// TestForgeOverlayKeySpace is the duplicate-key test for the overlay's key
// space (spec #18 §4): its own keys, the row actions that work inside it,
// scrolling and quit each mean one thing, Cyrillic twins included.
func TestForgeOverlayKeySpace(t *testing.T) {
	seen := map[string]string{}
	claim := func(key, owner string) {
		for _, alias := range hotkeyAliases(key) {
			if prev, ok := seen[alias]; ok && prev != owner {
				t.Errorf("in the overlay %q means both %q and %q", alias, prev, owner)
				continue
			}
			seen[alias] = owner
		}
	}
	for _, action := range forgeOverlayActionOrder {
		key := defaultForgeHotkeyBindings[action]
		if key == "" {
			t.Errorf("overlay action %q has no default key", action)
		}
		if !strings.HasPrefix(action, "forge_") {
			t.Errorf("overlay action %q is not prefixed forge_", action)
		}
		claim(key, action)
	}
	for key, owner := range forgeOverlayFixedKeys {
		claim(key, "fixed:"+owner)
	}
	claim("ctrl+c", "quit")
}

// TestForgeOverlayGolden freezes the overlay over an Issue and a PR at
// dual, stacked and single widths.
func TestForgeOverlayGolden(t *testing.T) {
	for _, sz := range forgeFrameSizes {
		for _, kind := range []string{"issue", "pr"} {
			name := fmt.Sprintf("forge_overlay_%dx%d_%s", sz[0], sz[1], kind)
			t.Run(name, func(t *testing.T) {
				var h *Home
				if kind == "issue" {
					h, _ = overlayIssueHome(t, sz[0], sz[1])
				} else {
					h, _ = overlayPRHome(t, sz[0], sz[1])
				}
				h = openOverlay(t, h)
				frame := h.View()
				if got := len(frameLines(frame)); got != sz[1] {
					t.Fatalf("frame has %d rows, want %d", got, sz[1])
				}
				assertFrameGolden(t, name+".golden", stripAnsi(frame))
			})
		}
	}
}

// TestForgeOverlayEndGolden freezes later parts of each Conversation at
// 120x30: the comment chrome (bot, minimized, review verdict) and the
// inline review comment count.
func TestForgeOverlayEndGolden(t *testing.T) {
	for _, kind := range []string{"issue", "pr"} {
		name := "forge_overlay_120x30_" + kind + "_end"
		t.Run(name, func(t *testing.T) {
			var h *Home
			if kind == "issue" {
				h, _ = overlayIssueHome(t, 120, 30)
			} else {
				h, _ = overlayPRHome(t, 120, 30)
			}
			h = openOverlay(t, h)
			if kind == "issue" {
				// The end of the bot's comment, the minimized one and the
				// next.
				for i, l := range h.forgeOverlayLines() {
					if strings.Contains(stripAnsi(l), "▸ duongynhi000005-oss") {
						h.forge.overlay.scroll[forgeOverlayConversation].offset = i - 12
						break
					}
				}
			} else {
				h = drive(t, h, runeKey('G'))
			}
			assertFrameGolden(t, name+".golden", stripAnsi(h.View()))
		})
	}
}
