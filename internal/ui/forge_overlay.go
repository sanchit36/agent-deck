package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/markdown"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Forge overlay (spec #18 §9): enter on an Issues/PRs row opens the item
// full screen, on the ContextPager model: a header from the cached row,
// a tab row (Conversation, plus Files for a PR), the tab's body with its
// own scroll, and a two-row footer. It is registered in renderFrame,
// updateInner (keys and the wheel), hasModalVisible and the resize hook.
//
// The Conversation tab is one Freshness key (freshness.ConversationKey):
// the cached copy shows at once, every open fetches it fresh, and a list
// row reporting a newer updatedAt refetches it while the overlay is open.
// New comments keep the scroll position and are announced in the footer.
//
// While the overlay is open, the selected row is the overlay's item
// (forgeSelectedRow), so the list's row actions (o, y, a, r, and those
// later slices add) act on it even when a list refresh moves the cursor.
//
// The Files tab (forge_files.go) opens with openForgeOverlay(forgeOverlayFiles)
// (d on a PR row) and keeps its own state in forgeOverlay.files.

// forgeOverlayTab is one tab of the overlay.
type forgeOverlayTab int

const (
	forgeOverlayConversation forgeOverlayTab = iota
	forgeOverlayFiles
	forgeOverlayTabCount
)

func (t forgeOverlayTab) String() string {
	if t == forgeOverlayFiles {
		return "Files"
	}
	return "Conversation"
}

// forgeOverlayHeaderRows are the rows above the body: the header, the tab
// row and a rule. forgeOverlayFooterRows are the status and hint rows.
const (
	forgeOverlayHeaderRows = 3
	forgeOverlayFooterRows = 2
)

// forgeOverlayPanStep is how many cells h/l pan code and tables.
const forgeOverlayPanStep = 8

// forgeOverlayWheelStep is how many rows a wheel notch scrolls.
const forgeOverlayWheelStep = 3

// forgeOverlayScroll is one tab's scroll: the first body row shown and
// how far code and tables are panned.
type forgeOverlayScroll struct {
	offset, pan int
}

// forgeOverlay is the overlay's state; the zero value is closed.
type forgeOverlay struct {
	visible       bool
	width, height int
	// repo and row are the item, as the list showed it at open; the
	// header follows the list's newer copy of the row while it has one.
	repo github.Repo
	row  forgeRow
	pr   bool
	tab  forgeOverlayTab
	// scroll is each tab's own scroll.
	scroll [forgeOverlayTabCount]forgeOverlayScroll
	// expanded marks the minimized comments shown in full, by index into
	// Conversation.Comments.
	expanded map[int]bool
	// seen is how many comments the reader has had on screen: set when the
	// first copy arrives and whenever the body is scrolled to its end.
	// Comments past it are "new". -1 until the first copy.
	seen int
	// lookup maps a pressed key to its overlay action, and filesLookup to
	// its Files action (seen first while the Files tab shows).
	lookup      map[string]string
	filesLookup map[string]string
	memo        forgeOverlayMemo
	// files is the Files tab's state (forge_files.go).
	files forgeFilesView
}

// forgeOverlayMemo caches the Conversation's rendered lines: every key
// press and frame needs them, and rendering 100 markdown comments per frame
// is avoidable work.
type forgeOverlayMemo struct {
	key   forgeOverlayMemoKey
	valid bool
	lines []string
	// minimized are the rows of the minimized comments' header lines and
	// the comments' indexes.
	minimized []forgeMinimizedRow
	// codeWidth is the widest code or table line, the pan limit.
	codeWidth int
}

type forgeOverlayMemoKey struct {
	fetchedAt time.Time
	version   time.Time
	count     int
	width     int
	pan       int
	expanded  string
	now       time.Time
	failed    bool
}

type forgeMinimizedRow struct{ row, comment int }

