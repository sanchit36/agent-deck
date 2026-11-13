package ui

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Files tab (spec #18 §10): the overlay's second tab on a PR. A file list
// (status glyph, dim directory, file name, +a −d) beside one file's
// unified diff under a header (+a −d, file i/N); tab moves the focus
// between them. Under forgeFilesNarrowWidth columns the list hides, the
// header carries the position and f opens the list full screen.
//
// The diff is one Freshness key, freshness.DiffKey(repo, n, headSHA): the
// head SHA comes from the PR's cached detail (fetched here when no detail
// pane has it, as in the single column), and a diff is never refetched for
// the same head while it is cached. While a new head's diff loads, the
// last one stays on screen; the cached diffs are bounded
// (forge_files_cache.go). It is parsed where it is fetched (github.ParseDiff), off the
// render path; the view keeps a per-file row model and styles only the
// rows on screen, so a frame costs the same for a 10-line and a
// 1500-line file.
//
// Generated files and files over github.LargeFileLines changed lines start
// collapsed to one line that enter expands; binary files show a note; a
// diff over github.LargeDiffLines lines or github.LargeDiffFiles files
// gets a warning line. Long lines are cut with › and pan with h/l; n/N
// move by hunk across files and ]/[ by file.
//
// s toggles a side-by-side (split) view, old | new, which needs
// forgeFilesSplitWidth columns: narrower, the view stays unified and a
// line says why. | and \ hand the file's or the whole PR's diff to the
// pager (forge_pager.go).

// forgeFilesNarrowWidth is the overlay width under which the file list
// hides behind f.
const forgeFilesNarrowWidth = 90

// forgeFilesSplitWidth is the overlay width the split view needs.
const forgeFilesSplitWidth = 120

// forgeDiffTabWidth is the tab stop diff text is expanded to.
const forgeDiffTabWidth = 4

// Files tab actions ([hotkeys] names, spec #18 §4). They are seen before
// the overlay's actions while the tab shows.
const (
	// hotkeyForgeNextHunk and hotkeyForgePrevHunk move by hunk across
	// files.
	hotkeyForgeNextHunk = "forge_next_hunk"
	hotkeyForgePrevHunk = "forge_prev_hunk"
	// hotkeyForgeNextFile and hotkeyForgePrevFile move by file.
	hotkeyForgeNextFile = "forge_next_file"
	hotkeyForgePrevFile = "forge_prev_file"
	// hotkeyForgeFileList opens the file list full screen (narrow only).
	hotkeyForgeFileList = "forge_file_list"
	// hotkeyForgeFocus moves the keys between the list and the diff (wide
	// only).
	hotkeyForgeFocus = "forge_focus"
	// hotkeyForgeSplit toggles the side-by-side view.
	hotkeyForgeSplit = "forge_split"
	// hotkeyForgePagerFile and hotkeyForgePagerPR hand the file's or the
	// whole PR's diff to the pager.
	hotkeyForgePagerFile = "forge_pager_file"
	hotkeyForgePagerPR   = "forge_pager_pr"
	// hotkeyForgeFilesNew starts a session on the PR from the Files tab
	// while a Files action has forge_new's key (by default n is next hunk
	// there); otherwise forge_new's own key does it (forgeFilesNewAction).
	hotkeyForgeFilesNew = "forge_files_new"
)

// forgeFilesActionOrder is the Files tab's key space of [hotkeys] actions.
var forgeFilesActionOrder = []string{
	hotkeyForgeNextHunk,
	hotkeyForgePrevHunk,
	hotkeyForgeNextFile,
	hotkeyForgePrevFile,
	hotkeyForgeFileList,
	hotkeyForgeFocus,
	hotkeyForgeSplit,
	hotkeyForgePagerFile,
	hotkeyForgePagerPR,
	hotkeyForgeFilesNew,
}

// buildForgeFilesLookup maps each pressed key to its Files action. The new
// session key follows forge_new: forge_files_new is in the key space only
// while a Files action has forge_new's key; otherwise forge_new's key
// reaches the overlay's forge_new, and that is the one key that starts a
// session on Files.
func buildForgeFilesLookup(bindings map[string]string) map[string]string {
	order := forgeFilesActionOrder[:len(forgeFilesActionOrder)-1]
	lookup := buildForgeActionLookup(order, bindings)
	if forgeFilesNewAction(lookup, bindings) == hotkeyForgeFilesNew {
		lookup = buildForgeActionLookup(forgeFilesActionOrder, bindings)
	}
	return lookup
}

// forgeFilesNewAction is the action that starts a session on Files:
// forge_files_new when an action of lookup (the Files key space) has
// forge_new's key, else forge_new.
func forgeFilesNewAction(lookup, bindings map[string]string) string {
	for _, alias := range explicitHotkeyAliases(bindings[hotkeyForgeNew]) {
		if a := lookup[alias]; a != "" && a != hotkeyForgeFilesNew {
			return hotkeyForgeFilesNew
		}
	}
	return hotkeyForgeNew
}

// forgeFilesView is the Files tab's state; the zero value is a fresh tab.
type forgeFilesView struct {
	// sha is the head SHA of the diff on screen: the PR's current head
	// once its diff arrived, until then the previous one.
	sha string
	// file is the selected file's index.
	file int
	// listFocus is true while the list has the keys (wide layout).
	listFocus bool
	// listFull is true while f shows the list full screen (narrow).
	listFull bool
	// listTop is the first file the list shows.
	listTop int
	// expanded marks the collapsed files the reader expanded, by path.
	expanded map[string]bool
	// split is the reader's s: side by side when the overlay is
	// forgeFilesSplitWidth wide, else unified with a line saying why.
	split bool
	// pagerErr is the last pager's failure, shown in the status row until
	// the next Files key.
	pagerErr string
	memo     forgeDiffRowsMemo
}

// forgeDiffRowKind is what one row of a file's diff shows.
type forgeDiffRowKind int

const (
	diffRowHunk forgeDiffRowKind = iota
	diffRowLine
	// diffRowNoEOL follows a line with no newline at the end of the file.
	diffRowNoEOL
	// diffRowNote is a one-line note in place of the diff: collapsed,
	// binary, no content changes.
	diffRowNote
	// diffRowPair is a split row: old and new index the hunk's lines on
	// each side, -1 for a blank cell.
	diffRowPair
)

// forgeDiffRow is one row of a file's diff, by index into its hunks. In
// the split view a diffRowNoEOL row uses old and new (-1: no note on that
// side) instead of line.
type forgeDiffRow struct {
	kind       forgeDiffRowKind
	hunk, line int
	old, new   int
	note       string
}

// forgeDiffRows is the row model of one file's diff.
type forgeDiffRows struct {
	rows []forgeDiffRow
	// hunks are the rows of the hunk headers.
	hunks []int
	// numWidth is the width of the widest line number.
	numWidth int
	// textWidth is the widest line's text in cells (the pan limit).
	textWidth int
	// collapsed is true while the file shows as its one-line note.
	collapsed bool
	// split is true for the side-by-side rows.
	split bool
}

type forgeDiffRowsMemo struct {
	valid    bool
	sha      string
	file     int
	expanded bool
	split    bool
	rows     forgeDiffRows
}

// fetchDiff is the diff fetcher: the PR's diff, parsed. gh serves the diff
// at the PR's current head, which is k's unless a push landed in between.
func fetchDiff(ctx context.Context, c *github.Client, k freshness.Key) (any, error) {
	return c.PRDiff(ctx, k.Repo, k.Number)
}

// forgeFilesDetailKey is the detail key of the overlay's PR.
func (h *Home) forgeFilesDetailKey() (freshness.Key, bool) {
	o := &h.forge.overlay
	if !o.visible || !o.pr {
		return freshness.Key{}, false
	}
	return freshness.DetailKey(o.repo, o.row.number), true
}

// forgeFilesHead is the overlay PR's head SHA from its cached detail; ""
// until the detail arrives.
func (h *Home) forgeFilesHead() string {
	dk, ok := h.forgeFilesDetailKey()
	if !ok || h.forge.fresh == nil {
		return ""
	}
	e, _ := h.forge.fresh.Get(dk)
	if d, isPR := e.Value.(github.PRDetail); isPR && e.HasData() {
		return d.HeadSHA
	}
	return ""
}

// forgeFilesKeys are the keys the Files tab shows: the PR's detail (for
// the head SHA) and, once that is known, the diff at the head.
func (h *Home) forgeFilesKeys() []freshness.Key {
	o := &h.forge.overlay
	dk, ok := h.forgeFilesDetailKey()
	if !ok || o.tab != forgeOverlayFiles {
		return nil
	}
	keys := []freshness.Key{dk}
	if sha := h.forgeFilesHead(); sha != "" {
		keys = append(keys, freshness.DiffKey(o.repo, o.row.number, sha))
	}
	return keys
}

// forgeFilesFetch starts the Files tab's due fetches and adopts the head's
// diff once it is cached.
func (h *Home) forgeFilesFetch() tea.Cmd {
	h.adoptForgeFilesDiff()
	h.boundForgeDiffs()
	if h.forgePaused() {
		return nil
	}
	now := h.forgeNow()
	var cmds []tea.Cmd
	for _, k := range h.forgeFilesKeys() {
		if h.forgeCache().Due(k, now, h.forge.interval) {
			cmds = append(cmds, h.forgeFetch(k))
		}
	}
	return tea.Batch(cmds...)
}

// adoptForgeFilesDiff shows the head's diff once it is cached, keeping
// the selected file when the new diff still has it.
func (h *Home) adoptForgeFilesDiff() {
	o := &h.forge.overlay
	f := &o.files
	sha := h.forgeFilesHead()
	if sha == "" || sha == f.sha || h.forge.fresh == nil {
		return
	}
	e, _ := h.forge.fresh.Get(freshness.DiffKey(o.repo, o.row.number, sha))
	next, ok := e.Value.(github.Diff)
	if !ok || !e.HasData() {
		return
	}
	keep := ""
	if old, had := h.forgeFilesDiff(); had && f.file < len(old.Files) {
		keep = old.Files[f.file].Path()
	}
	kept := -1
	for i, nf := range next.Files {
		if keep != "" && nf.Path() == keep {
			kept = i
			break
		}
	}
	f.sha, f.memo = sha, forgeDiffRowsMemo{}
	if kept >= 0 {
		f.file = kept
	} else {
		f.file = 0
		o.scroll[forgeOverlayFiles] = forgeOverlayScroll{}
	}
	h.clampForgeFiles()
}

// forgeFilesDiff is the diff on screen; false while none has arrived.
func (h *Home) forgeFilesDiff() (github.Diff, bool) {
	o := &h.forge.overlay
	if o.files.sha == "" || h.forge.fresh == nil {
		return github.Diff{}, false
	}
	e, _ := h.forge.fresh.Get(freshness.DiffKey(o.repo, o.row.number, o.files.sha))
	d, ok := e.Value.(github.Diff)
	return d, ok && e.HasData()
}

// forgeFilesStatusKey is the key the Files tab's footer reports on: the
// head's diff once the head is known, else the detail.
func (h *Home) forgeFilesStatusKey() (freshness.Key, bool) {
	keys := h.forgeFilesKeys()
	if len(keys) == 0 {
		return freshness.Key{}, false
	}
	return keys[len(keys)-1], true
}

// forgeFilesLayout is the Files tab's geometry at the overlay's size: the
// warning row, the list and diff column widths (list 0 when hidden), and
// how many diff rows fit under the file header.
type forgeFilesLayout struct {
	warn          bool
	narrow        bool
	listW, diffW  int
	height        int
	diffRows      int
	listRows      int
	showList      bool
	showDiff      bool
	listFullWidth bool
	// split is true while the diff shows side by side; splitNote while
	// the reader asked for it but the overlay is too narrow (a line says
	// why).
	split, splitNote bool
}

func (h *Home) forgeFilesLayoutFor(d github.Diff) forgeFilesLayout {
	o := &h.forge.overlay
	width := max(1, o.width)
	l := forgeFilesLayout{warn: d.Large(), narrow: width < forgeFilesNarrowWidth, height: h.forgeOverlayBodyHeight()}
	l.splitNote = o.files.split && width < forgeFilesSplitWidth
	l.split = o.files.split && !l.splitNote
	top := 0
	if l.warn {
		top++
	}
	if l.splitNote {
		top++
	}
	avail := max(1, l.height-top)
	switch {
	case l.narrow && o.files.listFull:
		l.showList, l.listFullWidth, l.listW = true, true, width
		l.listRows = max(1, avail-1)
	case l.narrow:
		l.showDiff, l.diffW = true, width
	default:
		l.showList, l.showDiff = true, true
		l.listW = max(26, min(44, width*30/100))
		l.diffW = max(1, width-l.listW-1)
		l.listRows = max(1, avail-1)
	}
	l.diffRows = max(1, avail-1)
	return l
}

// forgeFilesLayoutNow is the layout of the diff on screen.
func (h *Home) forgeFilesLayoutNow() forgeFilesLayout {
	d, _ := h.forgeFilesDiff()
	return h.forgeFilesLayoutFor(d)
}

// forgeFilesSplit reports whether the diff shows side by side now.
func (h *Home) forgeFilesSplit() bool {
	o := &h.forge.overlay
	return o.files.split && o.width >= forgeFilesSplitWidth
}

// forgeFilesRows is the selected file's row model (memoised per diff,
// file, expanded state and split).
func (h *Home) forgeFilesRows() *forgeDiffRows {
	o := &h.forge.overlay
	f := &o.files
	d, ok := h.forgeFilesDiff()
	if !ok || f.file >= len(d.Files) {
		f.memo = forgeDiffRowsMemo{}
		return &f.memo.rows
	}
	file := d.Files[f.file]
	expanded := f.expanded[file.Path()]
	split := h.forgeFilesSplit()
	if f.memo.valid && f.memo.sha == f.sha && f.memo.file == f.file && f.memo.expanded == expanded && f.memo.split == split {
		return &f.memo.rows
	}
	f.memo = forgeDiffRowsMemo{valid: true, sha: f.sha, file: f.file, expanded: expanded, split: split, rows: forgeDiffRowsFor(file, expanded, split)}
	return &f.memo.rows
}

// forgeDiffRowsFor lays out file's diff as rows: each hunk's header then
// its lines (split: its lines paired old | new, github.PairLines), or a
// one-line note for a collapsed, binary or content-free file.
func forgeDiffRowsFor(file github.DiffFile, expanded, split bool) forgeDiffRows {
	var r forgeDiffRows
	switch {
	case file.Binary:
		r.rows = []forgeDiffRow{{kind: diffRowNote, note: "binary file · not shown"}}
		return r
	case file.StartsCollapsed() && !expanded:
		why := "large file"
		if file.Generated() {
			why = "generated file"
		}
		r.collapsed = true
		r.rows = []forgeDiffRow{{kind: diffRowNote, note: fmt.Sprintf("▸ %s · %s · enter expands", why, forgeChangedLines(file.Changed()))}}
		return r
	case len(file.Hunks) == 0:
		note := "no content changes"
		if file.Status == github.FileRenamed || file.Status == github.FileCopied {
			note = string(file.Status) + " from " + forgeOneLine(file.OldPath) + " · no content changes"
		}
		r.rows = []forgeDiffRow{{kind: diffRowNote, note: note}}
		return r
	}
	r.split = split
	maxNo := 1
	for hi, hk := range file.Hunks {
		r.hunks = append(r.hunks, len(r.rows))
		r.rows = append(r.rows, forgeDiffRow{kind: diffRowHunk, hunk: hi})
		maxNo = max(maxNo, hk.OldStart+hk.OldLines, hk.NewStart+hk.NewLines)
		if split {
			r.addPairs(hi, hk.Lines)
			continue
		}
		for li, l := range hk.Lines {
			r.rows = append(r.rows, forgeDiffRow{kind: diffRowLine, hunk: hi, line: li})
			r.textWidth = max(r.textWidth, ansi.StringWidth(forgeDiffText(l.Text)))
			if l.NoNewline {
				r.rows = append(r.rows, forgeDiffRow{kind: diffRowNoEOL, hunk: hi, line: li})
			}
		}
	}
	r.numWidth = len(strconv.Itoa(maxNo))
	return r
}

// addPairs appends hunk hi's lines as split rows: one row per pair, then a
// no-newline row under a pair whose old or new line has none.
func (r *forgeDiffRows) addPairs(hi int, lines []github.DiffLine) {
	noEOL := func(i int) int {
		if i >= 0 && lines[i].NoNewline {
			return i
		}
		return -1
	}
	for _, p := range github.PairLines(lines) {
		r.rows = append(r.rows, forgeDiffRow{kind: diffRowPair, hunk: hi, old: p.Old, new: p.New})
		for _, i := range []int{p.Old, p.New} {
			if i >= 0 {
				r.textWidth = max(r.textWidth, ansi.StringWidth(forgeDiffText(lines[i].Text)))
			}
		}
		if o, n := noEOL(p.Old), noEOL(p.New); o >= 0 || n >= 0 {
			r.rows = append(r.rows, forgeDiffRow{kind: diffRowNoEOL, hunk: hi, old: o, new: n})
		}
	}
}

// forgeDiffText makes diff text safe and one line for the terminal: tabs
// expanded, a CRLF's CR dropped, other control characters shown as �.
func forgeDiffText(s string) string {
	s = strings.TrimSuffix(s, "\r")
	clean := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f || c >= 0x80 {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := forgeDiffTabWidth - col%forgeDiffTabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case unicode.IsControl(r) || r == unicode.ReplacementChar:
			b.WriteRune(unicode.ReplacementChar)
			col++
		default:
			b.WriteRune(r)
			col += ansi.StringWidth(string(r))
		}
	}
	return b.String()
}

