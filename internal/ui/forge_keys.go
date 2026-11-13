package ui

import (
	"sort"
	"strings"
)

// Forge key space (spec #18 §4). The Issues and PRs tabs have their own
// keys, resolved apart from the overview's: forge actions live in
// [hotkeys] under forge_* names but never enter hotkeyLookup, so with
// [github] enabled = false the overview's keys are exactly what they were.
const (
	hotkeyForgePrevTab     = "forge_prev_tab"
	hotkeyForgeNextTab     = "forge_next_tab"
	hotkeyForgeTabSessions = "forge_tab_sessions"
	hotkeyForgeTabIssues   = "forge_tab_issues"
	hotkeyForgeTabPRs      = "forge_tab_prs"
	// hotkeyForgeProject opens the Project selector of the group.
	hotkeyForgeProject = "forge_project"
	// hotkeyForgeParent toggles the list between the Project and its
	// Parent Repository.
	hotkeyForgeParent = "forge_parent"
	// hotkeyForgeRefresh refetches the list and the open detail now
	// (ctrl+r stays the global reload).
	hotkeyForgeRefresh = "forge_refresh"
	// hotkeyForgeOpen opens the shown URL in the browser and hotkeyForgeCopy
	// copies it: the selected row's URL, or the SAML authorize URL of the
	// failure message.
	hotkeyForgeOpen = "forge_open"
	hotkeyForgeCopy = "forge_copy"
)

// forgeHotkeyActionOrder is every forge action; later slices append theirs.
var forgeHotkeyActionOrder = []string{
	hotkeyForgePrevTab,
	hotkeyForgeNextTab,
	hotkeyForgeTabSessions,
	hotkeyForgeTabIssues,
	hotkeyForgeTabPRs,
	hotkeyForgeProject,
	hotkeyForgeParent,
	hotkeyForgeRefresh,
	hotkeyForgeOpen,
	hotkeyForgeCopy,
}

// forgeTabActions switch tabs and work from every tab, Sessions included.
var forgeTabActions = []string{
	hotkeyForgePrevTab,
	hotkeyForgeNextTab,
	hotkeyForgeTabSessions,
	hotkeyForgeTabIssues,
	hotkeyForgeTabPRs,
}

var defaultForgeHotkeyBindings = map[string]string{
	hotkeyForgePrevTab:     "[",
	hotkeyForgeNextTab:     "]",
	hotkeyForgeTabSessions: "alt+s",
	hotkeyForgeTabIssues:   "alt+i",
	hotkeyForgeTabPRs:      "alt+p",
	hotkeyForgeProject:     "p",
	hotkeyForgeParent:      "P",
	hotkeyForgeRefresh:     "r",
	hotkeyForgeOpen:        "o",
	hotkeyForgeCopy:        "y",
}

// forgeFallThroughKeys are the overview keys (canonical, as normalizeMainKey
// returns them) that still reach the global switch on a forge tab: quit,
// help, esc, group navigation, layout, settings, report a problem, what's
// new, show me around and the global reload. Every other overview key is
// swallowed there.
var forgeFallThroughKeys = func() map[string]bool {
	keys := map[string]bool{"ctrl+c": true, "esc": true, "alt+j": true, "alt+k": true, "<": true, ">": true, "O": true}
	for d := '1'; d <= '9'; d++ {
		keys[string(d)] = true
	}
	for _, action := range []string{hotkeyQuit, hotkeyHelp, hotkeySettings, hotkeyReportProblem, hotkeyWhatsNew, hotkeyShowAround, hotkeyReload} {
		keys[defaultHotkeyBindings[action]] = true
	}
	return keys
}()

// forgeMovementKeys move the forge tab's own cursor (never the session
// cursor), with the step each one takes: ±1, ±page/2, ±page, or the ends.
var forgeMovementKeys = map[string]forgeMove{
	"j": moveDown, "down": moveDown, "ctrl+n": moveDown,
	"k": moveUp, "up": moveUp, "ctrl+p": moveUp,
	"ctrl+d": moveHalfDown, "pgdown": moveHalfDown,
	"ctrl+u": moveHalfUp, "pgup": moveHalfUp,
	"ctrl+f": movePageDown,
	"ctrl+b": movePageUp,
	"home":   moveTop,
	"end":    moveBottom,
}

type forgeMove int

const (
	moveDown forgeMove = iota
	moveUp
	moveHalfDown
	moveHalfUp
	movePageDown
	movePageUp
	moveTop
	moveBottom
)

// resolveForgeHotkeys applies the forge_* [hotkeys] overrides to the forge
// defaults. An empty value unbinds the action; other actions are ignored.
func resolveForgeHotkeys(overrides map[string]string) map[string]string {
	bindings := make(map[string]string, len(defaultForgeHotkeyBindings))
	for action, key := range defaultForgeHotkeyBindings {
		bindings[action] = key
	}
	actions := make([]string, 0, len(overrides))
	for action := range overrides {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		name := strings.TrimSpace(strings.ToLower(action))
		if _, ok := defaultForgeHotkeyBindings[name]; !ok {
			continue
		}
		key := strings.TrimSpace(overrides[action])
		if key == "" {
			delete(bindings, name)
			continue
		}
		bindings[name] = key
	}
	return bindings
}

// buildForgeHotkeyLookup maps each pressed key to its forge action. Like
// buildHotkeyLookup, explicit spellings claim their keys before any layout
// twin, so a derived twin never shadows a binding the user wrote.
func buildForgeHotkeyLookup(bindings map[string]string) map[string]string {
	lookup := make(map[string]string, len(bindings)*3)
	for _, action := range forgeHotkeyActionOrder {
		for _, alias := range explicitHotkeyAliases(bindings[action]) {
			if _, taken := lookup[alias]; !taken {
				lookup[alias] = action
			}
		}
	}
	for _, action := range forgeHotkeyActionOrder {
		for _, alias := range layoutHotkeyAliases(bindings[action]) {
			if _, taken := lookup[alias]; !taken {
				lookup[alias] = action
			}
		}
	}
	return lookup
}