// Overlay actions ([hotkeys] names). hotkeyForgeFiles is also the list's
// "open on Files" key (forgeHotkeyActionOrder).
const (
	// hotkeyForgeConversation and hotkeyForgeFiles switch the overlay's
	// tabs, as do their second keys hotkeyForgeConversationAlt and
	// hotkeyForgeFilesAlt.
	hotkeyForgeConversation    = "forge_conversation"
	hotkeyForgeFiles           = "forge_files"
	hotkeyForgeConversationAlt = "forge_conversation_alt"
	hotkeyForgeFilesAlt        = "forge_files_alt"
	// hotkeyForgeExpand expands or collapses every minimized comment.
	hotkeyForgeExpand = "forge_expand"
)

// forgeOverlayActionOrder is the overlay's key space of [hotkeys] actions:
// its own, then the list's row actions that work inside it (later slices
// append theirs, e.g. n and m).
var forgeOverlayActionOrder = []string{
	hotkeyForgeConversation,
	hotkeyForgeFiles,
	hotkeyForgeConversationAlt,
	hotkeyForgeFilesAlt,
	hotkeyForgeExpand,
	hotkeyForgeOpen,
	hotkeyForgeCopy,
	hotkeyForgeAttach,
	hotkeyForgeRefresh,
	hotkeyForgeNew,
	hotkeyForgeMerge,
}

// buildForgeOverlayLookup maps each pressed key to its overlay action,
// explicit spellings before layout twins as buildForgeHotkeyLookup does.
func buildForgeOverlayLookup(bindings map[string]string) map[string]string {
	return buildForgeActionLookup(forgeOverlayActionOrder, bindings)
}

// forgeOverlayFixedKeys are the overlay's keys that are not [hotkeys]
// actions, by what they do.
var forgeOverlayFixedKeys = map[string]string{
	"j": "down", "down": "down", "k": "up", "up": "up",
	"ctrl+d": "half down", "ctrl+u": "half up",
	" ": "page down", "pgdown": "page down", "pgup": "page up",
	"g": "top", "home": "top", "G": "bottom", "end": "bottom",
	"h": "pan left", "left": "pan left", "l": "pan right", "right": "pan right",
	"enter": "expand",
	"esc":   "close", "q": "close",
}

func (h *Home) forgeOverlayVisible() bool { return h.forge.overlay.visible }

// openForgeOverlay opens the selected row in the overlay on tab (an Issue
// has Conversation only) and fetches its Conversation fresh, and on Files
// what the diff needs.
func (h *Home) openForgeOverlay(tab forgeOverlayTab) tea.Cmd {
	lk, ok := h.forgeListKey()
	if !ok {
		return nil
	}
	r, ok := h.forgeSelectedRow()
	if !ok {
		return nil
	}
	// The overlay has its own key space: an open query bar closes.
	h.closeForgeBar()
	o := forgeOverlay{
		visible: true, width: h.width, height: h.height,
		repo: lk.Repo, row: r, pr: h.forge.tab == tabPRs,
		seen: -1, lookup: buildForgeOverlayLookup(h.forge.bindings),
		filesLookup: buildForgeFilesLookup(h.forge.bindings),
	}
	if o.pr {
		o.tab = tab
	}
	ck := freshness.ConversationKey(lk.Repo, r.number)
	if e, ok := h.forgeCache().Get(ck); ok && e.HasData() {
		o.seen = forgeConversationCount(e.Value)
	}
	h.forge.overlay = o
	return tea.Batch(h.forgeFetch(ck), h.forgeFilesFetch())
}

// setForgeOverlayTab shows tab (Files on a PR only) and starts the fetches
// it needs.
func (h *Home) setForgeOverlayTab(t forgeOverlayTab) tea.Cmd {
	o := &h.forge.overlay
	if t == forgeOverlayFiles && !o.pr {
		return nil
	}
	o.tab = t
	h.clampForgeOverlay()
	return h.forgeFilesFetch()
}

// closeForgeOverlay closes the overlay back to the list (and detail).
func (h *Home) closeForgeOverlay() {
	h.forge.overlay = forgeOverlay{}
}

