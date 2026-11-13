package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const gateOn = "[github]\nenabled = true\n"

func press(t *testing.T, h *Home, keys ...tea.KeyMsg) *Home {
	t.Helper()
	for _, k := range keys {
		model, _ := h.Update(k)
		h = model.(*Home)
	}
	return h
}

// TestForgeKeySpacesHaveSingleMeaning is the duplicate-key test of spec #18
// §4, per key space, Cyrillic twins included. On Issues/PRs the forge keys
// must be unique and must not clash with the global keys that fall
// through. On Sessions the tab keys (which work from every tab) must not
// clash with any key the overview dispatches.
func TestForgeKeySpacesHaveSingleMeaning(t *testing.T) {
	t.Run("forge tabs", func(t *testing.T) {
		seen := map[string]string{}
		claim := func(key, owner string) {
			for _, alias := range hotkeyAliases(key) {
				if prev, ok := seen[alias]; ok && prev != owner {
					t.Errorf("on a forge tab %q means both %q and %q", alias, prev, owner)
					continue
				}
				seen[alias] = owner
			}
		}
		for _, action := range forgeHotkeyActionOrder {
			key := defaultForgeHotkeyBindings[action]
			if key == "" {
				t.Errorf("forge action %q has no default key", action)
			}
			claim(key, action)
		}
		for key := range forgeFallThroughKeys {
			claim(key, "global:"+key)
		}
		for key := range forgeMovementKeys {
			claim(key, "move:"+key)
		}
	})

	t.Run("sessions tab", func(t *testing.T) {
		global := map[string]string{}
		for _, b := range mainDispatcherBindings(t) {
			for _, alias := range hotkeyAliases(b.key) {
				global[alias] = b.action
			}
		}
		for action, key := range defaultHotkeyBindings {
			for _, alias := range hotkeyAliases(key) {
				global[alias] = action
			}
		}
		for _, action := range forgeTabActions {
			for _, alias := range hotkeyAliases(defaultForgeHotkeyBindings[action]) {
				if owner, ok := global[alias]; ok {
					t.Errorf("tab key %q (%s) is already the overview's key for %s", alias, action, owner)
				}
			}
		}
	})

	t.Run("every forge action is named forge_", func(t *testing.T) {
		for _, action := range forgeHotkeyActionOrder {
			if len(action) < 6 || action[:6] != "forge_" {
				t.Errorf("forge action %q is not prefixed forge_", action)
			}
			if _, clash := defaultHotkeyBindings[action]; clash {
				t.Errorf("forge action %q is also a global action", action)
			}
		}
	})
}

// TestForgeHotkeysRebind: forge actions are [hotkeys] actions; "" unbinds,
// a rebound key wins over its default, and the Cyrillic twin follows.
func TestForgeHotkeysRebind(t *testing.T) {
	b := resolveForgeHotkeys(map[string]string{"forge_next_tab": "}", "forge_prev_tab": "", "quit": "x"})
	if b[hotkeyForgeNextTab] != "}" {
		t.Fatalf("forge_next_tab = %q, want }", b[hotkeyForgeNextTab])
	}
	if _, ok := b[hotkeyForgePrevTab]; ok {
		t.Fatal("forge_prev_tab = \"\" did not unbind it")
	}
	if _, ok := b["quit"]; ok {
		t.Fatal("a global action leaked into the forge bindings")
	}
	lookup := buildForgeHotkeyLookup(b)
	if lookup["}"] != hotkeyForgeNextTab || lookup["]"] != "" || lookup["["] != "" {
		t.Fatalf("lookup = %v", lookup)
	}
	if lookup["з"] != hotkeyForgeProject || lookup["З"] != hotkeyForgeParent {
		t.Fatalf("Cyrillic twins: з=%q З=%q", lookup["з"], lookup["З"])
	}
}

// TestForgeTabKeyYieldsToUserBinding: on Sessions a key the user bound to a
// session action keeps doing it with the gate on; the other tab keys work.
func TestForgeTabKeyYieldsToUserBinding(t *testing.T) {
	h := forgeFrameHome(t, 120, 30, false, gateOn+"\n[hotkeys]\nsettings = \"]\"\n")
	h = press(t, h, runeKey(']'))
	if h.forge.tab != tabSessions || !h.settingsPanel.IsVisible() {
		t.Fatalf("] bound to settings: tab=%v settings=%v", h.forge.tab, h.settingsPanel.IsVisible())
	}
	h.settingsPanel.Hide()
	h = press(t, h, runeKey('['))
	if h.forge.tab != tabPRs {
		t.Fatalf("[ = %v, want PRs", h.forge.tab)
	}
}

