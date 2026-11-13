package ui

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/desknotify"
	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/signals"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/charmbracelet/lipgloss"
)

// Attention signals (spec #18 §15): a poller of its own, started with the
// gate on and [github] signals not false, keeps the live state of every
// linked Issue and PR and the review requests for the user. It runs while
// the TUI runs, attached or not, so it does not ride the main tick (which
// stops while a session is attached). The render path only loads the
// published snapshot: no I/O, no lock.

// signalLink is one linked item and the session that links it.
type signalLink struct {
	target    signals.Target
	sessionID string
	title     string
}

// signalsState is the Attention signal part of forgeState.
type signalsState struct {
	// live is set by Init: only a running TUI polls (a Home a test builds
	// never does unless it asks).
	live   bool
	poller *signals.Poller
	// snap is the poller's latest snapshot, nil while it is off.
	snap atomic.Pointer[signals.Snapshot]
	// links are the linked items, refreshed on the UI goroutine and read
	// by the poller.
	links atomic.Pointer[[]signalLink]
	// projectHosts are the hosts of the resolved Projects, refreshed with
	// links: each gets its review search even with nothing linked there.
	projectHosts atomic.Pointer[[]string]
	// interval is [github] refresh_interval, for the poller goroutine.
	interval atomic.Int64

	mu sync.Mutex
	// marks are the sessions an Attention signal marked on the tmux
	// notification bar, until the user looks at them.
	marks map[string]session.NotificationMark

	// notify raises a desktop notification; nil means desknotify when
	// [notifications] desktop = true. Tests replace it.
	notify func(desknotify.Notification)
	// ticks drives the poller in tests; nil means its own ticker.
	ticks <-chan time.Time
}

// targetOf is the signals target of a session link.
func targetOf(kind session.LinkKind, l *session.Link) signals.Target {
	return signals.Target{
		Repo:   github.Repo{Host: l.Host, Owner: l.Owner, Name: l.Name},
		Number: l.Number,
		PR:     kind == session.LinkPR,
	}
}

// startSignals is Init's: the TUI runs, so the poller may.
func (h *Home) startSignals() {
	h.forge.signals.live = true
	cfg := loadForgeConfig()
	h.syncSignalsPoller(cfg)
}

// syncSignalsPoller starts or stops the poller to match the config.
func (h *Home) syncSignalsPoller(cfg *session.UserConfig) {
	s := &h.forge.signals
	s.interval.Store(int64(cfg.GitHubRefreshInterval()))
	want := s.live && cfg.GitHubSignals()
	switch {
	case want && s.poller == nil:
		h.refreshSignalLinks()
		s.poller = signals.New(signals.Config{
			Client:   h.forgeClient(),
			Cache:    h.forgeCache(),
			Targets:  s.targets,
			Hosts:    s.hosts,
			Interval: func() time.Duration { return time.Duration(s.interval.Load()) },
			Now:      h.forge.now,
			Ticks:    s.ticks,
			OnChange: h.applySignals,
		})
		s.poller.Start()
	case !want && s.poller != nil:
		h.stopSignals()
	}
}

// stopSignals stops and joins the poller and takes its badges and marks
// away.
func (h *Home) stopSignals() {
	s := &h.forge.signals
	if s.poller == nil {
		return
	}
	s.poller.Stop()
	s.poller = nil
	s.snap.Store(nil)
	s.mu.Lock()
	s.marks = nil
	s.mu.Unlock()
	if h.notificationManager != nil {
		h.notificationManager.SetMarks(nil)
	}
}

func (s *signalsState) targets() []signals.Target {
	links := s.links.Load()
	if links == nil {
		return nil
	}
	out := make([]signals.Target, 0, len(*links))
	for _, l := range *links {
		out = append(out, l.target)
	}
	return out
}

// refreshSignalLinks records the sessions' links for the poller. On the
// UI goroutine, from the main tick, while the poller runs.
func (h *Home) refreshSignalLinks() {
	var links []signalLink
	for _, inst := range h.instances {
		if inst == nil {
			continue
		}
		if l := inst.IssueLink; l != nil {
			links = append(links, signalLink{target: targetOf(session.LinkIssue, l), sessionID: inst.ID, title: inst.Title})
		}
		if l := inst.PRLink; l != nil {
			links = append(links, signalLink{target: targetOf(session.LinkPR, l), sessionID: inst.ID, title: inst.Title})
		}
	}
	h.forge.signals.links.Store(&links)
	var hosts []string
	for _, v := range h.forge.views {
		if v == nil {
			continue
		}
		for _, p := range v.group.Projects {
			if host := p.Repo.HostOrDefault(); !slices.Contains(hosts, host) {
				hosts = append(hosts, host)
			}
		}
	}
	slices.Sort(hosts)
	h.forge.signals.projectHosts.Store(&hosts)
}

// hosts are the resolved Projects' hosts, for the poller.
func (s *signalsState) hosts() []string {
	if p := s.projectHosts.Load(); p != nil {
		return *p
	}
	return nil
}

// signalsOnTick refreshes the links the poller watches.
func (h *Home) signalsOnTick() {
	if h.forge.signals.poller != nil {
		h.refreshSignalLinks()
	}
}

