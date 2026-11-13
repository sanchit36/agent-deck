package ui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/charmbracelet/lipgloss"
)

// Forge lists (spec #18 §3, §5): the Issues and PRs tabs show their
// cached list as two-line rows plus a blank. A list kind plugs in with a
// fetcher in defaultForgeFetchers, a case in forgeListCounts and one in
// forgeRows; rendering, the cursor, counts and o/y are shared.

// forgeRowHeight is the rows a list item takes: two lines and a blank.
const forgeRowHeight = 3

// fetchIssueList is the Issues list fetcher: one GraphQL request, the
// repository connection for the default list and a search otherwise.
func fetchIssueList(ctx context.Context, c *github.Client, k freshness.Key) (any, error) {
	if github.UsesSearch(k.Filter) {
		return c.SearchIssues(ctx, k.Repo, k.Filter)
	}
	return c.IssueList(ctx, k.Repo)
}

// forgeRow is one list item as the list draws it.
type forgeRow struct {
	// glyph is the state glyph, one cell wide (see issueGlyph).
	glyph      string
	glyphColor lipgloss.TerminalColor
	title      string
	// meta is line 2's dim text: #n · author · age.
	meta   string
	author string
	// tags are line 2's labels (Issues).
	tags []string
	// marks are line 2's state marks (PRs: ✓ ci  ✔ review).
	marks     []forgeMark
	url       string
	number    int
	updatedAt time.Time
}

// forgeMark is a coloured one-cell glyph and its dim label.
type forgeMark struct {
	glyph string
	color lipgloss.TerminalColor
	label string
}

// issueGlyph is an Issue's state glyph: ○ open, ✓ closed.
func issueGlyph(closed bool) string {
	if closed {
		return "✓"
	}
	return "○"
}

func issueGlyphColor(closed bool) lipgloss.TerminalColor {
	if closed {
		return ColorPurple
	}
	return ColorGreen
}

// issueGlyphStyled is issueGlyph in its colour.
func issueGlyphStyled(closed bool) string {
	return lipgloss.NewStyle().Foreground(issueGlyphColor(closed)).Render(issueGlyph(closed))
}

// forgeListCounts is how many rows a cached list value holds and how many
// items GitHub counts for it.
func forgeListCounts(v any) (rows, total int, ok bool) {
	switch l := v.(type) {
	case github.IssueList:
		return len(l.Issues), l.Total, true
	case github.PRList:
		return len(l.PRs), l.Total, true
	}
	return 0, 0, false
}

// forgeCounted reports whether v is a list value forgeListCounts counts.
func forgeCounted(v any) bool {
	_, _, ok := forgeListCounts(v)
	return ok
}

// forgeRows are a cached list value's rows, ages measured at now.
func forgeRows(v any, now time.Time) []forgeRow {
	switch l := v.(type) {
	case github.IssueList:
		rows := make([]forgeRow, 0, len(l.Issues))
		for _, is := range l.Issues {
			rows = append(rows, forgeRow{
				glyph:      issueGlyph(is.Closed),
				glyphColor: issueGlyphColor(is.Closed),
				title:      forgeOneLine(is.Title),
				meta:       fmt.Sprintf("#%d · %s · %s", is.Number, forgeOneLine(is.Author), forgeAge(now.Sub(is.UpdatedAt))),
				author:     is.Author,
				tags:       is.Labels,
				url:        is.URL,
				number:     is.Number,
				updatedAt:  is.UpdatedAt,
			})
		}
		return rows
	case github.PRList:
		return prRows(l, now)
	}
	return nil
}

// forgeOneLine makes text from GitHub safe for one row: control characters
// (newlines, escape sequences) become spaces.
func forgeOneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// forgeAge is a compact age for a row: now, 5m, 3h, 4d, 2w, 6mo, 1y.
func forgeAge(d time.Duration) string {
	const day = 24 * time.Hour
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < day:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d < 7*day:
		return fmt.Sprintf("%dd", int(d/day))
	case d < 30*day:
		return fmt.Sprintf("%dw", int(d/(7*day)))
	case d < 365*day:
		return fmt.Sprintf("%dmo", int(d/(30*day)))
	}
	return fmt.Sprintf("%dy", int(d/(365*day)))
}