// forgeChangedLines is "n changed line(s)".
func forgeChangedLines(n int) string {
	if n == 1 {
		return "1 changed line"
	}
	return forgeThousands(n) + " changed lines"
}

// forgeThousands spells n with thousands separators.
func forgeThousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// forgeDiffTint is the background tint of added and removed lines.
func forgeDiffTint() (lipgloss.Color, lipgloss.Color) {
	if ColorBg == lightColors.Bg {
		return lipgloss.Color("#d3e3c6"), lipgloss.Color("#efd0d6")
	}
	return lipgloss.Color("#1f3326"), lipgloss.Color("#3d2229")
}

// forgeFileGlyph is a file's one-cell status glyph and its colour.
func forgeFileGlyph(f github.DiffFile) (string, lipgloss.TerminalColor) {
	switch f.Status {
	case github.FileAdded:
		return "A", ColorGreen
	case github.FileDeleted:
		return "D", ColorRed
	case github.FileRenamed:
		return "R", ColorCyan
	case github.FileCopied:
		return "C", ColorCyan
	}
	return "M", ColorYellow
}

// forgeFileName is a file's name for the list and header: the path, or
// old → new for a rename or copy.
func forgeFileName(f github.DiffFile) string {
	if (f.Status == github.FileRenamed || f.Status == github.FileCopied) && f.OldPath != f.NewPath {
		return forgeOneLine(f.OldPath) + " → " + forgeOneLine(f.NewPath)
	}
	return forgeOneLine(f.Path())
}