// TestForgeTabSwitching: [ ] cycle and wrap, alt+s/i/p jump, from every tab,
// and each tab keeps its own cursor.
func TestForgeTabSwitching(t *testing.T) {
	h := forgeFrameHome(t, 120, 30, false, gateOn)
	if !h.forge.enabled {
		t.Fatal("[github] enabled = true did not turn the gate on")
	}
	steps := []struct {
		key  tea.KeyMsg
		want leftTab
	}{
		{runeKey(']'), tabIssues},
		{runeKey(']'), tabPRs},
		{runeKey(']'), tabSessions},
		{runeKey('['), tabPRs},
		{runeKey('['), tabIssues},
		{altKeyMsg('s'), tabSessions},
		{altKeyMsg('p'), tabPRs},
		{altKeyMsg('i'), tabIssues},
		{altKeyMsg('i'), tabIssues},
		{altKeyMsg('s'), tabSessions},
	}
	for i, s := range steps {
		h = press(t, h, s.key)
		if h.forge.tab != s.want {
			t.Fatalf("step %d (%s): tab = %v, want %v", i, s.key, h.forge.tab, s.want)
		}
	}

	// Each tab has its own cursor: moving on Issues leaves Sessions alone.
	h.forge.lists[tabIssues].count = 5
	h.forge.lists[tabPRs].count = 5
	sessionsCursor := h.cursor
	h = press(t, h, altKeyMsg('i'), runeKey('j'), runeKey('j'), altKeyMsg('p'), runeKey('j'))
	if got := h.forge.lists[tabIssues].cursor; got != 2 {
		t.Fatalf("issues cursor = %d, want 2", got)
	}
	if got := h.forge.lists[tabPRs].cursor; got != 1 {
		t.Fatalf("prs cursor = %d, want 1", got)
	}
	if h.cursor != sessionsCursor {
		t.Fatalf("moving on a forge tab moved the session cursor %d -> %d", sessionsCursor, h.cursor)
	}
	h = press(t, h, altKeyMsg('s'), runeKey('j'))
	if h.cursor == sessionsCursor {
		t.Fatal("j on Sessions no longer moves the session cursor")
	}
}

// TestForgeFallThrough: on a forge tab only the listed global keys reach
// the global switch; every other overview key is swallowed.
func TestForgeFallThrough(t *testing.T) {
	t.Run("swallowed", func(t *testing.T) {
		for _, k := range []tea.KeyMsg{runeKey('n'), runeKey('d'), runeKey('r'), runeKey('g'), runeKey('/'), runeKey('m'), runeKey('w'), runeKey('M'), runeKey('т')} {
			h := forgeFrameHome(t, 120, 30, false, gateOn)
			h = press(t, h, altKeyMsg('i'), k)
			if h.hasModalVisible() || h.newDialog.IsVisible() || h.confirmDialog.IsVisible() || h.search.IsVisible() || h.groupDialog.IsVisible() {
				t.Errorf("%q on the Issues tab opened a Sessions surface", k.String())
			}
			if h.forge.tab != tabIssues {
				t.Errorf("%q left the Issues tab", k.String())
			}
		}
	})
	t.Run("help", func(t *testing.T) {
		h := press(t, forgeFrameHome(t, 120, 30, false, gateOn), altKeyMsg('i'), runeKey('?'))
		if !h.helpOverlay.IsVisible() {
			t.Fatal("? on the Issues tab did not open help")
		}
	})
	t.Run("settings", func(t *testing.T) {
		h := press(t, forgeFrameHome(t, 120, 30, false, gateOn), altKeyMsg('p'), runeKey('S'))
		if !h.settingsPanel.IsVisible() {
			t.Fatal("S on the PRs tab did not open settings")
		}
	})
	t.Run("group navigation", func(t *testing.T) {
		h := forgeFrameHome(t, 120, 30, false, gateOn)
		h.cursor = 1
		h = press(t, h, altKeyMsg('i'), altKeyMsg('j'))
		if h.cursor != 2 {
			t.Fatalf("alt+j on the Issues tab: session cursor = %d, want 2", h.cursor)
		}
	})
}