// forgeListKeyFor is the cache key of tab t's list for the shown Project;
// false when t is not a forge tab or no Project is shown.
func (h *Home) forgeListKeyFor(t leftTab) (freshness.Key, bool) {
	if !h.forge.enabled || t == tabSessions {
		return freshness.Key{}, false
	}
	v := h.forgeView()
	if v == nil || v.resolving {
		return freshness.Key{}, false
	}
	repo, ok := v.shownRepo()
	if !ok {
		return freshness.Key{}, false
	}
	kind := freshness.IssueList
	if t == tabPRs {
		kind = freshness.PRList
	}
	return freshness.ListKey(repo, kind, h.forgeListFilter(t)), true
}

// forgeDefaultKeyFor is the key of tab t's default list for the shown
// Project: the unfiltered list whose total the tab chip shows.
func forgeDefaultKeyFor(k freshness.Key) freshness.Key {
	return freshness.ListKey(k.Repo, k.Kind, github.DefaultFilter)
}

// syncForgeLists points each forge tab's counts and cursor at the cached
// list of the shown Project: the rows shown (after narrowing), the rows
// fetched and GitHub's total, the unfiltered open total the tab chip shows
// (from the default list, whatever the filter), and a cursor kept on a
// row. A tab whose list key changed (another Project or filter, the Parent
// Repository) starts over at the top, uncounted until its list loads; the
// chip keeps its total while the Project stays.
func (h *Home) syncForgeLists() {
	for _, t := range []leftTab{tabIssues, tabPRs} {
		k, ok := h.forgeListKeyFor(t)
		if !ok {
			continue
		}
		l := &h.forge.lists[t]
		moved := l.key != k
		get := func(k freshness.Key) (freshness.Entry, bool) {
			if h.forge.fresh == nil {
				return freshness.Entry{}, false
			}
			return h.forge.fresh.Get(k)
		}
		open, openKnown := l.open, l.openKnown
		if moved && l.key != (freshness.Key{}) && l.key.Repo != k.Repo {
			open, openKnown = 0, false
		}
		if de, ok := get(forgeDefaultKeyFor(k)); ok && de.HasData() {
			if _, total, ok := forgeListCounts(de.Value); ok {
				open, openKnown = total, true
			}
		}
		if e, has := get(k); has && e.HasData() {
			if rows, total, ok := forgeListCounts(e.Value); ok {
				if moved {
					l.cursor, l.offset = 0, 0
				}
				shown := rows
				if words := h.forgeNarrowWords(t); len(words) > 0 {
					shown = len(forgeNarrow(forgeRows(e.Value, h.forgeNow()), words))
				}
				l.key, l.count, l.fetched, l.total = k, shown, rows, total
				l.open, l.openKnown = open, openKnown
				l.cursor = max(0, min(l.cursor, shown-1))
				l.offset = max(0, min(l.offset, l.cursor))
				continue
			}
		}
		// A zero key is a list never counted: leave it as it is.
		if moved && l.key != (freshness.Key{}) {
			*l = forgeList{key: k, open: open, openKnown: openKnown}
		}
	}
}

// markForgeRowsSeen tells the Freshness cache each row's updatedAt, so a
// detail cached at an older version refetches.
func (h *Home) markForgeRowsSeen(k freshness.Key, v any) {
	for _, r := range forgeRows(v, h.forgeNow()) {
		h.forgeCache().Seen(freshness.DetailKey(k.Repo, r.number), r.updatedAt)
	}
}

// forgeVisibleRows are the rows of the list on screen; ok is false while
// there is no list (loading, or a failure with nothing cached).
func (h *Home) forgeVisibleRows() ([]forgeRow, freshness.Entry, bool) {
	_, e, ok := h.forgeVisibleEntry()
	if !ok || !e.HasData() {
		return nil, e, false
	}
	return forgeNarrow(forgeRows(e.Value, h.forgeNow()), h.forgeNarrowWords(h.forge.tab)), e, true
}

