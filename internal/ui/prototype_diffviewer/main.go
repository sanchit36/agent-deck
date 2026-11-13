// PROTOTYPE: throwaway, never merged. Lives on branch prototype/diff-viewer only.
//
// Answers the wayfinder ticket "Diff viewer prototype: how should a PR diff look
// and move?" (sanchit36/agent-deck#11). Four structurally different diff viewers
// over the same real diff, switchable with 1-4 or { }:
//
//	1 Stream       one unified scroll over every file, sticky file header
//	2 Files+diff   file list on the left, one file's diff on the right
//	3 Split        side-by-side old | new, falls back to unified under 120 cols
//	4 Hunk review  one hunk per screen, file progress dots, "viewed" marks
//
// Run from the repo root:
//
//	go run ./internal/ui/prototype_diffviewer                 # a real 25-file commit
//	go run ./internal/ui/prototype_diffviewer -rev v1.16.30..HEAD   # a huge diff
//	go run ./internal/ui/prototype_diffviewer -pr 3095 -R asheshgoplani/agent-deck
//	go run ./internal/ui/prototype_diffviewer -file some.diff
//
// No tests, no real error handling, keyword-only syntax colour (the real thing
// would need chroma, a go.mod change, or delta). Arrow keys are not used to
// switch variants because the viewer needs them for scrolling.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ---------- diff model ----------

type lineKind byte

const (
	ctx  lineKind = ' '
	add  lineKind = '+'
	del  lineKind = '-'
	meta lineKind = '\\'
)

type dline struct {
	kind     lineKind
	old, new int
	text     string
}

type hunk struct {
	header string
	lines  []dline
}

type dfile struct {
	oldPath, newPath, status string
	adds, dels               int
	hunks                    []hunk
	binary, generated        bool
	collapsed, viewed        bool
	raw                      []string
}

func (f *dfile) name() string {
	switch f.status {
	case "deleted":
		return f.oldPath
	case "renamed":
		return f.oldPath + " → " + f.newPath
	}
	return f.newPath
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

const largeFileLines = 400

func parseDiff(s string) []*dfile {
	var files []*dfile
	var f *dfile
	var h *hunk
	o, n := 0, 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, "diff --git ") {
			f = &dfile{status: "modified"}
			files = append(files, f)
			h = nil
			parts := strings.SplitN(strings.TrimPrefix(ln, "diff --git "), " b/", 2)
			f.oldPath = strings.TrimPrefix(parts[0], "a/")
			f.newPath = f.oldPath
			if len(parts) == 2 {
				f.newPath = parts[1]
			}
			f.raw = append(f.raw, ln)
			continue
		}
		if f == nil {
			continue
		}
		f.raw = append(f.raw, ln)
		switch {
		case strings.HasPrefix(ln, "@@"):
			f.hunks = append(f.hunks, hunk{header: ln})
			h = &f.hunks[len(f.hunks)-1]
			if m := hunkRe.FindStringSubmatch(ln); m != nil {
				o, _ = strconv.Atoi(m[1])
				n, _ = strconv.Atoi(m[2])
			}
		case h == nil:
			switch {
			case strings.HasPrefix(ln, "new file mode"):
				f.status = "added"
			case strings.HasPrefix(ln, "deleted file mode"):
				f.status = "deleted"
			case strings.HasPrefix(ln, "rename from"):
				f.status = "renamed"
			case strings.HasPrefix(ln, "Binary files"):
				f.binary = true
			}
		case ln == "":
		default:
			text := strings.ReplaceAll(ln[1:], "\t", "    ")
			switch ln[0] {
			case '+':
				h.lines = append(h.lines, dline{add, 0, n, text})
				n++
				f.adds++
			case '-':
				h.lines = append(h.lines, dline{del, o, 0, text})
				o++
				f.dels++
			case '\\':
				h.lines = append(h.lines, dline{meta, 0, 0, ln})
			default:
				h.lines = append(h.lines, dline{ctx, o, n, text})
				o++
				n++
			}
		}
	}
	for _, f := range files {
		p := f.newPath
		for _, g := range []string{"go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "/testdata/", "/goldens/", ".pb.go", "_gen.go", ".min.js", ".min.css"} {
			if strings.Contains(p, g) {
				f.generated = true
			}
		}
		f.collapsed = f.generated || f.adds+f.dels > largeFileLines
	}
	return files
}

// ---------- styles ----------

