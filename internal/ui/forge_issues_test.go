package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// issuesT0 is shortly after the issue-list fixture was recorded.
var issuesT0 = time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)

// serveIssueList answers the Issues list query for me/widgets with f.
func serveIssueList(fake *githubtest.Fake, f githubtest.Fixture) {
	fake.Serve(f, "api", "--hostname", "github.com", "graphql",
		"-f", "query="+github.IssueListQuery, "-f", "owner=me", "-f", "name=widgets")
}

// issuesHome is a gate-on Home at w x h whose Issues list is fetched by the
// real fetcher through a fake serving fixture, on Issues once it has
// landed.
func issuesHome(t *testing.T, w, h int, fixture string) (*Home, *githubtest.Fake) {
	t.Helper()
	fake := githubtest.NewFake()
	serveIssueList(fake, githubtest.MustLoad(t, fixture))
	home := forgeHome(t, w, h, false, fake, widgetsCheckout)
	home.forge.client = github.NewClient(fake)
	home.forge.fetchers = defaultForgeFetchers()
	home.forge.now = func() time.Time { return issuesT0 }
	home.forge.interval = time.Minute
	return drive(t, home, altKeyMsg('i')), fake
}

// TestForgeIssuesListOneRequest: opening Issues fetches the list in one gh
// call and shows two-line rows; the Project line and the tab chip count
// the list (6 rows of 1075 open).
func TestForgeIssuesListOneRequest(t *testing.T) {
	h, fake := issuesHome(t, 120, 30, githubtest.IssueList)
	if n := listCalls(fake); n != 1 {
		t.Fatalf("%d list requests, want 1", n)
	}
	pane := stripAnsi(h.renderForgeList(200, 30))
	for _, want := range []string{
		"○ gh run view --job --log-failed fails on required shared-workflow metadata 404",
		"#14822 · henrymodisett · 15m",
		"bug, priority-3, gh-run",
		"#14558 · ghost",
	} {
		if !strings.Contains(pane, want) {
			t.Fatalf("pane lacks %q:\n%s", want, pane)
		}
	}
	if line := projectLine(h); !strings.Contains(line, "6 of 1075") {
		t.Fatalf("Project line %q lacks 6 of 1075", line)
	}
	if strip := frameLines(h.View())[h.getListContentStartY()-2]; !strings.Contains(strip, "ISSUES 1075") {
		t.Fatalf("tab strip %q lacks ISSUES 1075", strip)
	}
}

// TestForgeIssuesOpenCount: a list that holds every open Issue says N open.
func TestForgeIssuesOpenCount(t *testing.T) {
	h, _ := issuesHome(t, 120, 30, githubtest.IssueList)
	k, _ := h.forgeListKey()
	list := h.forge.fresh
	e, _ := list.Get(k)
	il := e.Value.(github.IssueList)
	il.Total = len(il.Issues)
	h = drive(t, h, forgeFetchedMsg{key: k, value: il})
	if line := projectLine(h); !strings.Contains(line, "6 open") || strings.Contains(line, " of ") {
		t.Fatalf("Project line %q, want 6 open", line)
	}
}

// TestForgeIssueGlyphsSingleWidth: every Issue state glyph is one cell
// wide by every width measure the TUI uses.
func TestForgeIssueGlyphsSingleWidth(t *testing.T) {
	for _, closed := range []bool{false, true} {
		g := issueGlyph(closed)
		for name, w := range map[string]int{
			"runewidth": runewidth.StringWidth(g),
			"lipgloss":  lipgloss.Width(issueGlyphStyled(closed)),
			"ansi":      ansi.StringWidth(g),
		} {
			if w != 1 {
				t.Fatalf("glyph %q (closed=%v) is %d cells by %s, want 1", g, closed, w, name)
			}
		}
	}
}

