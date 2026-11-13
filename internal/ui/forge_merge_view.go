package ui

import (
	"fmt"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/gitstate"
	"github.com/charmbracelet/lipgloss"
)

// The merge dialog and its follow-up, drawn as a centred box styled like
// the worktree finish dialog (forge_merge.go has the flow).

// forgeDialog builds a dialog's lines at a content width: plain lines are
// cut to it, notes and hints wrap.
type forgeDialog struct {
	width int
	lines []string
}

func (d *forgeDialog) add(s string) { d.lines = append(d.lines, truncateDisplay(s, d.width, "…")) }

// wrap adds text word-wrapped to the width, each line in style.
func (d *forgeDialog) wrap(style lipgloss.Style, text string) {
	for _, l := range strings.Split(lipgloss.NewStyle().Width(d.width).Render(forgeOneLine(text)), "\n") {
		d.add(style.Render(strings.TrimRight(l, " ")))
	}
}

// hints adds key hints joined by " · ", starting a new line rather than
// cutting one.
func (d *forgeDialog) hints(style lipgloss.Style, hints []string) {
	line := ""
	for _, h := range hints {
		next := h
		if line != "" {
			next = line + " · " + h
		}
		if line != "" && lipgloss.Width(next) > d.width {
			d.add(style.Render(line))
			next = h
		}
		line = next
	}
	if line != "" {
		d.add(style.Render(line))
	}
}

// forgeDialogBox is the dialog frame and its content width.
func (h *Home) forgeDialogBox(border lipgloss.TerminalColor) (lipgloss.Style, int) {
	w := max(min(80, h.width-4), 24)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(1, 2).Width(w), w - 4
}

// placeForgeDialog centres the dialog on the screen. On a short screen it
// drops the blank lines, then the vertical padding, then lines above the
// last (the key hints), so the frame keeps the screen's height.
func (h *Home) placeForgeDialog(box lipgloss.Style, lines []string) string {
	room := h.height - 4 // border and vertical padding
	if h.height > 0 && len(lines) > room {
		kept := lines[:0:0]
		for _, l := range lines {
			if l != "" {
				kept = append(kept, l)
			}
		}
		lines = kept
		if len(lines) > room {
			box = box.Padding(0, 2)
			room += 2
		}
		if room > 1 && len(lines) > room {
			lines = append(append([]string{}, lines[:room-1]...), lines[len(lines)-1])
		}
	}
	return lipgloss.Place(h.width, h.height, lipgloss.Center, lipgloss.Center, box.Render(strings.Join(lines, "\n")))
}

func (h *Home) forgeSpinner() string {
	return lipgloss.NewStyle().Foreground(ColorCyan).Render(forgeSpinnerFrames[h.animationFrame%len(forgeSpinnerFrames)])
}