// renderForgeFiles draws the Files tab's body: exactly height rows, none
// wider than the overlay. No I/O.
func (h *Home) renderForgeFiles(height int) []string {
	o := &h.forge.overlay
	width := max(1, o.width)
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	pad := func(rows []string) []string {
		for len(rows) < height {
			rows = append(rows, "")
		}
		return rows[:height]
	}
	d, ok := h.forgeFilesDiff()
	if !ok {
		return pad(h.forgeFilesPending(width))
	}
	if len(d.Files) == 0 {
		return pad([]string{" " + dim.Render("No changed files.")})
	}
	l := h.forgeFilesLayoutFor(d)
	var out []string
	if l.warn {
		warn := fmt.Sprintf("⚠ large PR: %s in %s files", forgeChangedLines(d.Changed()), forgeThousands(len(d.Files)))
		out = append(out, lipgloss.NewStyle().Foreground(ColorYellow).Render(truncateDisplay(" "+warn, width, "…")))
	}
	if l.splitNote {
		note := fmt.Sprintf("split view needs %d+ columns (this is %d) · showing unified", forgeFilesSplitWidth, width)
		if k := h.forgeKey(hotkeyForgeSplit); k != "" {
			note += " · " + k + " turns split off"
		}
		out = append(out, dim.Render(truncateDisplay(" "+note, width, "…")))
	}
	rows := height - len(out)
	var list, diff []string
	if l.showList {
		list = h.renderForgeFileList(d, l.listW, rows, !l.showDiff || o.files.listFocus)
	}
	if l.showDiff {
		diff = h.renderForgeFileDiff(d, l, rows)
	}
	switch {
	case l.showList && l.showDiff:
		sep := lipgloss.NewStyle().Foreground(ColorBorder).Render("│")
		for i := 0; i < rows; i++ {
			out = append(out, forgeFitCells(list[i], l.listW)+sep+diff[i])
		}
	case l.showList:
		out = append(out, list...)
	default:
		out = append(out, diff...)
	}
	return pad(out)
}

