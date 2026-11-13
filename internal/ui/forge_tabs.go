package ui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/gitstate"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// leftTab is the left panel's tab: Sessions (Agents in the embedded layout),
// Issues or PRs (spec #18 §3). Only [github] enabled = true shows the strip.
type leftTab int

const (
	tabSessions leftTab = iota
	tabIssues
	tabPRs
	leftTabCount
)

func (t leftTab) String() string {
	switch t {
	case tabIssues:
		return "issues"
	case tabPRs:
		return "prs"
	}
	return "sessions"
}

// forgeList is one forge tab's list state: its rows (filled by the list
// slices), cursor and viewport, apart from the session list's.
type forgeList struct {
	count  int
	cursor int
	offset int
	// open is the unfiltered open count the tab chip shows; openKnown is
	// false until a list has loaded.
	open      int
	openKnown bool
	// fetched is the rows the cached list holds and total how many GitHub
	// counts for its filter; count is the rows shown after narrowing.
	fetched int
	total   int
	// key is the cache key of the list counted (see syncForgeLists).
	key freshness.Key
}

// forgeGroupView is what a group's forge tabs show: its resolved Projects,
// the selected one, and the Parent Repository toggle. In memory only.
type forgeGroupView struct {
	resolving bool
	group     github.Group
	err       error
	// unsupported replaces resolution for groups the forge never resolves
	// (SSH remote groups).
	unsupported string
	selected    int

	parent          bool
	parentResolving bool
	parents         map[github.Repo]github.Repo
	// note is a one-line remark on the Project line (not a fork, the
	// parent lookup failed).
	note string
}

// forgeState is the forge tabs' part of Home.
type forgeState struct {
	enabled  bool
	tab      leftTab
	lists    [leftTabCount]forgeList
	bindings map[string]string
	lookup   map[string]string
	resolver *github.Resolver
	views    map[string]*forgeGroupView
	// groupPath is the group the forge tabs show (the session cursor's).
	groupPath string
	picker    bool
	pickerAt  int
	// bypass lets a fall-through key run handleMainKey's global switch.
	bypass bool

	// Freshness (forge_fresh.go). fresh caches every fetch by key;
	// interval is [github] refresh_interval (0 = manual only); fetchers
	// fetch each kind; jobs runs and joins the gh work.
	fresh    *freshness.Cache
	interval time.Duration
	fetchers map[freshness.Kind]forgeFetchFunc
	client   *github.Client
	jobs     *forgeJobs
	// detail is the key of the open detail (zero Kind: none); r refreshes
	// it with the list. Set by syncForgeDetail (forge_detail.go).
	detail freshness.Key
	// detailFull is true while the single column shows the full-screen
	// detail (enter opens it, esc returns).
	detailFull bool
	// detailDelay is the debounce before an open detail is fetched:
	// forgeDetailDelay when zero, none when negative (tests).
	detailDelay time.Duration
	// sessionPick is the attach picker of a row with several linked
	// sessions; nil when closed.
	sessionPick *forgeSessionPick
	// attach attaches to a session by id; nil means as enter does on the
	// Sessions tab. Injectable for tests.
	attach func(id string) tea.Cmd
	// background is true while the terminal reports it lost focus.
	background bool
	// now, openURL and copyURL are injectable for tests; nil means
	// time.Now, the platform browser and the clipboard.
	now     func() time.Time
	openURL func(string) error
	copyURL func(string) error

	// signals is the Attention signal poller and what it publishes
	// (forge_signals.go).
	signals signalsState

	// Filtering (forge_filter.go): the applied filter per Project + tab
	// (absent: the starting one), recent queries, the query bar, and the
	// starting filters from [github] issue_filter / pr_filter.
	filters     map[forgeFilterScope]forgeFilter
	recent      map[forgeFilterScope][]string
	bar         forgeFilterBar
	issueFilter string
	prFilter    string

	// local caches the PR detail's local row by PR head SHA; localRead
	// reads it (gitstate.ReadPRLocal; injectable for tests). See
	// forge_local.go.
	local     map[prLocalKey]prLocalEntry
	localRead func(context.Context, gitstate.PRQuery) gitstate.PRLocal

	// overlay is the full-screen Issue/PR overlay (forge_overlay.go).
	overlay forgeOverlay
}

type forgeGroupResolvedMsg struct {
	groupPath string
	group     github.Group
	selected  int
	err       error
}

