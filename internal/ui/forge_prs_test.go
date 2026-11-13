package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// prsT0 is shortly after the pr-list fixture was recorded.
var prsT0 = time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)

// widgetsPRs is the PRs list key of me/widgets.
var widgetsPRs = freshness.ListKey(widgetsRepo, freshness.PRList, "is:open")

// servePRList answers the PRs list query for me/widgets with f.
func servePRList(fake *githubtest.Fake, f githubtest.Fixture) {
	fake.Serve(f, "api", "--hostname", "github.com", "graphql",
		"-f", "query="+github.PRListQuery, "-f", "owner=me", "-f", "name=widgets")
}

// prsHome is a gate-on Home at w x h whose PRs list is fetched by the real
// fetcher through a fake serving fixture, with instances as its sessions,
// on PRs once it has landed.
func prsHome(t *testing.T, w, h int, fixture string, instances ...*session.Instance) (*Home, *githubtest.Fake) {
	t.Helper()
	fake := githubtest.NewFake()
	servePRList(fake, githubtest.MustLoad(t, fixture))
	local := map[string]github.Local{}
	for k, v := range widgetsCheckout {
		local[k] = v
	}
	local["/src/upstream"] = checkout("/src/upstream", "https://github.com/acme/widgets.git")
	home := forgeHome(t, w, h, false, fake, local)
	home.forge.client = github.NewClient(fake)
	home.forge.fetchers = defaultForgeFetchers()
	home.forge.now = func() time.Time { return prsT0 }
	home.forge.interval = time.Minute
	setForgeInstances(home, instances)
	return drive(t, home, altKeyMsg('p')), fake
}

func setForgeInstances(h *Home, instances []*session.Instance) {
	h.instancesMu.Lock()
	defer h.instancesMu.Unlock()
	h.instances = instances
	h.instanceByID = make(map[string]*session.Instance, len(instances))
	for _, inst := range instances {
		h.instanceByID[inst.ID] = inst
	}
}

// worktreeSession is a session in a git worktree of the repo at root on
// branch.
func worktreeSession(id, title, root, branch string) *session.Instance {
	return &session.Instance{ID: id, Title: title, Tool: "claude", Status: session.StatusStopped,
		GroupPath: "widgets", ProjectPath: "/wt/" + id, WorktreePath: "/wt/" + id,
		WorktreeRepoRoot: root, WorktreeBranch: branch}
}

// TestForgePRsListOneRequest: opening PRs fetches the list in one gh call
// and shows two-line rows with the state glyph, then #n · author · age and
// the check and review state.
func TestForgePRsListOneRequest(t *testing.T) {
	h, fake := prsHome(t, 120, 30, githubtest.PRList)
	if n := listCalls(fake); n != 1 {
		t.Fatalf("%d list requests, want 1", n)
	}
	pane := stripAnsi(h.renderForgeList(200, 40))
	for _, want := range []string{
		"● chore(deps): Bump golang.org/x/term from 0.47.0 to 0.48.0",
		"#15048 · dependabot · 30m  ✓ ci  ? review",
		"◌ Add guided multi-account setup and evaluation",
		"#14927 · williammartin · 2h  ✗ ci  ? review",
		"#13781 · platinummonkey · 6d  · ci  ? review",
		"#13665 · steiza · 2mo  ✓ ci  ✔ review",
		"#13899 · imkp1 · 2mo  ✓ ci  ± review",
	} {
		if !strings.Contains(pane, want) {
			t.Fatalf("pane lacks %q:\n%s", want, pane)
		}
	}
	if line := projectLine(h); !strings.Contains(line, "6 of 59") {
		t.Fatalf("Project line %q lacks 6 of 59", line)
	}
	if strip := frameLines(h.View())[h.getListContentStartY()-2]; !strings.Contains(strip, "PRS 59") {
		t.Fatalf("tab strip %q lacks PRS 59", strip)
	}
}

// TestForgePRGlyphsSingleWidth: every PR, check and review glyph is one
// cell wide by every width measure the TUI uses.
func TestForgePRGlyphsSingleWidth(t *testing.T) {
	var glyphs []string
	for _, p := range []github.PullRequest{
		{State: github.PROpen}, {State: github.PROpen, Draft: true},
		{State: github.PRMerged}, {State: github.PRClosed},
	} {
		glyphs = append(glyphs, prGlyph(p))
	}
	for _, c := range []github.CheckState{github.ChecksPassing, github.ChecksFailing, github.ChecksRunning, github.ChecksNone} {
		glyphs = append(glyphs, checkGlyph(c))
	}
	for _, r := range []github.ReviewDecision{github.ReviewApproved, github.ReviewChangesRequested, github.ReviewRequired, github.ReviewNone} {
		glyphs = append(glyphs, reviewGlyph(r))
	}
	want := []string{"●", "◌", "◆", "✕", "✓", "✗", "◷", "·", "✔", "±", "?", "·"}
	for i, g := range glyphs {
		if g != want[i] {
			t.Fatalf("glyph %d = %q, want %q", i, g, want[i])
		}
		for name, w := range map[string]int{
			"runewidth": runewidth.StringWidth(g),
			"lipgloss":  lipgloss.Width(g),
			"ansi":      ansi.StringWidth(g),
		} {
			if w != 1 {
				t.Fatalf("glyph %q is %d cells by %s, want 1", g, w, name)
			}
		}
	}
}

