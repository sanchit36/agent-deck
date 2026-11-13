package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

// forgeBenchHome is forgeFrameHome at 120x40 for a benchmark.
func forgeBenchHome(b *testing.B, config string) *Home {
	// No forceTrueColorProfile here: it is process-wide and would make every
	// later benchmark in the run (BenchmarkOp_*) render with colour.
	b.Helper()
	home := b.TempDir()
	b.Setenv("HOME", home)
	b.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir := filepath.Join(home, ".config", "agent-deck")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.UserConfigFileName), []byte(config), 0o600); err != nil {
		b.Fatal(err)
	}
	session.ClearUserConfigCache()
	b.Cleanup(session.ClearUserConfigCache)
	return newForgeFrameHome(120, 40, false)
}

// BenchmarkForgeHomeView times one full View() at 120x40 with the gate
// off, and with it on showing Sessions and Issues (Project resolved), so
// the strip's cost on the hot path shows next to today's frame.
func BenchmarkForgeHomeView(b *testing.B) {
	for _, tc := range []struct {
		name   string
		config string
		tab    leftTab
	}{
		{"gate=off", "", tabSessions},
		{"gate=on/sessions", gateOn, tabSessions},
		{"gate=on/issues", gateOn, tabIssues},
	} {
		b.Run(tc.name, func(b *testing.B) {
			h := forgeBenchHome(b, tc.config)
			if tc.tab != tabSessions {
				h.forge.tab = tc.tab
				h.forge.groupPath = "widgets"
				h.forge.views = map[string]*forgeGroupView{"widgets": {group: github.Group{Projects: []github.Project{{Repo: github.Repo{Host: "github.com", Owner: "me", Name: "widgets"}}}}}}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if h.View() == "" {
					b.Fatal("empty frame")
				}
			}
		})
	}
}

// BenchmarkForgeKeyNav times a cursor key plus its frame with the gate off
// and on (Sessions tab), so the forge handler's cost before the global
// switch shows.
func BenchmarkForgeKeyNav(b *testing.B) {
	for _, tc := range []struct{ name, config string }{{"gate=off", ""}, {"gate=on", gateOn}} {
		b.Run(tc.name, func(b *testing.B) {
			h := forgeBenchHome(b, tc.config)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				key := tea.KeyDown
				if i%2 == 1 {
					key = tea.KeyUp
				}
				h.Update(tea.KeyMsg{Type: key})
				_ = h.View()
			}
		})
	}
}