var (
	none      = lipgloss.NoColor{}
	cAddBg    = lipgloss.Color("#12301f")
	cDelBg    = lipgloss.Color("#3b1519")
	cAddFg    = lipgloss.Color("#56d364")
	cDelFg    = lipgloss.Color("#ff7b72")
	cDim      = lipgloss.Color("#6e7681")
	cHunkFg   = lipgloss.Color("#79c0ff")
	cHunkBg   = lipgloss.Color("#161f2e")
	cFileBg   = lipgloss.Color("#2d333b")
	cEmptyBg  = lipgloss.Color("#1c2128")
	cSel      = lipgloss.Color("#2d333b")
	cSelFocus = lipgloss.Color("#1f4a7a")
	cWarn     = lipgloss.Color("#d29922")
	cKw       = lipgloss.Color("#d2a8ff")
	cStr      = lipgloss.Color("#a5d6ff")
	cCom      = lipgloss.Color("#8b949e")
	cNum      = lipgloss.Color("#79c0ff")
	cFn       = lipgloss.Color("#ffa657")
	cRen      = lipgloss.Color("#58a6ff")
	cMod      = lipgloss.Color("#e3b341")
)

func st(fg, bg lipgloss.TerminalColor) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(fg).Background(bg)
}

var kw = map[string]bool{}

func init() {
	for _, k := range strings.Fields("break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var nil true false string int int64 bool error byte rune any fi then do done esac local echo export") {
		kw[k] = true
	}
}

func commentPrefix(path string) string {
	switch filepath.Ext(path) {
	case ".go", ".js", ".ts", ".tsx", ".c", ".rs", ".java", ".css":
		return "//"
	case ".sh", ".py", ".yml", ".yaml", ".toml", ".rb":
		return "#"
	}
	if strings.HasSuffix(path, "Makefile") {
		return "#"
	}
	return ""
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

func highlight(text, path string, on bool, fg, bg lipgloss.TerminalColor) string {
	base := st(fg, bg)
	ext := filepath.Ext(path)
	if !on || ext == ".md" || ext == ".txt" || ext == "" {
		return base.Render(text)
	}
	cp := commentPrefix(path)
	rs := []rune(text)
	var b strings.Builder
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case cp != "" && strings.HasPrefix(string(rs[i:]), cp):
			b.WriteString(st(cCom, bg).Italic(true).Render(string(rs[i:])))
			i = len(rs)
		case r == '"' || r == '`':
			j := i + 1
			for j < len(rs) && rs[j] != r {
				if rs[j] == '\\' && r != '`' {
					j++
				}
				j++
			}
			j = min(j+1, len(rs))
			b.WriteString(st(cStr, bg).Render(string(rs[i:j])))
			i = j
		case unicode.IsLetter(r) || r == '_':
			j := i
			for j < len(rs) && isWord(rs[j]) {
				j++
			}
			s := base
			if kw[string(rs[i:j])] {
				s = st(cKw, bg)
			} else if j < len(rs) && rs[j] == '(' {
				s = st(cFn, bg)
			}
			b.WriteString(s.Render(string(rs[i:j])))
			i = j
		case unicode.IsDigit(r):
			j := i
			for j < len(rs) && (isWord(rs[j]) || rs[j] == '.') {
				j++
			}
			b.WriteString(st(cNum, bg).Render(string(rs[i:j])))
			i = j
		default:
			j := i + 1
			for j < len(rs) && !isWord(rs[j]) && rs[j] != '"' && rs[j] != '`' && (cp == "" || !strings.HasPrefix(string(rs[j:]), cp)) {
				j++
			}
			b.WriteString(base.Render(string(rs[i:j])))
			i = j
		}
	}
	return b.String()
}

// fit pads or truncates a styled string to exactly w cells.
func fit(s string, w int, bg lipgloss.TerminalColor) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		return ansi.Truncate(s, w-1, "") + st(cDim, bg).Render("›")
	}
	return s + st(none, bg).Render(strings.Repeat(" ", w-sw))
}

func truncLeft(s string, w int) string {
	if w <= 1 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	rs := []rune(s)
	return "…" + string(rs[len(rs)-(w-1):])
}

