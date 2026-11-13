package ui

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Filtering (spec #18 §6). / opens a one-line query bar in the Project
// line, in GitHub search syntax. Plain words narrow the fetched rows as
// they are typed; any qualifier runs on enter as one search (scoped to the
// Project and the tab's kind), and over a list GitHub cut at the page,
// enter also sends the plain words to the server. The default list
// (github.DefaultFilter) is always the repository connection, never a
// search. The applied filter is kept per Project + tab for the run;
// [github] issue_filter / pr_filter set the starting one, and esc on the
// list goes back to it.

// forgeFilterScope is what a filter belongs to: one Project (or Parent
// Repository) on one tab.
type forgeFilterScope struct {
	repo github.Repo
	tab  leftTab
}

// forgeFilter is an applied filter: the query as typed and whether its
// plain words went to the server (enter over a cut list).
type forgeFilter struct {
	query  string
	server bool
}

// forgeFilterBar is the query bar while it is open.
type forgeFilterBar struct {
	open  bool
	scope forgeFilterScope
	draft string
	// pick is the preset or recent query ↑/↓ last filled in; -1 none.
	pick int
	// err is GitHub's message for the last query it rejected.
	err string
	// pending is the submitted filter waiting for its search to land.
	pending *forgePendingFilter
}

type forgePendingFilter struct {
	key    freshness.Key
	filter forgeFilter
}

// forgeFilterPreset is a named starting point ↑/↓ fill the bar with.
type forgeFilterPreset struct {
	name, query string
}

var forgeFilterPresets = map[leftTab][]forgeFilterPreset{
	tabIssues: {
		{"Open", "is:open"},
		{"Assigned to me", "is:open assignee:@me"},
		{"Created by me", "is:open author:@me"},
		{"Closed", "is:closed"},
	},
	tabPRs: {
		{"Open", "is:open"},
		{"Review requested from me", "is:open review-requested:@me"},
		{"Created by me", "is:open author:@me"},
		{"Drafts", "is:open draft:true"},
		{"Merged", "is:merged"},
	},
}

// forgeRecentFilters is how many recent queries a Project + tab keeps.
const forgeRecentFilters = 5

// forgeStartFilter is tab t's starting query: [github] issue_filter or
// pr_filter, is:open when unset.
func (h *Home) forgeStartFilter(t leftTab) string {
	q := h.forge.issueFilter
	if t == tabPRs {
		q = h.forge.prFilter
	}
	if strings.TrimSpace(q) == "" {
		return github.DefaultFilter
	}
	return q
}

// forgeFilterScopeOf is the scope of tab t for the shown repository.
func (h *Home) forgeFilterScopeOf(t leftTab) (forgeFilterScope, bool) {
	if t == tabSessions {
		return forgeFilterScope{}, false
	}
	repo, ok := h.forgeView().shownRepo()
	if !ok {
		return forgeFilterScope{}, false
	}
	repo.Host = repo.HostOrDefault()
	return forgeFilterScope{repo: repo, tab: t}, true
}

// forgeAppliedFilter is the filter tab t shows for the shown repository.
func (h *Home) forgeAppliedFilter(t leftTab) forgeFilter {
	if s, ok := h.forgeFilterScopeOf(t); ok {
		if f, ok := h.forge.filters[s]; ok {
			return f
		}
	}
	return forgeFilter{query: h.forgeStartFilter(t)}
}

// forgeFilterIsDefault reports whether tab t shows its starting filter.
func (h *Home) forgeFilterIsDefault(t leftTab) bool {
	f := h.forgeAppliedFilter(t)
	return !f.server && f.query == h.forgeStartFilter(t)
}

// forgeListFilter is the filter the tab's list is fetched with: the
// applied query's qualifiers (and its words, once sent to the server).
func (h *Home) forgeListFilter(t leftTab) string {
	f := h.forgeAppliedFilter(t)
	fetch, _ := github.ParseFilter(f.query).Fetch(f.server)
	return fetch
}

// forgeBarShowing reports whether the bar is open on tab t's scope.
func (h *Home) forgeBarShowing(t leftTab) bool {
	if !h.forge.bar.open {
		return false
	}
	s, ok := h.forgeFilterScopeOf(t)
	return ok && s == h.forge.bar.scope
}

// forgeNarrowWords are the plain words narrowing tab t's rows: the bar's
// while it is open, else the applied filter's (none once they went to the
// server).
func (h *Home) forgeNarrowWords(t leftTab) []string {
	if h.forgeBarShowing(t) {
		_, words := github.ParseFilter(h.forge.bar.draft).Fetch(false)
		return words
	}
	f := h.forgeAppliedFilter(t)
	_, words := github.ParseFilter(f.query).Fetch(f.server)
	return words
}

// forgeNarrow keeps the rows matching every word: in the title, the
// author, a label, or the number (#123 or 123), ignoring case.
func forgeNarrow(rows []forgeRow, words []string) []forgeRow {
	if len(words) == 0 {
		return rows
	}
	lower := make([]string, 0, len(words))
	for _, w := range words {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			lower = append(lower, w)
		}
	}
	out := make([]forgeRow, 0, len(rows))
	for _, r := range rows {
		hay := strings.ToLower(fmt.Sprintf("%s\x00%s\x00%s\x00#%d", r.title, r.author, strings.Join(r.tags, "\x00"), r.number))
		ok := true
		for _, w := range lower {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, r)
		}
	}
	return out
}