// TestForgePRRowLinkedSession: a PR linked to a session shows ◉ and the
// session's title; several show the first and how many more. A link to
// the same number in another repo does not count.
func TestForgePRRowLinkedSession(t *testing.T) {
	a := &session.Instance{ID: "a", Title: "fix login", GroupPath: "widgets", ProjectPath: "/src/widgets"}
	b := &session.Instance{ID: "b", Title: "review", GroupPath: "widgets", ProjectPath: "/src/widgets"}
	c := &session.Instance{ID: "c", Title: "other repo", GroupPath: "widgets", ProjectPath: "/src/widgets"}
	a.SetLink(session.LinkPR, session.Link{Host: "github.com", Owner: "me", Name: "widgets", Number: 15048})
	b.SetLink(session.LinkPR, session.Link{Host: "github.com", Owner: "Me", Name: "Widgets", Number: 15048})
	c.SetLink(session.LinkPR, session.Link{Host: "github.com", Owner: "acme", Name: "widgets", Number: 14927})
	// An Issue link with a PR's number is not a PR link.
	c.SetLink(session.LinkIssue, session.Link{Host: "github.com", Owner: "me", Name: "widgets", Number: 14927})
	h, _ := prsHome(t, 120, 30, githubtest.PRList, a, b, c)
	pane := stripAnsi(h.renderForgeList(200, 40))
	if !strings.Contains(pane, "#15048 · dependabot · 30m  ✓ ci  ? review  ◉ fix login +1") {
		t.Fatalf("pane lacks the linked sessions of #15048:\n%s", pane)
	}
	if strings.Count(pane, "◉") != 1 {
		t.Fatalf("◉ on more than #15048:\n%s", pane)
	}
}

