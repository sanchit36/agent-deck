package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/gitstate"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// gitCheckoutPath is the checkout whose git state a session shows: its
// worktree for a worktree session, its project path otherwise. It keys the
// git-state cache.
func (h *Home) gitCheckoutPath(inst *session.Instance) string {
	if inst.IsWorktree() && inst.WorktreePath != "" {
		return inst.WorktreePath
	}
	return inst.ProjectPath
}

// isJJWorkspace reports a session created as a Jujutsu workspace, known
// without any fetch.
func isJJWorkspace(inst *session.Instance) bool {
	return inst.IsWorktree() && inst.WorktreeType == "jujutsu"
}

// gitStateCmd returns an async fetch of inst's git state, or nil when the
// cache is fresh or a fetch is already in flight. force fetches even when
// the cached state is fresh (a dialog that needs a current answer).
func (h *Home) gitStateCmd(inst *session.Instance, force bool) tea.Cmd {
	path := h.gitCheckoutPath(inst)
	if path == "" || isJJWorkspace(inst) {
		return nil
	}
	now := time.Now()
	if force {
		h.gitState.BeginForce(path, now)
	} else if !h.gitState.Begin(path, now) {
		return nil
	}
	fetch := h.gitStateFetch
	sid := inst.ID
	return func() tea.Msg {
		return gitStateFetchedMsg{path: path, sessionID: sid, state: fetch(context.Background(), path)}
	}
}

// gitSectionCompactHeight is the preview height below which the Git section
// collapses to one row (plus one hint row for a worktree session). The full
// section is six rows; in an 80x24 terminal it would push the session's
// output out of the preview.
const gitSectionCompactHeight = 30

