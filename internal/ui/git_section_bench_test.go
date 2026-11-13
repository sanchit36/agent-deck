package ui

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// benchWorktreeHome builds a Home whose selected session is a git worktree
// session, so renderPreviewPane walks the session Git section.
func benchWorktreeHome(b *testing.B) *Home {
	b.Helper()
	h := NewHome()
	h.width = 160
	h.height = 50
	h.initialLoading = false

	dir := b.TempDir()
	inst := session.NewInstance("bench-wt", dir)
	inst.Tool = "bash"
	inst.WorktreePath = dir
	inst.WorktreeRepoRoot = dir
	inst.WorktreeBranch = "feature/bench"

	h.instancesMu.Lock()
	h.instances = []*session.Instance{inst}
	h.instanceByID[inst.ID] = inst
	h.instancesMu.Unlock()
	h.flatItems = []session.Item{{Type: session.ItemTypeSession, Session: inst}}
	h.cursor = 0
	h.setHotkeys(resolveHotkeys(nil))
	return h
}

// BenchmarkPreviewPaneWorktreeSession times one preview-pane render for a
// selected worktree session (the hot path the Git section sits on).
func BenchmarkPreviewPaneWorktreeSession(b *testing.B) {
	h := benchWorktreeHome(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.renderPreviewPane(100, 50)
	}
}