// forgeOverlayKey is the Conversation key of the open overlay.
func (h *Home) forgeOverlayKey() (freshness.Key, bool) {
	o := &h.forge.overlay
	if !o.visible {
		return freshness.Key{}, false
	}
	return freshness.ConversationKey(o.repo, o.row.number), true
}

// forgeOverlayRow is the overlay's row: the list's current copy when it
// still has the item, else the copy taken at open.
func (h *Home) forgeOverlayRow(rows []forgeRow) forgeRow {
	for _, r := range rows {
		if r.number == h.forge.overlay.row.number {
			return r
		}
	}
	return h.forge.overlay.row
}

// forgeOverlaySetSize is the resize hook.
func (h *Home) forgeOverlaySetSize(width, height int) {
	o := &h.forge.overlay
	if !o.visible {
		return
	}
	o.width, o.height = width, height
	h.clampForgeOverlay()
}

// forgeOverlayFetched notes a Conversation that arrived for the open
// overlay: its first copy is all seen.
func (h *Home) forgeOverlayFetched(k freshness.Key) {
	h.adoptForgeFilesDiff()
	want, open := h.forgeOverlayKey()
	if !open || k != want {
		return
	}
	if e, has := h.forgeCache().Get(k); has && e.HasData() && h.forge.overlay.seen < 0 {
		h.forge.overlay.seen = forgeConversationCount(e.Value)
	}
	h.clampForgeOverlay()
}

func forgeConversationCount(v any) int {
	if c, ok := v.(github.Conversation); ok {
		return len(c.Comments) + c.More
	}
	return 0
}

// forgeOverlayBodyHeight is how many body rows fit.
func (h *Home) forgeOverlayBodyHeight() int {
	return max(1, h.forge.overlay.height-forgeOverlayHeaderRows-forgeOverlayFooterRows)
}

// forgeOverlayWidth is the body's text width: one column of margin.
func (h *Home) forgeOverlayWidth() int {
	return max(1, h.forge.overlay.width-2)
}

// forgeOverlayLines are the body lines of the tab on screen (the Files
// tab's whole body, as renderForgeFiles draws it).
func (h *Home) forgeOverlayLines() []string {
	if h.forge.overlay.tab == forgeOverlayFiles {
		return h.renderForgeFiles(h.forgeOverlayBodyHeight())
	}
	return h.forgeConversationLines().lines
}

// forgeOverlayMaxOffset is the furthest the tab on screen scrolls.
func (h *Home) forgeOverlayMaxOffset() int {
	if h.forge.overlay.tab == forgeOverlayFiles {
		return h.forgeFilesMaxOffset()
	}
	return max(0, len(h.forgeOverlayLines())-h.forgeOverlayBodyHeight())
}

// forgeOverlayPage is how many rows the tab on screen pages by.
func (h *Home) forgeOverlayPage() int {
	if h.forge.overlay.tab == forgeOverlayFiles {
		return h.forgeFilesLayoutNow().diffRows
	}
	return h.forgeOverlayBodyHeight()
}

func (h *Home) clampForgeOverlay() {
	o := &h.forge.overlay
	if o.tab == forgeOverlayFiles {
		h.clampForgeFiles()
		return
	}
	s := &o.scroll[o.tab]
	s.offset = max(0, min(s.offset, h.forgeOverlayMaxOffset()))
	maxPan := max(0, h.forgeConversationLines().codeWidth-h.forgeOverlayWidth())
	s.pan = max(0, min(s.pan, maxPan))
	h.markForgeOverlaySeen()
}

// markForgeOverlaySeen counts every comment as seen once the Conversation
// is scrolled to its end.
func (h *Home) markForgeOverlaySeen() {
	o := &h.forge.overlay
	if o.tab != forgeOverlayConversation || o.seen < 0 || o.scroll[o.tab].offset < h.forgeOverlayMaxOffset() {
		return
	}
	if k, ok := h.forgeOverlayKey(); ok {
		if e, has := h.forgeCache().Get(k); has {
			o.seen = max(o.seen, forgeConversationCount(e.Value))
		}
	}
}

