// PROTOTYPE — throwaway. Do not merge to main.
//
// Question (wayfinder ticket "Layout prototype: Sessions | Issues | PRs tabs with
// contextual detail", sanchit36/agent-deck#10): how should the left-pane tab
// switcher, Issue/PR list rows, status glyphs and the right-pane detail look, in
// classic and embedded layouts and at narrow widths?
//
// Three structurally different variants, all on mock data, no network, no state:
//
//	A  Title-row tabs   tabs in the panel title row, one-line rows, detail as one card
//	B  Chip tabs        tabs as chips in the filter bar, two-line rows, detail with section sub-tabs
//	C  Rail + table     vertical tab rail, dense columned table, narrow key/value detail + timeline
//
// Run:  go run ./internal/ui/prototype_forge_layout
//
// Keys:  , .  variant   L  classic/embedded   W  width preset   [ ]  left tab
//
//	j/k  move   enter  detail (single column)   esc back   { }  detail section (B)
//	?    glyph legend   q  quit
package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ---------- palette (agent-deck dark theme) ----------

var (
	cBg      = lipgloss.Color("#1a1b26")
	cSurface = lipgloss.Color("#24283b")
	cBorder  = lipgloss.Color("#414868")
	cText    = lipgloss.Color("#c0caf5")
	cDim     = lipgloss.Color("#787fa0")
	cAccent  = lipgloss.Color("#7aa2f7")
	cPurple  = lipgloss.Color("#bb9af7")
	cCyan    = lipgloss.Color("#7dcfff")
	cGreen   = lipgloss.Color("#9ece6a")
	cYellow  = lipgloss.Color("#e0af68")
	cOrange  = lipgloss.Color("#ff9e64")
	cRed     = lipgloss.Color("#f7768e")

	sText   = lipgloss.NewStyle().Foreground(cText)
	sDim    = lipgloss.NewStyle().Foreground(cDim)
	sBold   = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sTitle  = lipgloss.NewStyle().Foreground(cCyan).Bold(true)
	sBorder = lipgloss.NewStyle().Foreground(cBorder)
	sGreen  = lipgloss.NewStyle().Foreground(cGreen)
	sRed    = lipgloss.NewStyle().Foreground(cRed)
	sYellow = lipgloss.NewStyle().Foreground(cYellow)
	sPurple = lipgloss.NewStyle().Foreground(cPurple)
	sOrange = lipgloss.NewStyle().Foreground(cOrange)
	sAccent = lipgloss.NewStyle().Foreground(cAccent)
	sCyan   = lipgloss.NewStyle().Foreground(cCyan)
	sSel    = lipgloss.NewStyle().Background(cSurface).Foreground(cText).Bold(true)
	sTabOn  = lipgloss.NewStyle().Foreground(cBg).Background(cCyan).Bold(true)
	sTabOff = lipgloss.NewStyle().Foreground(cDim)
	sChipOn = lipgloss.NewStyle().Foreground(cBg).Background(cAccent).Bold(true)
	sChip   = lipgloss.NewStyle().Foreground(cText).Background(cSurface)
	sLabel  = lipgloss.NewStyle().Foreground(cPurple)
)

// ---------- mock data ----------

type comment struct{ who, age, text string }

type issue struct {
	n                  int
	title, author, age string
	open               bool
	labels             []string
	assignee           string
	comments           []comment
	sessions           []string
	body               []string
}

type pr struct {
	n                      int
	title, author, age     string
	state                  string // open draft merged closed
	head, base             string
	checks                 string // pass fail pending none
	checksText             string
	review                 string // approved changes required none
	merge                  string // clean conflict behind blocked
	add, del, files, ncomm int
	labels                 []string
	sessions               []string
	body                   []string
	timeline               []comment
}

type sess struct {
	title, status, tool, badge string
	group                      bool
}

var sessions = []sess{
	{title: "Agent-Deck", group: true},
	{title: "forge-tabs", status: "running", tool: "claude", badge: "#12 PR#3101"},
	{title: "fix-status-flicker", status: "waiting", tool: "claude", badge: "#142"},
	{title: "review-3098", status: "idle", tool: "codex", badge: "PR#3098"},
	{title: "docs-glossary", status: "idle", tool: "claude"},
	{title: "Scratch", group: true},
	{title: "bench-sqlite", status: "error", tool: "gemini"},
}

var issues = []issue{
	{n: 142, title: "Status flickers between waiting and running on resize", author: "mkrause", age: "2d", open: true, labels: []string{"bug", "tui"}, assignee: "sanchit36",
		comments: []comment{{"mkrause", "2d", "Repro: drag the split with < > while a session is waiting."}, {"sanchit36", "1d", "Looks like the status cache is keyed on width. Taking it."}},
		sessions: []string{"fix-status-flicker"},
		body:     []string{"When the terminal is resized, waiting sessions briefly show", "as running for one frame.", "", "Steps:", "1. Start a claude session and let it wait", "2. Resize the terminal", "", "Expected: status stays `waiting`."}},
	{n: 12, title: "GitHub integration: Issues and PRs tabs in the TUI", author: "sanchit36", age: "3w", open: true, labels: []string{"feature", "forge"}, assignee: "sanchit36",
		comments: []comment{{"ashesh", "3w", "+1, would replace my gh dash pane."}}, sessions: []string{"forge-tabs"},
		body: []string{"Show Issues and PRs for the group's Project next to", "Sessions, with detail in the preview pane."}},
	{n: 139, title: "Conductor drops messages when the inbox is full", author: "lfeng", age: "4d", open: true, labels: []string{"bug", "conductor", "p1"},
		comments: []comment{{"lfeng", "4d", "Seen with 40+ children."}, {"ashesh", "3d", "Dead-letter panel shows them."}, {"lfeng", "2d", "Confirmed on v1.16.38."}},
		body:     []string{"With more than 32 queued records the oldest are dropped", "silently instead of going to dead-letter."}},
	{n: 137, title: "Support Zellij as an alternative to tmux", author: "oszi", age: "1w", open: true, labels: []string{"enhancement"},
		body: []string{"Would love Zellij support."}},
	{n: 131, title: "Docs: llms-full.txt is out of date on hotkeys", author: "rpatel", age: "2w", open: true, labels: []string{"docs", "good first issue"},
		body: []string{"The hotkey table still lists the old fork key."}},
	{n: 128, title: "Web UI: session list does not refresh after rename with a very long title that wraps", author: "jdoe", age: "3w", open: true, labels: []string{"web"},
		body: []string{"Rename a session; the web list keeps the old title."}},
	{n: 120, title: "Crash on startup with an empty config.toml", author: "kim", age: "1mo", open: false, labels: []string{"bug"},
		body: []string{"Fixed in #3071."}},
}