func hcut(s string, n int) string {
	rs := []rune(s)
	if n >= len(rs) {
		return ""
	}
	return string(rs[n:])
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

func statusGlyph(s string, bg lipgloss.TerminalColor) string {
	switch s {
	case "added":
		return st(cAddFg, bg).Bold(true).Render("A")
	case "deleted":
		return st(cDelFg, bg).Bold(true).Render("D")
	case "renamed":
		return st(cRen, bg).Bold(true).Render("R")
	}
	return st(cMod, bg).Bold(true).Render("M")
}

func markFg(k lineKind) lipgloss.TerminalColor {
	switch k {
	case add:
		return cAddFg
	case del:
		return cDelFg
	}
	return cDim
}

// ---------- rows ----------

type rowKind int

const (
	rFile rowKind = iota
	rHunk
	rLine
	rSplit
	rNote
	rBlank
)

type row struct {
	kind       rowKind
	fi, hi, li int
	l, r       *dline
	note       string
}

func fileNote(f *dfile) string {
	switch {
	case f.binary:
		return "binary file, not shown"
	case f.collapsed:
		why := "large file"
		if f.generated {
			why = "generated file"
		}
		return fmt.Sprintf("▸ %s: %d changed lines hidden · enter expands · o opens it in the pager", why, f.adds+f.dels)
	case len(f.hunks) == 0:
		return "no content changes (mode or rename only)"
	}
	return ""
}

func (m *model) buildStream(split bool, only int, noFileHdr bool) []row {
	var rows []row
	for fi, f := range m.files {
		if only >= 0 && fi != only {
			continue
		}
		if !noFileHdr {
			rows = append(rows, row{kind: rFile, fi: fi, hi: -1})
		}
		if n := fileNote(f); n != "" {
			rows = append(rows, row{kind: rNote, fi: fi, hi: -1, note: n})
		} else {
			for hi := range f.hunks {
				rows = append(rows, row{kind: rHunk, fi: fi, hi: hi})
				if split {
					rows = append(rows, pairRows(fi, hi, &f.hunks[hi])...)
				} else {
					for li := range f.hunks[hi].lines {
						rows = append(rows, row{kind: rLine, fi: fi, hi: hi, li: li})
					}
				}
			}
		}
		if only < 0 {
			rows = append(rows, row{kind: rBlank, fi: fi, hi: -1})
		}
	}
	return rows
}

func pairRows(fi, hi int, h *hunk) []row {
	var out []row
	var dels, adds []*dline
	flush := func() {
		for i := 0; i < max(len(dels), len(adds)); i++ {
			r := row{kind: rSplit, fi: fi, hi: hi}
			if i < len(dels) {
				r.l = dels[i]
			}
			if i < len(adds) {
				r.r = adds[i]
			}
			out = append(out, r)
		}
		dels, adds = nil, nil
	}
	for i := range h.lines {
		l := &h.lines[i]
		switch l.kind {
		case del:
			if len(adds) > 0 {
				flush()
			}
			dels = append(dels, l)
		case add:
			adds = append(adds, l)
		case meta:
		default:
			flush()
			out = append(out, row{kind: rSplit, fi: fi, hi: hi, l: l, r: l})
		}
	}
	flush()
	return out
}

// ---------- model ----------

var variantNames = []string{"Stream", "Files + diff", "Split", "Hunk review"}

var modeNames = []string{"fg only", "tint", "tint+syntax"}

type loadedMsg struct{}
type pagerDoneMsg struct{ err error }

type model struct {
	files      []*dfile
	raw, src   string
	pager      string
	W, H, simW int
	variant    int
	mode       int
	hs         int
	help       bool
	loading    bool
	toast      string

	off [4]int // stream / split offsets

	sel       int // files+diff
	bOff      int
	focusList bool
	listOpen  bool

	stop int // hunk review
	dOff int
}

func (m *model) width() int {
	if m.simW > 0 && m.simW < m.W {
		return m.simW
	}
	return m.W
}

func (m *model) helpLines() []string {
	if !m.help {
		return nil
	}
	keys := "1-4 or { } variant · j/k ↑/↓ wheel scroll · ^d/^u half page · g/G top/bottom · n/N next/prev hunk · ]/[ next/prev file · enter expand/collapse file · h/l pan · s colour mode · -/=/0 narrower/wider/real width · o file→pager · O whole diff→pager · ? keys · q quit"
	switch m.variant {
	case 1:
		keys += " · tab focus list/diff · f file list (under 90 cols)"
	case 3:
		keys += " · space next hunk · b back · v toggle viewed"
	}
	var out []string
	line := ""
	for _, w := range strings.Fields(keys) {
		if ansi.StringWidth(line)+len(w)+1 > m.W-2 {
			out = append(out, line)
			line = ""
		}
		line += " " + w
	}
	return append(out, line)
}

func (m *model) bodyH() int { return m.H - 1 - len(m.helpLines()) }

func (m *model) totals() (a, d int) {
	for _, f := range m.files {
		a += f.adds
		d += f.dels
	}
	return
}

func (m *model) banner() string {
	a, d := m.totals()
	if a+d > 1500 || len(m.files) > 50 {
		return fmt.Sprintf("⚠ large diff: %d files, %d changed lines · big and generated files start collapsed", len(m.files), a+d)
	}
	return ""
}

func (m *model) Init() tea.Cmd {
	return tea.Tick(700*time.Millisecond, func(time.Time) tea.Msg { return loadedMsg{} })
}

// ---------- rendering ----------

func (m *model) colors(k lineKind) (fg, bg lipgloss.TerminalColor) {
	if k == meta {
		return cDim, none
	}
	if m.mode == 0 {
		switch k {
		case add:
			return cAddFg, none
		case del:
			return cDelFg, none
		}
		return none, none
	}
	switch k {
	case add:
		return none, cAddBg
	case del:
		return none, cDelBg
	}
	return none, none
}

func (m *model) unifiedLine(path string, l dline, w int, dimCtx bool) string {
	o, n := "", ""
	if l.kind != add && l.kind != meta {
		o = strconv.Itoa(l.old)
	}
	if l.kind != del && l.kind != meta {
		n = strconv.Itoa(l.new)
	}
	fg, bg := m.colors(l.kind)
	syn := m.mode == 2 && l.kind != meta
	if dimCtx && l.kind == ctx {
		fg, syn = cDim, false
	}
	gut := st(cDim, bg).Render(fmt.Sprintf("%5s %5s ", o, n))
	mark := st(markFg(l.kind), bg).Render(string(rune(l.kind)))
	return fit(gut+mark+st(none, bg).Render(" ")+highlight(hcut(l.text, m.hs), path, syn, fg, bg), w, bg)
}

func (m *model) half(path string, l *dline, w int, left bool) string {
	if l == nil {
		return st(cDim, cEmptyBg).Render(strings.Repeat(" ", w))
	}
	num := l.new
	if left {
		num = l.old
	}
	fg, bg := m.colors(l.kind)
	mark := " "
	if l.kind != ctx {
		mark = string(rune(l.kind))
	}
	gut := st(cDim, bg).Render(fmt.Sprintf("%5d ", num))
	return fit(gut+st(markFg(l.kind), bg).Render(mark)+st(none, bg).Render(" ")+highlight(hcut(l.text, m.hs), path, m.mode == 2, fg, bg), w, bg)
}

func (m *model) fileHeader(fi, w int, extra string) string {
	f := m.files[fi]
	bg := cFileBg
	right := st(cAddFg, bg).Render(fmt.Sprintf("+%d", f.adds)) + st(none, bg).Render(" ") +
		st(cDelFg, bg).Render(fmt.Sprintf("−%d", f.dels)) +
		st(cDim, bg).Render(fmt.Sprintf("  file %d/%d %s ", fi+1, len(m.files), extra))
	tags := ""
	if f.generated {
		tags += " [generated]"
	}
	if f.binary {
		tags += " [binary]"
	}
	if f.viewed {
		tags += " ✓ viewed"
	}
	rw := ansi.StringWidth(right)
	name := truncLeft(f.name(), w-rw-4-ansi.StringWidth(tags))
	left := st(none, bg).Render(" ") + statusGlyph(f.status, bg) + st(none, bg).Bold(true).Render(" "+name) + st(cDim, bg).Render(tags)
	gap := w - ansi.StringWidth(left) - rw
	if gap < 1 {
		return fit(left, w, bg)
	}
	return left + st(none, bg).Render(strings.Repeat(" ", gap)) + right
}

func (m *model) renderRow(r row, w int) string {
	f := m.files[r.fi]
	path := f.newPath
	switch r.kind {
	case rFile:
		return m.fileHeader(r.fi, w, "")
	case rHunk:
		return fit(st(cHunkFg, cHunkBg).Render(" "+f.hunks[r.hi].header), w, cHunkBg)
	case rLine:
		return m.unifiedLine(path, f.hunks[r.hi].lines[r.li], w, false)
	case rSplit:
		lw := (w - 1) / 2
		return m.half(path, r.l, lw, true) + st(cDim, none).Render("│") + m.half(path, r.r, w-1-lw, false)
	case rNote:
		return st(cDim, none).Italic(true).Render("      " + r.note)
	}
	return ""
}

func (m *model) rowsFrom(rows []row, off, w, h int) []string {
	off = clamp(off, 0, max(0, len(rows)-h))
	var out []string
	for i := off; i < len(rows) && len(out) < h; i++ {
		out = append(out, m.renderRow(rows[i], w))
	}
	return out
}

// stream + split

func (m *model) streamRows() ([]row, string) {
	split := m.variant == 2
	note := ""
	if split && m.width() < 120 {
		split = false
		note = fmt.Sprintf("split needs ≥120 cols (have %d), showing unified", m.width())
	}
	return m.buildStream(split, -1, false), note
}

func (m *model) streamTop(note string) int {
	if m.banner() != "" || note != "" {
		return 2
	}
	return 1
}

func (m *model) viewStream(w, h int) []string {
	rows, note := m.streamRows()
	var out []string
	if b := strings.TrimSpace(note + "  " + m.banner()); b != "" {
		out = append(out, st(cWarn, none).Render(" "+b))
	}
	if len(rows) == 0 {
		return append(out, " no changes")
	}
	avail := h - len(out) - 1
	off := clamp(m.off[m.variant], 0, max(0, len(rows)-avail))
	top := rows[off]
	extra := ""
	if top.hi >= 0 {
		extra = fmt.Sprintf("· hunk %d/%d", top.hi+1, len(m.files[top.fi].hunks))
	}
	extra += fmt.Sprintf(" · %d%%", 100*(off+avail)/max(len(rows), 1)%101)
	out = append(out, m.fileHeader(top.fi, w, extra))
	start := off
	if top.kind == rFile {
		start++
	}
	for i := start; i < len(rows) && len(out) < h; i++ {
		out = append(out, m.renderRow(rows[i], w))
	}
	return out
}

// files + diff

func (m *model) fileList(w, h int, focused bool) []string {
	a, d := m.totals()
	out := []string{fit(lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf(" Files %d  ", len(m.files)))+
		st(cAddFg, none).Render(fmt.Sprintf("+%d ", a))+st(cDelFg, none).Render(fmt.Sprintf("−%d", d)), w, none)}
	avail := h - 1
	start := clamp(m.sel-avail/2, 0, max(0, len(m.files)-avail))
	for i := start; i < len(m.files) && len(out) < h; i++ {
		f := m.files[i]
		var bg lipgloss.TerminalColor = none
		if i == m.sel {
			bg = cSel
			if focused {
				bg = cSelFocus
			}
		}
		statsTxt := fmt.Sprintf(" +%d −%d ", f.adds, f.dels)
		stats := st(none, bg).Render(" ") + st(cAddFg, bg).Render(fmt.Sprintf("+%d", f.adds)) + st(none, bg).Render(" ") + st(cDelFg, bg).Render(fmt.Sprintf("−%d", f.dels)) + st(none, bg).Render(" ")
		mark := " "
		if f.viewed {
			mark = "✓"
		}
		nameW := w - 3 - ansi.StringWidth(statsTxt)
		dir, base := filepath.Split(f.newPath)
		var name string
		baseFg := lipgloss.TerminalColor(none)
		if f.generated {
			baseFg = cDim
		}
		if ansi.StringWidth(base) >= nameW {
			name = st(baseFg, bg).Render(truncLeft(base, nameW))
		} else {
			name = st(cDim, bg).Render(truncLeft(dir, nameW-ansi.StringWidth(base))) + st(baseFg, bg).Render(base)
		}
		left := st(cAddFg, bg).Render(mark) + statusGlyph(f.status, bg) + st(none, bg).Render(" ") + name
		gap := max(0, w-ansi.StringWidth(left)-ansi.StringWidth(statsTxt))
		out = append(out, fit(left+st(none, bg).Render(strings.Repeat(" ", gap))+stats, w, bg))
	}
	return out
}