type forgeParentResolvedMsg struct {
	groupPath string
	repo      github.Repo
	parent    github.Repo
	ok        bool
	err       error
}

// forgeResolveTimeout bounds one group resolution (git per path plus at
// most a gh auth status per host).
const forgeResolveTimeout = 30 * time.Second

// reloadForgeFromConfig reads the gate, the refresh interval and the
// forge_* hotkeys. Turning the gate off returns to the Sessions tab.
func (h *Home) reloadForgeFromConfig() {
	cfg, _ := session.LoadUserConfig()
	h.forge.interval = cfg.GitHubRefreshInterval()
	h.forge.issueFilter, h.forge.prFilter = cfg.GitHubIssueFilter(), cfg.GitHubPRFilter()
	h.setForgeEnabled(cfg.GitHubEnabled(), session.GetHotkeyOverrides())
	h.syncSignalsPoller(cfg)
}

func (h *Home) setForgeEnabled(enabled bool, overrides map[string]string) {
	h.forgeCache()
	h.forge.enabled = enabled
	h.forge.bindings = resolveForgeHotkeys(overrides)
	h.forge.lookup = buildForgeHotkeyLookup(h.forge.bindings)
	if !enabled {
		h.forge.tab = tabSessions
		h.forge.picker = false
		h.forge.bar = forgeFilterBar{}
		h.forge.sessionPick = nil
		h.forge.detailFull = false
		h.closeForgeOverlay()
	}
	h.syncForgeHelp()
}

// forgeTabActive reports whether a forge tab (Issues or PRs) is showing.
func (h *Home) forgeTabActive() bool {
	return h.forge.enabled && h.forge.tab != tabSessions
}

func (h *Home) forgeResolver() *github.Resolver {
	if h.forge.resolver == nil {
		h.forge.resolver = github.NewResolver(nil, nil)
	}
	return h.forge.resolver
}

func (h *Home) forgeKey(action string) string {
	return strings.TrimSpace(h.forge.bindings[action])
}

// switchForgeTab shows tab t and, for a forge tab, starts resolving the
// cursor group's Project if it has not been.
func (h *Home) switchForgeTab(t leftTab) tea.Cmd {
	h.forge.tab = t
	h.forge.picker = false
	h.closeForgeBar()
	h.forge.sessionPick = nil
	h.forge.detailFull = false
	h.syncForgeHelp()
	return h.forgeEnsureProject()
}

// handleForgeKey routes a key while the gate is on. Tab keys work from
// every tab. On a forge tab the forge keys come first, movement moves the
// tab's own cursor, the listed global keys fall through to the global
// switch and every other key is swallowed (spec #18 §4).
func (h *Home) handleForgeKey(msg tea.KeyMsg, raw, key string) (bool, tea.Model, tea.Cmd) {
	if h.forge.bypass || h.welcomeActive() || h.sessionRefPicking() {
		return false, h, nil
	}
	if h.forge.bar.open && h.forgeTabActive() {
		return h.handleForgeBarKey(msg)
	}
	action := h.forge.lookup[raw]
	// On Sessions a key the user bound to a session action keeps it.
	if _, global := h.hotkeyLookup[raw]; global && h.forge.tab == tabSessions {
		return false, h, nil
	}
	switch action {
	case hotkeyForgePrevTab:
		return true, h, h.switchForgeTab((h.forge.tab + leftTabCount - 1) % leftTabCount)
	case hotkeyForgeNextTab:
		return true, h, h.switchForgeTab((h.forge.tab + 1) % leftTabCount)
	case hotkeyForgeTabSessions:
		return true, h, h.switchForgeTab(tabSessions)
	case hotkeyForgeTabIssues:
		return true, h, h.switchForgeTab(tabIssues)
	case hotkeyForgeTabPRs:
		return true, h, h.switchForgeTab(tabPRs)
	}
	if h.forge.tab == tabSessions {
		return false, h, nil
	}
	if h.forge.picker {
		return h.handleForgePickerKey(msg, key)
	}
	if h.forge.sessionPick != nil {
		return h.handleForgeSessionPickKey(msg, key)
	}
	switch action {
	case hotkeyForgeProject:
		h.openForgePicker()
		return true, h, nil
	case hotkeyForgeParent:
		return true, h, h.toggleForgeParent()
	case hotkeyForgeRefresh:
		return true, h, h.forgeManualRefresh()
	case hotkeyForgeOpen:
		return true, h, h.forgeOpenURL()
	case hotkeyForgeCopy:
		return true, h, h.forgeCopyURL()
	case hotkeyForgeAttach:
		return true, h, h.forgeAttach()
	case hotkeyForgeView:
		return true, h, h.forgeOpenItem()
	case hotkeyForgeFilter:
		h.openForgeBar()
		return true, h, nil
	}
	if mv, ok := forgeMovementKeys[raw]; ok {
		h.moveForgeCursor(mv)
		return true, h, h.syncForgeDetail()
	}
	// esc resets a filter first, then backs out of the full-screen
	// detail, then falls through (spec #18 §4).
	if key == "esc" {
		if cmd, ok := h.resetForgeFilter(); ok {
			return true, h, tea.Batch(cmd, h.syncForgeDetail())
		}
	}
	if key == "esc" && h.forgeDetailFull() {
		h.forge.detailFull = false
		return true, h, h.syncForgeDetail()
	}
	return h.forgeFallThrough(msg, key)
}

