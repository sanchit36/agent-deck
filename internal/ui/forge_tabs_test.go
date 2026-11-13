package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// forgeLocal is a github.LocalReader over fixed checkouts.
func forgeLocal(checkouts map[string]github.Local) github.LocalReader {
	return func(_ context.Context, path string) (github.Local, error) {
		l, ok := checkouts[path]
		if !ok {
			return github.Local{}, fmt.Errorf("reading %s: %w", path, github.ErrNotGit)
		}
		return l, nil
	}
}

func checkout(root, remoteURL string) github.Local {
	return github.Local{Root: root, Remotes: []github.Remote{{Name: "origin", URL: remoteURL}}}
}

// forgeHome is a gate-on Home whose resolver reads checkouts from local and
// asks gh through fake.
func forgeHome(t *testing.T, w, h int, embedded bool, fake *githubtest.Fake, local map[string]github.Local) *Home {
	t.Helper()
	home := forgeFrameHome(t, w, h, embedded, gateOn)
	if fake == nil {
		fake = githubtest.NewFake()
	}
	home.forge.resolver = github.NewResolver(github.NewClient(fake), forgeLocal(local))
	// Lists are fetched only by tests that ask for it, and never by the
	// real gh.
	home.forge.client = github.NewClient(fake)
	home.forge.fetchers = map[freshness.Kind]forgeFetchFunc{}
	return home
}

// drive sends msg and runs every command it returns (and theirs) to the end,
// feeding each message back into Home, as Bubble Tea would.
func drive(t *testing.T, h *Home, msg tea.Msg) *Home {
	t.Helper()
	model, cmd := h.Update(msg)
	h = model.(*Home)
	return runCmds(t, h, cmd)
}

func runCmds(t *testing.T, h *Home, cmd tea.Cmd) *Home {
	t.Helper()
	if cmd == nil {
		return h
	}
	switch m := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			h = runCmds(t, h, c)
		}
	case forgeGroupResolvedMsg, forgeParentResolvedMsg, forgeFetchedMsg, forgeURLDoneMsg, forgeAutoLinkedMsg, forgeDetailOpenMsg:
		model, next := h.Update(m)
		h = runCmds(t, model.(*Home), next)
	}
	return h
}

func frameLines(s string) []string { return strings.Split(stripAnsi(s), "\n") }

var widgetsCheckout = map[string]github.Local{"/src/widgets": checkout("/src/widgets", "git@github.com:me/widgets.git")}

// TestForgeTabStripGolden freezes the strip at dual, stacked and single
// widths on each tab, and the embedded layout's AGENTS strip with the
// FLAT/GROUPED label moved to its right end.
func TestForgeTabStripGolden(t *testing.T) {
	type tc struct {
		w, h     int
		embedded bool
	}
	cases := []tc{{120, 30, false}, {70, 24, false}, {45, 20, false}, {120, 30, true}}
	for _, c := range cases {
		for _, tab := range []leftTab{tabSessions, tabIssues, tabPRs} {
			name := fmt.Sprintf("forge_strip_%dx%d_%s", c.w, c.h, tab)
			if c.embedded {
				name = fmt.Sprintf("forge_strip_%dx%d_embedded_%s", c.w, c.h, tab)
			}
			t.Run(name, func(t *testing.T) {
				h := forgeHome(t, c.w, c.h, c.embedded, nil, widgetsCheckout)
				h.forge.lists[tabIssues].open, h.forge.lists[tabIssues].openKnown = 12, true
				h.forge.lists[tabPRs].open, h.forge.lists[tabPRs].openKnown = 3, true
				switch tab {
				case tabIssues:
					h = drive(t, h, altKeyMsg('i'))
				case tabPRs:
					h = drive(t, h, altKeyMsg('p'))
				}
				frame := h.View()
				if got := len(frameLines(frame)); got != c.h {
					t.Fatalf("frame has %d rows, want %d", got, c.h)
				}
				assertFrameGolden(t, name+".golden", stripAnsi(frame))
			})
		}
	}
}