func (m *model) filesNarrow() bool { return m.width() < 90 }

func (m *model) viewFiles(w, h int) []string {
	if m.filesNarrow() && m.listOpen {
		return m.fileList(w, h, true)
	}
	rows := m.buildStream(false, m.sel, true)
	if m.filesNarrow() {
		out := []string{m.fileHeader(m.sel, w, "· [ ] file · f list")}
		return append(out, m.rowsFrom(rows, m.bOff, w, h-1)...)
	}
	lw := clamp(w*30/100, 26, 44)
	dw := w - lw - 1
	left := m.fileList(lw, h, m.focusList)
	right := append([]string{m.fileHeader(m.sel, dw, "")}, m.rowsFrom(rows, m.bOff, dw, h-1)...)
	sep := st(cDim, none).Render("│")
	out := make([]string, h)
	for i := range out {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = fit(l, lw, none) + sep + fit(r, dw, none)
	}
	return out
}

// hunk review

type stop struct{ fi, hi int }

func (m *model) stops() []stop {
	var s []stop
	for fi, f := range m.files {
		if fileNote(f) != "" {
			s = append(s, stop{fi, -1})
			continue
		}
		for hi := range f.hunks {
			s = append(s, stop{fi, hi})
		}
	}
	return s
}

func (m *model) progress(w, cur int) string {
	viewed := 0
	for _, f := range m.files {
		if f.viewed {
			viewed++
		}
	}
	if len(m.files)*2+20 > w {
		return st(cDim, none).Render(fmt.Sprintf(" file %d/%d · %d viewed", cur+1, len(m.files), viewed))
	}
	var b strings.Builder
	b.WriteString(" ")
	for i, f := range m.files {
		switch {
		case i == cur:
			b.WriteString(lipgloss.NewStyle().Bold(true).Render("◉ "))
		case f.viewed:
			b.WriteString(st(cAddFg, none).Render("● "))
		default:
			b.WriteString(st(cDim, none).Render("○ "))
		}
	}
	b.WriteString(st(cDim, none).Render(fmt.Sprintf(" %d/%d viewed", viewed, len(m.files))))
	return b.String()
}