// forgeFallThrough runs a listed global key through the global switch,
// then follows the session cursor's group (alt+j/k and 1-9 move it).
func (h *Home) forgeFallThrough(msg tea.KeyMsg, key string) (bool, tea.Model, tea.Cmd) {
	if key == "" || !forgeFallThroughKeys[key] {
		return true, h, nil
	}
	h.forge.bypass = true
	model, cmd := h.handleMainKey(msg)
	h.forge.bypass = false
	if hm, ok := model.(*Home); ok {
		return true, hm, tea.Batch(cmd, hm.forgeEnsureProject())
	}
	return true, model, cmd
}

func (h *Home) handleForgePickerKey(msg tea.KeyMsg, key string) (bool, tea.Model, tea.Cmd) {
	v := h.forgeView()
	n := 0
	if v != nil {
		n = len(v.group.Projects)
	}
	switch msg.String() {
	case "j", "down", "ctrl+n":
		if h.forge.pickerAt < n-1 {
			h.forge.pickerAt++
		}
	case "k", "up", "ctrl+p":
		if h.forge.pickerAt > 0 {
			h.forge.pickerAt--
		}
	case "enter":
		if v != nil && h.forge.pickerAt < n {
			if v.selected != h.forge.pickerAt {
				v.selected = h.forge.pickerAt
				v.parent = false
				v.note = ""
			}
		}
		h.forge.picker = false
		return true, h, h.forgeRefreshDue()
	case "esc":
		h.forge.picker = false
	default:
		if h.forge.lookup[msg.String()] == hotkeyForgeProject {
			h.forge.picker = false
			return true, h, nil
		}
		if key == defaultHotkeyBindings[hotkeyQuit] || key == "ctrl+c" {
			h.forge.picker = false
			return h.forgeFallThrough(msg, key)
		}
	}
	return true, h, nil
}

func (h *Home) openForgePicker() {
	v := h.forgeView()
	if v == nil || len(v.group.Projects) == 0 {
		return
	}
	h.closeForgeBar()
	h.forge.picker = true
	h.forge.pickerAt = v.selected
}

func (h *Home) moveForgeCursor(mv forgeMove) {
	l := &h.forge.lists[h.forge.tab]
	if l.count == 0 {
		l.cursor, l.offset = 0, 0
		return
	}
	page := h.forgeListPage()
	switch mv {
	case moveDown:
		l.cursor++
	case moveUp:
		l.cursor--
	case moveHalfDown:
		l.cursor += max(1, page/2)
	case moveHalfUp:
		l.cursor -= max(1, page/2)
	case movePageDown:
		l.cursor += page
	case movePageUp:
		l.cursor -= page
	case moveTop:
		l.cursor = 0
	case moveBottom:
		l.cursor = l.count - 1
	}
	l.cursor = max(0, min(l.cursor, l.count-1))
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+page {
		l.offset = l.cursor - page + 1
	}
}

// forgeListPage is how many list items fit under the Project line.
func (h *Home) forgeListPage() int {
	return max(1, (h.getVisibleHeight()-1)/forgeRowHeight)
}