// renderGitSection renders the session detail Git section from the cache
// only; it never runs git. A worktree session always has the section; any
// other session has it once its path is known to be in a repository. A
// preview shorter than gitSectionCompactHeight gets the one-row form.
func (h *Home) renderGitSection(inst *session.Instance, width, previewHeight int) string {
	isWT := inst.IsWorktree()
	st, cached := h.gitState.Get(h.gitCheckoutPath(inst))
	if !isWT && (!cached || st.NotRepo || st.Repo == "") {
		return ""
	}
	if previewHeight < gitSectionCompactHeight {
		return h.renderGitSectionCompact(inst, st, cached, width)
	}

	labelStyle := lipgloss.NewStyle().Foreground(ColorText)
	branchStyle := lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)
	valueStyle := lipgloss.NewStyle().Foreground(ColorText)
	hintStyle := lipgloss.NewStyle().Foreground(ColorText).Italic(true)
	keyStyle := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	commentStyle := lipgloss.NewStyle().Foreground(ColorComment)

	var b strings.Builder
	b.WriteString(renderSectionDivider("Git", width-4))
	b.WriteString("\n")
	row := func(label, value string, style lipgloss.Style) {
		b.WriteString(labelStyle.Render(label))
		b.WriteString(style.Render(value))
		b.WriteString("\n")
	}

	branch, repo, path := st.Branch, st.Repo, inst.ProjectPath
	if isWT {
		if inst.WorktreeBranch != "" {
			branch = inst.WorktreeBranch
		}
		repo, path = inst.WorktreeRepoRoot, inst.WorktreePath
	}
	if branch != "" {
		row("Branch:  ", branch, branchStyle)
	}
	if repo != "" {
		row("Repo:    ", truncatePath(repo, width-4-9), valueStyle)
	}
	if path != "" {
		row("Path:    ", truncatePath(path, width-4-9), valueStyle)
	}

	// A jj workspace keeps its Branch/Repo/Path rows (session fields, not
	// git state); only the git status and upstream are not shown in v1.
	if isJJWorkspace(inst) || (cached && st.JJ) {
		b.WriteString(commentStyle.Render("jj workspace: not shown in v1"))
		b.WriteString("\n")
	} else {
		status, statusStyle := gitStatusLabel(st, cached)
		row("Status:  ", status, statusStyle)

		if upstream := upstreamLabel(st, cached); upstream != "" {
			row("Upstream: ", truncateDisplay(upstream, width-4-10, "…"), valueStyle)
		}
	}

	// Setup and Finish hints stay for worktree sessions only.
	if isWT {
		if setupKey := h.actionKey(hotkeyWorktreeSetup); setupKey != "" {
			b.WriteString(hintStyle.Render("Setup:   "))
			b.WriteString(keyStyle.Render(setupKey))
			b.WriteString(hintStyle.Render(" re-run setup script"))
			b.WriteString("\n")
		}
		if finishKey := h.actionKey(hotkeyWorktreeFinish); finishKey != "" {
			b.WriteString(hintStyle.Render("Finish:  "))
			b.WriteString(keyStyle.Render(finishKey))
			b.WriteString(hintStyle.Render(" merge + cleanup"))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderGitSectionCompact is the Git section in one row:
// "Git:     feature/x · dirty (uncommitted changes) · origin/x · ↑2 ↓1",
// then, for a worktree session, one row with the Setup and Finish keys.
func (h *Home) renderGitSectionCompact(inst *session.Instance, st gitstate.State, cached bool, width int) string {
	labelStyle := lipgloss.NewStyle().Foreground(ColorText)
	valueStyle := lipgloss.NewStyle().Foreground(ColorText)
	hintStyle := lipgloss.NewStyle().Foreground(ColorText).Italic(true)
	keyStyle := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)

	type part struct {
		text  string
		style lipgloss.Style
	}
	var parts []part
	if isJJWorkspace(inst) || (cached && st.JJ) {
		parts = append(parts, part{"jj workspace: not shown in v1", lipgloss.NewStyle().Foreground(ColorComment)})
	} else {
		branch := st.Branch
		if inst.IsWorktree() && inst.WorktreeBranch != "" {
			branch = inst.WorktreeBranch
		}
		status, statusStyle := gitStatusLabel(st, cached)
		upstream := upstreamLabel(st, cached)
		if upstream == status {
			upstream = ""
		}
		room := width - 4 - 9
		if upstream != "" && lipgloss.Width(branch)+lipgloss.Width(status)+lipgloss.Width(upstream)+6 > room {
			// Short of room the upstream goes first, then the branch is
			// cut, so the status stays visible.
			upstream = ""
		}
		if branch != "" {
			branch = truncateDisplay(branch, max(room-lipgloss.Width(status)-3, 12), "…")
			parts = append(parts, part{branch, lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)})
		}
		parts = append(parts, part{status, statusStyle})
		if upstream != "" {
			parts = append(parts, part{upstream, valueStyle})
		}
	}

	plain := make([]string, len(parts))
	styled := make([]string, len(parts))
	for i, p := range parts {
		plain[i] = p.text
		styled[i] = p.style.Render(p.text)
	}
	value := strings.Join(styled, valueStyle.Render(" · "))
	if room, joined := width-4-9, strings.Join(plain, " · "); lipgloss.Width(joined) > room {
		value = valueStyle.Render(truncateDisplay(joined, room, "…"))
	}

	var b strings.Builder
	b.WriteString(labelStyle.Render("Git:     "))
	b.WriteString(value)
	b.WriteString("\n")
	if inst.IsWorktree() {
		var hints []string
		if k := h.actionKey(hotkeyWorktreeSetup); k != "" {
			hints = append(hints, keyStyle.Render(k)+hintStyle.Render(" setup"))
		}
		if k := h.actionKey(hotkeyWorktreeFinish); k != "" {
			hints = append(hints, keyStyle.Render(k)+hintStyle.Render(" finish"))
		}
		if len(hints) > 0 {
			b.WriteString(hintStyle.Render("         "))
			b.WriteString(strings.Join(hints, hintStyle.Render(" · ")))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// gitStatusLabel is the Status row value and its style.
func gitStatusLabel(st gitstate.State, cached bool) (string, lipgloss.Style) {
	switch {
	case !cached:
		return "checking...", lipgloss.NewStyle().Foreground(ColorText)
	case st.Err != nil:
		return "unavailable", lipgloss.NewStyle().Foreground(ColorYellow)
	case st.Dirty:
		return "dirty (uncommitted changes)", lipgloss.NewStyle().Foreground(ColorYellow)
	default:
		return "clean", lipgloss.NewStyle().Foreground(ColorGreen)
	}
}

// upstreamLabel is the Upstream row value: "origin/x · ↑a ↓b", the bare
// upstream when it has no counts (its remote branch is gone), "no upstream",
// "checking..." before the first fetch, or "" when the fetch failed (Status
// already says unavailable).
func upstreamLabel(st gitstate.State, cached bool) string {
	switch {
	case !cached:
		return "checking..."
	case st.Err != nil:
		return ""
	case st.Upstream == "":
		return "no upstream"
	case !st.HasCounts:
		return st.Upstream
	default:
		return fmt.Sprintf("%s · ↑%d ↓%d", st.Upstream, st.Ahead, st.Behind)
	}
}
