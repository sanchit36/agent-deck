package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// BenchmarkForgeRenderRows times drawing a page of PR rows (glyphs, ci and
// review labels, a linked session on every other row) at the dual layout's
// narrow left pane and at full width.
func BenchmarkForgeRenderRows(b *testing.B) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	var l github.PRList
	var instances []*session.Instance
	for i := 1; i <= 30; i++ {
		l.PRs = append(l.PRs, github.PullRequest{
			Number: i, Title: fmt.Sprintf("Fix the widget renderer for case %d", i),
			Author: "octocat", State: github.PROpen, UpdatedAt: now.Add(-time.Duration(i) * time.Hour),
			HeadBranch: fmt.Sprintf("fix/widget-%d", i), BaseBranch: "main",
			Checks: github.ChecksPassing, Review: github.ReviewApproved,
		})
		if i%2 == 0 {
			instances = append(instances, linkedTo(fmt.Sprintf("s%d", i), fmt.Sprintf("widget fix %d", i), session.LinkPR, i))
		}
	}
	l.Total = len(l.PRs)
	for _, width := range []int{42, 120} {
		b.Run(fmt.Sprintf("width=%d", width), func(b *testing.B) {
			h := forgeBenchHome(b, gateOn)
			h.forge.tab = tabPRs
			h.forge.groupPath = "widgets"
			h.forge.views = map[string]*forgeGroupView{"widgets": {group: github.Group{Projects: []github.Project{{Repo: github.Repo{Host: "github.com", Owner: "me", Name: "widgets"}}}}}}
			h.forge.now = func() time.Time { return now }
			setForgeInstances(h, instances)
			k, ok := h.forgeListKey()
			if !ok {
				b.Fatal("no list key")
			}
			h.forgeCache().Begin(k)
			h.forgeCache().Done(k, l, nil, now)
			if len(h.renderForgeRows(width, 30)) == 0 {
				b.Fatal("no rows")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = h.renderForgeRows(width, 30)
			}
		})
	}
}