// forgeTarget is the group the session cursor is in and what resolving it
// needs. remote is true for an SSH remote row.
func (h *Home) forgeTarget() (groupPath, defaultPath string, sessionPaths []string, cursorPath string, remote bool) {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) {
		return "", "", nil, "", false
	}
	item := h.flatItems[h.cursor]
	switch item.Type {
	case session.ItemTypeRemoteGroup, session.ItemTypeRemoteSession:
		return "remote:" + item.RemoteName + "/" + item.Path, "", nil, "", true
	case session.ItemTypeGroup:
		groupPath = item.Path
	case session.ItemTypeSession:
		if item.Session != nil {
			groupPath, cursorPath = item.Session.GroupPath, item.Session.ProjectPath
		} else {
			groupPath = item.Path
		}
	case session.ItemTypeWindow:
		if inst := h.getInstanceByID(item.WindowSessionID); inst != nil {
			groupPath, cursorPath = inst.GroupPath, inst.ProjectPath
		}
	default:
		groupPath = item.Path
	}
	if h.groupTree != nil {
		if g := h.groupTree.Groups[groupPath]; g != nil {
			defaultPath = g.DefaultPath
			for _, inst := range g.Sessions {
				if inst != nil && inst.ProjectPath != "" {
					sessionPaths = append(sessionPaths, inst.ProjectPath)
				}
			}
		}
	}
	return groupPath, defaultPath, sessionPaths, cursorPath, false
}

// forgeView is the view of the group the forge tabs show, or nil.
func (h *Home) forgeView() *forgeGroupView {
	return h.forge.views[h.forge.groupPath]
}

// forgeEnsureProject points the forge tabs at the cursor's group and, the
// first time a forge tab shows that group, resolves its Projects in a
// tea.Cmd (spec #18 §2: lazily, on opening a forge tab, never on render).
// A group already resolved fetches whatever its visible view is due for.
func (h *Home) forgeEnsureProject() tea.Cmd {
	cmd, resolving := h.forgeResolveProject()
	if resolving {
		return cmd
	}
	return h.forgeRefreshDue()
}

// forgeResolveProject starts resolving the cursor group's Projects the
// first time a forge tab shows it; false when it needs no resolving.
func (h *Home) forgeResolveProject() (tea.Cmd, bool) {
	if !h.forgeTabActive() {
		return nil, false
	}
	groupPath, defaultPath, sessionPaths, cursorPath, remote := h.forgeTarget()
	if groupPath != h.forge.groupPath {
		h.forge.picker = false
	}
	h.forge.groupPath = groupPath
	if h.forge.views == nil {
		h.forge.views = map[string]*forgeGroupView{}
	}
	if _, ok := h.forge.views[groupPath]; ok {
		return nil, false
	}
	if remote {
		h.forge.views[groupPath] = &forgeGroupView{unsupported: "remote groups have no GitHub tabs"}
		return nil, false
	}
	h.forge.views[groupPath] = &forgeGroupView{resolving: true}
	r := h.forgeResolver()
	return h.forgeJobs().run(func(jobCtx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(jobCtx, forgeResolveTimeout)
		defer cancel()
		g, err := r.ResolveGroup(ctx, defaultPath, sessionPaths)
		msg := forgeGroupResolvedMsg{groupPath: groupPath, group: g, err: err}
		if err == nil && cursorPath != "" {
			if res, ok := r.Cached(cursorPath); ok && res.Found() {
				for i, p := range g.Projects {
					if p.Repo == res.Repo {
						msg.selected = i
						break
					}
				}
			}
		}
		return msg
	}), true
}

// applyForgeGroupResolved installs a group's Projects and fetches what the
// visible view is due for.
func (h *Home) applyForgeGroupResolved(msg forgeGroupResolvedMsg) tea.Cmd {
	if h.forge.views == nil {
		h.forge.views = map[string]*forgeGroupView{}
	}
	v := h.forge.views[msg.groupPath]
	if v == nil {
		v = &forgeGroupView{}
		h.forge.views[msg.groupPath] = v
	}
	v.resolving = false
	v.group, v.err, v.selected = msg.group, msg.err, msg.selected
	if v.selected >= len(v.group.Projects) {
		v.selected = 0
	}
	return h.forgeRefreshDue()
}

// forgeProject is the selected Project of the shown group.
func (v *forgeGroupView) project() (github.Repo, bool) {
	if v == nil || v.selected >= len(v.group.Projects) {
		return github.Repo{}, false
	}
	return v.group.Projects[v.selected].Repo, true
}