// forgeOverlayNew is how many comments arrived since the reader saw the
// end of the Conversation.
func (h *Home) forgeOverlayNew() int {
	o := &h.forge.overlay
	k, ok := h.forgeOverlayKey()
	if !ok || o.seen < 0 {
		return 0
	}
	e, has := h.forgeCache().Get(k)
	if !has {
		return 0
	}
	return max(0, forgeConversationCount(e.Value)-o.seen)
}

// forgeOverlayScrollBy scrolls the tab on screen by delta rows.
func (h *Home) forgeOverlayScrollBy(delta int) {
	o := &h.forge.overlay
	o.scroll[o.tab].offset += delta
	h.clampForgeOverlay()
}

// handleForgeOverlayKey runs the overlay's key space (spec #18 §4): tabs,
// scrolling, panning, expanding minimized comments and the row actions;
// esc and q close it and only quit falls through.
func (h *Home) handleForgeOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := &h.forge.overlay
	raw := msg.String()
	files := o.tab == forgeOverlayFiles
	if raw == "ctrl+c" || (raw != "q" && o.lookup[raw] == "" && forgeOverlayFixedKeys[raw] == "" &&
		(!files || o.filesLookup[raw] == "") &&
		h.hotkeyLookup[raw] == defaultHotkeyBindings[hotkeyQuit]) {
		h.closeForgeOverlay()
		return h.handleMainKey(msg)
	}
	if files {
		if handled, cmd := h.handleForgeFilesKey(raw); handled {
			return h, cmd
		}
	}
	page := h.forgeOverlayPage()
	switch o.lookup[raw] {
	case hotkeyForgeConversation, hotkeyForgeConversationAlt:
		return h, h.setForgeOverlayTab(forgeOverlayConversation)
	case hotkeyForgeFiles, hotkeyForgeFilesAlt:
		return h, h.setForgeOverlayTab(forgeOverlayFiles)
	case hotkeyForgeExpand:
		h.toggleForgeMinimized()
		return h, nil
	case hotkeyForgeOpen:
		return h, h.forgeOpenURL()
	case hotkeyForgeCopy:
		return h, h.forgeCopyURL()
	case hotkeyForgeRefresh:
		return h, h.forgeManualRefresh()
	case hotkeyForgeNew:
		// The dialog (or the chooser, in the list pane) replaces the
		// overlay; the row is read while the overlay still names it.
		cmd := h.forgeNew()
		if h.forge.sessionPick != nil || h.newDialog.IsVisible() {
			h.closeForgeOverlay()
		}
		return h, cmd
	case hotkeyForgeMerge:
		// The dialog opens over the overlay, which esc returns to.
		return h, h.openForgeMerge()
	case hotkeyForgeAttach:
		cmd := h.forgeAttach()
		if h.forge.sessionPick != nil {
			// The picker draws in the list pane.
			h.closeForgeOverlay()
		}
		return h, cmd
	}
	switch forgeOverlayFixedKeys[raw] {
	case "down":
		h.forgeOverlayScrollBy(1)
	case "up":
		h.forgeOverlayScrollBy(-1)
	case "half down":
		h.forgeOverlayScrollBy(max(1, page/2))
	case "half up":
		h.forgeOverlayScrollBy(-max(1, page/2))
	case "page down":
		h.forgeOverlayScrollBy(page)
	case "page up":
		h.forgeOverlayScrollBy(-page)
	case "top":
		o.scroll[o.tab].offset = 0
		h.clampForgeOverlay()
	case "bottom":
		o.scroll[o.tab].offset = h.forgeOverlayMaxOffset()
		h.clampForgeOverlay()
	case "pan left":
		o.scroll[o.tab].pan -= forgeOverlayPanStep
		h.clampForgeOverlay()
	case "pan right":
		o.scroll[o.tab].pan += forgeOverlayPanStep
		h.clampForgeOverlay()
	case "expand":
		h.expandForgeMinimizedOnScreen()
	case "close":
		h.closeForgeOverlay()
	}
	return h, nil
}