// forgeFiltered reports whether tab t shows anything but its default list
// in full: a non-default fetch or narrowing words.
func (h *Home) forgeFiltered(t leftTab) bool {
	return h.forgeListFilter(t) != github.DefaultFilter || len(h.forgeNarrowWords(t)) > 0
}

// openForgeBar is /: open the bar on the applied query.
func (h *Home) openForgeBar() {
	s, ok := h.forgeFilterScopeOf(h.forge.tab)
	if !ok {
		return
	}
	h.forge.detailFull = false
	h.forge.bar = forgeFilterBar{open: true, scope: s, draft: h.forgeAppliedFilter(h.forge.tab).query, pick: -1}
	h.syncForgeHelp()
}

// closeForgeBar drops the bar, its draft and any submitted query.
func (h *Home) closeForgeBar() {
	if !h.forge.bar.open {
		return
	}
	h.forge.bar = forgeFilterBar{}
	h.syncForgeLists()
	h.syncForgeHelp()
}

// handleForgeBarKey owns every key while the bar is open; only ctrl+c
// falls through (quit).
func (h *Home) handleForgeBarKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	bar := &h.forge.bar
	switch msg.Type {
	case tea.KeyCtrlC:
		h.closeForgeBar()
		return h.forgeFallThrough(msg, "ctrl+c")
	case tea.KeyEsc:
		h.closeForgeBar()
		return true, h, h.syncForgeDetail()
	case tea.KeyEnter:
		return true, h, h.submitForgeFilter()
	case tea.KeyUp, tea.KeyDown:
		h.cycleForgeFilter(msg.Type == tea.KeyDown)
	case tea.KeyBackspace:
		if _, size := utf8.DecodeLastRuneInString(bar.draft); size > 0 {
			bar.draft = bar.draft[:len(bar.draft)-size]
		}
	case tea.KeyCtrlU:
		bar.draft = ""
	case tea.KeyCtrlW:
		bar.draft = strings.TrimRight(bar.draft, " ")
		if i := strings.LastIndex(bar.draft, " "); i >= 0 {
			bar.draft = bar.draft[:i+1]
		} else {
			bar.draft = ""
		}
	case tea.KeySpace:
		bar.draft += " "
	case tea.KeyRunes:
		bar.draft += strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == '\t' {
				return ' '
			}
			if r < ' ' {
				return -1
			}
			return r
		}, string(msg.Runes))
	default:
		return true, h, nil
	}
	if msg.Type != tea.KeyUp && msg.Type != tea.KeyDown {
		bar.pick = -1
	}
	bar.err = ""
	bar.pending = nil
	h.syncForgeLists()
	return true, h, h.syncForgeDetail()
}