func (m *model) viewHunk(w, h int) []string {
	stops := m.stops()
	si := clamp(m.stop, 0, len(stops)-1)
	s := stops[si]
	f := m.files[s.fi]
	out := []string{m.progress(w, s.fi)}
	extra := ""
	if s.hi >= 0 {
		extra = fmt.Sprintf("· hunk %d/%d", s.hi+1, len(f.hunks))
	}
	out = append(out, m.fileHeader(s.fi, w, extra))
	footer := fmt.Sprintf(" stop %d/%d · space next · b back · ]/[ file · v viewed", si+1, len(stops))
	if s.hi < 0 {
		out = append(out, "", st(cDim, none).Italic(true).Render("      "+fileNote(f)))
	} else {
		hk := f.hunks[s.hi]
		out = append(out, fit(st(cHunkFg, cHunkBg).Render(" "+hk.header), w, cHunkBg))
		avail := h - len(out) - 1
		off := clamp(m.dOff, 0, max(0, len(hk.lines)-avail))
		for i := off; i < len(hk.lines) && i-off < avail; i++ {
			out = append(out, m.unifiedLine(f.newPath, hk.lines[i], w, true))
		}
		if rest := len(hk.lines) - off - avail; rest > 0 {
			footer = fmt.Sprintf(" ↓ %d more lines in this hunk (j) ·", rest) + footer
		}
	}
	for len(out) < h-1 {
		out = append(out, "")
	}
	return append(out, st(cDim, none).Render(footer))
}