func (h *Home) renderForgeMerge() string {
	if h.forge.follow.visible {
		return h.renderForgeMergeFollow()
	}
	m := &h.forge.merge
	title := lipgloss.NewStyle().Bold(true).Foreground(ColorCyan)
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	label := lipgloss.NewStyle().Foreground(ColorComment)
	yellow := lipgloss.NewStyle().Foreground(ColorYellow)
	red := lipgloss.NewStyle().Foreground(ColorRed).Bold(true)

	g := github.GuardMerge(m.info)
	border := ColorAccent
	if m.has && (g.Blocked != "" || m.failure != "") {
		border = ColorRed
	}
	box, cw := h.forgeDialogBox(border)
	d := &forgeDialog{width: cw}
	row := func(name, value string) { d.add(label.Render(fmt.Sprintf("%-7s ", name)) + value) }

	d.add(title.Render(fmt.Sprintf("Merge PR #%d", m.number)))
	switch {
	case !m.has && m.err != nil:
		d.add("")
		d.wrap(red, forgeErrorText(m.err))
		d.add("")
		d.hints(dim, []string{"r retry", "esc close"})
		return h.placeForgeDialog(box, d.lines)
	case !m.has:
		d.add("")
		d.add(h.forgeSpinner() + dim.Render(" reading the pull request…"))
		d.add("")
		d.hints(dim, []string{"esc close"})
		return h.placeForgeDialog(box, d.lines)
	}

	i := m.info
	d.add(lipgloss.NewStyle().Bold(true).Render(forgeOneLine(i.Title)))
	d.add(dim.Render(forgeMergeBranches(i, cw)))
	d.add("")
	row("Checks", checksValue(i.Checks, i.CheckCounts))
	row("Review", lipgloss.NewStyle().Foreground(reviewGlyphColor(i.Review)).Render(reviewGlyph(i.Review))+" "+reviewWord(i.Review))
	switch g.Action {
	case github.ActionMerge, github.ActionAutoMerge:
		if forgeMergeShowsMethod(g, i) {
			row("Method", lipgloss.NewStyle().Foreground(ColorCyan).Render("◀ "+m.method.Word()+" ▶"))
		} else {
			row("Method", m.method.Word())
		}
	case github.ActionQueue:
		row("Method", dim.Render("set by the merge queue"))
	}
	if forgeMergeShowsDelete(g, i) {
		check := "[ ]"
		if m.deleteBranch {
			check = "[x]"
		}
		row("Branch", check+" Delete branch "+forgeOneLine(i.HeadBranch))
	}
	if t := i.CommitTitle(m.method); t != "" && (g.Action == github.ActionMerge || g.Action == github.ActionAutoMerge) {
		row("Commit", forgeOneLine(t))
	}
	switch g.Action {
	case github.ActionAutoMerge:
		d.wrap(dim, "Auto-merge: GitHub merges it once its checks and reviews pass.")
	case github.ActionQueue:
		d.wrap(dim, "Merge queue: GitHub merges it from the queue once it passes.")
	case github.ActionDisableAuto:
		d.wrap(dim, "Auto-merge is on: GitHub merges it once it can.")
	}

	type note struct {
		style lipgloss.Style
		text  string
	}
	var notes []note
	if w := h.forgeMergeLocalWarning(); w != "" {
		notes = append(notes, note{yellow, w})
	}
	if g.Warning != "" {
		notes = append(notes, note{red, "✗ " + g.Warning})
	}
	if g.Blocked != "" {
		notes = append(notes, note{red, "⊘ " + g.Blocked})
	}
	if m.notice != "" {
		notes = append(notes, note{yellow, "⚠ " + m.notice})
	}
	if m.failure != "" {
		notes = append(notes, note{red, "✗ " + m.failure})
	}
	if m.err != nil {
		notes = append(notes, note{yellow, "⚠ refresh failed: " + forgeErrorText(m.err)})
	}
	if len(notes) > 0 {
		d.add("")
		for _, n := range notes {
			d.wrap(n.style, n.text)
		}
	}
	d.add("")
	switch {
	case m.running:
		d.add(h.forgeSpinner() + dim.Render(" "+forgeMergeRunning(g.Action)+" (up to 60 s)"))
	case m.loading:
		d.add(h.forgeSpinner() + dim.Render(" refreshing…"))
	default:
		d.hints(dim, forgeMergeHints(g, i))
	}
	return h.placeForgeDialog(box, d.lines)
}

// forgeMergeBranches is the `base ← head · sha` line, cut to width with
// the short SHA always shown: each branch gives way to the other's room.
func forgeMergeBranches(i github.MergeInfo, width int) string {
	head := forgeOneLine(i.HeadBranch)
	if i.CrossRepo && i.HeadRepo.Owner != "" {
		head = forgeOneLine(i.HeadRepo.Owner) + ":" + head
	}
	base := forgeOneLine(i.BaseBranch)
	tail := " · " + truncateDisplay(i.HeadSHA, 7, "")
	room := width - lipgloss.Width(" ← "+tail)
	bw, hw := lipgloss.Width(base), lipgloss.Width(head)
	if bw+hw > room {
		half := max(1, room/2)
		switch {
		case bw <= half:
			hw = max(1, room-bw)
		case hw <= half:
			bw = max(1, room-hw)
		default:
			bw, hw = half, max(1, room-half)
		}
	}
	return truncateDisplay(base, bw, "…") + " ← " + truncateDisplay(head, hw, "…") + tail
}