// forgeFitCells pads or cuts s to exactly w cells.
func forgeFitCells(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return truncateDisplay(s, w, "…")
}

// forgeFilesPending is the body before a diff arrives: the failure of the
// detail or diff fetch, else a loading line.
func (h *Home) forgeFilesPending(width int) []string {
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	k, ok := h.forgeFilesStatusKey()
	var e freshness.Entry
	if ok && h.forge.fresh != nil {
		e, _ = h.forge.fresh.Get(k)
	}
	if e.Err == nil {
		return []string{" " + dim.Render("loading…")}
	}
	f := forgeFailureFor(e.Err, k, h.forgeNow())
	warn := lipgloss.NewStyle().Foreground(ColorYellow)
	var out []string
	for i, line := range f.lines {
		style := warn
		if i > 0 {
			style = dim
		}
		for _, w := range strings.Split(ansi.Wrap(line, max(1, width-2), ""), "\n") {
			out = append(out, " "+style.Render(w))
		}
	}
	return out
}

// renderForgeFileList draws the file list: a `Files N  +a −d` title, then
// one row per file from listTop, the selected one marked (accent when the
// list has the keys).
func (h *Home) renderForgeFileList(d github.Diff, width, height int, focused bool) []string {
	o := &h.forge.overlay
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	green := lipgloss.NewStyle().Foreground(ColorGreen)
	red := lipgloss.NewStyle().Foreground(ColorRed)
	title := lipgloss.NewStyle().Bold(true).Foreground(ColorText).Render(fmt.Sprintf(" Files %d", len(d.Files))) + "  " +
		green.Render(fmt.Sprintf("+%d", d.Additions)) + " " + red.Render(fmt.Sprintf("−%d", d.Deletions))
	out := []string{truncateDisplay(title, width, "…")}
	for i := o.files.listTop; i < len(d.Files) && len(out) < height; i++ {
		f := d.Files[i]
		glyph, color := forgeFileGlyph(f)
		stats := fmt.Sprintf("+%d −%d", f.Additions, f.Deletions)
		styledStats := green.Render(fmt.Sprintf("+%d", f.Additions)) + " " + red.Render(fmt.Sprintf("−%d", f.Deletions))
		if f.Binary {
			stats = "bin"
			styledStats = dim.Render(stats)
		}
		marker := " "
		if i == o.files.file {
			marker = "▌"
		}
		room := max(1, width-1-1-1-lipgloss.Width(stats)-2)
		name := forgeFileName(f)
		dir, base := path.Split(name)
		if f.Status == github.FileRenamed || f.Status == github.FileCopied {
			dir, base = "", name
		}
		var shown string
		if lipgloss.Width(base) >= room {
			shown = truncateDisplayLeft(base, room, "…")
		} else {
			shown = dim.Render(truncateDisplayLeft(dir, room-lipgloss.Width(base), "…")) + base
		}
		if i == o.files.file {
			style := lipgloss.NewStyle().Foreground(ColorText).Bold(true)
			if focused {
				style = style.Foreground(ColorAccent)
			}
			marker = style.Render(marker)
			if lipgloss.Width(base) < room {
				shown = dim.Render(truncateDisplayLeft(dir, room-lipgloss.Width(base), "…")) + style.Render(base)
			} else {
				shown = style.Render(shown)
			}
		}
		left := marker + lipgloss.NewStyle().Foreground(color).Render(glyph) + " " + shown
		gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(stats)-1)
		row := left + strings.Repeat(" ", gap) + styledStats
		out = append(out, truncateDisplay(row, width, "…"))
	}
	for len(out) < height {
		out = append(out, "")
	}
	return out
}