// frame

func (m *model) bar() string {
	bg := cWarn
	s := st(lipgloss.Color("#000000"), bg).Bold(true)
	wtxt := fmt.Sprintf("%d cols", m.W)
	if m.width() < m.W {
		wtxt = fmt.Sprintf("sim %d of %d cols", m.width(), m.W)
	}
	txt := fmt.Sprintf(" PROTOTYPE  { %d/4 %s }  │ %s (-/=/0) │ s: %s │ o/O → %s │ ? keys │ q quit ",
		m.variant+1, variantNames[m.variant], wtxt, modeNames[m.mode], m.pager)
	if m.W < 110 {
		txt = fmt.Sprintf(" PROTO { %d/4 %s } │ %s │ s:%s │ ? keys ", m.variant+1, variantNames[m.variant], wtxt, modeNames[m.mode])
	}
	if m.toast != "" {
		txt = " " + m.toast + " │" + txt
	}
	return fit(s.Render(txt), m.W, bg)
}

func (m *model) View() string {
	if m.W == 0 {
		return ""
	}
	w, h := m.width(), m.bodyH()
	var lines []string
	switch {
	case m.loading:
		lines = append(make([]string, h/2), st(cDim, none).Render(fmt.Sprintf("   ⠋ loading diff (%s)…", m.src)))
	case len(m.files) == 0:
		lines = []string{" no changes in " + m.src}
	case m.variant == 0, m.variant == 2:
		lines = m.viewStream(w, h)
	case m.variant == 1:
		lines = m.viewFiles(w, h)
	default:
		lines = m.viewHunk(w, h)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	lines = lines[:h]
	for i := range lines {
		lines[i] = fit(lines[i], w, none)
		if m.W > w {
			lines[i] += st(cEmptyBg, none).Render(strings.Repeat("░", m.W-w))
		}
	}
	lines = append(lines, m.bar())
	for _, l := range m.helpLines() {
		lines = append(lines, fit(st(cWarn, none).Render(l), m.W, none))
	}
	return strings.Join(lines, "\n")
}

// ---------- update ----------

func (m *model) curFile() int {
	switch m.variant {
	case 1:
		return m.sel
	case 3:
		stops := m.stops()
		return stops[clamp(m.stop, 0, len(stops)-1)].fi
	}
	rows, _ := m.streamRows()
	if len(rows) == 0 {
		return 0
	}
	return rows[clamp(m.off[m.variant], 0, len(rows)-1)].fi
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (m *model) openPager(all bool) tea.Cmd {
	content := m.raw
	if !all && len(m.files) > 0 {
		content = strings.Join(m.files[m.curFile()].raw, "\n")
	}
	tmp, err := os.CreateTemp("", "proto-diff-*.diff")
	if err != nil {
		m.toast = err.Error()
		return nil
	}
	_, _ = tmp.WriteString(content)
	_ = tmp.Close()
	c := exec.Command("sh", "-c", m.pager+" < "+shq(tmp.Name())) //nolint:gosec // prototype
	return tea.ExecProcess(c, func(err error) tea.Msg {
		_ = os.Remove(tmp.Name())
		return pagerDoneMsg{err}
	})
}

func (m *model) scroll(d int) {
	switch m.variant {
	case 1:
		if m.listMode() {
			m.sel = clamp(m.sel+d, 0, len(m.files)-1)
			m.bOff = 0
		} else {
			m.bOff = clamp(m.bOff+d, 0, max(0, len(m.buildStream(false, m.sel, true))-(m.bodyH()-1)))
		}
	case 3:
		m.dOff = max(0, m.dOff+d)
	default:
		rows, note := m.streamRows()
		m.off[m.variant] = clamp(m.off[m.variant]+d, 0, max(0, len(rows)-(m.bodyH()-m.streamTop(note))))
	}
}

func (m *model) listMode() bool {
	if m.filesNarrow() {
		return m.listOpen
	}
	return m.focusList
}

func findRow(rows []row, from, dir int, k rowKind) int {
	for i := from + dir; i >= 0 && i < len(rows); i += dir {
		if rows[i].kind == k {
			return i
		}
	}
	return -1
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.W, m.H = msg.Width, msg.Height
		return m, nil
	case loadedMsg:
		m.loading = false
		return m, nil
	case pagerDoneMsg:
		m.toast = ""
		if msg.err != nil {
			m.toast = "pager: " + msg.err.Error()
		}
		return m, nil
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scroll(-3)
		case tea.MouseButtonWheelDown:
			m.scroll(3)
		}
		return m, nil
	case tea.KeyMsg:
		return m, m.key(msg.String())
	}
	return m, nil
}