// forgeFilterChoices are what ↑/↓ cycle: the tab's presets, then this
// Project's recent queries.
func (h *Home) forgeFilterChoices() []string {
	var out []string
	for _, p := range forgeFilterPresets[h.forge.bar.scope.tab] {
		out = append(out, p.query)
	}
	return append(out, h.forge.recent[h.forge.bar.scope]...)
}

func (h *Home) cycleForgeFilter(down bool) {
	choices := h.forgeFilterChoices()
	if len(choices) == 0 {
		return
	}
	bar := &h.forge.bar
	switch {
	case down:
		bar.pick = (bar.pick + 1) % len(choices)
	case bar.pick <= 0:
		bar.pick = len(choices) - 1
	default:
		bar.pick--
	}
	bar.draft = choices[bar.pick]
}

// forgeFilterPresetName is the preset name of query on tab t, or "".
func forgeFilterPresetName(t leftTab, query string) string {
	for _, p := range forgeFilterPresets[t] {
		if p.query == query {
			return p.name
		}
	}
	return ""
}

// submitForgeFilter is enter in the bar. A filter whose list is already
// cached (or needs no new fetch) applies at once; one that needs a fetch
// applies when it lands, so a query GitHub rejects leaves the old rows and
// the bar in place.
func (h *Home) submitForgeFilter() tea.Cmd {
	bar := &h.forge.bar
	s := bar.scope
	query := strings.TrimSpace(bar.draft)
	if query == "" {
		query = h.forgeStartFilter(s.tab)
	}
	pf := github.ParseFilter(query)
	f := forgeFilter{query: query}
	// Over a list GitHub cut at the page, the words go to the server.
	if base, words := pf.Fetch(false); len(words) > 0 && h.forgeListCut(freshness.ListKey(s.repo, forgeListKind(s.tab), base)) {
		f.server = true
	}
	fetch, _ := pf.Fetch(f.server)
	key := freshness.ListKey(s.repo, forgeListKind(s.tab), fetch)
	if e, ok := h.forgeCache().Get(key); ok && e.HasData() {
		h.applyForgeFilter(s, f)
		return tea.Batch(h.forgeRefreshDue(), h.syncForgeDetail())
	}
	bar.err = ""
	bar.pending = &forgePendingFilter{key: key, filter: f}
	if cmd := h.forgeFetch(key); cmd != nil {
		return cmd
	}
	// Already in flight: the result applies it when it lands.
	return nil
}

// forgeListCut reports whether k's cached list holds fewer rows than
// GitHub counts.
func (h *Home) forgeListCut(k freshness.Key) bool {
	e, ok := h.forgeCache().Get(k)
	if !ok || !e.HasData() {
		return false
	}
	rows, total, ok := forgeListCounts(e.Value)
	return ok && rows < total
}

// applyForgeFilter makes f scope s's filter, remembers it as a recent
// query, and closes the bar.
func (h *Home) applyForgeFilter(s forgeFilterScope, f forgeFilter) {
	if h.forge.filters == nil {
		h.forge.filters = map[forgeFilterScope]forgeFilter{}
	}
	if !f.server && f.query == h.forgeStartFilter(s.tab) {
		delete(h.forge.filters, s)
	} else {
		h.forge.filters[s] = f
	}
	h.rememberForgeFilter(s, f.query)
	h.forge.bar = forgeFilterBar{}
	h.syncForgeLists()
	h.syncForgeHelp()
}

// rememberForgeFilter puts query first in scope s's recent queries; the
// presets are not repeated there.
func (h *Home) rememberForgeFilter(s forgeFilterScope, query string) {
	if forgeFilterPresetName(s.tab, query) != "" {
		return
	}
	if h.forge.recent == nil {
		h.forge.recent = map[forgeFilterScope][]string{}
	}
	list := []string{query}
	for _, q := range h.forge.recent[s] {
		if q != query && len(list) < forgeRecentFilters {
			list = append(list, q)
		}
	}
	h.forge.recent[s] = list
}