func forgeMergeRunning(a github.MergeAction) string {
	switch a {
	case github.ActionAutoMerge:
		return "enabling auto-merge…"
	case github.ActionQueue:
		return "adding to the merge queue…"
	case github.ActionDisableAuto:
		return "disabling auto-merge…"
	}
	return "merging…"
}

func forgeMergeHints(g github.MergeGuard, i github.MergeInfo) []string {
	var hints []string
	switch g.Action {
	case github.ActionMerge:
		hints = append(hints, "y merge")
	case github.ActionAutoMerge:
		hints = append(hints, "y enable auto-merge")
	case github.ActionQueue:
		hints = append(hints, "y add to merge queue")
	case github.ActionDisableAuto:
		hints = append(hints, "y disable auto-merge")
	}
	if forgeMergeShowsMethod(g, i) {
		hints = append(hints, "←→ method")
	}
	if forgeMergeShowsDelete(g, i) {
		hints = append(hints, "space branch")
	}
	return append(hints, "r refresh", "esc cancel")
}

// forgeMergeLocalWarning is the dialog's yellow ⚠ for the first local
// checkout of the PR that is unpushed, diverged or dirty, from the
// local-state cache.
func (h *Home) forgeMergeLocalWarning() string {
	m := &h.forge.merge
	l, ok := h.forgePRLocal(m.repo, forgeMergeLocalDetail(m.info))
	if !ok || !l.Warn() {
		return ""
	}
	for _, c := range l.Checkouts {
		var parts []string
		switch c.Compare.Kind {
		case gitstate.Unpushed:
			parts = append(parts, fmt.Sprintf("↑%d unpushed", c.Compare.Ahead))
		case gitstate.Diverged:
			parts = append(parts, fmt.Sprintf("↑%d ↓%d diverged", c.Compare.Ahead, c.Compare.Behind))
		}
		if c.Dirty {
			parts = append(parts, "dirty")
		}
		if len(parts) > 0 {
			return "⚠ local " + forgeOneLine(c.Branch) + ": " + strings.Join(parts, " · ")
		}
	}
	return ""
}

func (h *Home) renderForgeMergeFollow() string {
	f := &h.forge.follow
	title := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	yellow := lipgloss.NewStyle().Foreground(ColorYellow).Bold(true)
	border := ColorAccent
	if len(f.dirty) > 0 {
		border = ColorYellow
	}
	box, cw := h.forgeDialogBox(border)
	d := &forgeDialog{width: cw}
	titleOf := func(id string) string {
		for k, x := range f.ids {
			if x == id {
				return f.titles[k]
			}
		}
		return id
	}

	d.add(title.Render(fmt.Sprintf("✓ Merged #%d (%s)", f.number, f.method.Word())))
	d.add("")
	if len(f.dirty) > 0 {
		d.wrap(yellow, "⚠ uncommitted changes: these worktrees were kept")
		for _, id := range f.dirty {
			d.add("  • " + forgeOneLine(titleOf(id)))
		}
		d.add("")
		d.wrap(lipgloss.NewStyle(), "Delete them too? Their uncommitted changes are lost.")
		d.add("")
		d.hints(dim, []string{"y delete them too", "n keep them"})
		return h.placeForgeDialog(box, d.lines)
	}
	d.add(forgePlural(len(f.ids), "session is", "sessions are") + fmt.Sprintf(" linked to #%d:", f.number))
	for _, t := range f.titles {
		d.add("  • " + forgeOneLine(t))
	}
	d.add("")
	var chips []string
	for k, c := range forgeFollowChoices {
		if k == f.choice {
			chips = append(chips, lipgloss.NewStyle().Bold(true).Foreground(ColorCyan).Render("["+c+"]"))
		} else {
			chips = append(chips, dim.Render(" "+c+" "))
		}
	}
	d.add(strings.Join(chips, " "))
	d.wrap(dim, "Delete removes their worktrees; a dirty one is asked about first.")
	d.add("")
	if f.checking {
		d.add(h.forgeSpinner() + dim.Render(" checking worktrees…"))
	} else {
		d.hints(dim, []string{"←→ choose", "enter confirm", "esc keep all"})
	}
	return h.placeForgeDialog(box, d.lines)
}
