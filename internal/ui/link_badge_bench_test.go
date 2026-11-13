package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// BenchmarkSessionRowsRender times the classic session-row render path for
// 500 rows (dual layout, 120 columns), with no links and with every row
// linked to an Issue and a PR, so the badge's cost on the hot path shows.
func BenchmarkSessionRowsRender(b *testing.B) {
	for _, linked := range []bool{false, true} {
		b.Run(fmt.Sprintf("linked=%v", linked), func(b *testing.B) {
			h := NewHome()
			h.width, h.height = 120, 40
			width := h.sessionsPaneWidth()
			const n = 500
			items := make([]session.Item, n)
			snapshot := make(map[string]sessionRenderState, n)
			for i := range items {
				id := fmt.Sprintf("s%03d", i)
				inst := &session.Instance{ID: id, Title: "session " + id, Tool: "claude", Status: session.StatusIdle}
				if linked {
					inst.SetLink(session.LinkIssue, session.Link{Host: "github.com", Owner: "acme", Name: "app", Number: i + 1})
					inst.SetLink(session.LinkPR, session.Link{Host: "github.com", Owner: "acme", Name: "app", Number: 1000 + i})
				}
				items[i] = session.Item{Type: session.ItemTypeSession, Session: inst, Level: 1}
				snapshot[id] = sessionRenderState{status: session.StatusIdle, tool: "claude", title: inst.Title}
			}
			var sb strings.Builder
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sb.Reset()
				for j, item := range items {
					h.renderSessionItem(&sb, item, j == 0, snapshot, width)
				}
			}
		})
	}
}