// handleForgeOverlayClick switches tab on a click on the tab row (row 1).
// An Issue's overlay has Conversation only.
func (h *Home) handleForgeOverlayClick(x, y int) tea.Cmd {
	o := &h.forge.overlay
	if y != 1 {
		return nil
	}
	for _, s := range forgeOverlayTabSegments(o.pr, o.tab) {
		if x >= s.x0 && x < s.x1 {
			return h.setForgeOverlayTab(s.tab)
		}
	}
	return nil
}

// handleForgeOverlayWheel scrolls the overlay a few rows per notch.
func (h *Home) handleForgeOverlayWheel(up bool) {
	if up {
		h.forgeOverlayScrollBy(-forgeOverlayWheelStep)
	} else {
		h.forgeOverlayScrollBy(forgeOverlayWheelStep)
	}
}

// toggleForgeMinimized is e: expand every minimized comment, or collapse
// them all when every one is expanded.
func (h *Home) toggleForgeMinimized() {
	o := &h.forge.overlay
	rows := h.forgeConversationLines().minimized
	if len(rows) == 0 {
		return
	}
	all := true
	for _, m := range rows {
		if !o.expanded[m.comment] {
			all = false
		}
	}
	o.expanded = map[int]bool{}
	if !all {
		for _, m := range rows {
			o.expanded[m.comment] = true
		}
	}
	h.clampForgeOverlay()
}

// expandForgeMinimizedOnScreen is enter: expand (or collapse) the first
// minimized comment whose header is on screen.
func (h *Home) expandForgeMinimizedOnScreen() {
	o := &h.forge.overlay
	if o.tab != forgeOverlayConversation {
		return
	}
	top := o.scroll[o.tab].offset
	for _, m := range h.forgeConversationLines().minimized {
		if m.row >= top && m.row < top+h.forgeOverlayBodyHeight() {
			expanded := make(map[int]bool, len(o.expanded)+1)
			for k, v := range o.expanded {
				expanded[k] = v
			}
			expanded[m.comment] = !expanded[m.comment]
			o.expanded = expanded
			h.clampForgeOverlay()
			return
		}
	}
}

// forgeConversationLines renders the open Conversation (memoised): the
// body on top, then the comments and review summaries oldest first, each
// under its chrome and a dim rule, then the counts of what is not shown.
// Without a copy it is the loading line or the failure.
func (h *Home) forgeConversationLines() *forgeOverlayMemo {
	o := &h.forge.overlay
	width := h.forgeOverlayWidth()
	var e freshness.Entry
	if k, ok := h.forgeOverlayKey(); ok && h.forge.fresh != nil {
		e, _ = h.forge.fresh.Get(k)
	}
	now := h.forgeNow().Truncate(time.Minute)
	var version time.Time
	if v, ok := e.Value.(freshness.Versioned); ok {
		version = v.Version()
	}
	key := forgeOverlayMemoKey{fetchedAt: e.FetchedAt, version: version, count: forgeConversationCount(e.Value), width: width, pan: o.scroll[forgeOverlayConversation].pan,
		expanded: fmt.Sprint(o.expanded), now: now, failed: e.Err != nil && !e.HasData()}
	if o.memo.valid && o.memo.key == key {
		return &o.memo
	}
	m := forgeOverlayMemo{key: key, valid: true}
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	c, ok := e.Value.(github.Conversation)
	switch {
	case ok && e.HasData():
		m.lines, m.minimized, m.codeWidth = renderConversation(c, width, key.pan, o.expanded, h.forgeNow())
	case e.Err != nil:
		k, _ := h.forgeOverlayKey()
		f := forgeFailureFor(e.Err, k, h.forgeNow())
		warn := lipgloss.NewStyle().Foreground(ColorYellow)
		for i, l := range f.lines {
			style := warn
			if i > 0 {
				style = dim
			}
			for _, w := range strings.Split(ansi.Wrap(l, width, ""), "\n") {
				m.lines = append(m.lines, style.Render(w))
			}
		}
	default:
		m.lines = []string{dim.Render("loading…")}
	}
	o.memo = m
	return &o.memo
}