// forgeSelectedRow is the row under the cursor of the list on screen.
func (h *Home) forgeSelectedRow() (forgeRow, bool) {
	rows, _, ok := h.forgeVisibleRows()
	if !ok || len(rows) == 0 {
		return forgeRow{}, false
	}
	c := h.forge.lists[h.forge.tab].cursor
	return rows[max(0, min(c, len(rows)-1))], true
}

// forgeCountText is the Project line's count for the tab on screen: N
// open for the whole default list, N match for narrowed rows or a whole
// search, else the rows fetched of GitHub's total (100 of 342).
func (h *Home) forgeCountText() string {
	l := h.forge.lists[h.forge.tab]
	switch {
	case len(h.forgeNarrowWords(h.forge.tab)) > 0:
		return fmt.Sprintf("%d match", l.count)
	case l.fetched < l.total:
		return fmt.Sprintf("%d of %d", l.fetched, l.total)
	case h.forgeFiltered(h.forge.tab):
		return fmt.Sprintf("%d match", l.total)
	}
	return fmt.Sprintf("%d open", l.total)
}

// renderForgeRows draws the list on screen in width x height: two-line
// rows and a blank, the selected one with the ▌ accent bar, the cursor
// kept in view. Dimmed when the list is stale.
func (h *Home) renderForgeRows(width, height int) []string {
	rows, e, ok := h.forgeVisibleRows()
	if !ok || height < 1 {
		return nil
	}
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	if len(rows) == 0 {
		what := "No open issues"
		switch {
		case h.forge.tab == tabPRs && h.forgeFiltered(tabPRs):
			what = "No pull requests match"
		case h.forge.tab == tabPRs:
			what = "No open pull requests"
		case h.forgeFiltered(tabIssues):
			what = "No issues match"
		}
		return []string{dim.Render(truncateDisplay(what, width, "…"))}
	}
	stale := e.Stale()
	var linked map[int][]string
	if k, ok := h.forgeListKey(); ok {
		kind := session.LinkIssue
		if k.Kind == freshness.PRList {
			kind = session.LinkPR
		}
		linked = h.forgeLinkedTitles(k.Repo, kind)
	}
	l := h.forge.lists[h.forge.tab]
	cursor := max(0, min(l.cursor, len(rows)-1))
	page := max(1, height/forgeRowHeight)
	offset := min(l.offset, cursor)
	if cursor >= offset+page {
		offset = cursor - page + 1
	}
	text := lipgloss.NewStyle().Foreground(ColorText)
	bold := lipgloss.NewStyle().Foreground(ColorText).Bold(true)
	tag := lipgloss.NewStyle().Foreground(ColorPurple)
	accent := lipgloss.NewStyle().Foreground(ColorAccent)
	var out []string
	for i := offset; i < len(rows) && len(out) < height; i++ {
		r := rows[i]
		bar := "  "
		if i == cursor {
			bar = accent.Render("▌ ")
		}
		glyph, title, tags := lipgloss.NewStyle().Foreground(r.glyphColor), text, tag
		if i == cursor {
			title = bold
		}
		if stale {
			glyph, title, tags = dim, dim, dim
		}
		line1 := bar + glyph.Render(r.glyph) + " " + title.Render(r.title)
		line2 := bar + "  " + dim.Render(r.meta)
		if len(r.tags) > 0 {
			parts := make([]string, 0, len(r.tags))
			for _, t := range r.tags {
				parts = append(parts, tags.Render(forgeOneLine(t)))
			}
			line2 += "  " + strings.Join(parts, dim.Render(", "))
		}
		for _, m := range r.marks {
			g := lipgloss.NewStyle().Foreground(m.color)
			if stale {
				g = dim
			}
			line2 += "  " + g.Render(m.glyph) + dim.Render(" "+m.label)
		}
		if t := linkedText(linked[r.number]); t != "" {
			link := accent
			if stale {
				link = dim
			}
			line2 += "  " + link.Render(t)
		}
		out = append(out, truncateDisplay(line1, width, "…"), truncateDisplay(line2, width, "…"), "")
	}
	if len(out) > height {
		out = out[:height]
	}
	return out
}
