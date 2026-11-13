package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Detail pane (spec #18 §3, §7): on a forge tab the right pane (dual and
// stacked layouts) shows the selected row's Issue or PR, titled ISSUE #n /
// PR #n: the title, a key/value block and a short timeline, newest first.
// The single column shows the list only; enter opens the detail full
// screen and esc returns. The detail is one Freshness key (forge.detail):
// its cached copy shows at once, every open fetches it fresh (after a
// short debounce, so moving over rows fetches only the one the cursor
// stops on), and a list row reporting a newer updatedAt refetches it while
// it is on screen.

// forgeDetailDelay is how long the cursor must rest on a row before its
// detail is fetched.
const forgeDetailDelay = 150 * time.Millisecond

// fetchDetail is the detail fetcher: one GraphQL request for the Issue or
// PR (they share GitHub's numbering).
func fetchDetail(ctx context.Context, c *github.Client, k freshness.Key) (any, error) {
	return c.Detail(ctx, k.Repo, k.Number)
}

// forgeDetailOpenMsg ends an open's debounce: fetch key if it is still the
// open detail.
type forgeDetailOpenMsg struct {
	key freshness.Key
	at  time.Time
}

// forgeSessionPick is the picker a opens when several sessions are linked
// to the selected row: the sessions' ids, titles and the cursor.
type forgeSessionPick struct {
	ids    []string
	titles []string
	at     int
	number int
}

// forgeDetailFull reports whether the single column shows the full-screen
// detail.
func (h *Home) forgeDetailFull() bool {
	return h.forge.detailFull && h.forgeTabActive() && h.getLayoutMode() == LayoutModeSingle
}

// forgeOpenItem is enter on a forge list: the selected row's overlay on
// Conversation, except that in the single column the first enter opens
// the full-screen detail.
func (h *Home) forgeOpenItem() tea.Cmd {
	if _, ok := h.forgeSelectedRow(); !ok {
		return nil
	}
	if h.getLayoutMode() == LayoutModeSingle && !h.forge.detailFull {
		h.forge.detailFull = true
		return h.syncForgeDetail()
	}
	return h.openForgeOverlay(forgeOverlayConversation)
}

// fetchConversation is the Conversation fetcher of the overlay.
func fetchConversation(ctx context.Context, c *github.Client, k freshness.Key) (any, error) {
	return c.Conversation(ctx, k.Repo, k.Number)
}

// forgeDetailShown reports whether a detail pane is on screen: the right
// pane of the dual and stacked layouts, or the single column's full-screen
// detail.
func (h *Home) forgeDetailShown() bool {
	if !h.forgeTabActive() || h.embeddedMode {
		return false
	}
	if h.getLayoutMode() == LayoutModeSingle {
		return h.forgeDetailFull()
	}
	return true
}

// forgeSelectedKey is the detail key of the selected row.
func (h *Home) forgeSelectedKey() (freshness.Key, forgeRow, bool) {
	lk, ok := h.forgeListKey()
	if !ok {
		return freshness.Key{}, forgeRow{}, false
	}
	r, ok := h.forgeSelectedRow()
	if !ok {
		return freshness.Key{}, forgeRow{}, false
	}
	return freshness.DetailKey(lk.Repo, r.number), r, true
}

// syncForgeDetail points forge.detail at the detail on screen and opens it
// when it changed: a fresh fetch after the debounce, whatever is cached.
func (h *Home) syncForgeDetail() tea.Cmd {
	var want freshness.Key
	if h.forgeDetailShown() {
		want, _, _ = h.forgeSelectedKey()
	}
	if want == h.forge.detail {
		return nil
	}
	h.forge.detail = want
	if want.Kind == "" {
		return nil
	}
	h.forgeExpirePRLocal()
	return h.forgeOpenDetail(want)
}

// forgeOpenDetail fetches k after the debounce (at once with a negative
// detailDelay).
func (h *Home) forgeOpenDetail(k freshness.Key) tea.Cmd {
	if h.forge.fetchers == nil {
		h.forge.fetchers = defaultForgeFetchers()
	}
	if h.forge.fetchers[k.Kind] == nil {
		return nil
	}
	at := h.forgeNow()
	delay := h.forge.detailDelay
	if delay == 0 {
		delay = forgeDetailDelay
	}
	if delay < 0 {
		return h.forgeFetch(k)
	}
	return h.forgeJobs().run(func(ctx context.Context) tea.Msg {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		return forgeDetailOpenMsg{key: k, at: at}
	})
}

// applyForgeDetailOpen fetches the detail whose debounce ended, unless the
// cursor moved on or a fetch since the open already covers it.
func (h *Home) applyForgeDetailOpen(msg forgeDetailOpenMsg) tea.Cmd {
	if msg.key != h.forge.detail {
		return nil
	}
	if e, ok := h.forgeCache().Get(msg.key); ok && (e.Fetching || e.FetchedAt.After(msg.at)) {
		return nil
	}
	return h.forgeFetch(msg.key)
}

// forgeLinkedSessions are the sessions linked to item number of repo's
// list of kind, in session order. It reads the stored links only.
func (h *Home) forgeLinkedSessions(repo github.Repo, kind session.LinkKind, number int) []*session.Instance {
	h.instancesMu.RLock()
	defer h.instancesMu.RUnlock()
	var out []*session.Instance
	for _, inst := range h.instances {
		if inst == nil {
			continue
		}
		if l := inst.LinkOf(kind); l != nil && l.Number == number && linkInRepo(*l, repo) {
			out = append(out, inst)
		}
	}
	return out
}

// forgeLinkKind is the Link kind of the forge tab on screen.
func (h *Home) forgeLinkKind() session.LinkKind {
	if h.forge.tab == tabPRs {
		return session.LinkPR
	}
	return session.LinkIssue
}

// forgeSelectedSessions are the sessions linked to the selected row.
func (h *Home) forgeSelectedSessions() ([]*session.Instance, forgeRow, bool) {
	k, r, ok := h.forgeSelectedKey()
	if !ok {
		return nil, r, false
	}
	return h.forgeLinkedSessions(k.Repo, h.forgeLinkKind(), r.number), r, true
}

// forgeAttach is a: attach to the session linked to the selected row, or
// open the picker when several are.
func (h *Home) forgeAttach() tea.Cmd {
	insts, r, ok := h.forgeSelectedSessions()
	if !ok {
		return nil
	}
	switch len(insts) {
	case 0:
		h.setError(fmt.Errorf("no session linked to #%d", r.number))
		return nil
	case 1:
		return h.forgeAttachSession(insts[0].ID)
	}
	p := &forgeSessionPick{number: r.number}
	for _, inst := range insts {
		p.ids = append(p.ids, inst.ID)
		p.titles = append(p.titles, inst.Title)
	}
	h.forge.sessionPick = p
	return nil
}

// forgeAttachSession attaches to session id as enter does on the Sessions
// tab: the session row is selected first.
func (h *Home) forgeAttachSession(id string) tea.Cmd {
	if h.forge.attach != nil {
		return h.forge.attach(id)
	}
	if !h.SelectSessionByID(id) {
		return nil
	}
	if h.embeddedLayout {
		if h.enterEmbeddedMode() {
			return h.startEmbeddedModeCmd()
		}
		return nil
	}
	return h.attachSelectedLegacy()
}

// handleForgeSessionPickKey runs the attach picker: j/k move, enter
// attaches, esc closes, quit still quits.
func (h *Home) handleForgeSessionPickKey(msg tea.KeyMsg, key string) (bool, tea.Model, tea.Cmd) {
	p := h.forge.sessionPick
	switch msg.String() {
	case "j", "down", "ctrl+n":
		if p.at < len(p.ids)-1 {
			p.at++
		}
	case "k", "up", "ctrl+p":
		if p.at > 0 {
			p.at--
		}
	case "enter":
		h.forge.sessionPick = nil
		return true, h, h.forgeAttachSession(p.ids[p.at])
	case "esc":
		h.forge.sessionPick = nil
	default:
		if key == defaultHotkeyBindings[hotkeyQuit] || key == "ctrl+c" {
			h.forge.sessionPick = nil
			return h.forgeFallThrough(msg, key)
		}
	}
	return true, h, nil
}

func (h *Home) renderForgeSessionPick(width int) []string {
	p := h.forge.sessionPick
	sel := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	plain := lipgloss.NewStyle().Foreground(ColorText)
	head := fmt.Sprintf("Attach to a session of #%d (enter attach, esc close)", p.number)
	out := []string{lipgloss.NewStyle().Foreground(ColorTextDim).Render(truncateDisplay(head, width, "…"))}
	for i, title := range p.titles {
		text, style := "  "+forgeOneLine(title), plain
		if i == p.at {
			text, style = "▌ "+forgeOneLine(title), sel
		}
		out = append(out, style.Render(truncateDisplay(text, width, "…")))
	}
	return out
}

// forgeDetailTitle is the detail pane's title: ISSUE #n or PR #n, or
// PREVIEW when no row is selected.
func (h *Home) forgeDetailTitle() string {
	r, ok := h.forgeSelectedRow()
	if !ok {
		return "PREVIEW"
	}
	if h.forge.tab == tabPRs {
		return fmt.Sprintf("PR #%d", r.number)
	}
	return fmt.Sprintf("ISSUE #%d", r.number)
}

// previewPaneTitle is the right pane's title: the detail's on a forge tab.
func (h *Home) previewPaneTitle() string {
	if h.forgeTabActive() {
		return h.forgeDetailTitle()
	}
	return "PREVIEW"
}

// forgeKV is one row of the detail's key/value block.
type forgeKV struct{ key, value string }

const forgeKeyWidth = 10

// renderForgeDetail is the detail pane's body in width x height. No I/O:
// it reads the cache and the stored links only.
func (h *Home) renderForgeDetail(width, height int) string {
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	k, r, ok := h.forgeSelectedKey()
	if !ok {
		return dim.Render(truncateDisplay("Nothing selected", width, "…"))
	}
	var e freshness.Entry
	var has bool
	if h.forge.fresh != nil {
		e, has = h.forge.fresh.Get(k)
	}
	stale := has && e.Stale()
	now := h.forgeNow()
	var lines []string
	add := func(s string) { lines = append(lines, truncateDisplay(s, width, "…")) }

	title := lipgloss.NewStyle().Foreground(ColorText).Bold(true)
	glyph := lipgloss.NewStyle().Foreground(r.glyphColor)
	if stale {
		title, glyph = dim, dim
	}
	for i, w := range strings.Split(ansi.Wrap(r.title, max(1, width-2), ""), "\n") {
		prefix := "  "
		if i == 0 {
			prefix = glyph.Render(r.glyph) + " "
		}
		add(prefix + title.Render(w))
	}
	lines = append(lines, "")

	var kvs []forgeKV
	var timeline []github.Event
	switch d := e.Value.(type) {
	case github.IssueDetail:
		if has && e.HasData() && d.Number == r.number {
			kvs, timeline = h.issueDetailRows(d, now), d.Timeline
		}
	case github.PRDetail:
		if has && e.HasData() && d.Number == r.number {
			kvs, timeline = h.prDetailRows(k.Repo, d), d.Timeline
		}
	}
	if kvs == nil {
		switch {
		case has && e.Err != nil:
			f := forgeFailureFor(e.Err, k, now)
			warn := lipgloss.NewStyle().Foreground(ColorYellow)
			for i, l := range f.lines {
				style := warn
				if i > 0 {
					style = dim
				}
				for _, w := range strings.Split(ansi.Wrap(l, max(1, width), ""), "\n") {
					add(style.Render(w))
				}
			}
		default:
			add(dim.Render("loading…"))
		}
		return strings.Join(clipLines(lines, height), "\n")
	}

	keyStyle := dim
	for _, kv := range kvs {
		v := kv.value
		if stale {
			v = dim.Render(ansi.Strip(v))
		}
		add(keyStyle.Render(fmt.Sprintf("%-*s", forgeKeyWidth, kv.key)) + v)
	}
	if stale {
		add(lipgloss.NewStyle().Foreground(ColorYellow).Render("⚠ stale: " + forgeFailureFor(e.Err, k, now).lines[0]))
	}
	if len(timeline) > 0 {
		lines = append(lines, "")
		add(dim.Render("TIMELINE"))
		for _, ev := range timeline {
			add(dim.Render(fmt.Sprintf("%-4s ", forgeAge(now.Sub(ev.At)))) + forgeEventText(ev))
		}
	}
	return strings.Join(clipLines(lines, height), "\n")
}

func clipLines(lines []string, height int) []string {
	if len(lines) > height {
		return lines[:max(0, height)]
	}
	return lines
}

// issueDetailRows are an Issue's key/value rows (spec #18 §3): state,
// author, age, labels, assignee, comments, sessions.
func (h *Home) issueDetailRows(d github.IssueDetail, now time.Time) []forgeKV {
	state := issueGlyphStyled(d.Closed) + " open"
	if d.Closed {
		state = issueGlyphStyled(d.Closed) + " closed"
	}
	labels := "none"
	if len(d.Labels) > 0 {
		labels = lipgloss.NewStyle().Foreground(ColorPurple).Render(forgeOneLine(strings.Join(d.Labels, ", ")))
	}
	assignee := "none"
	if len(d.Assignees) > 0 {
		assignee = forgeOneLine(strings.Join(d.Assignees, ", "))
	}
	return []forgeKV{
		{"state", state},
		{"author", forgeOneLine(d.Author)},
		{"age", forgeAge(now.Sub(d.CreatedAt))},
		{"labels", labels},
		{"assignee", assignee},
		{"comments", fmt.Sprintf("%d", d.Comments)},
		{"sessions", h.forgeSessionsValue(h.forgeLinkedSessions(h.forgeSelectedRepo(), session.LinkIssue, d.Number))},
	}
}

// prDetailRows are a PR's key/value rows (spec #18 §3): state, branch,
// into, checks, review, merge, diff, sessions, local (forge_local.go).
func (h *Home) prDetailRows(repo github.Repo, d github.PRDetail) []forgeKV {
	p := github.PullRequest{State: d.State, Draft: d.Draft}
	state := lipgloss.NewStyle().Foreground(prGlyphColor(p)).Render(prGlyph(p)) + " " + prStateWord(p)
	branch := forgeOneLine(d.HeadBranch)
	if d.HeadRepo.Owner != "" && !sameRepo(d.HeadRepo, repo) {
		branch = forgeOneLine(d.HeadRepo.Owner) + ":" + branch
	}
	diff := fmt.Sprintf("%s %s · %d files",
		lipgloss.NewStyle().Foreground(ColorGreen).Render(fmt.Sprintf("+%d", d.Additions)),
		lipgloss.NewStyle().Foreground(ColorRed).Render(fmt.Sprintf("−%d", d.Deletions)), d.ChangedFiles)
	if d.ChangedFiles == 1 {
		diff = strings.TrimSuffix(diff, "s")
	}
	return []forgeKV{
		{"state", state},
		{"branch", branch},
		{"into", forgeOneLine(d.BaseBranch)},
		{"checks", checksValue(d.Checks, d.CheckCounts)},
		{"review", lipgloss.NewStyle().Foreground(reviewGlyphColor(d.Review)).Render(reviewGlyph(d.Review)) + " " + reviewWord(d.Review)},
		{"merge", lipgloss.NewStyle().Foreground(mergeGlyphColor(d.MergeState)).Render(mergeGlyph(d.MergeState)) + " " + mergeWord(d.MergeState)},
		{"diff", diff},
		{"sessions", h.forgeSessionsValue(h.forgeLinkedSessions(repo, session.LinkPR, d.Number))},
		{"local", h.prLocalRow(repo, d)},
	}
}

// forgeSelectedRepo is the repository of the list on screen.
func (h *Home) forgeSelectedRepo() github.Repo {
	k, _ := h.forgeListKey()
	return k.Repo
}

// forgeSessionsValue is the sessions row: the linked sessions' titles and
// the attach key, or none.
func (h *Home) forgeSessionsValue(insts []*session.Instance) string {
	if len(insts) == 0 {
		return "none"
	}
	titles := make([]string, 0, len(insts))
	for _, inst := range insts {
		titles = append(titles, forgeOneLine(inst.Title))
	}
	v := lipgloss.NewStyle().Foreground(ColorAccent).Render("◉ " + strings.Join(titles, ", "))
	if key := h.forgeKey(hotkeyForgeAttach); key != "" {
		v += lipgloss.NewStyle().Foreground(ColorTextDim).Render("  " + key + " attach")
	}
	return v
}

func prStateWord(p github.PullRequest) string {
	switch {
	case p.State == github.PRMerged:
		return "merged"
	case p.State == github.PRClosed:
		return "closed"
	case p.Draft:
		return "draft"
	}
	return "open"
}

// checksValue is the checks row: the overall glyph and the counts.
func checksValue(state github.CheckState, c github.CheckCounts) string {
	g := lipgloss.NewStyle().Foreground(checkGlyphColor(state)).Render(checkGlyph(state))
	var parts []string
	for _, p := range []struct {
		n    int
		word string
	}{{c.Failed, "failed"}, {c.Running, "running"}, {c.Passed, "passed"}, {c.Skipped, "skipped"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.word))
		}
	}
	if len(parts) == 0 {
		return g + " none"
	}
	return g + " " + strings.Join(parts, " · ")
}

