package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

// Filtering (spec #18 §6) tests: the query bar, where each query runs, the
// counts, presets, persistence and rejected queries, driven through the
// real fetchers and a fake gh.

// serveSearch answers the search of me/widgets for search string q with f.
func serveSearch(fake *githubtest.Fake, query, q string, f githubtest.Fixture) {
	fake.Serve(f, "api", "--hostname", "github.com", "graphql", "-f", "query="+query, "-f", "q="+q)
}

// searchCalls are the search strings of the searches fake has answered.
func searchCalls(fake *githubtest.Fake) []string {
	var out []string
	for _, c := range fake.Calls() {
		for _, a := range c.Args {
			if q, ok := strings.CutPrefix(a, "q="); ok {
				out = append(out, q)
			}
		}
	}
	return out
}

// typeText sends s as key presses, a space as the space key.
func typeText(t *testing.T, h *Home, s string) *Home {
	t.Helper()
	for _, r := range s {
		if r == ' ' {
			h = drive(t, h, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
			continue
		}
		h = drive(t, h, runeKey(r))
	}
	return h
}

func specialKey(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }

// filterQuery opens the bar, clears it, types q and presses enter.
func filterQuery(t *testing.T, h *Home, q string) *Home {
	t.Helper()
	h = drive(t, h, runeKey('/'))
	h = drive(t, h, specialKey(tea.KeyCtrlU))
	h = typeText(t, h, q)
	return drive(t, h, specialKey(tea.KeyEnter))
}

func paneRows(h *Home) string { return stripAnsi(h.renderForgeList(200, 40)) }

func tabStrip(h *Home) string { return frameLines(h.View())[h.getListContentStartY()-2] }

// TestForgeFilterDefaultListNeverSearches: the default list comes from the
// repository connection; opening the bar, typing plain words and applying
// them over a list that holds every open item never searches.
func TestForgeFilterDefaultListNeverSearches(t *testing.T) {
	h, fake := issuesHome(t, 120, 30, githubtest.IssueList)
	k, _ := h.forgeListKey()
	if k.Filter != github.DefaultFilter {
		t.Fatalf("default list filter %q, want %q", k.Filter, github.DefaultFilter)
	}
	e, _ := h.forge.fresh.Get(k)
	whole := e.Value.(github.IssueList)
	whole.Total = len(whole.Issues)
	h = drive(t, h, forgeFetchedMsg{key: k, value: whole})

	h = drive(t, h, runeKey('/'))
	h = typeText(t, h, " gh run")
	h = drive(t, h, specialKey(tea.KeyEnter))
	h = drive(t, h, specialKey(tea.KeyEsc))
	h = drive(t, h, runeKey('r'))
	if s := searchCalls(fake); len(s) != 0 {
		t.Fatalf("the default list searched: %q", s)
	}
	if k2, _ := h.forgeListKey(); k2 != k {
		t.Fatalf("list key moved to %+v", k2)
	}
}

// TestForgeFilterLiveNarrowing: plain words in the bar narrow the fetched
// rows as they are typed (title, number, author, labels), the Project line
// says N match, and the cursor stays on the narrowed rows.
func TestForgeFilterLiveNarrowing(t *testing.T) {
	h, fake := issuesHome(t, 120, 30, githubtest.IssueList)
	h = drive(t, h, runeKey('/'))
	h = typeText(t, h, " henrymodisett")
	pane := paneRows(h)
	if !strings.Contains(pane, "#14822 · henrymodisett") || strings.Contains(pane, "#8954") {
		t.Fatalf("author narrowing:\n%s", pane)
	}
	if line := projectLine(h); !strings.Contains(line, "1 match") {
		t.Fatalf("Project line %q lacks 1 match", line)
	}
	h = drive(t, h, specialKey(tea.KeyCtrlU))
	h = typeText(t, h, "priority-3")
	if n := h.forge.lists[tabIssues].count; n == 0 || n == 6 {
		t.Fatalf("label narrowing left %d rows", n)
	}
	h = drive(t, h, specialKey(tea.KeyCtrlU))
	h = typeText(t, h, "#14558")
	if pane := paneRows(h); !strings.Contains(pane, "#14558") || strings.Contains(pane, "#14822") {
		t.Fatalf("number narrowing:\n%s", pane)
	}
	h = drive(t, h, specialKey(tea.KeyCtrlU))
	h = typeText(t, h, "zzzz-nothing")
	if pane := paneRows(h); !strings.Contains(pane, "No issues match") {
		t.Fatalf("no-match state:\n%s", pane)
	}
	if s := searchCalls(fake); len(s) != 0 {
		t.Fatalf("typing searched: %q", s)
	}
	// esc in the bar drops the draft: every row is back.
	h = drive(t, h, specialKey(tea.KeyEsc))
	if n := h.forge.lists[tabIssues].count; n != 6 {
		t.Fatalf("%d rows after esc, want 6", n)
	}
}

// TestForgeFilterQualifierSearches: a qualifier runs on enter as one
// search scoped to the Project and kind; the rows are the matches, the
// Project line counts them, and the tab chip keeps the unfiltered total.
func TestForgeFilterQualifierSearches(t *testing.T) {
	h, fake := issuesHome(t, 120, 30, githubtest.IssueList)
	q := "repo:me/widgets is:issue is:closed label:bug sort:updated-desc"
	serveSearch(fake, github.IssueSearchQuery, q, githubtest.MustLoad(t, githubtest.IssueSearch))

	h = drive(t, h, runeKey('/'))
	h = drive(t, h, specialKey(tea.KeyCtrlU))
	h = typeText(t, h, "is:closed label:bug")
	if s := searchCalls(fake); len(s) != 0 {
		t.Fatalf("searched before enter: %q", s)
	}
	h = drive(t, h, specialKey(tea.KeyEnter))
	if s := searchCalls(fake); !slices.Equal(s, []string{q}) {
		t.Fatalf("searches %q, want one %q", s, q)
	}
	if h.forge.bar.open {
		t.Fatal("the bar stayed open after the search landed")
	}
	pane := paneRows(h)
	if !strings.Contains(pane, "#14053 · MumuTW") || strings.Contains(pane, "#14822") {
		t.Fatalf("rows are not the matches:\n%s", pane)
	}
	line := stripAnsi(h.renderForgeProjectLine(200))
	if !strings.Contains(line, "is:closed label:bug") || !strings.Contains(line, "4 of 2002") {
		t.Fatalf("Project line %q lacks the filter chip and 4 of 2002", line)
	}
	if strip := tabStrip(h); !strings.Contains(strip, "ISSUES 1075") || strings.Contains(strip, "2002") {
		t.Fatalf("tab chip %q, want the unfiltered ISSUES 1075", strip)
	}
}

// TestForgeFilterOver100SearchesWords: plain words over a list GitHub cut
// at the page show the hint, and enter sends them to the server with the
// qualifiers.
func TestForgeFilterOver100SearchesWords(t *testing.T) {
	h, fake := issuesHome(t, 120, 30, githubtest.IssueList)
	q := "repo:me/widgets is:issue is:open crash sort:updated-desc"
	serveSearch(fake, github.IssueSearchQuery, q, githubtest.MustLoad(t, githubtest.IssueSearch))
	h = drive(t, h, runeKey('/'))
	h = typeText(t, h, " crash")
	if pane := squash(listPane(h)); !strings.Contains(pane, squash("showing matches in the latest 6 · enter to search all")) {
		t.Fatalf("no hint over a cut list:\n%s", pane)
	}
	h = drive(t, h, specialKey(tea.KeyEnter))
	if s := searchCalls(fake); !slices.Equal(s, []string{q}) {
		t.Fatalf("searches %q, want %q", s, q)
	}
	if k, _ := h.forgeListKey(); k.Filter != "is:open crash" {
		t.Fatalf("list filter %q, want is:open crash", k.Filter)
	}
	// The server matched the words: they no longer narrow locally.
	if n := h.forge.lists[tabIssues].count; n != 4 {
		t.Fatalf("%d rows, want the 4 matches", n)
	}
}

// TestForgeFilterRejectedQuery: a query GitHub rejects shows inline under
// the bar; the bar stays open with the query and the old rows stay.
func TestForgeFilterRejectedQuery(t *testing.T) {
	h, fake := issuesHome(t, 120, 30, githubtest.IssueList)
	q := "repo:me/widgets is:issue is:open label:(bug sort:updated-desc"
	serveSearch(fake, github.IssueSearchQuery, q, githubtest.MustLoad(t, githubtest.SearchRejected))
	h = filterQuery(t, h, "is:open label:(bug")
	if !h.forge.bar.open {
		t.Fatal("the bar closed on a rejected query")
	}
	pane := listPane(h)
	if !strings.Contains(squash(pane), squash("The search query is invalid: unbalanced parentheses.")) {
		t.Fatalf("no inline error:\n%s", pane)
	}
	if k, _ := h.forgeListKey(); k.Filter != github.DefaultFilter {
		t.Fatalf("the rejected filter was applied: %q", k.Filter)
	}
	if !strings.Contains(paneRows(h), "#14822") {
		t.Fatal("the old rows went away")
	}
	// Editing clears the error; esc closes the bar on the old filter.
	h = drive(t, h, specialKey(tea.KeyBackspace))
	if h.forge.bar.err != "" {
		t.Fatal("editing kept the error")
	}
	h = drive(t, h, specialKey(tea.KeyEsc))
	if h.forge.bar.open {
		t.Fatal("esc left the bar open")
	}
}

// TestForgeFilterEscResets: esc on the list resets a non-default filter to
// the starting one; on the default filter esc falls through as before; /
// reopens the bar with the applied query filled in.
func TestForgeFilterEscResets(t *testing.T) {
	h, fake := issuesHome(t, 120, 30, githubtest.IssueList)
	serveSearch(fake, github.IssueSearchQuery, "repo:me/widgets is:issue is:closed sort:updated-desc", githubtest.MustLoad(t, githubtest.IssueSearch))
	h = filterQuery(t, h, "is:closed")
	if k, _ := h.forgeListKey(); k.Filter != "is:closed" {
		t.Fatalf("filter %q, want is:closed", k.Filter)
	}
	h = drive(t, h, runeKey('/'))
	if h.forge.bar.draft != "is:closed" {
		t.Fatalf("bar reopened with %q, want is:closed", h.forge.bar.draft)
	}
	h = drive(t, h, specialKey(tea.KeyEsc))
	h = drive(t, h, specialKey(tea.KeyEsc))
	if k, _ := h.forgeListKey(); k.Filter != github.DefaultFilter {
		t.Fatalf("esc left filter %q", k.Filter)
	}
	if line := stripAnsi(h.renderForgeProjectLine(200)); !strings.Contains(line, "is:open") || !strings.Contains(line, "6 of 1075") {
		t.Fatalf("Project line after reset %q", line)
	}
	if !h.forgeFilterIsDefault(tabIssues) {
		t.Fatal("filter not default after esc")
	}
}

// TestForgeFilterEscBeforeDetail: in the single column's full-screen
// detail, esc first resets a filter, then backs out of the detail.
func TestForgeFilterEscBeforeDetail(t *testing.T) {
	h, fake := detailIssuesHome(t, 45, 20)
	serveSearch(fake, github.IssueSearchQuery, "repo:me/widgets is:issue is:closed sort:updated-desc", githubtest.MustLoad(t, githubtest.IssueSearch))
	h = filterQuery(t, h, "is:closed")
	h = drive(t, h, specialKey(tea.KeyEnter))
	if !h.forgeDetailFull() {
		t.Fatal("enter did not open the full-screen detail")
	}
	h = drive(t, h, specialKey(tea.KeyEsc))
	if !h.forgeFilterIsDefault(tabIssues) {
		t.Fatal("the first esc did not reset the filter")
	}
	if !h.forgeDetailFull() {
		t.Fatal("the first esc backed out of the detail before resetting the filter")
	}
	h = drive(t, h, specialKey(tea.KeyEsc))
	if h.forgeDetailFull() {
		t.Fatal("the second esc did not back out of the detail")
	}
	if !h.forgeTabActive() {
		t.Fatal("esc left the Issues tab")
	}
}

// TestForgeFilterPerProjectAndTab: a filter belongs to its Project and tab
// for the run: the other tab and the Parent Repository keep their own.
func TestForgeFilterPerProjectAndTab(t *testing.T) {
	h, fake := issuesHome(t, 120, 30, githubtest.IssueList)
	serveSearch(fake, github.IssueSearchQuery, "repo:me/widgets is:issue is:closed sort:updated-desc", githubtest.MustLoad(t, githubtest.IssueSearch))
	h = filterQuery(t, h, "is:closed")
	h = drive(t, h, altKeyMsg('p'))
	if k, _ := h.forgeListKey(); k.Filter != github.DefaultFilter {
		t.Fatalf("PRs tab took the Issues filter: %q", k.Filter)
	}
	h = drive(t, h, altKeyMsg('i'))
	if k, _ := h.forgeListKey(); k.Filter != "is:closed" {
		t.Fatalf("Issues filter lost: %q", k.Filter)
	}
	// Another repository shown on the tab starts on the default filter.
	v := h.forgeView()
	v.parents = map[github.Repo]github.Repo{widgetsRepo: {Host: "github.com", Owner: "acme", Name: "widgets"}}
	v.parent = true
	if k, _ := h.forgeListKey(); k.Filter != github.DefaultFilter || k.Repo.Owner != "acme" {
		t.Fatalf("parent key %+v, want acme/widgets on the default filter", k)
	}
	v.parent = false
	if k, _ := h.forgeListKey(); k.Filter != "is:closed" {
		t.Fatalf("back on the Project, filter %q", k.Filter)
	}
}

// TestForgeFilterPresets: ↑/↓ in the bar fill it with the tab's presets,
// then this Project's recent queries; the filled query stays editable.
func TestForgeFilterPresets(t *testing.T) {
	h, fake := issuesHome(t, 120, 30, githubtest.IssueList)
	serveSearch(fake, github.IssueSearchQuery, "repo:me/widgets is:issue is:open label:bug sort:updated-desc", githubtest.MustLoad(t, githubtest.IssueSearch))
	h = filterQuery(t, h, "is:open label:bug")
	h = drive(t, h, runeKey('/'))
	var got []string
	for range 6 {
		h = drive(t, h, specialKey(tea.KeyDown))
		got = append(got, h.forge.bar.draft)
	}
	want := []string{"is:open", "is:open assignee:@me", "is:open author:@me", "is:closed", "is:open label:bug", "is:open"}
	if !slices.Equal(got, want) {
		t.Fatalf("↓ cycle %q, want %q", got, want)
	}
	h = drive(t, h, specialKey(tea.KeyUp))
	if h.forge.bar.draft != "is:open label:bug" {
		t.Fatalf("↑ gave %q", h.forge.bar.draft)
	}
	h = typeText(t, h, "x")
	if h.forge.bar.draft != "is:open label:bugx" {
		t.Fatalf("a preset is not editable: %q", h.forge.bar.draft)
	}
	h = drive(t, h, specialKey(tea.KeyEsc))

	h = drive(t, h, altKeyMsg('p'))
	h = drive(t, h, runeKey('/'))
	var prs []string
	for range 5 {
		h = drive(t, h, specialKey(tea.KeyDown))
		prs = append(prs, h.forge.bar.draft)
	}
	wantPRs := []string{"is:open", "is:open review-requested:@me", "is:open author:@me", "is:open draft:true", "is:merged"}
	if !slices.Equal(prs, wantPRs) {
		t.Fatalf("PR presets %q, want %q", prs, wantPRs)
	}
}

// filterConfigHome is issuesHome under a config that sets the starting
// Issues filter, before Issues opens.
func filterConfigHome(t *testing.T, config string, serve func(*githubtest.Fake)) (*Home, *githubtest.Fake) {
	t.Helper()
	fake := githubtest.NewFake()
	serve(fake)
	home := forgeFrameHome(t, 120, 30, false, gateOn+config)
	home.forge.resolver = github.NewResolver(github.NewClient(fake), forgeLocal(widgetsCheckout))
	home.forge.client = github.NewClient(fake)
	home.forge.fetchers = defaultForgeFetchers()
	home.forge.now = func() time.Time { return issuesT0 }
	home.forge.interval = time.Minute
	return drive(t, home, altKeyMsg('i')), fake
}

// TestForgeFilterConfigStart: [github] issue_filter is the starting query;
// a non-default one searches, and esc comes back to it, not to is:open.
func TestForgeFilterConfigStart(t *testing.T) {
	q := "repo:me/widgets is:issue is:open assignee:@me sort:updated-desc"
	h, fake := filterConfigHome(t, "issue_filter = \"is:open assignee:@me\"\n", func(f *githubtest.Fake) {
		serveSearch(f, github.IssueSearchQuery, q, githubtest.MustLoad(t, githubtest.IssueSearch))
	})
	if s := searchCalls(fake); !slices.Equal(s, []string{q}) {
		t.Fatalf("searches %q, want %q", s, q)
	}
	if !h.forgeFilterIsDefault(tabIssues) {
		t.Fatal("the configured filter does not count as the starting one")
	}
	h = drive(t, h, specialKey(tea.KeyEsc))
	if k, _ := h.forgeListKey(); k.Filter != "is:open assignee:@me" {
		t.Fatalf("esc moved the filter to %q", k.Filter)
	}
}

// TestForgeFilterConfigDefault: issue_filter = "is:open" is the default
// list, never a search.
func TestForgeFilterConfigDefault(t *testing.T) {
	_, fake := filterConfigHome(t, "issue_filter = \"is:open\"\n", func(f *githubtest.Fake) {
		serveIssueList(f, githubtest.MustLoad(t, githubtest.IssueList))
	})
	if s := searchCalls(fake); len(s) != 0 {
		t.Fatalf("searched: %q", s)
	}
	if n := listCalls(fake); n != 1 {
		t.Fatalf("%d list calls, want 1", n)
	}
}

// TestForgeFilterSearchDoesNotAutoLink: only the default PRs list links
// worktree sessions; a filtered PRs list (merged PRs, say) never does.
func TestForgeFilterSearchDoesNotAutoLink(t *testing.T) {
	match := worktreeSession("m", "on feat", "/src/widgets", "feat/x")
	h, _ := prsHome(t, 120, 30, githubtest.PRList, match)
	k := freshness.ListKey(widgetsRepo, freshness.PRList, "is:merged")
	_ = drive(t, h, forgeFetchedMsg{key: k, value: prListWith("feat/x")})
	if l := match.LinkOf(session.LinkPR); l != nil {
		t.Fatalf("a filtered list linked the session to %v", l)
	}
}

// TestForgeFilterGolden freezes the open bar with live narrowing, the
// inline error of a rejected query, and an applied search with its chip
// and count, at dual, stacked and single widths.
func TestForgeFilterGolden(t *testing.T) {
	for _, sz := range forgeFrameSizes {
		for _, c := range []string{"bar", "rejected", "applied"} {
			name := fmt.Sprintf("forge_filter_%dx%d_%s", sz[0], sz[1], c)
			t.Run(name, func(t *testing.T) {
				// The detail pane shows the recorded #14822; the
				// search's rows have no recorded detail, so the applied
				// case shows the detail loading.
				h, fake := detailIssuesHome(t, sz[0], sz[1])
				if c == "applied" {
					delete(h.forge.fetchers, freshness.Detail)
				}
				switch c {
				case "bar":
					h = drive(t, h, runeKey('/'))
					h = typeText(t, h, " gh")
				case "rejected":
					serveSearch(fake, github.IssueSearchQuery, "repo:me/widgets is:issue is:open label:(bug sort:updated-desc", githubtest.MustLoad(t, githubtest.SearchRejected))
					h = filterQuery(t, h, "is:open label:(bug")
				case "applied":
					serveSearch(fake, github.IssueSearchQuery, "repo:me/widgets is:issue is:closed label:bug sort:updated-desc", githubtest.MustLoad(t, githubtest.IssueSearch))
					h = filterQuery(t, h, "is:closed label:bug")
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