// forgeMarkdownStyle styles rendered markdown.
func forgeMarkdownStyle() markdown.Style {
	bold := lipgloss.NewStyle().Bold(true)
	code := lipgloss.NewStyle().Foreground(ColorCyan)
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	return markdown.Style{
		Bold: func(s string) string { return bold.Render(s) },
		Code: func(s string) string { return code.Render(s) },
		Dim:  func(s string) string { return dim.Render(s) },
	}
}

// renderConversation lays out c at width (code panned by pan) with the
// expanded minimized comments shown in full. It returns the lines, the
// minimized comments' header rows and the widest code line.
func renderConversation(c github.Conversation, width, pan int, expanded map[int]bool, now time.Time) ([]string, []forgeMinimizedRow, int) {
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	author := lipgloss.NewStyle().Foreground(ColorText).Bold(true)
	opts := markdown.Options{Width: width, Pan: pan, Style: forgeMarkdownStyle()}
	var lines []string
	var minimized []forgeMinimizedRow
	codeWidth := 0
	body := func(src string) {
		if strings.TrimSpace(src) == "" {
			return
		}
		r := markdown.Render(src, opts)
		lines = append(lines, r.Lines...)
		codeWidth = max(codeWidth, r.CodeWidth)
	}
	chrome := func(prefix, who string, bot bool, verb string, at time.Time, edited bool) string {
		name := forgeOneLine(who)
		if bot {
			name += " [bot]"
		}
		tail := " · " + forgeAge(now.Sub(at))
		if edited {
			tail += " (edited)"
		}
		if verb != "" {
			name += " " + verb
		}
		return truncateDisplay(prefix+author.Render(name)+dim.Render(tail), width, "…")
	}
	rule := dim.Render(strings.Repeat("─", min(width, markdown.MaxProseWidth)))

	lines = append(lines, chrome("", c.Author, c.Bot, "", c.CreatedAt, c.Edited))
	if strings.TrimSpace(c.Body) == "" {
		lines = append(lines, dim.Render("No description provided."))
	} else {
		body(c.Body)
	}
	for i, cm := range c.Comments {
		lines = append(lines, "", rule)
		prefix, verb := "", ""
		if cm.Review != "" {
			prefix, verb = forgeReviewChrome(cm.Review)
		}
		if cm.Minimized {
			why := "hidden"
			if cm.MinimizedReason != "" {
				why = "hidden as " + forgeOneLine(cm.MinimizedReason)
			}
			marker := "▸ "
			if expanded[i] {
				marker = "▾ "
			}
			minimized = append(minimized, forgeMinimizedRow{row: len(lines), comment: i})
			name := forgeOneLine(cm.Author)
			if cm.Bot {
				name += " [bot]"
			}
			lines = append(lines, dim.Render(truncateDisplay(marker+name+" · "+forgeAge(now.Sub(cm.At))+" · "+why, width, "…")))
			if expanded[i] {
				body(cm.Body)
			}
			continue
		}
		lines = append(lines, chrome(prefix, cm.Author, cm.Bot, verb, cm.At, cm.Edited))
		body(cm.Body)
	}
	if c.More > 0 || c.InlineComments > 0 {
		lines = append(lines, "", rule)
	}
	if c.More > 0 {
		lines = append(lines, dim.Render(truncateDisplay(forgePlural(c.More, "more comment", "more comments")+": open in browser", width, "…")))
	}
	if c.InlineComments > 0 {
		lines = append(lines, dim.Render(truncateDisplay(forgePlural(c.InlineComments, "inline review comment", "inline review comments")+": open in browser", width, "…")))
	}
	return lines, minimized, codeWidth
}