func (m *model) key(k string) tea.Cmd {
	m.toast = ""
	half := max(1, m.bodyH()/2)
	switch k {
	case "q", "ctrl+c":
		return tea.Quit
	case "esc":
		switch {
		case m.listOpen:
			m.listOpen = false
		case m.help:
			m.help = false
		default:
			return tea.Quit
		}
		return nil
	case "1", "2", "3", "4":
		m.variant = int(k[0] - '1')
		return nil
	case "}":
		m.variant = (m.variant + 1) % 4
		return nil
	case "{":
		m.variant = (m.variant + 3) % 4
		return nil
	case "?":
		m.help = !m.help
		return nil
	case "s":
		m.mode = (m.mode + 1) % 3
		return nil
	case "-":
		m.simW = max(40, m.width()-10)
		return nil
	case "=", "+":
		m.simW = m.width() + 10
		if m.simW >= m.W {
			m.simW = 0
		}
		return nil
	case "0":
		m.simW = 0
		return nil
	case "h", "left":
		m.hs = max(0, m.hs-8)
		return nil
	case "l", "right":
		m.hs += 8
		return nil
	case "o":
		return m.openPager(false)
	case "O":
		return m.openPager(true)
	case "j", "down":
		m.scroll(1)
		return nil
	case "k", "up":
		m.scroll(-1)
		return nil
	case "ctrl+d", "pgdown":
		m.scroll(half)
		return nil
	case "ctrl+u", "pgup":
		m.scroll(-half)
		return nil
	}
	switch m.variant {
	case 1:
		m.keyFiles(k)
	case 3:
		m.keyHunk(k)
	default:
		m.keyStream(k)
	}
	return nil
}

func (m *model) keyStream(k string) {
	rows, _ := m.streamRows()
	off := m.off[m.variant]
	switch k {
	case "g", "home":
		off = 0
	case "G", "end":
		off = len(rows)
	case " ", "space":
		m.scroll(m.bodyH() - 2)
		return
	case "n":
		if i := findRow(rows, off, 1, rHunk); i >= 0 {
			off = i
		}
	case "N":
		if i := findRow(rows, off, -1, rHunk); i >= 0 {
			off = i
		}
	case "]":
		if i := findRow(rows, off, 1, rFile); i >= 0 {
			off = i
		}
	case "[":
		if i := findRow(rows, off, -1, rFile); i >= 0 {
			off = i
		}
	case "enter":
		if len(rows) == 0 {
			return
		}
		fi := rows[clamp(off, 0, len(rows)-1)].fi
		m.files[fi].collapsed = !m.files[fi].collapsed
		rows, _ = m.streamRows()
		for i, r := range rows {
			if r.kind == rFile && r.fi == fi {
				off = i
				break
			}
		}
	}
	m.off[m.variant] = off
	m.scroll(0)
}