// TestForgeAutoLinks: the table of PR head-branch auto-detect. A session is
// linked to a PR when its worktree branch is the PR's head branch in the
// repository the session's Project resolves to; never twice.
func TestForgeAutoLinks(t *testing.T) {
	widgets := github.Repo{Host: "github.com", Owner: "me", Name: "widgets"}
	fork := github.Repo{Host: "github.com", Owner: "zoe", Name: "widgets"}
	pr := func(n int, branch string, head github.Repo) github.PullRequest {
		return github.PullRequest{Number: n, State: github.PROpen, HeadBranch: branch, HeadRepo: head}
	}
	link := func(n int) session.Link {
		return session.Link{Host: "github.com", Owner: "me", Name: "widgets", Number: n}
	}
	cases := []struct {
		name  string
		prs   []github.PullRequest
		cands []forgeLinkCandidate
		want  []forgeAutoLink
	}{
		{
			name:  "same branch same Project links",
			prs:   []github.PullRequest{pr(1, "feat/x", widgets)},
			cands: []forgeLinkCandidate{{id: "s1", branch: "feat/x", repo: widgets}},
			want:  []forgeAutoLink{{id: "s1", link: link(1)}},
		},
		{
			name:  "Project compared without case",
			prs:   []github.PullRequest{pr(1, "feat/x", widgets)},
			cands: []forgeLinkCandidate{{id: "s1", branch: "feat/x", repo: github.Repo{Host: "GitHub.com", Owner: "Me", Name: "Widgets"}}},
			want:  []forgeAutoLink{{id: "s1", link: link(1)}},
		},
		{
			name:  "other branch does not link",
			prs:   []github.PullRequest{pr(1, "feat/x", widgets)},
			cands: []forgeLinkCandidate{{id: "s1", branch: "feat/y", repo: widgets}},
		},
		{
			name:  "branch names are case-sensitive",
			prs:   []github.PullRequest{pr(1, "feat/x", widgets)},
			cands: []forgeLinkCandidate{{id: "s1", branch: "Feat/X", repo: widgets}},
		},
		{
			name:  "same branch name in a fork PR does not link a Project session",
			prs:   []github.PullRequest{pr(1, "main", fork)},
			cands: []forgeLinkCandidate{{id: "s1", branch: "main", repo: widgets}},
		},
		{
			name:  "a session in the fork links to the fork's PR",
			prs:   []github.PullRequest{pr(1, "fix", fork)},
			cands: []forgeLinkCandidate{{id: "s1", branch: "fix", repo: fork}},
			want:  []forgeAutoLink{{id: "s1", link: link(1)}},
		},
		{
			name:  "another Project's session does not link",
			prs:   []github.PullRequest{pr(1, "feat/x", widgets)},
			cands: []forgeLinkCandidate{{id: "s1", branch: "feat/x", repo: github.Repo{Host: "github.com", Owner: "me", Name: "gadgets"}}},
		},
		{
			name:  "another host does not link",
			prs:   []github.PullRequest{pr(1, "feat/x", widgets)},
			cands: []forgeLinkCandidate{{id: "s1", branch: "feat/x", repo: github.Repo{Host: "ghe.example.com", Owner: "me", Name: "widgets"}}},
		},
		{
			name:  "deleted head repository does not link",
			prs:   []github.PullRequest{pr(1, "feat/x", github.Repo{})},
			cands: []forgeLinkCandidate{{id: "s1", branch: "feat/x", repo: widgets}},
		},
		{
			name: "two PRs from one branch link the session once, to the newest",
			prs:  []github.PullRequest{pr(2, "feat/x", widgets), pr(1, "feat/x", widgets)},
			cands: []forgeLinkCandidate{
				{id: "s1", branch: "feat/x", repo: widgets},
			},
			want: []forgeAutoLink{{id: "s1", link: link(2)}},
		},
		{
			name: "one PR links every session on its branch",
			prs:  []github.PullRequest{pr(1, "feat/x", widgets)},
			cands: []forgeLinkCandidate{
				{id: "s1", branch: "feat/x", repo: widgets},
				{id: "s2", branch: "feat/x", repo: widgets},
			},
			want: []forgeAutoLink{{id: "s1", link: link(1)}, {id: "s2", link: link(1)}},
		},
		{
			name: "a session listed twice is linked once",
			prs:  []github.PullRequest{pr(1, "feat/x", widgets)},
			cands: []forgeLinkCandidate{
				{id: "s1", branch: "feat/x", repo: widgets},
				{id: "s1", branch: "feat/x", repo: widgets},
			},
			want: []forgeAutoLink{{id: "s1", link: link(1)}},
		},
		{
			name:  "empty branch never links",
			prs:   []github.PullRequest{pr(1, "", widgets)},
			cands: []forgeLinkCandidate{{id: "s1", branch: "", repo: widgets}},
		},
		{
			name:  "closed PR does not link",
			prs:   []github.PullRequest{{Number: 1, State: github.PRClosed, HeadBranch: "feat/x", HeadRepo: widgets}},
			cands: []forgeLinkCandidate{{id: "s1", branch: "feat/x", repo: widgets}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := forgeAutoLinks(widgets, c.prs, c.cands)
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// prListWith is a PRs list of me/widgets with one PR per head branch, in
// order, each from me/widgets.
func prListWith(branches ...string) github.PRList {
	l := github.PRList{Total: len(branches)}
	for i, b := range branches {
		l.PRs = append(l.PRs, github.PullRequest{Number: 100 + i, Title: "pr " + b, State: github.PROpen,
			Author: "me", UpdatedAt: prsT0.Add(-time.Hour), HeadBranch: b, HeadRepo: widgetsRepo})
	}
	return l
}

// TestForgePRsAutoLink: when the PRs list arrives, a worktree session whose
// branch heads a PR of the same Project is linked to it and shows ◉ on the
// row. Sessions of another Project, without a worktree, remote over SSH, or
// already linked to a PR are left alone, and a second arrival adds nothing.
func TestForgePRsAutoLink(t *testing.T) {
	match := worktreeSession("m", "on feat", "/src/widgets", "feat/x")
	other := worktreeSession("o", "other project", "/src/upstream", "feat/x")
	plain := &session.Instance{ID: "p", Title: "plain", GroupPath: "widgets", ProjectPath: "/src/widgets"}
	ssh := worktreeSession("r", "remote", "/src/widgets", "feat/x")
	ssh.SSHHost = "box"
	linked := worktreeSession("l", "linked", "/src/widgets", "feat/x")
	keep := session.Link{Host: "github.com", Owner: "me", Name: "widgets", Number: 7}
	linked.SetLink(session.LinkPR, keep)

	h, _ := prsHome(t, 120, 30, githubtest.PRList, match, other, plain, ssh, linked)
	h = drive(t, h, forgeFetchedMsg{key: widgetsPRs, value: prListWith("main", "feat/x")})

	want := session.Link{Host: "github.com", Owner: "me", Name: "widgets", Number: 101}
	if l := match.LinkOf(session.LinkPR); l == nil || *l != want {
		t.Fatalf("matching session link = %v, want %v", l, want)
	}
	for _, inst := range []*session.Instance{other, plain, ssh} {
		if l := inst.LinkOf(session.LinkPR); l != nil {
			t.Fatalf("session %q linked to %v", inst.Title, l)
		}
	}
	if l := linked.LinkOf(session.LinkPR); l == nil || *l != keep {
		t.Fatalf("an existing PR link changed to %v", l)
	}
	pane := stripAnsi(h.renderForgeList(200, 40))
	if !strings.Contains(pane, "#101 · me · 1h  · ci  · review  ◉ on feat") {
		t.Fatalf("row of #101 lacks ◉ on feat:\n%s", pane)
	}

	_ = drive(t, h, forgeFetchedMsg{key: widgetsPRs, value: prListWith("feat/x")})
	if l := match.LinkOf(session.LinkPR); l == nil || *l != want {
		t.Fatalf("a second list relinked the session to %v", l)
	}
}

// TestForgePRsAutoLinkOffRenderPath: the list's arrival does not link in
// the update itself: resolving the sessions' Projects runs in a command,
// and View never resolves.
func TestForgePRsAutoLinkOffRenderPath(t *testing.T) {
	match := worktreeSession("m", "on feat", "/src/widgets", "feat/x")
	h, _ := prsHome(t, 120, 30, githubtest.PRList, match)
	model, cmd := h.Update(forgeFetchedMsg{key: widgetsPRs, value: prListWith("feat/x")})
	h = model.(*Home)
	_ = h.View()
	if match.LinkOf(session.LinkPR) != nil {
		t.Fatal("linked before the auto-detect command ran")
	}
	if cmd == nil {
		t.Fatal("no auto-detect command")
	}
	_ = runCmds(t, h, cmd)
	if match.LinkOf(session.LinkPR) == nil {
		t.Fatal("not linked after the auto-detect command ran")
	}
}

// TestForgePRsAutoLinkNoCandidates: with no worktree session to link, the
// list's arrival starts no command.
func TestForgePRsAutoLinkNoCandidates(t *testing.T) {
	plain := &session.Instance{ID: "p", Title: "plain", GroupPath: "widgets", ProjectPath: "/src/widgets"}
	h, _ := prsHome(t, 120, 30, githubtest.PRList, plain)
	// The new list moves the selection to another PR, whose detail opens:
	// that fetch is not what this test is about.
	delete(h.forge.fetchers, freshness.Detail)
	if _, cmd := h.Update(forgeFetchedMsg{key: widgetsPRs, value: prListWith("feat/x")}); cmd != nil {
		t.Fatal("a command started with nothing to link")
	}
}

// TestForgePRsOpenCopyRow: o opens the selected PR's URL.
func TestForgePRsOpenCopyRow(t *testing.T) {
	h, _ := prsHome(t, 120, 30, githubtest.PRList)
	var opened []string
	h.forge.openURL = func(u string) error { opened = append(opened, u); return nil }
	h = drive(t, h, runeKey('j'))
	_ = drive(t, h, runeKey('o'))
	if len(opened) != 1 || opened[0] != "https://github.com/cli/cli/pull/14927" {
		t.Fatalf("opened %v, want the second PR's URL", opened)
	}
}

// TestForgePRsGolden freezes the PRs list (one row linked to a session)
// and an empty list at dual, stacked and single widths.
func TestForgePRsGolden(t *testing.T) {
	for _, sz := range forgeFrameSizes {
		for _, name := range []string{"rows", "none"} {
			golden := fmt.Sprintf("forge_prs_%dx%d_%s", sz[0], sz[1], name)
			t.Run(golden, func(t *testing.T) {
				a := &session.Instance{ID: "a", Title: "fix login", GroupPath: "widgets", ProjectPath: "/src/widgets"}
				a.SetLink(session.LinkPR, session.Link{Host: "github.com", Owner: "me", Name: "widgets", Number: 14927})
				h, _ := prsHome(t, sz[0], sz[1], githubtest.PRList, a)
				if name == "none" {
					h = drive(t, h, forgeFetchedMsg{key: widgetsPRs, value: github.PRList{}})
				}
				frame := h.View()
				if got := len(frameLines(frame)); got != sz[1] {
					t.Fatalf("frame has %d rows, want %d", got, sz[1])
				}
				assertFrameGolden(t, golden+".golden", stripAnsi(frame))
			})
		}
	}
}