func forgePlural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// forgeReviewChrome is a review summary's glyph and verb.
func forgeReviewChrome(state string) (string, string) {
	switch state {
	case "APPROVED":
		return lipgloss.NewStyle().Foreground(ColorGreen).Render("✔") + " ", "approved"
	case "CHANGES_REQUESTED":
		return lipgloss.NewStyle().Foreground(ColorRed).Render("±") + " ", "requested changes"
	case "DISMISSED":
		return "", "reviewed (dismissed)"
	}
	return "", "reviewed"
}

// forgeOverlayTabSegment is one tab chip of the overlay's tab row (row 1)
// and the columns [x0, x1) it covers.
type forgeOverlayTabSegment struct {
	tab    forgeOverlayTab
	text   string
	x0, x1 int
}

// forgeOverlayTabSegments lays out the tab row: a leading space, then each
// tab (`[ Name ]` when active, `  Name  ` otherwise) and a space. An
// Issue's overlay has Conversation only.
func forgeOverlayTabSegments(pr bool, active forgeOverlayTab) []forgeOverlayTabSegment {
	tabs := []forgeOverlayTab{forgeOverlayConversation}
	if pr {
		tabs = append(tabs, forgeOverlayFiles)
	}
	segs := make([]forgeOverlayTabSegment, 0, len(tabs))
	x := 1
	for _, t := range tabs {
		text := "  " + t.String() + "  "
		if t == active {
			text = "[ " + t.String() + " ]"
		}
		w := lipgloss.Width(text)
		segs = append(segs, forgeOverlayTabSegment{tab: t, text: text, x0: x, x1: x + w})
		x += w + 1
	}
	return segs
}

// renderForgeOverlay draws the overlay full screen. No I/O: it reads the
// cache and the overlay's state.
func (h *Home) renderForgeOverlay() string {
	o := &h.forge.overlay
	width := max(1, o.width)
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	var out []string

	// Header: glyph, #n, title, and the state on the right.
	rows, _, _ := h.forgeVisibleRows()
	r := h.forgeOverlayRow(rows)
	state := r.state
	left := fmt.Sprintf("#%d  ", r.number)
	room := max(0, width-2-lipgloss.Width(r.glyph)-1-lipgloss.Width(left)-lipgloss.Width(state)-2)
	title := truncateDisplay(r.title, room, "…")
	pad := max(1, width-2-lipgloss.Width(r.glyph)-1-lipgloss.Width(left)-lipgloss.Width(title)-lipgloss.Width(state)-1)
	header := " " + lipgloss.NewStyle().Foreground(r.glyphColor).Render(r.glyph) + " " +
		lipgloss.NewStyle().Foreground(ColorText).Bold(true).Render(left+title) + strings.Repeat(" ", pad) + dim.Render(state)
	out = append(out, truncateDisplay(header, width, "…"))

	// Tab row.
	active := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	var tabRow strings.Builder
	tabRow.WriteString(" ")
	for _, s := range forgeOverlayTabSegments(o.pr, o.tab) {
		if s.tab == o.tab {
			tabRow.WriteString(active.Render(s.text))
		} else {
			tabRow.WriteString(dim.Render(s.text))
		}
		tabRow.WriteString(" ")
	}
	out = append(out, truncateDisplay(tabRow.String(), width, "…"))
	out = append(out, lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("─", width)))

	// Body.
	body := h.forgeOverlayBodyHeight()
	var total, offset, page int
	if o.tab == forgeOverlayFiles {
		out = append(out, h.renderForgeFiles(body)...)
		total, offset, page = h.forgeFilesPosition()
	} else {
		lines := h.forgeOverlayLines()
		offset = max(0, min(o.scroll[o.tab].offset, len(lines)-1))
		for i := offset; i < len(lines) && i < offset+body; i++ {
			out = append(out, truncateDisplay(" "+lines[i], width, "…"))
		}
		total, page = len(lines), body
	}
	for len(out) < forgeOverlayHeaderRows+body {
		out = append(out, "")
	}

	// Footer: status, then the key hints.
	out = append(out, truncateDisplay(" "+h.forgeOverlayStatus(total, offset, page), width, "…"))
	out = append(out, dim.Render(truncateDisplay(" "+h.forgeOverlayHints(width-1), width, "…")))
	return strings.Join(out, "\n")
}

