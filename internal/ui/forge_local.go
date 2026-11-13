package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/gitstate"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PR detail local row (spec #18 §8): the checkouts of the PR head (linked
// sessions' first, then worktrees and branches of the Project's local repo
// that track it) compared with the head SHA, never fetching. The read runs
// as a forge job; the cache is keyed by the PR head SHA (and where it
// looks), so a redraw or the same head arriving again reads nothing. It
// is read again on a new head SHA, on reopening the PR, on r, or when the
// detail arrives after gitstate.TTL. Nothing polls it.

// prLocalKey is what a local read depends on.
type prLocalKey struct {
	root   string
	repo   github.Repo
	number int
	sha    string
	linked string
}

type prLocalEntry struct {
	value    gitstate.PRLocal
	readAt   time.Time
	read     bool
	fetching bool
}

// forgePRLocalReadMsg carries a finished local read.
type forgePRLocalReadMsg struct {
	key   prLocalKey
	value gitstate.PRLocal
}

// prLocalCacheMax bounds the cache: PRs looked at in one sitting.
const prLocalCacheMax = 32

// forgePRLocalQuery is the local read for PR d of repo, and its cache key.
func (h *Home) forgePRLocalQuery(repo github.Repo, d github.PRDetail) (gitstate.PRQuery, prLocalKey) {
	q := gitstate.PRQuery{
		Number:    d.Number,
		Branch:    d.HeadBranch,
		SHA:       d.HeadSHA,
		CrossRepo: d.HeadRepo.Owner != "" && !sameRepo(d.HeadRepo, repo),
		HeadOwner: d.HeadRepo.Owner,
	}
	if v := h.forgeView(); v != nil && v.selected < len(v.group.Projects) {
		q.Root = v.group.Projects[v.selected].Root
	}
	for _, inst := range h.forgeLinkedSessions(repo, session.LinkPR, d.Number) {
		if p := h.gitCheckoutPath(inst); p != "" {
			q.Linked = append(q.Linked, p)
		}
	}
	return q, prLocalKey{root: q.Root, repo: repo, number: d.Number, sha: d.HeadSHA, linked: strings.Join(q.Linked, "\x00")}
}

// forgePRLocal is the cached local state of PR d of repo; false before its
// first read. The merge flow reads it (PRLocal.Warn) for its ⚠.
func (h *Home) forgePRLocal(repo github.Repo, d github.PRDetail) (gitstate.PRLocal, bool) {
	_, key := h.forgePRLocalQuery(repo, d)
	e, ok := h.forge.local[key]
	return e.value, ok && e.read
}

// forgePRLocalCmd starts the local read of PR d of repo unless its cache
// entry is fresh or a read is in flight. force reads whenever none is in
// flight.
func (h *Home) forgePRLocalCmd(repo github.Repo, d github.PRDetail, force bool) tea.Cmd {
	q, key := h.forgePRLocalQuery(repo, d)
	now := h.forgeNow()
	e, ok := h.forge.local[key]
	if ok && (e.fetching || (!force && e.read && now.Sub(e.readAt) <= gitstate.TTL)) {
		return nil
	}
	if h.forge.local == nil {
		h.forge.local = map[prLocalKey]prLocalEntry{}
	}
	h.prunePRLocal()
	e.fetching = true
	h.forge.local[key] = e
	read := h.forge.localRead
	if read == nil {
		read = gitstate.ReadPRLocal
	}
	return h.forgeJobs().run(func(ctx context.Context) tea.Msg {
		return forgePRLocalReadMsg{key: key, value: read(ctx, q)}
	})
}

// prunePRLocal drops the oldest finished entries once the cache is full.
func (h *Home) prunePRLocal() {
	for len(h.forge.local) >= prLocalCacheMax {
		var oldest prLocalKey
		found := false
		for k, e := range h.forge.local {
			if e.fetching {
				continue
			}
			if !found || e.readAt.Before(h.forge.local[oldest].readAt) {
				oldest, found = k, true
			}
		}
		if !found {
			return
		}
		delete(h.forge.local, oldest)
	}
}