// applySignals publishes a poll's snapshot and acts on its transitions:
// each marks the linked sessions on the tmux notification bar and raises a
// desktop notification when [notifications] desktop = true. On the poller
// goroutine.
func (h *Home) applySignals(snap *signals.Snapshot, events []signals.Event) {
	s := &h.forge.signals
	s.snap.Store(snap)
	if len(events) == 0 {
		return
	}
	now := time.Now()
	if h.forge.now != nil {
		now = h.forge.now()
	}
	var links []signalLink
	if p := s.links.Load(); p != nil {
		links = *p
	}
	var notes []desknotify.Notification
	s.mu.Lock()
	if s.marks == nil {
		s.marks = map[string]session.NotificationMark{}
	}
	for _, e := range events {
		if e.Kind == signals.EventReviewRequested {
			notes = append(notes, desknotify.Notification{SessionTitle: "GitHub", Profile: h.profile, Detail: e.Text()})
			continue
		}
		for _, l := range links {
			if !sameTarget(l.target, e.Target) {
				continue
			}
			s.marks[l.sessionID] = session.NotificationMark{Glyph: e.Glyph(), At: now}
			notes = append(notes, desknotify.Notification{SessionTitle: l.title, Profile: h.profile, Detail: e.Text()})
		}
	}
	marks := maps.Clone(s.marks)
	s.mu.Unlock()
	if h.notificationManager != nil {
		h.notificationManager.SetMarks(marks)
	}
	for _, n := range notes {
		uiLog.Info("attention_signal", slog.String("session", n.SessionTitle), slog.String("event", n.Detail))
		h.notifyDesktop(n)
	}
}

func (h *Home) notifyDesktop(n desknotify.Notification) {
	if f := h.forge.signals.notify; f != nil {
		f(n)
		return
	}
	if !session.GetNotificationsSettings().GetDesktopEnabled() {
		return
	}
	desknotify.New().Notify(n)
}

// clearSignalMark drops a session's mark once the user is looking at it
// (attached, or jumped to from the bar).
func (h *Home) clearSignalMark(sessionID string) {
	s := &h.forge.signals
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	_, had := s.marks[sessionID]
	delete(s.marks, sessionID)
	marks := maps.Clone(s.marks)
	s.mu.Unlock()
	if had && h.notificationManager != nil {
		h.notificationManager.SetMarks(marks)
	}
}

func sameTarget(a, b signals.Target) bool {
	a.Repo.Host, b.Repo.Host = a.Repo.HostOrDefault(), b.Repo.HostOrDefault()
	return a == b
}

// ---- rendering (no I/O: reads the published snapshot only) ----

// linkBadgeText is a session row's link badge: the static `#12` / `PR#34`
// labels, each followed by its live glyphs while the poller runs.
func (h *Home) linkBadgeText(inst *session.Instance) string {
	snap := h.forge.signals.snap.Load()
	if snap == nil {
		return sessionLinkBadgeText(inst)
	}
	issue, pr := linkBadgeLabel(snap, session.LinkIssue, inst.IssueLink), linkBadgeLabel(snap, session.LinkPR, inst.PRLink)
	switch {
	case issue != "" && pr != "":
		return issue + " " + pr
	case issue != "":
		return issue
	}
	return pr
}

// linkBadgeFallbacks are the shorter forms of the link badge, longest
// first, for a column too narrow for linkBadgeText: with both links only
// the PR (with its glyphs, then without), with one link its bare label.
// With the poller off that is just the PR label of a session with both.
func (h *Home) linkBadgeFallbacks(inst *session.Instance) []string {
	snap := h.forge.signals.snap.Load()
	var out []string
	add := func(s string) {
		if s != "" && !slices.Contains(out, s) && s != h.linkBadgeText(inst) {
			out = append(out, s)
		}
	}
	switch {
	case inst.IssueLink != nil && inst.PRLink != nil:
		add(linkBadgeLabel(snap, session.LinkPR, inst.PRLink))
		add(inst.PRLink.Label(session.LinkPR))
	case inst.PRLink != nil:
		add(inst.PRLink.Label(session.LinkPR))
	case inst.IssueLink != nil:
		add(inst.IssueLink.Label(session.LinkIssue))
	}
	return out
}

// linkBadgeLabel is one link's label and live glyphs: "PR#34 ✗±".
func linkBadgeLabel(snap *signals.Snapshot, kind session.LinkKind, l *session.Link) string {
	if l == nil {
		return ""
	}
	label := l.Label(kind)
	if g := snap.Badge(targetOf(kind, l)); g != "" {
		return label + " " + g
	}
	return label
}

// reviewCountSegment is the header's `⇠ n reviews`, empty when there are
// none or the poller is off.
func (h *Home) reviewCountSegment() string {
	n := h.forge.signals.snap.Load().ReviewCount()
	if n == 0 {
		return ""
	}
	word := "reviews"
	if n == 1 {
		word = "review"
	}
	return lipgloss.NewStyle().Foreground(ColorPurple).Render(fmt.Sprintf("⇠ %d %s", n, word))
}

// prsReviewShare is the PRS chip's share of the review requests: those in
// the Project on screen.
func (h *Home) prsReviewShare() int {
	snap := h.forge.signals.snap.Load()
	if snap == nil {
		return 0
	}
	repo, ok := h.forgeView().shownRepo()
	if !ok {
		return 0
	}
	return snap.ReviewCountFor(repo)
}