// renderForgeFileDiff draws the selected file's header, then the diff
// rows on screen, each exactly l.diffW cells wide.
func (h *Home) renderForgeFileDiff(d github.Diff, l forgeFilesLayout, height int) []string {
	o := &h.forge.overlay
	width := l.diffW
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	file := d.Files[o.files.file]
	glyph, color := forgeFileGlyph(file)

	right := lipgloss.NewStyle().Foreground(ColorGreen).Render(fmt.Sprintf("+%d", file.Additions)) + " " +
		lipgloss.NewStyle().Foreground(ColorRed).Render(fmt.Sprintf("−%d", file.Deletions)) +
		dim.Render(fmt.Sprintf("  file %d/%d", o.files.file+1, len(d.Files)))
	if k := h.forgeKey(hotkeyForgeFileList); l.narrow && k != "" {
		right += dim.Render(" · " + k + " list")
	}
	right += " "
	room := max(1, width-lipgloss.Width(right)-4)
	name := truncateDisplayLeft(forgeFileName(file), room, "…")
	left := " " + lipgloss.NewStyle().Foreground(color).Render(glyph) + " " + lipgloss.NewStyle().Bold(true).Foreground(ColorText).Render(name)
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	out := []string{forgeFitCells(left+strings.Repeat(" ", gap)+right, width)}

	r := h.forgeFilesRows()
	s := o.scroll[forgeOverlayFiles]
	for i := s.offset; i < len(r.rows) && len(out) < height; i++ {
		row := r.rows[i]
		if r.split && (row.kind == diffRowPair || row.kind == diffRowNoEOL) {
			out = append(out, renderForgeSplitRow(file, r, row, width, s.pan))
			continue
		}
		out = append(out, renderForgeDiffRow(file, r, row, width, s.pan))
	}
	for len(out) < height {
		out = append(out, strings.Repeat(" ", width))
	}
	return out
}

