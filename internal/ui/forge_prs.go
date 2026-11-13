package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PRs list (spec #18 §3, §5, §11): rows add the check and review state to
// line 2, and each list that arrives links worktree sessions to the PR
// their branch heads, off the render path.

// fetchPRList is the PRs list fetcher: one GraphQL request.
func fetchPRList(ctx context.Context, c *github.Client, k freshness.Key) (any, error) {
	return c.PRList(ctx, k.Repo)
}

// prGlyph is a PR's state glyph: ● open, ◌ draft, ◆ merged, ✕ closed.
func prGlyph(p github.PullRequest) string {
	switch {
	case p.State == github.PRMerged:
		return "◆"
	case p.State == github.PRClosed:
		return "✕"
	case p.Draft:
		return "◌"
	}
	return "●"
}

func prGlyphColor(p github.PullRequest) lipgloss.TerminalColor {
	switch {
	case p.State == github.PRMerged:
		return ColorPurple
	case p.State == github.PRClosed:
		return ColorRed
	case p.Draft:
		return ColorTextDim
	}
	return ColorGreen
}

// checkGlyph is the overall check state: ✓ passing, ✗ failing, ◷ running,
// · none.
func checkGlyph(c github.CheckState) string {
	switch c {
	case github.ChecksPassing:
		return "✓"
	case github.ChecksFailing:
		return "✗"
	case github.ChecksRunning:
		return "◷"
	}
	return "·"
}

func checkGlyphColor(c github.CheckState) lipgloss.TerminalColor {
	switch c {
	case github.ChecksPassing:
		return ColorGreen
	case github.ChecksFailing:
		return ColorRed
	case github.ChecksRunning:
		return ColorYellow
	}
	return ColorTextDim
}

// reviewGlyph is the review decision: ✔ approved, ± changes requested,
// ? review required, · none.
func reviewGlyph(r github.ReviewDecision) string {
	switch r {
	case github.ReviewApproved:
		return "✔"
	case github.ReviewChangesRequested:
		return "±"
	case github.ReviewRequired:
		return "?"
	}
	return "·"
}

func reviewGlyphColor(r github.ReviewDecision) lipgloss.TerminalColor {
	switch r {
	case github.ReviewApproved:
		return ColorGreen
	case github.ReviewChangesRequested:
		return ColorRed
	case github.ReviewRequired:
		return ColorYellow
	}
	return ColorTextDim
}

// prRows are a PRs list's rows.
func prRows(l github.PRList, now time.Time) []forgeRow {
	rows := make([]forgeRow, 0, len(l.PRs))
	for _, p := range l.PRs {
		rows = append(rows, forgeRow{
			glyph:      prGlyph(p),
			glyphColor: prGlyphColor(p),
			title:      forgeOneLine(p.Title),
			meta:       fmt.Sprintf("#%d · %s · %s", p.Number, forgeOneLine(p.Author), forgeAge(now.Sub(p.UpdatedAt))),
			marks: []forgeMark{
				{glyph: checkGlyph(p.Checks), color: checkGlyphColor(p.Checks), label: "ci"},
				{glyph: reviewGlyph(p.Review), color: reviewGlyphColor(p.Review), label: "review"},
			},
			url:       p.URL,
			number:    p.Number,
			updatedAt: p.UpdatedAt,
			state:     prStateWord(p),
		})
	}
	return rows
}

// forgeLinkedTitles are the titles of the sessions linked to each item of
// repo's list of kind, by number: what ◉ shows on a row. It reads only the
// stored links (no network), so View may call it.
func (h *Home) forgeLinkedTitles(repo github.Repo, kind session.LinkKind) map[int][]string {
	h.instancesMu.RLock()
	defer h.instancesMu.RUnlock()
	var out map[int][]string
	for _, inst := range h.instances {
		if inst == nil {
			continue
		}
		l := inst.LinkOf(kind)
		if l == nil || !linkInRepo(*l, repo) {
			continue
		}
		if out == nil {
			out = map[int][]string{}
		}
		out[l.Number] = append(out[l.Number], inst.Title)
	}
	return out
}

// linkInRepo reports whether l points into repo (GitHub names are
// case-insensitive).
func linkInRepo(l session.Link, repo github.Repo) bool {
	return strings.EqualFold(l.Host, repo.HostOrDefault()) &&
		strings.EqualFold(l.Owner, repo.Owner) && strings.EqualFold(l.Name, repo.Name)
}

// sameRepo reports whether a and b name the same repository.
func sameRepo(a, b github.Repo) bool {
	return strings.EqualFold(a.HostOrDefault(), b.HostOrDefault()) &&
		strings.EqualFold(a.Owner, b.Owner) && strings.EqualFold(a.Name, b.Name)
}

// linkedText is the ◉ mark of a row: the first linked session's title, and
// how many more.
func linkedText(titles []string) string {
	if len(titles) == 0 {
		return ""
	}
	s := "◉ " + forgeOneLine(titles[0])
	if len(titles) > 1 {
		s += fmt.Sprintf(" +%d", len(titles)-1)
	}
	return s
}

// forgeLinkCandidate is a session auto-detect may link: its worktree
// branch, and the repository its Project resolves to.
type forgeLinkCandidate struct {
	id     string
	branch string
	repo   github.Repo
}

// forgeAutoLink is one Link auto-detect makes: session id to a PR.
type forgeAutoLink struct {
	id   string
	link session.Link
}

// forgeAutoLinks matches a PRs list of listRepo against candidate
// sessions (spec §5, §11): a session is linked to an open PR whose head
// branch is the session's worktree branch in the repository the session's
// Project resolves to (the Project itself, or the fork a PR into the
// Parent Repository comes from). Each session gets at most one Link: the
// most recently updated PR, the list's first. Pure; table-tested.
func forgeAutoLinks(listRepo github.Repo, prs []github.PullRequest, cands []forgeLinkCandidate) []forgeAutoLink {
	var out []forgeAutoLink
	done := map[string]bool{}
	for _, c := range cands {
		if c.branch == "" || done[c.id] {
			continue
		}
		for _, p := range prs {
			if p.State != github.PROpen || p.HeadBranch != c.branch || p.HeadRepo.Owner == "" || !sameRepo(p.HeadRepo, c.repo) {
				continue
			}
			done[c.id] = true
			out = append(out, forgeAutoLink{id: c.id, link: session.Link{
				Host: strings.ToLower(listRepo.HostOrDefault()), Owner: listRepo.Owner, Name: listRepo.Name, Number: p.Number,
			}})
			break
		}
	}
	return out
}

// forgeLinkSession is a session auto-detect may link, before its Project
// is resolved: one in a local git worktree with a branch and no PR link.
type forgeLinkSession struct {
	id, branch, path string
}

// forgeLinkSessions snapshots the sessions auto-detect may link.
func (h *Home) forgeLinkSessions() []forgeLinkSession {
	h.instancesMu.RLock()
	defer h.instancesMu.RUnlock()
	var out []forgeLinkSession
	for _, inst := range h.instances {
		if inst == nil || inst.PRLink != nil || inst.IsSSH() || !inst.IsWorktree() || inst.WorktreeBranch == "" {
			continue
		}
		p := inst.WorktreeRepoRoot
		if p == "" {
			p = inst.WorktreePath
		}
		out = append(out, forgeLinkSession{id: inst.ID, branch: inst.WorktreeBranch, path: p})
	}
	return out
}

// forgeAutoLinkedMsg carries the Links auto-detect found.
type forgeAutoLinkedMsg struct {
	links []forgeAutoLink
}

// forgeAutoLinkCmd runs auto-detect for a PRs list of repo that just
// arrived: it resolves the candidate sessions' Projects (git, maybe gh) in
// a forge job, never in View, and reports the Links to make. nil when no
// session could be linked.
func (h *Home) forgeAutoLinkCmd(repo github.Repo, l github.PRList) tea.Cmd {
	sessions := h.forgeLinkSessions()
	if len(sessions) == 0 || len(l.PRs) == 0 {
		return nil
	}
	resolver := h.forgeResolver()
	return h.forgeJobs().run(func(ctx context.Context) tea.Msg {
		cands := make([]forgeLinkCandidate, 0, len(sessions))
		for _, s := range sessions {
			res, err := resolver.Resolve(ctx, s.path)
			if err != nil || !res.Found() {
				continue
			}
			cands = append(cands, forgeLinkCandidate{id: s.id, branch: s.branch, repo: res.Repo})
		}
		links := forgeAutoLinks(repo, l.PRs, cands)
		if len(links) == 0 {
			return nil
		}
		return forgeAutoLinkedMsg{links: links}
	})
}

// applyForgeAutoLinked stores the Links auto-detect found, skipping any
// session that gained a PR link meanwhile, and saves.
func (h *Home) applyForgeAutoLinked(msg forgeAutoLinkedMsg) {
	changed := false
	h.instancesMu.Lock()
	for _, al := range msg.links {
		inst := h.instanceByID[al.id]
		if inst == nil || inst.PRLink != nil {
			continue
		}
		inst.SetLink(session.LinkPR, al.link)
		changed = true
	}
	h.instancesMu.Unlock()
	if changed {
		h.saveInstances()
	}
}