// shownRepo is the repository the lists show: the Project, or its Parent
// Repository while the P toggle is on.
func (v *forgeGroupView) shownRepo() (github.Repo, bool) {
	repo, ok := v.project()
	if !ok {
		return repo, false
	}
	if v.parent {
		if p, ok := v.parents[repo]; ok {
			return p, true
		}
	}
	return repo, true
}

// toggleForgeParent flips the group between its Project and the Project's
// Parent Repository, asking GitHub's fork metadata the first time.
func (h *Home) toggleForgeParent() tea.Cmd {
	v := h.forgeView()
	repo, ok := v.project()
	if !ok || v.parentResolving {
		return nil
	}
	h.closeForgeBar()
	v.note = ""
	if v.parent {
		v.parent = false
		return h.forgeRefreshDue()
	}
	if _, known := v.parents[repo]; known {
		v.parent = true
		return h.forgeRefreshDue()
	}
	v.parentResolving = true
	groupPath := h.forge.groupPath
	r := h.forgeResolver()
	return h.forgeJobs().run(func(jobCtx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(jobCtx, forgeResolveTimeout)
		defer cancel()
		parent, ok, err := r.Parent(ctx, repo)
		return forgeParentResolvedMsg{groupPath: groupPath, repo: repo, parent: parent, ok: ok, err: err}
	})
}

// applyForgeParentResolved installs a Parent Repository lookup and fetches
// what the visible view is due for.
func (h *Home) applyForgeParentResolved(msg forgeParentResolvedMsg) tea.Cmd {
	v := h.forge.views[msg.groupPath]
	if v == nil {
		return nil
	}
	v.parentResolving = false
	switch {
	case msg.err != nil:
		v.note = "parent lookup failed: " + forgeErrorText(msg.err)
	case !msg.ok:
		v.note = repoLabel(msg.repo) + " is not a fork"
	default:
		if v.parents == nil {
			v.parents = map[github.Repo]github.Repo{}
		}
		v.parents[msg.repo] = msg.parent
		if cur, ok := v.project(); ok && cur == msg.repo {
			v.parent = true
		}
	}
	return h.forgeRefreshDue()
}

// forgeErrorText is the one-line text of a resolution error.
func forgeErrorText(err error) string {
	if github.ClassOf(err) == github.ClassGhMissing || errors.Is(err, exec.ErrNotFound) {
		return "gh is not installed (https://cli.github.com)"
	}
	return err.Error()
}

// repoLabel is owner/name, prefixed with the host off github.com.
func repoLabel(r github.Repo) string {
	if h := r.HostOrDefault(); h != "github.com" {
		return h + "/" + r.Owner + "/" + r.Name
	}
	return r.Owner + "/" + r.Name
}

// ---- rendering (no I/O: everything below reads forgeState only) ----

var (
	forgeTabOnStyle  = lipgloss.NewStyle().Foreground(ColorBg).Background(ColorCyan).Bold(true)
	forgeTabOffStyle = lipgloss.NewStyle().Foreground(ColorTextDim)
	forgeSepStyle    = lipgloss.NewStyle().Foreground(ColorBorder)
)

// forgeStripSeg is one tab chip of the strip: its tab, its text and the
// cells [x0, x1) it covers, so rendering and mouse hit-testing agree.
type forgeStripSeg struct {
	tab    leftTab
	text   string
	x0, x1 int
}

// forgeStripSegments lays the strip out in width cells: full labels with
// counts, then without counts, then short labels.
func (h *Home) forgeStripSegments(width int) []forgeStripSeg {
	first, short := "SESSIONS", "SES"
	if h.embeddedLayout {
		first, short = "AGENTS", "AGT"
	}
	tiers := [][leftTabCount]string{
		{first, h.forgeTabLabel(tabIssues, "ISSUES", true), h.forgeTabLabel(tabPRs, "PRS", true)},
		{first, "ISSUES", "PRS"},
		{short, "ISS", "PRS"},
	}
	var segs []forgeStripSeg
	for _, labels := range tiers {
		segs = segs[:0]
		x := 0
		for t := tabSessions; t < leftTabCount; t++ {
			if t > 0 {
				x++ // the │ between chips
			}
			text := " " + labels[t] + " "
			w := lipgloss.Width(text)
			segs = append(segs, forgeStripSeg{tab: t, text: text, x0: x, x1: x + w})
			x += w
		}
		if x <= width {
			return segs
		}
	}
	return segs
}