var prs = []pr{
	{n: 3101, title: "feat(ui): Issues and PRs tabs in the left pane", author: "sanchit36", age: "3h", state: "open", head: "feature/forge-tabs", base: "main",
		checks: "pass", checksText: "12/12 passed", review: "approved", merge: "clean", add: 812, del: 64, files: 14, ncomm: 3, labels: []string{"feature"},
		sessions: []string{"forge-tabs"},
		body:     []string{"Adds the tab strip, per-tab list state and the", "Issue/PR detail branch in renderPreviewPane.", "", "Closes #12"},
		timeline: []comment{{"ashesh", "1h", "approved"}, {"ci", "2h", "12 checks passed"}, {"sanchit36", "3h", "pushed 4 commits"}}},
	{n: 3098, title: "fix(tmux): keep pane size when detaching from an embedded session", author: "lfeng", age: "1d", state: "open", head: "fix/pane-size", base: "main",
		checks: "fail", checksText: "1 failing · 11 passed", review: "changes", merge: "behind", add: 41, del: 9, files: 2, ncomm: 6, labels: []string{"bug", "tmux"},
		sessions: []string{"review-3098"},
		body:     []string{"Restores the pane size after detach."},
		timeline: []comment{{"ashesh", "5h", "requested changes"}, {"ci", "6h", "Full test suite failed"}, {"lfeng", "1d", "opened"}}},
	{n: 3095, title: "feat(web): show conductor inbox in the web UI", author: "mkrause", age: "2d", state: "draft", head: "feat/web-inbox", base: "main",
		checks: "pending", checksText: "3 running · 9 passed", review: "none", merge: "clean", add: 230, del: 12, files: 6, ncomm: 0,
		body:     []string{"WIP."},
		timeline: []comment{{"mkrause", "2d", "opened as draft"}}},
	{n: 3093, title: "docs(changelog): Unreleased since v1.16.38", author: "ashesh", age: "3d", state: "merged", head: "docs/changelog", base: "main",
		checks: "pass", checksText: "4/4 passed", review: "approved", merge: "clean", add: 33, del: 2, files: 2, ncomm: 1, labels: []string{"docs"},
		body:     []string{"Moves Unreleased."},
		timeline: []comment{{"ashesh", "3d", "merged"}}},
	{n: 3090, title: "refactor(session): split userconfig.go", author: "oszi", age: "5d", state: "open", head: "refactor/userconfig", base: "main",
		checks: "pass", checksText: "12/12 passed", review: "required", merge: "conflict", add: 1204, del: 1180, files: 9, ncomm: 2,
		body:     []string{"Pure move, no behaviour change."},
		timeline: []comment{{"oszi", "5d", "opened"}}},
	{n: 3087, title: "chore: bump golangci-lint", author: "bot", age: "1w", state: "closed", head: "deps/lint", base: "main",
		checks: "none", checksText: "no checks", review: "none", merge: "blocked", add: 1, del: 1, files: 1,
		body:     []string{"Superseded."},
		timeline: []comment{{"ashesh", "1w", "closed"}}},
}

// ---------- model ----------

var widths = []int{140, 100, 72, 46}

type variant struct{ key, name string }

var variants = []variant{{"A", "Title-row tabs"}, {"B", "Chip tabs + two-line rows"}, {"C", "Rail + dense table"}}

type model struct {
	v, wi, tab int
	embedded   bool
	cur        [3]int
	detailOpen bool
	section    int
	legend     bool
	termW      int
	termH      int
}

func (m model) Init() tea.Cmd { return nil }

func (m model) W() int { return widths[m.wi] }
func (m model) H() int {
	h := m.termH - 4
	if h > 36 {
		h = 36
	}
	if h < 16 {
		h = 16
	}
	return h
}

func (m model) layout() string {
	w := m.W()
	if m.embedded {
		if w < 80 {
			return "stacked"
		}
		return "dual"
	}
	switch {
	case w < 50:
		return "single"
	case w < 80:
		return "stacked"
	}
	return "dual"
}