func reviewWord(r github.ReviewDecision) string {
	switch r {
	case github.ReviewApproved:
		return "approved"
	case github.ReviewChangesRequested:
		return "changes requested"
	case github.ReviewRequired:
		return "review required"
	}
	return "none"
}

// mergeGlyph is the merge state: ⇢ clean, ≠ conflict, ↓ behind, ⊘ blocked,
// · anything else.
func mergeGlyph(m github.MergeState) string {
	switch m {
	case github.MergeClean, github.MergeUnstable, github.MergeHasHooks:
		return "⇢"
	case github.MergeDirty:
		return "≠"
	case github.MergeBehind:
		return "↓"
	case github.MergeBlocked:
		return "⊘"
	}
	return "·"
}

func mergeGlyphColor(m github.MergeState) lipgloss.TerminalColor {
	switch m {
	case github.MergeClean, github.MergeHasHooks:
		return ColorGreen
	case github.MergeDirty, github.MergeBlocked:
		return ColorRed
	case github.MergeBehind, github.MergeUnstable:
		return ColorYellow
	}
	return ColorTextDim
}

func mergeWord(m github.MergeState) string {
	switch m {
	case github.MergeClean:
		return "clean"
	case github.MergeUnstable:
		return "clean · failing checks"
	case github.MergeHasHooks:
		return "clean · hooks"
	case github.MergeDirty:
		return "conflict"
	case github.MergeBehind:
		return "behind"
	case github.MergeBlocked:
		return "blocked"
	case github.MergeDraft:
		return "draft"
	}
	return "checking"
}