func (h *Home) forgeTabLabel(t leftTab, name string, withCount bool) string {
	l := h.forge.lists[t]
	if !withCount {
		return name
	}
	label := name
	if l.openKnown {
		label = fmt.Sprintf("%s %d", name, l.open)
	}
	// The PRS chip carries the on-screen Project's share of the review
	// requests (spec #18 §15).
	if t == tabPRs {
		if n := h.prsReviewShare(); n > 0 {
			label += fmt.Sprintf(" ⇠%d", n)
		}
	}
	return label
}

// renderForgeTitle is the left panel's title block with the gate on: the
// tab strip in the title row (the FLAT/GROUPED label of the embedded layout
// moves to its right end) and the underline. Two lines, like
// renderPanelTitle, so the mouse Y math is unchanged.
func (h *Home) renderForgeTitle(width int) string {
	segs := h.forgeStripSegments(width)
	var b strings.Builder
	used := 0
	for i, s := range segs {
		if i > 0 {
			b.WriteString(forgeSepStyle.Render("│"))
		}
		if s.tab == h.forge.tab {
			b.WriteString(forgeTabOnStyle.Render(s.text))
		} else {
			b.WriteString(forgeTabOffStyle.Render(s.text))
		}
		used = s.x1
	}
	row := b.String()
	if h.embeddedLayout && h.forge.tab == tabSessions {
		mode := "GROUPED"
		if h.sidebarMode == sidebarFlat {
			mode = "FLAT"
		}
		if gap := width - used - len(mode); gap >= 2 {
			row += strings.Repeat(" ", gap) + lipgloss.NewStyle().Foreground(ColorCyan).Bold(true).Render(mode)
		}
	}
	if used > width {
		row = truncateDisplay(row, width, "…")
	}
	underline := lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("─", max(0, width)))
	return ensureExactWidth(row, width) + "\n" + underline
}

// leftPanelTitle is the left panel's title block: the tab strip with the
// gate on, today's title with it off.
func (h *Home) leftPanelTitle(title string, width int) string {
	if h.forge.enabled {
		return h.renderForgeTitle(width)
	}
	return h.renderPanelTitle(title, width)
}

// renderLeftPane is the left panel's body: the session list, or the forge
// tab's Project line and list.
func (h *Home) renderLeftPane(width, height int) string {
	if !h.forgeTabActive() {
		return h.renderSessionList(width, height)
	}
	return h.renderForgeList(width, height)
}

func (h *Home) renderForgeList(width, height int) string {
	var lines []string
	if h.forgeBarShowing(h.forge.tab) {
		lines = append([]string{h.renderForgeBarLine(width)}, h.renderForgeBarNotes(width)...)
	} else {
		lines = []string{h.renderForgeProjectLine(width)}
	}
	if h.forge.picker {
		lines = append(lines, h.renderForgePicker(width)...)
	} else if h.forge.sessionPick != nil {
		lines = append(lines, h.renderForgeSessionPick(width)...)
	} else if body := h.renderForgeListBody(width); body != nil {
		lines = append(lines, body...)
	} else {
		lines = append(lines, h.renderForgeRows(width, height-len(lines))...)
	}
	if len(lines) > height {
		lines = lines[:max(0, height)]
	}
	return strings.Join(lines, "\n")
}

// forgeProjectChip is the Project line's left chip, `owner/repo ▾`; empty
// when there is no Project to select.
func (h *Home) forgeProjectChip() string {
	v := h.forgeView()
	repo, ok := v.shownRepo()
	if !ok {
		return ""
	}
	return repoLabel(repo) + " ▾"
}

