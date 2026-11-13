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

// renderGitSection renders the session detail Git section from the cache
// only; it never runs git. A worktree session always has the section; any
// other session has it once its path is known to be in a repository.
func (h *Home) renderGitSection(inst *session.Instance, width int) string {
	isWT := inst.IsWorktree()
	st, cached := h.gitState.Get(h.gitCheckoutPath(inst))
	if !isWT && (!cached || st.NotRepo || st.Repo == "") {
		return ""
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
		status, statusStyle := "checking...", valueStyle
		switch {
		case !cached:
		case st.Err != nil:
			status, statusStyle = "unavailable", lipgloss.NewStyle().Foreground(ColorYellow)
		case st.Dirty:
			status, statusStyle = "dirty (uncommitted changes)", lipgloss.NewStyle().Foreground(ColorYellow)
		default:
			status, statusStyle = "clean", lipgloss.NewStyle().Foreground(ColorGreen)
		}
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