// forgeEventText is one timeline entry after its age.
func forgeEventText(e github.Event) string {
	who := forgeOneLine(e.Actor)
	switch e.Kind {
	case github.EventComment:
		return who + " commented"
	case github.EventReview:
		switch e.Review {
		case "APPROVED":
			return lipgloss.NewStyle().Foreground(ColorGreen).Render("✔") + " " + who + " approved"
		case "CHANGES_REQUESTED":
			return lipgloss.NewStyle().Foreground(ColorRed).Render("±") + " " + who + " requested changes"
		case "DISMISSED":
			return who + "'s review was dismissed"
		}
		return who + " reviewed"
	case github.EventPush:
		if e.Count == 1 {
			return who + " pushed 1 commit"
		}
		return fmt.Sprintf("%s pushed %d commits", who, e.Count)
	case github.EventForcePush:
		return who + " force-pushed"
	case github.EventChecks:
		g := lipgloss.NewStyle().Foreground(checkGlyphColor(e.Checks)).Render(checkGlyph(e.Checks))
		word := map[github.CheckState]string{github.ChecksPassing: "passed", github.ChecksFailing: "failed", github.ChecksRunning: "running"}[e.Checks]
		if word == "" {
			word = "reported"
		}
		return g + " checks " + word
	case github.EventClosed:
		return who + " closed"
	case github.EventReopened:
		return who + " reopened"
	case github.EventMerged:
		return who + " merged"
	case github.EventReady:
		return who + " marked ready for review"
	case github.EventReferenced:
		return who + " referenced this"
	}
	return who + " " + string(e.Kind)
}