// forgeOverlayStatus is the first footer row: new comments, a stale or
// fetching note, and the position.
func (h *Home) forgeOverlayStatus(total, offset, body int) string {
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	var parts []string
	if n := h.forgeOverlayNew(); n > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(ColorAccent).Bold(true).Render(forgePlural(n, "new comment", "new comments")))
	}
	k, ok := h.forgeOverlayKey()
	if h.forge.overlay.tab == forgeOverlayFiles {
		k, ok = h.forgeFilesStatusKey()
		if e := h.forge.overlay.files.pagerErr; e != "" {
			parts = append(parts, lipgloss.NewStyle().Foreground(ColorYellow).Render("⚠ "+forgeOneLine(e)))
		}
	}
	if ok && h.forge.fresh != nil {
		if e, has := h.forge.fresh.Get(k); has {
			switch {
			case e.Fetching && (e.HasData() || h.forge.overlay.files.sha != ""):
				parts = append(parts, lipgloss.NewStyle().Foreground(ColorCyan).Render(forgeSpinnerFrames[h.animationFrame%len(forgeSpinnerFrames)]))
			case e.Stale() || (e.Err != nil && h.forge.overlay.tab == forgeOverlayFiles && h.forge.overlay.files.sha != ""):
				parts = append(parts, lipgloss.NewStyle().Foreground(ColorYellow).Render("⚠ stale: "+forgeFailureFor(e.Err, k, h.forgeNow()).lines[0]))
			}
		}
	}
	if total > body {
		end := min(total, offset+body)
		parts = append(parts, dim.Render(fmt.Sprintf("%d–%d of %d", offset+1, end, total)))
	}
	return strings.Join(parts, dim.Render(" · "))
}

// forgeOverlayHintsSpare are the hints every overlay tab shares and the
// help lists, in the order they are left out of a footer wider than its
// row (spec #18 slice 21: the Files tab's own keys come first).
var forgeOverlayHintsSpare = []string{"refresh", "copy url", "open", "scroll"}

// forgeOverlayHints is the overlay's hint footer, at most width cells when
// leaving out forgeOverlayHintsSpare gets it there (the caller cuts the
// rest).
func (h *Home) forgeOverlayHints(width int) string {
	hints := h.forgeOverlayHintList()
	for _, spare := range forgeOverlayHintsSpare {
		if lipgloss.Width(strings.Join(hints, " · ")) <= width {
			break
		}
		hints = slices.DeleteFunc(hints, func(s string) bool { return strings.HasSuffix(s, " "+spare) })
	}
	return strings.Join(hints, " · ")
}

func (h *Home) forgeOverlayHintList() []string {
	o := &h.forge.overlay
	var hints []string
	add := func(key, label string) {
		if strings.TrimSpace(key) != "" {
			hints = append(hints, key+" "+label)
		}
	}
	key := func(action string) string { return h.forgeKey(action) }
	if o.pr {
		add(joinHotkeyLabels(key(hotkeyForgeConversation), key(hotkeyForgeFiles)), "tabs")
	}
	add("j/k", "scroll")
	if o.tab == forgeOverlayFiles {
		h.forgeFilesHints(add)
	}
	if o.tab == forgeOverlayConversation && len(h.forgeConversationLines().minimized) > 0 {
		add(key(hotkeyForgeExpand), "expand hidden")
	}
	if o.pr {
		add(key(hotkeyForgeMerge), "merge")
	}
	add(key(hotkeyForgeOpen), "open")
	add(key(hotkeyForgeCopy), "copy url")
	add(key(hotkeyForgeRefresh), "refresh")
	add("esc", "close")
	return hints
}

// issueStateWord is an Issue's state in words.
func issueStateWord(closed bool) string {
	if closed {
		return "closed"
	}
	return "open"
}