func (h *Home) applyForgePRLocalRead(msg forgePRLocalReadMsg) {
	if h.forge.local == nil {
		h.forge.local = map[prLocalKey]prLocalEntry{}
	}
	h.forge.local[msg.key] = prLocalEntry{value: msg.value, readAt: h.forgeNow(), read: true}
}

// forgeShownPRDetail is the open detail when it is a loaded PR.
func (h *Home) forgeShownPRDetail() (github.Repo, github.PRDetail, bool) {
	k := h.forge.detail
	if k.Kind == "" || h.forge.fresh == nil {
		return github.Repo{}, github.PRDetail{}, false
	}
	e, ok := h.forge.fresh.Get(k)
	if !ok || !e.HasData() {
		return github.Repo{}, github.PRDetail{}, false
	}
	d, ok := e.Value.(github.PRDetail)
	if !ok || d.Number != k.Number {
		return github.Repo{}, github.PRDetail{}, false
	}
	return k.Repo, d, true
}

// forgePRLocalDue reads the open PR's local state when it has none yet,
// or, after an arrival of its detail (arrived), when its entry expired.
func (h *Home) forgePRLocalDue(arrived bool) tea.Cmd {
	repo, d, ok := h.forgeShownPRDetail()
	if !ok {
		return nil
	}
	if !arrived {
		if _, key := h.forgePRLocalQuery(repo, d); h.forge.local[key].read || h.forge.local[key].fetching {
			return nil
		}
	}
	return h.forgePRLocalCmd(repo, d, false)
}

// forgeExpirePRLocal makes the next read of every PR's local state due:
// reopening a PR and r read it again.
func (h *Home) forgeExpirePRLocal() {
	for k, e := range h.forge.local {
		e.readAt = time.Time{}
		h.forge.local[k] = e
	}
}

// prLocalRow is the detail's local row for PR d of repo, from the cache.
func (h *Home) prLocalRow(repo github.Repo, d github.PRDetail) string {
	l, ok := h.forgePRLocal(repo, d)
	return prLocalValue(l, ok)
}

// prLocalValue is the local row's value: the first checkout's branch, its
// comparison and dirt, then +n more; none, unavailable, or checking…
// before the first read.
func prLocalValue(l gitstate.PRLocal, read bool) string {
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	warn := lipgloss.NewStyle().Foreground(ColorYellow)
	switch {
	case !read:
		return dim.Render("checking…")
	case len(l.Checkouts) == 0 && l.Err != nil:
		return warn.Render("unavailable")
	case len(l.Checkouts) == 0:
		return "none"
	}
	c := l.Checkouts[0]
	var parts []string
	if c.JJ || c.Err != nil {
		parts = append(parts, warn.Render("unavailable"))
	} else {
		parts = append(parts, lipgloss.NewStyle().Foreground(ColorCyan).Render(forgeOneLine(c.Branch)), prCompareText(c.Compare))
		if c.Dirty {
			parts = append(parts, warn.Render("dirty"))
		}
	}
	v := strings.Join(parts, dim.Render(" · "))
	if n := len(l.Checkouts) - 1; n > 0 {
		v += dim.Render(fmt.Sprintf("  +%d more", n))
	}
	return v
}

func prCompareText(c gitstate.Compare) string {
	switch c.Kind {
	case gitstate.InSync:
		return lipgloss.NewStyle().Foreground(ColorGreen).Render("in sync")
	case gitstate.Unpushed:
		return lipgloss.NewStyle().Foreground(ColorYellow).Render(fmt.Sprintf("↑%d unpushed", c.Ahead))
	case gitstate.Behind:
		return lipgloss.NewStyle().Foreground(ColorCyan).Render(fmt.Sprintf("↓%d behind", c.Behind))
	case gitstate.Diverged:
		return lipgloss.NewStyle().Foreground(ColorYellow).Render(fmt.Sprintf("↑%d ↓%d", c.Ahead, c.Behind))
	case gitstate.Newer:
		return lipgloss.NewStyle().Foreground(ColorCyan).Render("↓ PR has newer commits")
	}
	return lipgloss.NewStyle().Foreground(ColorYellow).Render("unavailable")
}