// renderForgeDiffRow draws one diff row at width cells: a hunk header, a
// line (both line numbers in the gutter, the +/− mark, the text panned by
// pan and cut with ›) on a green or red tint, or a note.
func renderForgeDiffRow(file github.DiffFile, r *forgeDiffRows, row forgeDiffRow, width, pan int) string {
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	switch row.kind {
	case diffRowNote:
		return forgeFitCells("  "+dim.Render(row.note), width)
	case diffRowHunk:
		return forgeFitCells(" "+lipgloss.NewStyle().Foreground(ColorCyan).Render(forgeOneLine(file.Hunks[row.hunk].Header)), width)
	case diffRowNoEOL:
		gutter := strings.Repeat(" ", 1+2*r.numWidth+2)
		return forgeFitCells(gutter+dim.Render("  \\ no newline at end of file"), width)
	}
	l := file.Hunks[row.hunk].Lines[row.line]
	addBg, delBg := forgeDiffTint()
	base := lipgloss.NewStyle()
	markColor := lipgloss.TerminalColor(ColorTextDim)
	mark := " "
	switch l.Kind {
	case github.LineAdd:
		base, markColor, mark = base.Background(addBg), ColorGreen, "+"
	case github.LineDel:
		base, markColor, mark = base.Background(delBg), ColorRed, "-"
	}
	num := func(n int) string {
		if n == 0 {
			return strings.Repeat(" ", r.numWidth)
		}
		s := strconv.Itoa(n)
		return strings.Repeat(" ", max(0, r.numWidth-len(s))) + s
	}
	gutter := " " + num(l.Old) + " " + num(l.New) + " "
	room := width - lipgloss.Width(gutter) - 2
	if room < 1 {
		return forgeFitCells(base.Inherit(dim).Render(gutter), width)
	}
	return base.Foreground(ColorTextDim).Render(gutter) +
		base.Foreground(markColor).Render(mark+" ") +
		base.Foreground(ColorText).Render(forgePanCells(forgeDiffText(l.Text), room, pan))
}

// forgePanCells is text panned by pan and fitted to exactly room cells:
// cut with › when more follows, padded otherwise.
func forgePanCells(text string, room, pan int) string {
	tw := ansi.StringWidth(text)
	var vis string
	switch {
	case pan >= tw:
		vis = ""
	case tw-pan > room:
		limit := room - 1
		vis = forgeCutCells(ansi.Cut(text, pan, pan+limit), limit) + "›"
	default:
		vis = forgeCutCells(ansi.Cut(text, pan, tw), room)
	}
	return vis + strings.Repeat(" ", max(0, room-ansi.StringWidth(vis)))
}

// forgeSplitWidths are the old and new cells' widths in a split row of
// width cells (one cell between them).
func forgeSplitWidths(width int) (int, int) {
	left := max(1, (width-1)/2)
	return left, max(1, width-1-left)
}

// renderForgeSplitRow draws one split row at width cells: the old line
// (its old number, red tint when removed) | the new line (its new number,
// green tint when added), a blank cell where a side has no line, both
// panned by pan; or the no-newline note under the side(s) it applies to.
func renderForgeSplitRow(file github.DiffFile, r *forgeDiffRows, row forgeDiffRow, width, pan int) string {
	lines := file.Hunks[row.hunk].Lines
	leftW, rightW := forgeSplitWidths(width)
	sep := lipgloss.NewStyle().Foreground(ColorBorder).Render("│")
	if row.kind == diffRowNoEOL {
		dim := lipgloss.NewStyle().Foreground(ColorTextDim)
		gutter := strings.Repeat(" ", 1+r.numWidth+1)
		note := func(i, w int) string {
			if i < 0 {
				return strings.Repeat(" ", w)
			}
			return forgeFitCells(gutter+dim.Render("  \\ no newline at end of file"), w)
		}
		return note(row.old, leftW) + sep + note(row.new, rightW)
	}
	return renderForgeSplitCell(lines, row.old, true, r.numWidth, leftW, pan) + sep +
		renderForgeSplitCell(lines, row.new, false, r.numWidth, rightW, pan)
}

// renderForgeSplitCell draws line i of a hunk on the old (old true) or new
// side in w cells; blank when i is -1.
func renderForgeSplitCell(lines []github.DiffLine, i int, old bool, numWidth, w, pan int) string {
	if i < 0 {
		return strings.Repeat(" ", w)
	}
	l := lines[i]
	addBg, delBg := forgeDiffTint()
	base := lipgloss.NewStyle()
	markColor := lipgloss.TerminalColor(ColorTextDim)
	mark := " "
	switch l.Kind {
	case github.LineDel:
		base, markColor, mark = base.Background(delBg), ColorRed, "-"
	case github.LineAdd:
		base, markColor, mark = base.Background(addBg), ColorGreen, "+"
	}
	n := l.New
	if old {
		n = l.Old
	}
	s := strconv.Itoa(n)
	gutter := " " + strings.Repeat(" ", max(0, numWidth-len(s))) + s + " "
	room := w - lipgloss.Width(gutter) - 2
	if room < 1 {
		return forgeFitCells(base.Foreground(ColorTextDim).Render(gutter), w)
	}
	return base.Foreground(ColorTextDim).Render(gutter) +
		base.Foreground(markColor).Render(mark+" ") +
		base.Foreground(ColorText).Render(forgePanCells(forgeDiffText(l.Text), room, pan))
}