// renderForgeProjectLine is the first row of a forge list (spec #18 §3):
// the Project chip and the filter chip, or the one-line empty state.
func (h *Home) renderForgeProjectLine(width int) string {
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	accent := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	warn := lipgloss.NewStyle().Foreground(ColorYellow)
	v := h.forgeView()
	var left, right string
	switch {
	case v == nil || v.resolving:
		left = dim.Render("resolving project…")
	case v.unsupported != "":
		left = dim.Render(v.unsupported)
	case v.err != nil:
		left = warn.Render("⚠ " + forgeErrorText(v.err))
	case len(v.group.Projects) == 0:
		left = dim.Render("No GitHub project: " + v.group.Empty.Message())
	default:
		left = accent.Render(h.forgeProjectChip())
		if v.parent {
			left += dim.Render(" (parent)")
		}
		left += dim.Render("  " + h.forgeFilterChip())
		switch {
		case v.parentResolving:
			right = dim.Render("finding parent…")
		case v.note != "":
			right = dim.Render(v.note)
		default:
			right = h.forgeFreshText()
		}
	}
	line := left
	if right != "" {
		rw := lipgloss.Width(right)
		if gap := width - lipgloss.Width(left) - rw; gap >= 2 {
			line = left + strings.Repeat(" ", gap) + right
		} else if keep := width - rw - 2; keep > lipgloss.Width(h.forgeProjectChip()) {
			// The count and freshness stay whole while only the filter
			// chip gives way; the Project chip is never cut for them.
			left = truncateDisplay(left, keep, "…")
			line = left + strings.Repeat(" ", width-lipgloss.Width(left)-rw) + right
		} else {
			line = left + "  " + right
		}
	}
	if lipgloss.Width(line) > width {
		line = truncateDisplay(line, width, "…")
	}
	return line
}

func (h *Home) renderForgePicker(width int) []string {
	v := h.forgeView()
	if v == nil {
		return nil
	}
	sel := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	plain := lipgloss.NewStyle().Foreground(ColorText)
	out := []string{lipgloss.NewStyle().Foreground(ColorTextDim).Render("Project (enter select, esc close)")}
	for i, p := range v.group.Projects {
		text := "  " + repoLabel(p.Repo)
		style := plain
		if i == h.forge.pickerAt {
			text = "▌ " + repoLabel(p.Repo)
			style = sel
		}
		out = append(out, style.Render(truncateDisplay(text, width, "…")))
	}
	return out
}

// renderForgeHelpBar is the bottom bar on a forge tab: forge keys first,
// settings and help last, in the curated style.
func (h *Home) renderForgeHelpBar() string {
	border := lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("─", max(0, h.width)))
	var ctx []footerHint
	add := func(key, label string) {
		if strings.TrimSpace(key) != "" {
			ctx = append(ctx, footerHint{key: key, label: label})
		}
	}
	switch {
	case h.forge.picker:
		add("↑↓", "move")
		add("enter", "select")
		add("esc", "close")
	case h.forgeBarShowing(h.forge.tab):
		add("enter", "apply")
		add("↑↓", "presets")
		add("esc", "cancel")
	case h.forge.sessionPick != nil:
		add("↑↓", "move")
		add("enter", "attach")
		add("esc", "close")
	default:
		// esc resets a filter before it backs out of the detail.
		resetting := !h.forgeFilterIsDefault(h.forge.tab)
		if h.forgeDetailFull() && !resetting {
			add("esc", "back")
		}
		add(joinHotkeyLabels(h.forgeKey(hotkeyForgePrevTab), h.forgeKey(hotkeyForgeNextTab)), "tabs")
		if _, ok := h.forgeSelectedRow(); ok {
			if h.getLayoutMode() == LayoutModeSingle && !h.forgeDetailFull() {
				add(h.forgeKey(hotkeyForgeView), "detail")
			} else {
				add(h.forgeKey(hotkeyForgeView), "view")
			}
			if insts, _, _ := h.forgeSelectedSessions(); len(insts) > 0 {
				add(h.forgeKey(hotkeyForgeAttach), "attach")
			}
			add(h.forgeKey(hotkeyForgeOpen), "open")
			add(h.forgeKey(hotkeyForgeCopy), "copy url")
		}
		add(h.forgeKey(hotkeyForgeFilter), "filter")
		if resetting {
			add("esc", "reset")
		}
		add(h.forgeKey(hotkeyForgeRefresh), "refresh")
		add(h.forgeKey(hotkeyForgeProject), "project")
		add(h.forgeKey(hotkeyForgeParent), "parent")
	}
	var global []footerHint
	if key := h.actionKey(hotkeySettings); key != "" {
		global = append(global, footerHint{key: key, label: "settings"})
	}
	if key := h.actionKey(hotkeyHelp); key != "" {
		global = append(global, footerHint{key: key, label: "help"})
	}
	hints := h.fitCuratedHints(ctx, global)
	parts := make([]string, 0, len(hints))
	for _, hint := range hints {
		parts = append(parts, h.curatedHint(hint))
	}
	content := strings.Join(parts, lipgloss.NewStyle().Foreground(ColorBorder).Render("  "))
	return lipgloss.NewStyle().MaxWidth(h.width).Render(lipgloss.JoinVertical(lipgloss.Left, border, content))
}

