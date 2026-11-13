package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/signals"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// BenchmarkSessionRowsRender times the classic session-row render path for
// 500 rows (dual layout, 120 columns), with no links and with every row
// linked to an Issue and a PR, so the badge's cost on the hot path shows;
// signals=true adds a published Attention signal snapshot covering every
// link (a glyph lookup per link).
func BenchmarkSessionRowsRender(b *testing.B) {
	for _, v := range []struct{ linked, signals bool }{{false, false}, {true, false}, {true, true}} {
		linked := v.linked
		name := fmt.Sprintf("linked=%v", linked)
		if v.signals {
			name += "/signals=true"
		}
		b.Run(name, func(b *testing.B) {
			h := NewHome()
			h.width, h.height = 120, 40
			snap := &signals.Snapshot{Items: map[signals.Target]signals.Item{}}
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
					app := github.Repo{Host: "github.com", Owner: "acme", Name: "app"}
					snap.Items[signals.Target{Repo: app, Number: i + 1}] = signals.Item{Found: true, State: signals.StateClosed}
					snap.Items[signals.Target{Repo: app, Number: 1000 + i, PR: true}] = signals.Item{Found: true, PR: true, Checks: signals.ChecksFailing, Review: signals.ReviewApproved}
				}
				items[i] = session.Item{Type: session.ItemTypeSession, Session: inst, Level: 1}
				snapshot[id] = sessionRenderState{status: session.StatusIdle, tool: "claude", title: inst.Title}
			}
			if v.signals {
				h.forge.signals.snap.Store(snap)
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