// TestForgeIssueRowsNoByteCuts: titles and labels too wide for the pane
// are cut on cell boundaries, never inside a rune, at every width.
func TestForgeIssueRowsNoByteCuts(t *testing.T) {
	h, _ := issuesHome(t, 120, 30, githubtest.IssueList)
	k, _ := h.forgeListKey()
	wide := github.IssueList{Total: 2, Issues: []github.Issue{
		{Number: 1, Title: "日本語のタイトルがとても長いのでペインに入りきらない😀😀😀", Author: "名前", Labels: []string{"バグ", "優先度高い", "🔥hot"}, UpdatedAt: issuesT0.Add(-time.Hour)},
		{Number: 2, Title: "naïve café résumé — “quoted” title with accents", Author: "zoë", Labels: []string{"ünïcode"}, UpdatedAt: issuesT0.Add(-48 * time.Hour)},
	}}
	h = drive(t, h, forgeFetchedMsg{key: k, value: wide})
	for width := 8; width <= 60; width++ {
		for i, line := range strings.Split(h.renderForgeList(width, 30), "\n") {
			plain := stripAnsi(line)
			if !utf8.ValidString(plain) || strings.ContainsRune(plain, utf8.RuneError) {
				t.Fatalf("width %d line %d cut inside a rune: %q", width, i, plain)
			}
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("width %d line %d is %d cells: %q", width, i, w, plain)
			}
		}
	}
}

// TestForgeIssuesOpenCopyRow: o opens and y copies the selected row's URL,
// each in a tea.Cmd; j moves the selection.
func TestForgeIssuesOpenCopyRow(t *testing.T) {
	h, _ := issuesHome(t, 120, 30, githubtest.IssueList)
	var opened, copied []string
	h.forge.openURL = func(u string) error { opened = append(opened, u); return nil }
	h.forge.copyURL = func(u string) error { copied = append(copied, u); return nil }

	h = drive(t, h, runeKey('o'))
	h = drive(t, h, runeKey('j'))
	model, cmd := h.Update(runeKey('y'))
	h = model.(*Home)
	if len(copied) != 0 {
		t.Fatal("y copied in the key handler")
	}
	_ = runCmds(t, h, cmd)
	if len(opened) != 1 || opened[0] != "https://github.com/cli/cli/issues/14822" {
		t.Fatalf("opened %v, want the first row's URL", opened)
	}
	if len(copied) != 1 || copied[0] != "https://github.com/cli/cli/issues/8954" {
		t.Fatalf("copied %v, want the second row's URL", copied)
	}
}

// TestForgeIssuesMarkDetailsSeen: a list row's updatedAt reaches the
// Freshness cache, so a detail cached at an older version becomes due.
func TestForgeIssuesMarkDetailsSeen(t *testing.T) {
	h, _ := issuesHome(t, 120, 30, githubtest.IssueList)
	dk := freshness.DetailKey(widgetsRepo, 14822)
	h.forge.fresh.Begin(dk)
	h.forge.fresh.Done(dk, seenVersion(issuesT0.Add(-24*time.Hour)), nil, issuesT0)
	if h.forge.fresh.Due(dk, issuesT0, time.Minute) {
		t.Fatal("detail due before any list arrived")
	}
	k, _ := h.forgeListKey()
	e, _ := h.forge.fresh.Get(k)
	h = drive(t, h, forgeFetchedMsg{key: k, value: e.Value})
	if !h.forge.fresh.Due(dk, issuesT0, time.Minute) {
		t.Fatal("a newer updatedAt in the list did not make the detail due")
	}
}

type seenVersion time.Time

func (v seenVersion) Version() time.Time { return time.Time(v) }

// TestForgeIssuesGolden freezes the Issues list (with the selected row's
// detail served), the Issues-disabled empty state and an empty list at
// dual, stacked and single widths.
func TestForgeIssuesGolden(t *testing.T) {
	for _, sz := range forgeFrameSizes {
		for _, c := range []struct{ name, fixture string }{
			{"rows", githubtest.IssueList},
			{"disabled", githubtest.IssueListDisabled},
			{"none", githubtest.IssueList},
		} {
			name := fmt.Sprintf("forge_issues_%dx%d_%s", sz[0], sz[1], c.name)
			t.Run(name, func(t *testing.T) {
				var h *Home
				if c.name == "rows" {
					h, _ = detailIssuesHome(t, sz[0], sz[1])
				} else {
					h, _ = issuesHome(t, sz[0], sz[1], c.fixture)
				}
				if c.name == "none" {
					k, _ := h.forgeListKey()
					h = drive(t, h, forgeFetchedMsg{key: k, value: github.IssueList{}})
				}
				frame := h.View()
				if got := len(frameLines(frame)); got != sz[1] {
					t.Fatalf("frame has %d rows, want %d", got, sz[1])
				}
				assertFrameGolden(t, name+".golden", stripAnsi(frame))
			})
		}
	}
}