// syncForgeHelp gives the help overlay the GitHub section while a forge tab
// shows, and takes it away otherwise.
func (h *Home) syncForgeHelp() {
	if h.helpOverlay == nil {
		return
	}
	if !h.forgeTabActive() {
		h.helpOverlay.SetForgeRows(nil)
		return
	}
	h.helpOverlay.SetForgeRows(h.forgeHelpRows())
}

// forgeHelpRows are the help overlay's GitHub section: the forge keys, then
// the global keys that still work on a forge tab.
func (h *Home) forgeHelpRows() [][2]string {
	return [][2]string{
		{joinHotkeyLabels(h.forgeKey(hotkeyForgePrevTab), h.forgeKey(hotkeyForgeNextTab)), "Previous / next tab"},
		{h.forgeKey(hotkeyForgeTabSessions), "Sessions tab"},
		{h.forgeKey(hotkeyForgeTabIssues), "Issues tab"},
		{h.forgeKey(hotkeyForgeTabPRs), "PRs tab"},
		{h.forgeKey(hotkeyForgeProject), "Choose the Project"},
		{h.forgeKey(hotkeyForgeParent), "Project / Parent Repository"},
		{h.forgeKey(hotkeyForgeRefresh), "Refresh the list"},
		{h.forgeKey(hotkeyForgeOpen), "Open in the browser"},
		{h.forgeKey(hotkeyForgeCopy), "Copy the URL"},
		{h.forgeKey(hotkeyForgeFilter), "Filter (GitHub search syntax)"},
		{"Esc", "Reset the filter"},
		{h.forgeKey(hotkeyForgeAttach), "Attach to the linked session"},
		{h.forgeKey(hotkeyForgeView), "Open the item (single column: detail first)"},
		{"j/k ↑↓", "Move in the list"},
		{joinHotkeyLabels(h.forgeKey(hotkeyForgeConversation), h.forgeKey(hotkeyForgeFiles)), "Open item: Conversation / Files"},
		{h.forgeKey(hotkeyForgeExpand), "Open item: expand hidden comments"},
		{"Esc / q", "Open item: close"},
		{"", ""},
		{h.actionKey(hotkeyHelp), "This help"},
		{h.actionKey(hotkeyQuit), "Quit"},
		{"Esc", "Back"},
		{"Alt+j / Alt+k", "Next / prev session in group"},
		{"1-9", "Jump to root group"},
		{"< / > / O", "Preview size / orientation"},
		{h.actionKey(hotkeySettings), "Settings"},
		{h.actionKey(hotkeyReload), "Reload from disk"},
		{h.actionKey(hotkeyReportProblem), "Report a problem"},
		{h.actionKey(hotkeyWhatsNew), "What's new"},
		{h.actionKey(hotkeyShowAround), "Show me around"},
	}
}

// forgeHitTab returns the tab whose chip covers column x of the title row.
func (h *Home) forgeHitTab(x, width int) (leftTab, bool) {
	for _, s := range h.forgeStripSegments(width) {
		if x >= s.x0 && x < s.x1 {
			return s.tab, true
		}
	}
	return 0, false
}

// forgeLeftPaneWidth is the left panel's width in the current layout.
func (h *Home) forgeLeftPaneWidth() int {
	if h.getLayoutMode() == LayoutModeDual {
		return h.sessionsPaneWidth()
	}
	return h.width
}

// handleForgeMouseClick handles a left click with the gate on: a tab chip
// switches tabs; on a forge tab, the Project chip opens the selector and
// the rest of the list area is the forge list's. handled is false for
// clicks the session list should take.
func (h *Home) handleForgeMouseClick(x, y int) (bool, tea.Cmd) {
	if h.welcomeActive() {
		return false, nil
	}
	width := h.forgeLeftPaneWidth()
	if x >= width {
		return false, nil
	}
	listTop := h.getListContentStartY()
	if y == listTop-2 {
		if t, ok := h.forgeHitTab(x, width); ok {
			return true, h.switchForgeTab(t)
		}
		return true, nil
	}
	if !h.forgeTabActive() {
		return false, nil
	}
	if y == listTop && x < lipgloss.Width(h.forgeProjectChip()) {
		h.openForgePicker()
	}
	return true, nil
}
