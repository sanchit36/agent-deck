package ui

import (
	"fmt"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

// forgeFrameSizes are the dual, stacked and single-column layouts.
var forgeFrameSizes = [][2]int{{120, 30}, {70, 24}, {45, 20}}

// forgeFrameHome is a Home with one group of two sessions at w x h under
// configTOML. Without a [github] section the gate is on (its default);
// gateOff turns it off.
func forgeFrameHome(t *testing.T, w, h int, embedded bool, configTOML string) *Home {
	t.Helper()
	forceTrueColorProfile()
	withTempAgentDeckHome(t, configTOML)
	withControllerVersion(t, "1.16.10")
	return newForgeFrameHome(w, h, embedded)
}

// newForgeFrameHome builds forgeFrameHome's Home under whatever HOME and
// config are already in place.
func newForgeFrameHome(w, h int, embedded bool) *Home {
	grp := &session.Group{Name: "widgets", Path: "widgets", Expanded: true, DefaultPath: "/src/widgets"}
	a := &session.Instance{ID: "a", Title: "fix login", Tool: "claude", Status: session.StatusStopped, GroupPath: "widgets", ProjectPath: "/src/widgets"}
	b := &session.Instance{ID: "b", Title: "review", Tool: "codex", Status: session.StatusStopped, GroupPath: "widgets", ProjectPath: "/src/widgets"}
	grp.Sessions = []*session.Instance{a, b}
	home := newTestHomeWithItems(w, h, []session.Item{
		{Type: session.ItemTypeGroup, Group: grp, Path: "widgets", Level: 0},
		{Type: session.ItemTypeSession, Session: a, Path: "widgets", Level: 1},
		{Type: session.ItemTypeSession, Session: b, Path: "widgets", Level: 1, IsLastInGroup: true},
	})
	home.groupTree.Groups["widgets"] = grp
	home.cursor = 1
	home.embeddedLayout = embedded
	return home
}

// forgeTabKeys are the tab keys the forge adds. With the gate off none of
// them may do anything.
var forgeTabKeys = []tea.KeyMsg{
	{Type: tea.KeyRunes, Runes: []rune{'['}},
	{Type: tea.KeyRunes, Runes: []rune{']'}},
	{Type: tea.KeyRunes, Runes: []rune{'s'}, Alt: true},
	{Type: tea.KeyRunes, Runes: []rune{'i'}, Alt: true},
	{Type: tea.KeyRunes, Runes: []rune{'p'}, Alt: true},
}

// TestForgeGateOffFrameGolden: with [github] enabled = false, the home frame
// is the one recorded before the forge existed, at dual, stacked and
// single widths, classic and embedded, and the forge's tab keys leave it
// byte-identical.
func TestForgeGateOffFrameGolden(t *testing.T) {
	for _, embedded := range []bool{false, true} {
		for _, sz := range forgeFrameSizes {
			w, h := sz[0], sz[1]
			name := fmt.Sprintf("forge_gate_off_%dx%d", w, h)
			if embedded {
				name += "_embedded"
			}
			t.Run(name, func(t *testing.T) {
				home := forgeFrameHome(t, w, h, embedded, gateOff)
				before := home.View()
				assertFrameGolden(t, name+".golden", stripAnsi(before))
				for _, k := range forgeTabKeys {
					model, _ := home.Update(k)
					home = model.(*Home)
				}
				if after := home.View(); after != before {
					t.Fatalf("gate off: tab keys changed the frame\n--- before\n%s\n--- after\n%s", stripAnsi(before), stripAnsi(after))
				}
			})
		}
	}
}