// applyForgeFilterFetched settles a submitted filter whose fetch landed:
// a rejected query shows in the bar, anything else applies (a failure then
// shows as any list failure does).
func (h *Home) applyForgeFilterFetched(msg forgeFetchedMsg) {
	p := h.forge.bar.pending
	if p == nil || p.key != msg.key {
		return
	}
	var qe *github.QueryError
	if errors.As(msg.err, &qe) {
		h.forge.bar.pending = nil
		h.forge.bar.err = qe.Message
		return
	}
	h.applyForgeFilter(h.forge.bar.scope, p.filter)
}

// resetForgeFilter is esc on the list: back to the starting filter. False
// when the tab already shows it.
func (h *Home) resetForgeFilter() (tea.Cmd, bool) {
	if h.forgeFilterIsDefault(h.forge.tab) {
		return nil, false
	}
	s, ok := h.forgeFilterScopeOf(h.forge.tab)
	if !ok {
		return nil, false
	}
	delete(h.forge.filters, s)
	h.syncForgeLists()
	return h.forgeRefreshDue(), true
}

func forgeListKind(t leftTab) freshness.Kind {
	if t == tabPRs {
		return freshness.PRList
	}
	return freshness.IssueList
}

// ---- rendering ----

// renderForgeBarLine is the Project line while the bar is open: / and the
// query, its end kept in view, then the preset name or the count.
func (h *Home) renderForgeBarLine(width int) string {
	accent := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	right := ""
	if name := forgeFilterPresetName(h.forge.tab, strings.TrimSpace(h.forge.bar.draft)); name != "" {
		right = dim.Render(name)
	} else if _, e, ok := h.forgeVisibleEntry(); ok && e.HasData() && forgeCounted(e.Value) {
		right = dim.Render(h.forgeCountText())
	}
	rw := lipgloss.Width(right)
	room := width - 2 - 1 // "/ " and the cursor
	if rw > 0 {
		room -= rw + 2
	}
	if room < 4 {
		right, rw, room = "", 0, width-3
	}
	draft := forgeOneLine(h.forge.bar.draft)
	if lipgloss.Width(draft) > room {
		draft = truncateDisplayLeft(draft, max(1, room), "…")
	}
	left := accent.Render("/") + " " + lipgloss.NewStyle().Foreground(ColorText).Render(draft) + accent.Render("▏")
	line := left
	if right != "" {
		line = left + strings.Repeat(" ", max(2, width-lipgloss.Width(left)-rw)) + right
	}
	if lipgloss.Width(line) > width {
		line = truncateDisplay(line, width, "…")
	}
	return line
}

// renderForgeBarNotes are the lines under the open bar: GitHub's message
// for a rejected query, or the hint that the words only narrow a list cut
// at the page.
func (h *Home) renderForgeBarNotes(width int) []string {
	var text string
	var style lipgloss.Style
	switch {
	case h.forge.bar.err != "":
		text, style = "⚠ "+h.forge.bar.err, lipgloss.NewStyle().Foreground(ColorYellow)
	case h.forge.bar.pending != nil:
		text, style = "searching…", lipgloss.NewStyle().Foreground(ColorTextDim)
	case len(h.forgeNarrowWords(h.forge.tab)) > 0:
		k, ok := h.forgeListKey()
		if !ok || !h.forgeListCut(k) {
			return nil
		}
		e, _ := h.forgeCache().Get(k)
		rows, _, _ := forgeListCounts(e.Value)
		text, style = fmt.Sprintf("showing matches in the latest %d · enter to search all", rows), lipgloss.NewStyle().Foreground(ColorTextDim)
	default:
		return nil
	}
	var out []string
	for _, w := range strings.Split(ansi.Wrap(text, max(1, width), ""), "\n") {
		out = append(out, style.Render(w))
	}
	return out
}

// forgeFilterChip is the dim filter chip after `owner/repo ▾`: the
// applied query.
func (h *Home) forgeFilterChip() string {
	return forgeOneLine(h.forgeAppliedFilter(h.forge.tab).query)
}