// forgeCutCells trims s to at most n cells (ansi.Cut can keep a wide rune
// straddling its left edge).
func forgeCutCells(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "")
}

// forgeFilesPosition is what the footer's position counts: the list's
// rows while it shows full screen, else the selected file's diff rows.
func (h *Home) forgeFilesPosition() (total, offset, page int) {
	o := &h.forge.overlay
	l := h.forgeFilesLayoutNow()
	if d, ok := h.forgeFilesDiff(); ok && l.listFullWidth {
		return len(d.Files), o.files.listTop, l.listRows
	}
	return len(h.forgeFilesRows().rows), o.scroll[forgeOverlayFiles].offset, l.diffRows
}

// forgeFilesMaxOffset is the furthest the selected file's diff scrolls.
func (h *Home) forgeFilesMaxOffset() int {
	return max(0, len(h.forgeFilesRows().rows)-h.forgeFilesLayoutNow().diffRows)
}

// forgeFilesMaxPan is how far the selected file's text pans.
func (h *Home) forgeFilesMaxPan() int {
	l := h.forgeFilesLayoutNow()
	r := h.forgeFilesRows()
	room := l.diffW - (1 + 2*r.numWidth + 2) - 2
	if r.split {
		side, _ := forgeSplitWidths(l.diffW)
		room = side - (1 + r.numWidth + 1) - 2
	}
	return max(0, r.textWidth-room)
}

// clampForgeFiles keeps the selected file, the list's top and the diff's
// scroll and pan in range.
func (h *Home) clampForgeFiles() {
	o := &h.forge.overlay
	f := &o.files
	d, ok := h.forgeFilesDiff()
	if !ok {
		return
	}
	f.file = max(0, min(f.file, len(d.Files)-1))
	l := h.forgeFilesLayoutFor(d)
	rows := max(1, l.listRows)
	if f.file < f.listTop {
		f.listTop = f.file
	}
	if f.file >= f.listTop+rows {
		f.listTop = f.file - rows + 1
	}
	f.listTop = max(0, min(f.listTop, max(0, len(d.Files)-rows)))
	s := &o.scroll[forgeOverlayFiles]
	s.offset = max(0, min(s.offset, h.forgeFilesMaxOffset()))
	s.pan = max(0, min(s.pan, h.forgeFilesMaxPan()))
}

// selectForgeFile shows file i from the top of its diff.
func (h *Home) selectForgeFile(i int) {
	o := &h.forge.overlay
	o.files.file = i
	o.scroll[forgeOverlayFiles] = forgeOverlayScroll{}
	h.clampForgeFiles()
}

// forgeFilesListMode reports whether the movement keys move the file
// cursor: the list has the focus (wide) or shows full screen (narrow).
func (h *Home) forgeFilesListMode() bool {
	o := &h.forge.overlay
	if h.forgeFilesLayoutNow().narrow {
		return o.files.listFull
	}
	return o.files.listFocus
}

// moveForgeHunk is n (dir 1) and N (dir -1): the next or previous hunk
// header to the top of the diff, crossing into the next or previous file
// that shows hunks.
func (h *Home) moveForgeHunk(dir int) {
	o := &h.forge.overlay
	d, ok := h.forgeFilesDiff()
	if !ok {
		return
	}
	s := &o.scroll[forgeOverlayFiles]
	r := h.forgeFilesRows()
	maxOff := h.forgeFilesMaxOffset()
	if dir > 0 {
		for _, row := range r.hunks {
			if row > s.offset && min(row, maxOff) > s.offset {
				s.offset = min(row, maxOff)
				return
			}
		}
	} else {
		for i := len(r.hunks) - 1; i >= 0; i-- {
			if r.hunks[i] < s.offset {
				s.offset = r.hunks[i]
				return
			}
		}
	}
	for i := o.files.file + dir; i >= 0 && i < len(d.Files); i += dir {
		rows := forgeDiffRowsFor(d.Files[i], o.files.expanded[d.Files[i].Path()], h.forgeFilesSplit())
		if len(rows.hunks) == 0 {
			continue
		}
		h.selectForgeFile(i)
		if dir < 0 {
			s.offset = rows.hunks[len(rows.hunks)-1]
			h.clampForgeFiles()
		}
		return
	}
}

// toggleForgeFilesSplit is s: switch between unified and split, keeping
// the hunk at the top of the diff at the top.
func (h *Home) toggleForgeFilesSplit() {
	o := &h.forge.overlay
	s := &o.scroll[forgeOverlayFiles]
	hunk := -1
	if r := h.forgeFilesRows(); s.offset < len(r.rows) && r.rows[s.offset].kind != diffRowNote {
		hunk = r.rows[s.offset].hunk
	}
	was := h.forgeFilesSplit()
	o.files.split = !o.files.split
	if h.forgeFilesSplit() == was {
		return // too narrow either way: only the note changes
	}
	if r := h.forgeFilesRows(); hunk >= 0 && hunk < len(r.hunks) {
		s.offset = r.hunks[hunk]
	}
	h.clampForgeFiles()
}