// TestForgeStripKeepsPanelHeight: the strip lives in the title row, so the
// list starts on the same row as with the gate off and the session rows
// are where they were.
func TestForgeStripKeepsPanelHeight(t *testing.T) {
	for _, sz := range forgeFrameSizes {
		for _, embedded := range []bool{false, true} {
			off := forgeFrameHome(t, sz[0], sz[1], embedded, "")
			on := forgeHome(t, sz[0], sz[1], embedded, nil, nil)
			if off.getListContentStartY() != on.getListContentStartY() {
				t.Fatalf("%v embedded=%v: list start %d -> %d", sz, embedded, off.getListContentStartY(), on.getListContentStartY())
			}
			a, b := frameLines(off.View()), frameLines(on.View())
			if len(a) != len(b) {
				t.Fatalf("%v embedded=%v: %d rows -> %d", sz, embedded, len(a), len(b))
			}
			start := on.getListContentStartY()
			if a[start] != b[start] || a[start+1] != b[start+1] {
				t.Fatalf("%v embedded=%v: session rows moved:\n%q\n%q", sz, embedded, a[start:start+2], b[start:start+2])
			}
		}
	}
}

// TestForgeTabsClickable: clicking a chip in the title row switches tab;
// clicks on a forge list never move the session cursor.
func TestForgeTabsClickable(t *testing.T) {
	for _, sz := range forgeFrameSizes {
		h := forgeHome(t, sz[0], sz[1], false, nil, widgetsCheckout)
		y := h.getListContentStartY() - 2
		segs := h.forgeStripSegments(h.forgeLeftPaneWidth())
		for _, want := range []leftTab{tabPRs, tabIssues, tabSessions, tabIssues} {
			s := segs[want]
			h = drive(t, h, tea.MouseMsg{X: s.x0 + 1, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			if h.forge.tab != want {
				t.Fatalf("%v: click on %q at x=%d: tab = %v, want %v", sz, s.text, s.x0+1, h.forge.tab, want)
			}
		}
		cursor := h.cursor
		h = drive(t, h, tea.MouseMsg{X: 2, Y: h.getListContentStartY() + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		if h.cursor != cursor {
			t.Fatalf("%v: a click on the Issues list moved the session cursor", sz)
		}
		// The Project chip opens the selector.
		h = drive(t, h, tea.MouseMsg{X: 1, Y: h.getListContentStartY(), Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		if !h.forge.picker {
			t.Fatalf("%v: a click on the Project chip did not open the selector", sz)
		}
	}
}

// TestForgeProjectLine: opening a forge tab resolves the cursor group's
// Project off the render path; the Project line shows it, or the empty
// state with its reason.
func TestForgeProjectLine(t *testing.T) {
	t.Run("project", func(t *testing.T) {
		h := forgeHome(t, 120, 30, false, nil, widgetsCheckout)
		h.View()
		if h.forge.views != nil {
			t.Fatal("rendering the Sessions tab resolved a Project")
		}
		model, cmd := h.Update(altKeyMsg('i'))
		h = model.(*Home)
		if cmd == nil {
			t.Fatal("opening the Issues tab did not start resolving the Project")
		}
		if !strings.Contains(stripAnsi(h.View()), "resolving project…") {
			t.Fatalf("no loading state before the Project arrives:\n%s", stripAnsi(h.View()))
		}
		h = runCmds(t, h, cmd)
		if line := frameLines(h.View())[h.getListContentStartY()]; !strings.Contains(line, "me/widgets ▾") || !strings.Contains(line, "is:open") {
			t.Fatalf("Project line = %q", line)
		}
		if cmd := h.forgeEnsureProject(); cmd != nil {
			t.Fatal("a resolved group was resolved again")
		}
	})
	t.Run("no GitHub project", func(t *testing.T) {
		h := forgeHome(t, 120, 30, false, nil, nil)
		h = drive(t, h, altKeyMsg('p'))
		if line := frameLines(h.View())[h.getListContentStartY()]; !strings.Contains(line, "No GitHub project: not a git repository") {
			t.Fatalf("empty state = %q", line)
		}
		if h.forge.tab != tabPRs {
			t.Fatal("the tabs must stay visible without a Project")
		}
	})
	t.Run("gh missing", func(t *testing.T) {
		fake := githubtest.NewFake().ServeFunc(githubtest.MustLoad(t, githubtest.GhMissing), func([]string) bool { return true })
		h := forgeHome(t, 120, 30, false, fake, map[string]github.Local{"/src/widgets": checkout("/src/widgets", "git@ghe.corp.example:me/widgets.git")})
		h = drive(t, h, altKeyMsg('i'))
		if line := frameLines(h.View())[h.getListContentStartY()]; !strings.Contains(line, "gh is not installed") {
			t.Fatalf("gh-missing line = %q", line)
		}
	})
}

// TestForgeProjectSelectorAndParent: p picks among a group's Projects
// (defaulting to the cursor session's), P toggles the Parent Repository.
func TestForgeProjectSelectorAndParent(t *testing.T) {
	repoCall := func(owner, name string) []string {
		return []string{"api", "--hostname", "github.com", "repos/" + owner + "/" + name}
	}
	fake := githubtest.NewFake().
		Serve(githubtest.MustLoad(t, githubtest.RepoFork), repoCall("me", "widgets")...).
		Serve(githubtest.MustLoad(t, githubtest.RepoSource), repoCall("acme", "gadgets")...)
	local := map[string]github.Local{
		"/src/widgets": checkout("/src/widgets", "git@github.com:me/widgets.git"),
		"/src/gadgets": checkout("/src/gadgets", "git@github.com:acme/gadgets.git"),
	}
	h := forgeHome(t, 120, 30, false, fake, local)
	grp := h.groupTree.Groups["widgets"]
	grp.DefaultPath = ""
	grp.Sessions[1].ProjectPath = "/src/gadgets"
	h.cursor = 2 // the gadgets session

	projectLine := func() string { return frameLines(h.View())[h.getListContentStartY()] }
	h = drive(t, h, altKeyMsg('i'))
	if line := projectLine(); !strings.Contains(line, "acme/gadgets ▾") {
		t.Fatalf("selector did not default to the cursor session's Project: %q", line)
	}

	// p opens the selector; the Cyrillic twin does too.
	h = drive(t, h, runeKey('з'))
	if !h.forge.picker {
		t.Fatal("з did not open the Project selector")
	}
	frame := stripAnsi(h.View())
	if !strings.Contains(frame, "me/widgets") || !strings.Contains(frame, "▌ acme/gadgets") {
		t.Fatalf("selector rows:\n%s", frame)
	}
	h = drive(t, h, runeKey('k'))
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	if h.forge.picker || !strings.Contains(projectLine(), "me/widgets ▾") {
		t.Fatalf("enter did not select me/widgets: picker=%v line=%q", h.forge.picker, projectLine())
	}

	// P: me/widgets is a fork of acme/widgets.
	h = drive(t, h, runeKey('P'))
	if line := projectLine(); !strings.Contains(line, "acme/widgets ▾") || !strings.Contains(line, "(parent)") {
		t.Fatalf("P did not show the Parent Repository: %q", line)
	}
	calls := len(fake.Calls())
	h = drive(t, h, runeKey('P'))
	h = drive(t, h, runeKey('P'))
	if len(fake.Calls()) != calls {
		t.Fatal("toggling back to a known parent asked gh again")
	}
	h = drive(t, h, runeKey('P'))
	if line := projectLine(); !strings.Contains(line, "me/widgets ▾") || strings.Contains(line, "(parent)") {
		t.Fatalf("second P did not return to the Project: %q", line)
	}

	// acme/gadgets is not a fork: P says so and stays on the Project.
	h = drive(t, h, runeKey('p'))
	h = drive(t, h, runeKey('j'))
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	h = drive(t, h, runeKey('P'))
	if line := projectLine(); !strings.Contains(line, "acme/gadgets ▾") || strings.Contains(line, "(parent)") {
		t.Fatalf("non-fork P left the Project: %q", line)
	}
	if note := h.forgeView().note; note != "acme/gadgets is not a fork" {
		t.Fatalf("non-fork note = %q", note)
	}
}

// TestForgeStripFitsNarrowPanes: the strip drops counts, then shortens its
// labels, so its row is exactly the pane's width down to 17 cells.
func TestForgeStripFitsNarrowPanes(t *testing.T) {
	h := forgeHome(t, 120, 30, false, nil, nil)
	h.forge.lists[tabIssues].open, h.forge.lists[tabIssues].openKnown = 1234, true
	h.forge.lists[tabPRs].open, h.forge.lists[tabPRs].openKnown = 56, true
	for w := 17; w <= 60; w++ {
		segs := h.forgeStripSegments(w)
		if end := segs[len(segs)-1].x1; end > w {
			t.Fatalf("width %d: strip needs %d cells", w, end)
		}
		row := strings.Split(h.renderForgeTitle(w), "\n")[0]
		if got := lipgloss.Width(row); got != w {
			t.Fatalf("width %d: title row is %d cells", w, got)
		}
	}
	if s := h.forgeStripSegments(28); !strings.Contains(s[1].text, "ISSUES") || strings.Contains(s[1].text, "1234") {
		t.Fatalf("28 cells: want labels without counts, got %+v", s)
	}
}

// TestForgeHelp: on a forge tab ? opens the GitHub section (forge keys and
// the global keys that still work) and the bottom bar shows forge keys; on
// Sessions both are as before.
func TestForgeHelp(t *testing.T) {
	h := forgeHome(t, 120, 40, false, nil, widgetsCheckout)
	h = drive(t, h, runeKey('?'))
	if got := stripAnsi(h.helpOverlay.View()); strings.Contains(got, "GITHUB") {
		t.Fatalf("Sessions tab help shows the GitHub section:\n%s", got)
	}
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEsc})

	h = drive(t, h, altKeyMsg('p'))
	bar := stripAnsi(h.renderHelpBar())
	for _, want := range []string{"[/] tabs", "p project", "P parent", "? help"} {
		if !strings.Contains(bar, want) {
			t.Errorf("forge help bar %q lacks %q", bar, want)
		}
	}
	h = drive(t, h, runeKey('?'))
	if !h.helpOverlay.IsVisible() {
		t.Fatal("? on the PRs tab did not open help")
	}
	got := stripAnsi(h.View())
	for _, want := range []string{"GITHUB", "Previous / next tab", "alt+i", "Project / Parent Repository", "ALSO ON THIS TAB", "Settings", "Report a problem"} {
		if !strings.Contains(got, want) {
			t.Errorf("GitHub help lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Rename session") {
		t.Error("GitHub help lists Sessions-only keys")
	}
}

// TestForgeGateOffAtRuntime: turning the gate off returns to Sessions and
// the tab keys stop working.
func TestForgeGateOffAtRuntime(t *testing.T) {
	h := forgeHome(t, 120, 30, false, nil, widgetsCheckout)
	h = drive(t, h, altKeyMsg('i'))
	h.setForgeEnabled(false, nil)
	if h.forge.tab != tabSessions {
		t.Fatal("gate off left a forge tab showing")
	}
	h = drive(t, h, runeKey(']'))
	if h.forge.tab != tabSessions {
		t.Fatal("] switched tabs with the gate off")
	}
}