func (m *model) keyFiles(k string) {
	rows := m.buildStream(false, m.sel, true)
	switch k {
	case "tab":
		m.focusList = !m.focusList
	case "f":
		m.listOpen = !m.listOpen
	case "]":
		m.sel = min(m.sel+1, len(m.files)-1)
		m.bOff = 0
	case "[":
		m.sel = max(m.sel-1, 0)
		m.bOff = 0
	case "g", "home":
		if m.listMode() {
			m.sel = 0
		}
		m.bOff = 0
	case "G", "end":
		if m.listMode() {
			m.sel = len(m.files) - 1
		} else {
			m.bOff = len(rows)
		}
	case " ", "space":
		m.scroll(m.bodyH() - 2)
	case "n":
		if i := findRow(rows, m.bOff, 1, rHunk); i >= 0 {
			m.bOff = i
		} else if m.sel < len(m.files)-1 {
			m.sel++
			m.bOff = 0
		}
	case "N":
		if i := findRow(rows, m.bOff, -1, rHunk); i >= 0 && m.bOff > 0 {
			m.bOff = i
		} else if m.sel > 0 {
			m.sel--
			m.bOff = 0
		}
	case "enter":
		switch {
		case m.filesNarrow() && m.listOpen:
			m.listOpen = false
		case !m.filesNarrow() && m.focusList:
			m.focusList = false
		default:
			m.files[m.sel].collapsed = !m.files[m.sel].collapsed
		}
	}
	m.scroll(0)
}

func (m *model) keyHunk(k string) {
	stops := m.stops()
	si := clamp(m.stop, 0, len(stops)-1)
	cur := stops[si]
	switch k {
	case "n", " ", "space":
		if si+1 >= len(stops) || stops[si+1].fi != cur.fi {
			m.files[cur.fi].viewed = true
		}
		si++
	case "N", "b":
		si--
	case "]":
		for si < len(stops) && stops[si].fi == cur.fi {
			si++
		}
	case "[":
		target := cur.fi
		if cur.hi <= 0 {
			target = cur.fi - 1
		}
		for i, s := range stops {
			if s.fi == target {
				si = i
				break
			}
		}
	case "g", "home":
		si = 0
	case "G", "end":
		si = len(stops) - 1
	case "v":
		m.files[cur.fi].viewed = !m.files[cur.fi].viewed
	case "enter":
		m.files[cur.fi].collapsed = !m.files[cur.fi].collapsed
		for i, s := range m.stops() {
			if s.fi == cur.fi {
				si = i
				break
			}
		}
	}
	if si != m.stop {
		m.dOff = 0
	}
	m.stop = clamp(si, 0, len(m.stops())-1)
}

// ---------- main ----------

func main() {
	rev := flag.String("rev", "08635248", "commit (git show) or A..B range (git diff)")
	pr := flag.Int("pr", 0, "PR number, fetched with gh pr diff")
	repo := flag.String("R", "asheshgoplani/agent-deck", "repo for -pr")
	file := flag.String("file", "", "read a unified diff from this file")
	flag.Parse()

	var out []byte
	var err error
	src := ""
	switch {
	case *file != "":
		src = *file
		out, err = os.ReadFile(*file)
	case *pr > 0:
		src = fmt.Sprintf("gh pr diff %d -R %s", *pr, *repo)
		out, err = exec.Command("gh", "pr", "diff", strconv.Itoa(*pr), "-R", *repo, "--color=never").Output() //nolint:gosec // prototype
	case strings.Contains(*rev, ".."):
		src = "git diff " + *rev
		out, err = exec.Command("git", "--no-pager", "diff", "--no-color", "-M", *rev).Output() //nolint:gosec // prototype
	default:
		src = "git show " + *rev
		out, err = exec.Command("git", "--no-pager", "show", "--no-color", "--format=", "-M", *rev).Output() //nolint:gosec // prototype
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "loading diff:", err)
		os.Exit(1)
	}

	pager := os.Getenv("AGENTDECK_DIFF_PAGER")
	if pager == "" {
		if _, err := exec.LookPath("delta"); err == nil {
			pager = "delta --paging=always"
		} else if p := os.Getenv("PAGER"); p != "" {
			pager = p
		} else {
			pager = "less -R"
		}
	}

	m := &model{files: parseDiff(string(out)), raw: string(out), src: src, pager: pager, mode: 2, loading: true}
	if _, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