func (m model) count(tab int) int {
	switch tab {
	case 0:
		return len(sessions)
	case 1:
		return len(issues)
	}
	return len(prs)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW, m.termH = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case ".", "right":
			m.v = (m.v + 1) % len(variants)
		case ",", "left":
			m.v = (m.v + len(variants) - 1) % len(variants)
		case "L":
			m.embedded = !m.embedded
		case "W":
			m.wi = (m.wi + 1) % len(widths)
		case "]":
			m.tab = (m.tab + 1) % 3
			m.detailOpen = false
		case "[":
			m.tab = (m.tab + 2) % 3
			m.detailOpen = false
		case "j", "down":
			if m.cur[m.tab] < m.count(m.tab)-1 {
				m.cur[m.tab]++
			}
		case "k", "up":
			if m.cur[m.tab] > 0 {
				m.cur[m.tab]--
			}
		case "enter":
			if m.tab != 0 {
				m.detailOpen = true
			}
		case "esc":
			m.detailOpen = false
			m.legend = false
		case "}":
			m.section = (m.section + 1) % 5
		case "{":
			m.section = (m.section + 4) % 5
		case "?":
			m.legend = !m.legend
		}
	}
	return m, nil
}

// ---------- layout helpers (exact width/height, display-width safe) ----------

func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