// toggleForgeFileExpanded is enter on the diff: expand a collapsed file,
// or collapse it again.
func (h *Home) toggleForgeFileExpanded() {
	o := &h.forge.overlay
	d, ok := h.forgeFilesDiff()
	if !ok || o.files.file >= len(d.Files) {
		return
	}
	file := d.Files[o.files.file]
	if !file.StartsCollapsed() || file.Binary {
		return
	}
	expanded := make(map[string]bool, len(o.files.expanded)+1)
	for k, v := range o.files.expanded {
		expanded[k] = v
	}
	expanded[file.Path()] = !expanded[file.Path()]
	o.files.expanded = expanded
	o.scroll[forgeOverlayFiles] = forgeOverlayScroll{}
	h.clampForgeFiles()
}

// handleForgeFilesKey runs the Files tab's keys; handled is false for a
// key the overlay's own key space handles.
func (h *Home) handleForgeFilesKey(raw string) (bool, tea.Cmd) {
	o := &h.forge.overlay
	f := &o.files
	f.pagerErr = ""
	d, has := h.forgeFilesDiff()
	switch o.filesLookup[raw] {
	case hotkeyForgeNextHunk:
		h.moveForgeHunk(1)
		return true, nil
	case hotkeyForgePrevHunk:
		h.moveForgeHunk(-1)
		return true, nil
	case hotkeyForgeNextFile:
		if has && f.file+1 < len(d.Files) {
			h.selectForgeFile(f.file + 1)
		}
		return true, nil
	case hotkeyForgePrevFile:
		if has && f.file > 0 {
			h.selectForgeFile(f.file - 1)
		}
		return true, nil
	case hotkeyForgeFileList:
		if h.forgeFilesLayoutNow().narrow {
			f.listFull = !f.listFull
			h.clampForgeFiles()
		}
		return true, nil
	case hotkeyForgeFocus:
		if !h.forgeFilesLayoutNow().narrow {
			f.listFocus = !f.listFocus
		}
		return true, nil
	case hotkeyForgeFilesNew:
		cmd := h.forgeNew()
		if h.forge.sessionPick != nil || h.newDialog.IsVisible() {
			h.closeForgeOverlay()
		}
		return true, cmd
	case hotkeyForgeSplit:
		h.toggleForgeFilesSplit()
		return true, nil
	case hotkeyForgePagerFile:
		return true, h.forgeOpenPager(false)
	case hotkeyForgePagerPR:
		return true, h.forgeOpenPager(true)
	}
	listMode := h.forgeFilesListMode()
	switch forgeOverlayFixedKeys[raw] {
	case "expand":
		switch {
		case listMode && f.listFull:
			f.listFull = false
		case listMode:
			f.listFocus = false
		default:
			h.toggleForgeFileExpanded()
		}
		return true, nil
	case "close":
		if raw == "esc" && f.listFull {
			f.listFull = false
			return true, nil
		}
		return false, nil
	}
	if !listMode || !has {
		return false, nil
	}
	page := max(1, h.forgeFilesLayoutNow().listRows-1)
	switch forgeOverlayFixedKeys[raw] {
	case "down":
		h.selectForgeFile(min(f.file+1, len(d.Files)-1))
	case "up":
		h.selectForgeFile(max(f.file-1, 0))
	case "half down":
		h.selectForgeFile(min(f.file+max(1, page/2), len(d.Files)-1))
	case "half up":
		h.selectForgeFile(max(f.file-max(1, page/2), 0))
	case "page down":
		h.selectForgeFile(min(f.file+page, len(d.Files)-1))
	case "page up":
		h.selectForgeFile(max(f.file-page, 0))
	case "top":
		h.selectForgeFile(0)
	case "bottom":
		h.selectForgeFile(len(d.Files) - 1)
	default:
		return false, nil
	}
	return true, nil
}

// forgeFilesHints is the Files tab's part of the overlay's hint footer.
func (h *Home) forgeFilesHints(add func(key, label string)) {
	key := h.forgeKey
	l := h.forgeFilesLayoutNow()
	if l.narrow {
		add(key(hotkeyForgeFileList), "files")
	} else {
		add(key(hotkeyForgeFocus), "focus")
	}
	add(joinHotkeyLabels(key(hotkeyForgeNextHunk), key(hotkeyForgePrevHunk)), "hunk")
	add(joinHotkeyLabels(key(hotkeyForgeNextFile), key(hotkeyForgePrevFile)), "file")
	if h.forgeFilesRows().collapsed {
		add("enter", "expand")
	}
	add("h/l", "pan")
	if l.split {
		add(key(hotkeyForgeSplit), "unified")
	} else {
		add(key(hotkeyForgeSplit), "split")
	}
	add(joinHotkeyLabels(key(hotkeyForgePagerFile), key(hotkeyForgePagerPR)), "pager")
	add(h.forgeFilesNewKey(), "new session")
}

// forgeFilesNewKey is the key that starts a session on the Files tab
// (buildForgeFilesLookup).
func (h *Home) forgeFilesNewKey() string {
	files := buildForgeFilesLookup(h.forge.bindings)
	return h.forgeKey(forgeFilesNewAction(files, h.forge.bindings))
}
