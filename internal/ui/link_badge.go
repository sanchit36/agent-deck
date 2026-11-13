package ui

import (
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/charmbracelet/lipgloss"
)

// renderLinkBadge styles badge text for a classic session row, with its
// leading space. Only linked rows pay for the style.
func renderLinkBadge(text string, selected bool) string {
	if selected {
		return SessionStatusSelStyle.Render(" " + text)
	}
	return lipgloss.NewStyle().Foreground(ColorPurple).Render(" " + text)
}

// sessionLinkBadgeText is the static Link badge of a session row (spec #18
// §11): "#12" for a linked Issue, "PR#34" for a linked PR, both when the
// session has both, "" when it has none. It reads the stored links only, so
// the row render path never does I/O for it; live state (Attention signals)
// is a later, separate badge.
func sessionLinkBadgeText(inst *session.Instance) string {
	issue, pr := inst.IssueLink, inst.PRLink
	switch {
	case issue != nil && pr != nil:
		return issue.Label(session.LinkIssue) + " " + pr.Label(session.LinkPR)
	case issue != nil:
		return issue.Label(session.LinkIssue)
	case pr != nil:
		return pr.Label(session.LinkPR)
	}
	return ""
}