func block(lines []string, w, h int) []string {
	out := make([]string, h)
	for i := 0; i < h; i++ {
		if i < len(lines) {
			out[i] = fit(lines[i], w)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return out
}

func hjoin(left []string, sep string, right []string) []string {
	out := make([]string, len(left))
	for i := range left {
		r := ""
		if i < len(right) {
			r = right[i]
		}
		out[i] = left[i] + sep + r
	}
	return out
}

func rightAlign(left, right string, w int) string {
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	if lw+rw+1 > w {
		left = ansi.Truncate(left, max(0, w-rw-1), "…")
		lw = lipgloss.Width(left)
	}
	return left + strings.Repeat(" ", max(1, w-lw-rw)) + right
}

func panelTitle(title string, w int) []string {
	return []string{fit(title, w), sBorder.Render(strings.Repeat("─", w))}
}

func wrap(s string, w int) []string {
	if w < 4 {
		return []string{s}
	}
	return strings.Split(ansi.Wordwrap(s, w, ""), "\n")
}

// ---------- glyphs ----------

func issueGlyph(i issue) string {
	if i.open {
		return sGreen.Render("○")
	}
	return sPurple.Render("✓")
}

func prGlyph(p pr) string {
	switch p.state {
	case "open":
		return sGreen.Render("●")
	case "draft":
		return sDim.Render("◌")
	case "merged":
		return sPurple.Render("◆")
	}
	return sRed.Render("✕")
}

func checksGlyph(c string) string {
	switch c {
	case "pass":
		return sGreen.Render("✓")
	case "fail":
		return sRed.Render("✗")
	case "pending":
		return sYellow.Render("◷")
	}
	return sDim.Render("·")
}

func reviewGlyph(r string) string {
	switch r {
	case "approved":
		return sGreen.Render("✔")
	case "changes":
		return sRed.Render("±")
	case "required":
		return sYellow.Render("?")
	}
	return sDim.Render("·")
}

func mergeGlyph(s string) string {
	switch s {
	case "clean":
		return sGreen.Render("⇢")
	case "conflict":
		return sRed.Render("≠")
	case "behind":
		return sYellow.Render("↓")
	}
	return sDim.Render("⊘")
}

func stateBadge(p pr) string {
	switch p.state {
	case "open":
		return lipgloss.NewStyle().Background(cGreen).Foreground(cBg).Bold(true).Render(" OPEN ")
	case "draft":
		return lipgloss.NewStyle().Background(cBorder).Foreground(cText).Bold(true).Render(" DRAFT ")
	case "merged":
		return lipgloss.NewStyle().Background(cPurple).Foreground(cBg).Bold(true).Render(" MERGED ")
	}
	return lipgloss.NewStyle().Background(cRed).Foreground(cBg).Bold(true).Render(" CLOSED ")
}

func issueBadge(i issue) string {
	if i.open {
		return lipgloss.NewStyle().Background(cGreen).Foreground(cBg).Bold(true).Render(" OPEN ")
	}
	return lipgloss.NewStyle().Background(cPurple).Foreground(cBg).Bold(true).Render(" CLOSED ")
}

func statusDot(s string) string {
	switch s {
	case "running":
		return sGreen.Render("●")
	case "waiting":
		return sYellow.Render("◐")
	case "error":
		return sRed.Render("✕")
	}
	return sDim.Render("○")
}

func labels(ls []string) string {
	var parts []string
	for _, l := range ls {
		parts = append(parts, sLabel.Render(l))
	}
	return strings.Join(parts, sDim.Render(","))
}

func reviewWord(r string) string {
	switch r {
	case "approved":
		return sGreen.Render("approved")
	case "changes":
		return sRed.Render("changes requested")
	case "required":
		return sYellow.Render("review required")
	}
	return sDim.Render("no reviews")
}

func mergeWord(s string) string {
	switch s {
	case "clean":
		return sGreen.Render("ready to merge")
	case "conflict":
		return sRed.Render("conflicts")
	case "behind":
		return sYellow.Render("behind base")
	}
	return sDim.Render("blocked")
}

func checksWord(p pr) string {
	switch p.checks {
	case "pass":
		return sGreen.Render(p.checksText)
	case "fail":
		return sRed.Render(p.checksText)
	case "pending":
		return sYellow.Render(p.checksText)
	}
	return sDim.Render(p.checksText)
}

// ---------- shared chrome ----------

const project = "sanchit36/agent-deck"

func (m model) header(w int) string {
	l := sGreen.Render("●") + sYellow.Render("●") + sDim.Render("○") + " " + sBold.Render("agent-deck") + sDim.Render("  default  ·  Agent-Deck")
	r := sGreen.Render("● 1") + " " + sYellow.Render("◐ 1") + " " + sDim.Render("○ 2")
	return rightAlign(l, r, w)
}

func (m model) statusChips() string {
	return sDim.Render("0 all  ! running  @ waiting  # idle  $ error")
}

func (m model) helpBar(w int) []string {
	var keys string
	switch m.tab {
	case 0:
		keys = "enter attach  n new  [ ] tabs  / search  ? help"
	case 1:
		keys = "enter launch  o browser  y copy url  f filter  ctrl+r refresh  [ ] tabs"
	default:
		keys = "enter launch  d diff  M merge  o browser  y copy  f filter  [ ] tabs"
	}
	return []string{sBorder.Render(strings.Repeat("─", w)), fit(sDim.Render(keys), w)}
}

// projectLine is the Project selector + freshness line shown above forge lists.
func projectLine(w int, tab int) string {
	n := len(issues)
	if tab == 2 {
		n = len(prs)
	}
	l := sAccent.Render(project) + sDim.Render(" ▾")
	r := sDim.Render(fmt.Sprintf("%d open · 2m ago", n-1))
	return rightAlign(l, r, w)
}

func tabName(t int) string { return []string{"Sessions", "Issues", "PRs"}[t] }

// ---------- sessions tab (shared by all variants) ----------

func (m model) sessionRows(w int) []string {
	var out []string
	for i, s := range sessions {
		var line string
		if s.group {
			line = sAccent.Render("▾ "+s.title) + sDim.Render(" (4)")
		} else {
			left := "  " + statusDot(s.status) + " " + sText.Render(s.title) + " " + sDim.Render(s.tool)
			right := ""
			if s.badge != "" {
				right = sCyan.Render(s.badge)
			}
			line = rightAlign(left, right, w)
		}
		if i == m.cur[0] {
			line = sSel.Render(fit(ansi.Strip(line), w))
		}
		out = append(out, line)
	}
	return out
}

func (m model) sessionDetail(w, h int) []string {
	s := sessions[m.cur[0]]
	if m.embedded && !s.group {
		lines := []string{sDim.Render("── live terminal: " + s.title + " ──"), "",
			sGreen.Render("❯ ") + "fix the flicker in renderSessionItem",
			"", sText.Render("● Reading internal/ui/home_view_list.go"), sText.Render("● Editing 2 files"), "",
			sDim.Render("  ⎿  +14 −3"), "", sYellow.Render("Waiting for input…")}
		return block(lines, w, h)
	}
	if s.group {
		return block([]string{sBold.Render(s.title), sDim.Render("group · 4 sessions"), "", sDim.Render("Project: ") + sAccent.Render(project)}, w, h)
	}
	lines := []string{sBold.Render(s.title), sDim.Render(s.tool + " · " + s.status), ""}
	if s.badge != "" {
		lines = append(lines, sDim.Render("Linked: ")+sCyan.Render(s.badge), "")
	}
	lines = append(lines, sDim.Render("── output ──"), sText.Render("● Editing internal/ui/home.go"), sDim.Render("  ⎿  +14 −3"))
	return block(lines, w, h)
}

// ---------- detail bodies (used by A; B and C have their own) ----------

func issueCard(i issue, w int) []string {
	out := wrap(sBold.Render(fmt.Sprintf("#%d ", i.n))+sBold.Render(i.title), w)
	out = append(out,
		issueBadge(i)+" "+sDim.Render(i.author+" opened "+i.age+" ago · "+fmt.Sprint(len(i.comments))+" comments"),
		sDim.Render("Labels   ")+labels(i.labels),
		sDim.Render("Assignee ")+sText.Render(orDash(i.assignee)),
	)
	out = append(out, sessionsSection(i.sessions)...)
	out = append(out, "", sDim.Render("── body ──"))
	for _, b := range i.body {
		out = append(out, wrap(sText.Render(b), w)...)
	}
	out = append(out, "", sDim.Render(fmt.Sprintf("── comments (%d) ──", len(i.comments))))
	for _, c := range i.comments {
		out = append(out, sAccent.Render(c.who)+sDim.Render(" · "+c.age))
		out = append(out, wrap(sText.Render("  "+c.text), w)...)
	}
	return out
}

func prCard(p pr, w int) []string {
	out := wrap(sBold.Render(fmt.Sprintf("#%d ", p.n))+sBold.Render(p.title), w)
	out = append(out,
		stateBadge(p)+" "+sDim.Render(p.author+" · "+p.head+" → "+p.base+" · "+p.age),
		sGreen.Render(fmt.Sprintf("+%d", p.add))+" "+sRed.Render(fmt.Sprintf("−%d", p.del))+sDim.Render(fmt.Sprintf(" · %d files · %d comments", p.files, p.ncomm)),
		"",
		sDim.Render("Checks   ")+checksGlyph(p.checks)+" "+checksWord(p),
		sDim.Render("Review   ")+reviewGlyph(p.review)+" "+reviewWord(p.review),
		sDim.Render("Merge    ")+mergeGlyph(p.merge)+" "+mergeWord(p.merge),
	)
	if len(p.labels) > 0 {
		out = append(out, sDim.Render("Labels   ")+labels(p.labels))
	}
	out = append(out, sessionsSection(p.sessions)...)
	out = append(out, "", sDim.Render("── description ──"))
	for _, b := range p.body {
		out = append(out, wrap(sText.Render(b), w)...)
	}
	return out
}

func sessionsSection(ss []string) []string {
	if len(ss) == 0 {
		return []string{sDim.Render("Sessions ") + sDim.Render("none · enter to launch")}
	}
	out := []string{sDim.Render("Sessions ") + sCyan.Render(ss[0]) + sDim.Render("  (g to jump)")}
	for _, s := range ss[1:] {
		out = append(out, "         "+sCyan.Render(s))
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// ---------- Variant A: title-row tabs, one-line rows, single detail card ----------

func (m model) aTitle(w int) string {
	var parts []string
	for t := 0; t < 3; t++ {
		name := tabName(t)
		if t == 0 && m.embedded {
			name = "Agents"
		}
		label := strings.ToUpper(name)
		if t > 0 {
			label = fmt.Sprintf("%s %d", label, m.count(t)-1)
		}
		if t == m.tab {
			parts = append(parts, sTabOn.Render(" "+label+" "))
		} else {
			parts = append(parts, sTabOff.Render(" "+label+" "))
		}
	}
	return strings.Join(parts, sBorder.Render("│"))
}

func (m model) aList(w, h int) []string {
	if m.tab == 0 {
		return block(m.sessionRows(w), w, h)
	}
	out := []string{projectLine(w, m.tab)}
	if m.tab == 1 {
		for idx, i := range issues {
			left := issueGlyph(i) + " " + sDim.Render(fmt.Sprintf("#%-4d", i.n)) + sText.Render(i.title)
			right := ""
			if len(i.sessions) > 0 {
				right += sCyan.Render("⧉ ")
			}
			if len(i.comments) > 0 {
				right += sDim.Render(fmt.Sprintf("%d ", len(i.comments)))
			}
			right += sDim.Render(fmt.Sprintf("%3s", i.age))
			line := rightAlign(left, right, w)
			if idx == m.cur[1] {
				line = sSel.Render(fit(ansi.Strip(line), w))
			}
			out = append(out, line)
		}
	} else {
		for idx, p := range prs {
			left := prGlyph(p) + " " + sDim.Render(fmt.Sprintf("#%-5d", p.n)) + sText.Render(p.title)
			right := ""
			if len(p.sessions) > 0 {
				right += sCyan.Render("⧉ ")
			}
			right += checksGlyph(p.checks) + reviewGlyph(p.review) + mergeGlyph(p.merge) + " " + sDim.Render(fmt.Sprintf("%3s", p.age))
			line := rightAlign(left, right, w)
			if idx == m.cur[2] {
				line = sSel.Render(fit(ansi.Strip(line), w))
			}
			out = append(out, line)
		}
	}
	return block(out, w, h)
}

func (m model) aDetailTitle() string {
	switch m.tab {
	case 1:
		return fmt.Sprintf("ISSUE #%d", issues[m.cur[1]].n)
	case 2:
		return fmt.Sprintf("PR #%d", prs[m.cur[2]].n)
	}
	if m.embedded {
		return "TERMINAL"
	}
	return "PREVIEW"
}

func (m model) aDetail(w, h int) []string {
	switch m.tab {
	case 1:
		return block(issueCard(issues[m.cur[1]], w), w, h)
	case 2:
		return block(prCard(prs[m.cur[2]], w), w, h)
	}
	return m.sessionDetail(w, h)
}

// ---------- Variant B: chip tabs in filter bar, two-line rows, sectioned detail ----------

func (m model) bFilterBar(w int) string {
	var chips []string
	for t := 0; t < 3; t++ {
		label := fmt.Sprintf(" %s %d ", tabName(t), m.count(t)-1)
		if t == 0 {
			label = fmt.Sprintf(" %s %d ", tabName(t), 5)
		}
		if t == m.tab {
			chips = append(chips, sChipOn.Render(label))
		} else {
			chips = append(chips, sChip.Render(label))
		}
	}
	left := strings.Join(chips, " ")
	var right string
	if m.tab == 0 {
		right = m.statusChips()
	} else {
		right = sDim.Render("is:open  ") + sAccent.Render("@me") + sDim.Render("  label:any")
	}
	return rightAlign(left, right, w)
}

func (m model) bLeftTitle() string {
	if m.tab == 0 {
		if m.embedded {
			return "AGENTS  GROUPED"
		}
		return "SESSIONS"
	}
	return project + "  ⇄ parent"
}

func (m model) bList(w, h int) []string {
	if m.tab == 0 {
		return block(m.sessionRows(w), w, h)
	}
	var out []string
	add := func(sel bool, l1, l2 string) {
		bar := "  "
		if sel {
			bar = sAccent.Render("▌ ")
		}
		out = append(out, bar+l1, bar+"  "+l2, "")
	}
	if m.tab == 1 {
		for idx, i := range issues {
			title := sText.Render(i.title)
			if idx == m.cur[1] {
				title = sBold.Render(i.title)
			}
			l2 := sDim.Render(fmt.Sprintf("#%d · %s · %s", i.n, i.author, i.age))
			if len(i.labels) > 0 {
				l2 += " " + labels(i.labels)
			}
			if len(i.sessions) > 0 {
				l2 += " " + sCyan.Render("◉ "+i.sessions[0])
			}
			add(idx == m.cur[1], issueGlyph(i)+" "+title, l2)
		}
	} else {
		for idx, p := range prs {
			title := sText.Render(p.title)
			if idx == m.cur[2] {
				title = sBold.Render(p.title)
			}
			l2 := sDim.Render(fmt.Sprintf("#%d · %s · %s · ", p.n, p.author, p.age)) + checksGlyph(p.checks) + sDim.Render(" ci  ") + reviewGlyph(p.review) + sDim.Render(" review")
			if len(p.sessions) > 0 {
				l2 += " " + sCyan.Render("◉ "+p.sessions[0])
			}
			add(idx == m.cur[2], prGlyph(p)+" "+title, l2)
		}
	}
	// keep the cursor visible
	sel := m.cur[m.tab] * 3
	if sel+3 > h {
		out = out[sel+3-h:]
	}
	return block(out, w, h)
}

var bSections = []string{"Overview", "Checks", "Reviews", "Comments", "Sessions"}

func (m model) bDetail(w, h int) []string {
	if m.tab == 0 {
		return m.sessionDetail(w, h)
	}
	var head []string
	var body []string
	sec := bSections[m.section]
	if m.tab == 1 {
		i := issues[m.cur[1]]
		head = append(head, issueBadge(i)+" "+sBold.Render(fmt.Sprintf("#%d", i.n))+"  "+sDim.Render(i.author+" · "+i.age))
		head = append(head, wrap(sBold.Render(i.title), w)...)
		switch sec {
		case "Overview":
			body = append(body, sDim.Render("labels ")+labels(i.labels)+sDim.Render("  assignee ")+sText.Render(orDash(i.assignee)), "")
			for _, b := range i.body {
				body = append(body, wrap(sText.Render(b), w)...)
			}
		case "Comments":
			for _, c := range i.comments {
				body = append(body, sAccent.Render(c.who)+sDim.Render(" · "+c.age))
				body = append(body, wrap(sText.Render("  "+c.text), w)...)
				body = append(body, "")
			}
		case "Sessions":
			body = append(body, sessionsSection(i.sessions)...)
		default:
			body = append(body, sDim.Render("(not applicable to issues)"))
		}
	} else {
		p := prs[m.cur[2]]
		head = append(head, stateBadge(p)+" "+sBold.Render(fmt.Sprintf("#%d", p.n))+"  "+sDim.Render(p.head+" → "+p.base))
		head = append(head, wrap(sBold.Render(p.title), w)...)
		head = append(head, checksGlyph(p.checks)+" "+checksWord(p)+sDim.Render("  ·  ")+reviewGlyph(p.review)+" "+reviewWord(p.review)+sDim.Render("  ·  ")+mergeGlyph(p.merge)+" "+mergeWord(p.merge))
		switch sec {
		case "Overview":
			body = append(body, sGreen.Render(fmt.Sprintf("+%d", p.add))+" "+sRed.Render(fmt.Sprintf("−%d", p.del))+sDim.Render(fmt.Sprintf(" in %d files · %s · %s", p.files, p.author, p.age)), "")
			for _, b := range p.body {
				body = append(body, wrap(sText.Render(b), w)...)
			}
		case "Checks":
			body = append(body, sGreen.Render("✓")+" build", sGreen.Render("✓")+" lint", checksGlyph(p.checks)+" Full test suite (PR gate)", sGreen.Render("✓")+" macOS native SSH acceptance")
		case "Reviews":
			body = append(body, reviewGlyph(p.review)+" "+sAccent.Render("ashesh")+" "+reviewWord(p.review))
		case "Comments":
			for _, c := range p.timeline {
				body = append(body, sAccent.Render(c.who)+sDim.Render(" · "+c.age+" · ")+sText.Render(c.text))
			}
		case "Sessions":
			body = append(body, sessionsSection(p.sessions)...)
		}
	}
	var tabs []string
	for i, s := range bSections {
		if i == m.section {
			tabs = append(tabs, sTitle.Underline(true).Render(s))
		} else {
			tabs = append(tabs, sDim.Render(s))
		}
	}
	head = append(head, "", strings.Join(tabs, sDim.Render(" │ "))+sDim.Render("   { }"), sBorder.Render(strings.Repeat("─", w)))
	return block(append(head, body...), w, h)
}

// ---------- Variant C: vertical rail + dense table + key/value detail ----------

func (m model) cRail(h int) []string {
	icons := []string{"S", "I", "P"}
	if m.embedded {
		icons[0] = "A"
	}
	var out []string
	for t := 0; t < 3; t++ {
		n := fmt.Sprintf("%d", m.count(t)-1)
		if t == 0 {
			n = "5"
		}
		if t == m.tab {
			out = append(out, sTabOn.Render(" "+icons[t]+" "), sTabOn.Render(fmt.Sprintf("%3s", n)), "")
		} else {
			out = append(out, sTabOff.Render(" "+icons[t]+" "), sDim.Render(fmt.Sprintf("%3s", n)), "")
		}
	}
	return block(out, 3, h)
}

// cRailInline is the rail collapsed to one line for stacked/single widths.
func (m model) cRailInline() string {
	var p []string
	for t := 0; t < 3; t++ {
		s := " " + tabName(t)[:1] + " "
		if t == m.tab {
			p = append(p, sTabOn.Render(s))
		} else {
			p = append(p, sTabOff.Render(s))
		}
	}
	return strings.Join(p, "")
}

func (m model) cTable(w, h int) []string {
	if m.tab == 0 {
		return block(append(panelTitle(sTitle.Render(map[bool]string{true: "AGENTS", false: "SESSIONS"}[m.embedded]), w), m.sessionRows(w)...), w, h)
	}
	var out []string
	out = append(out, rightAlign(sAccent.Render(project)+sDim.Render(" ▾"), sDim.Render("2m ago"), w))
	titleW := w - 6 - 2 - 12 - 10 - 5
	if titleW < 10 {
		titleW = 10
	}
	if m.tab == 1 {
		out = append(out, sDim.Render(fit("#", 6)+fit("ST", 3)+fit("TITLE", titleW)+fit("LABELS", 12)+fit("ASSIGN", 10)+fit("AGE", 5)))
		out = append(out, sBorder.Render(strings.Repeat("─", w)))
		for idx, i := range issues {
			line := sDim.Render(fit(fmt.Sprint(i.n), 6)) + fit(issueGlyph(i), 3) + fit(sText.Render(i.title), titleW-1) + " " + fit(labels(i.labels), 12) + fit(sDim.Render(orDash(i.assignee)), 10) + fit(sDim.Render(i.age), 5)
			if idx == m.cur[1] {
				line = sSel.Render(fit(ansi.Strip(line), w))
			}
			out = append(out, line)
		}
	} else {
		narrow := w < 70 // narrow: drop the diff-size, linked and age columns
		titleW = w - 6 - 3 - 3 - 3 - 3 - 12 - 4 - 5
		if narrow {
			titleW = w - 6 - 3 - 3 - 3 - 3
		}
		if titleW < 10 {
			titleW = 10
		}
		hdr := fit("#", 6) + fit("ST", 3) + fit("TITLE", titleW) + fit("CI", 3) + fit("RV", 3) + fit("MG", 3)
		if !narrow {
			hdr += fit("±", 12) + fit("◉", 4) + fit("AGE", 5)
		}
		out = append(out, sDim.Render(hdr))
		out = append(out, sBorder.Render(strings.Repeat("─", w)))
		for idx, p := range prs {
			linked := ""
			if len(p.sessions) > 0 {
				linked = sCyan.Render(fmt.Sprint(len(p.sessions)))
			}
			line := sDim.Render(fit(fmt.Sprint(p.n), 6)) + fit(prGlyph(p), 3) + fit(sText.Render(p.title), titleW-1) + " " +
				fit(checksGlyph(p.checks), 3) + fit(reviewGlyph(p.review), 3) + fit(mergeGlyph(p.merge), 3)
			if !narrow {
				line += fit(sGreen.Render(fmt.Sprintf("+%d", p.add))+sRed.Render(fmt.Sprintf("−%d", p.del)), 12) + fit(linked, 4) + fit(sDim.Render(p.age), 5)
			}
			if idx == m.cur[2] {
				line = sSel.Render(fit(ansi.Strip(line), w))
			}
			out = append(out, line)
		}
	}
	return block(out, w, h)
}

func (m model) cDetail(w, h int) []string {
	if m.tab == 0 {
		return block(append(panelTitle(sTitle.Render(map[bool]string{true: "TERMINAL", false: "PREVIEW"}[m.embedded]), w), m.sessionDetail(w, h-2)...), w, h)
	}
	kv := func(k, v string) string { return sDim.Render(fit(k, 9)) + v }
	var out []string
	if m.tab == 1 {
		i := issues[m.cur[1]]
		out = append(out, panelTitle(sTitle.Render(fmt.Sprintf("ISSUE #%d", i.n)), w)...)
		out = append(out, wrap(sBold.Render(i.title), w)...)
		out = append(out, "",
			kv("state", issueGlyph(i)+" "+map[bool]string{true: "open", false: "closed"}[i.open]),
			kv("author", sText.Render(i.author)),
			kv("age", sText.Render(i.age)),
			kv("labels", labels(i.labels)),
			kv("assignee", sText.Render(orDash(i.assignee))),
			kv("comments", sText.Render(fmt.Sprint(len(i.comments)))),
			kv("sessions", sCyan.Render(orDash(strings.Join(i.sessions, ", ")))),
			"", sDim.Render("timeline"))
		for _, c := range i.comments {
			out = append(out, sDim.Render(fmt.Sprintf("%3s ", c.age))+sAccent.Render(c.who)+" "+sText.Render(c.text))
		}
		out = append(out, sDim.Render(fmt.Sprintf("%3s ", i.age))+sAccent.Render(i.author)+sDim.Render(" opened"))
		out = append(out, "", sDim.Render("v  read body + comments"))
	} else {
		p := prs[m.cur[2]]
		out = append(out, panelTitle(sTitle.Render(fmt.Sprintf("PR #%d", p.n)), w)...)
		out = append(out, wrap(sBold.Render(p.title), w)...)
		out = append(out, "",
			kv("state", prGlyph(p)+" "+sText.Render(p.state)),
			kv("branch", sText.Render(p.head)),
			kv("into", sText.Render(p.base)),
			kv("checks", checksGlyph(p.checks)+" "+checksWord(p)),
			kv("review", reviewGlyph(p.review)+" "+reviewWord(p.review)),
			kv("merge", mergeGlyph(p.merge)+" "+mergeWord(p.merge)),
			kv("diff", sGreen.Render(fmt.Sprintf("+%d", p.add))+" "+sRed.Render(fmt.Sprintf("−%d", p.del))+sDim.Render(fmt.Sprintf(" %d files", p.files))),
			kv("sessions", sCyan.Render(orDash(strings.Join(p.sessions, ", ")))),
			"", sDim.Render("timeline"))
		for _, c := range p.timeline {
			out = append(out, sDim.Render(fmt.Sprintf("%3s ", c.age))+sAccent.Render(c.who)+" "+sText.Render(c.text))
		}
		out = append(out, "", sDim.Render("v  read description"))
	}
	return block(out, w, h)
}

// ---------- frame ----------

func (m model) content(w, h int) []string {
	lay := m.layout()
	v := variants[m.v].key
	sep := sBorder.Render(" │ ")

	// single column: list only; enter opens the detail full screen
	if lay == "single" {
		if m.detailOpen && m.tab != 0 {
			title := "← esc   " + m.aDetailTitle()
			switch v {
			case "B":
				return append(panelTitle(sTitle.Render(title), w), m.bDetail(w, h-2)...)
			case "C":
				return m.cDetail(w, h)
			}
			return append(panelTitle(sTitle.Render(title), w), m.aDetail(w, h-2)...)
		}
		switch v {
		case "B":
			return append(panelTitle(sTitle.Render(m.bLeftTitle()), w), m.bList(w, h-2)...)
		case "C":
			return append([]string{m.cRailInline(), sBorder.Render(strings.Repeat("─", w))}, m.cTable(w, h-2)...)
		}
		return append(panelTitle(m.aTitle(w), w), m.aList(w, h-2)...)
	}

	if lay == "stacked" {
		listH := h * 45 / 100
		detH := h - listH
		var top, bot []string
		switch v {
		case "B":
			top = append(panelTitle(sTitle.Render(m.bLeftTitle()), w), m.bList(w, listH-2)...)
			bot = append(panelTitle(sTitle.Render(m.aDetailTitle()), w), m.bDetail(w, detH-2)...)
		case "C":
			top = append([]string{m.cRailInline()}, m.cTable(w, listH-1)...)
			bot = m.cDetail(w, detH)
		default:
			top = append(panelTitle(m.aTitle(w), w), m.aList(w, listH-2)...)
			bot = append(panelTitle(sTitle.Render(m.aDetailTitle()), w), m.aDetail(w, detH-2)...)
		}
		return append(top, bot...)
	}

	// dual
	switch v {
	case "B":
		lw := w * 38 / 100
		rw := w - lw - 3
		left := append(panelTitle(sTitle.Render(m.bLeftTitle()), lw), m.bList(lw, h-2)...)
		right := append(panelTitle(sTitle.Render(m.aDetailTitle()), rw), m.bDetail(rw, h-2)...)
		return hjoin(left, sep, right)
	case "C":
		lw := w * 38 / 100
		if m.tab != 0 {
			lw = w * 60 / 100
		}
		rw := w - 3 - 1 - lw - 3
		rail := m.cRail(h)
		left := m.cTable(lw, h)
		right := m.cDetail(rw, h)
		return hjoin(hjoin(rail, " ", left), sep, right)
	}
	lw := w * 38 / 100
	rw := w - lw - 3
	left := append(panelTitle(m.aTitle(lw), lw), m.aList(lw, h-2)...)
	right := append(panelTitle(sTitle.Render(m.aDetailTitle()), rw), m.aDetail(rw, h-2)...)
	return hjoin(left, sep, right)
}

func (m model) legendView(w, h int) []string {
	lines := []string{sTitle.Render("GLYPH LEGEND") + sDim.Render("   (esc to close)"), "",
		sBold.Render("Issue state"), "  " + sGreen.Render("○") + " open    " + sPurple.Render("✓") + " closed", "",
		sBold.Render("PR state"), "  " + sGreen.Render("●") + " open    " + sDim.Render("◌") + " draft    " + sPurple.Render("◆") + " merged    " + sRed.Render("✕") + " closed", "",
		sBold.Render("Checks"), "  " + sGreen.Render("✓") + " passing  " + sRed.Render("✗") + " failing  " + sYellow.Render("◷") + " running  " + sDim.Render("·") + " none", "",
		sBold.Render("Review"), "  " + sGreen.Render("✔") + " approved  " + sRed.Render("±") + " changes requested  " + sYellow.Render("?") + " required  " + sDim.Render("·") + " none", "",
		sBold.Render("Mergeability"), "  " + sGreen.Render("⇢") + " clean  " + sRed.Render("≠") + " conflicts  " + sYellow.Render("↓") + " behind  " + sDim.Render("⊘") + " blocked", "",
		sBold.Render("Links"), "  " + sCyan.Render("⧉ / ◉ / #12 PR#34") + " a session is linked",
	}
	return block(lines, w, h)
}

func (m model) View() string {
	w, h := m.W(), m.H()
	var rows []string
	rows = append(rows, m.header(w))
	if variants[m.v].key == "B" {
		rows = append(rows, m.bFilterBar(w))
	} else {
		rows = append(rows, fit(m.statusChips(), w))
	}
	contentH := h - 2 - 2
	if m.legend {
		rows = append(rows, m.legendView(w, contentH)...)
	} else {
		rows = append(rows, block(m.content(w, contentH), w, contentH)...)
	}
	rows = append(rows, m.helpBar(w)...)

	frame := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cBorder).Render(strings.Join(rows, "\n"))

	vr := variants[m.v]
	mode := "classic"
	if m.embedded {
		mode = "embedded"
	}
	bar := lipgloss.NewStyle().Background(lipgloss.Color("#ff9e64")).Foreground(cBg).Bold(true).Padding(0, 1).
		Render(fmt.Sprintf("◀ %s · %s ▶   %s · %d cols (%s)   , . variant  L layout  W width  [ ] tab  j/k  enter/esc  { }  ?  q", vr.key, vr.name, mode, w, m.layout()))
	warn := ""
	if m.termW > 0 && m.termW < w+2 {
		warn = "\n" + sRed.Render(fmt.Sprintf("terminal is %d cols; widen it to %d to see this preset", m.termW, w+2))
	}
	return frame + "\n" + bar + warn
}

func main() {
	// -dump prints every variant/layout/width/tab combination without a TTY.
	if len(os.Args) > 1 && os.Args[1] == "-dump" {
		for v := range variants {
			for _, emb := range []bool{false, true} {
				for wi := range widths {
					for tab := 0; tab < 3; tab++ {
						m := model{v: v, embedded: emb, wi: wi, tab: tab, termH: 30}
						m.cur[2] = 1
						fmt.Printf("\n===== %s emb=%v w=%d tab=%d\n%s\n", variants[v].key, emb, widths[wi], tab, m.View())
					}
				}
			}
		}
		return
	}
	if _, err := tea.NewProgram(model{termH: 40}, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
