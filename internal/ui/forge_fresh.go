package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/clipboard"
	"github.com/asheshgoplani/agent-deck/internal/feedback"
	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Freshness (spec #18 §5): every forge view reads its data from one
// freshness.Cache. Fetches run in tea.Cmds and come back as forgeFetchedMsg,
// written under their own key; a view renders only the entry of the key it
// shows, so a late result never lands in the wrong view. Polling rides the
// main tick (no ticker of its own) and covers only the visible tab of the
// visible Project, while the TUI is in the foreground and no session is
// attached.

// forgeFetchFunc fetches one key's value through c. ctx is cancelled when
// the TUI quits.
type forgeFetchFunc func(ctx context.Context, c *github.Client, key freshness.Key) (any, error)

// defaultForgeFetchers are the fetchers by kind. The list, detail and diff
// slices add theirs here; a kind with no fetcher is never fetched.
func defaultForgeFetchers() map[freshness.Kind]forgeFetchFunc {
	return map[freshness.Kind]forgeFetchFunc{
		freshness.IssueList: fetchIssueList,
		freshness.PRList:    fetchPRList,
		freshness.Detail:    fetchDetail,
		// The overlay's Conversation tab (forge_overlay.go).
		freshness.Conversation: fetchConversation,
	}
}

// forgeFetchedMsg is one fetch's outcome, for key whatever view shows now.
type forgeFetchedMsg struct {
	key   freshness.Key
	value any
	err   error
}

// forgeURLDoneMsg reports an open-in-browser or copy of a URL.
type forgeURLDoneMsg struct {
	done string
	err  error
}

// forgeStopTimeout bounds how long quitting waits for forge work.
const forgeStopTimeout = 5 * time.Second

// forgeJobs runs the forge's gh work (Project resolution, fetches) under
// one context. stop cancels it and joins every job: performFinalShutdown
// calls it, so no forge goroutine outlives the TUI.
type forgeJobs struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	stopped bool
}

func newForgeJobs() *forgeJobs {
	ctx, cancel := context.WithCancel(context.Background())
	return &forgeJobs{ctx: ctx, cancel: cancel}
}

// begin registers a job and returns its context; false once stopped, and
// the job must not run.
func (j *forgeJobs) begin() (context.Context, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.stopped {
		return nil, false
	}
	j.wg.Add(1)
	return j.ctx, true
}

func (j *forgeJobs) end() { j.wg.Done() }

// stop cancels every job and waits up to timeout for them to return;
// false on timeout. Jobs that have not begun never will.
func (j *forgeJobs) stop(timeout time.Duration) bool {
	j.mu.Lock()
	j.stopped = true
	j.cancel()
	j.mu.Unlock()
	done := make(chan struct{})
	go func() {
		j.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// run wraps job as a tea.Cmd under the forge's context.
func (j *forgeJobs) run(job func(ctx context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, ok := j.begin()
		if !ok {
			return nil
		}
		defer j.end()
		return job(ctx)
	}
}

func (h *Home) forgeJobs() *forgeJobs {
	if h.forge.jobs == nil {
		h.forge.jobs = newForgeJobs()
	}
	return h.forge.jobs
}

// stopForge cancels and joins the forge's gh work (see forgeJobs) and the
// Attention signal poller.
func (h *Home) stopForge() {
	h.stopSignals()
	if h.forge.jobs != nil && !h.forge.jobs.stop(forgeStopTimeout) {
		uiLog.Warn("forge_jobs_stop_timeout")
	}
}

func (h *Home) forgeNow() time.Time {
	if h.forge.now != nil {
		return h.forge.now()
	}
	return time.Now()
}

func (h *Home) forgeClient() *github.Client {
	if h.forge.client == nil {
		h.forge.client = github.NewClient(nil)
	}
	return h.forge.client
}

func (h *Home) forgeCache() *freshness.Cache {
	if h.forge.fresh == nil {
		h.forge.fresh = freshness.New()
	}
	return h.forge.fresh
}

// forgeListKey is the cache key of the list on screen; false when no forge
// tab shows a Project.
func (h *Home) forgeListKey() (freshness.Key, bool) {
	if !h.forgeTabActive() {
		return freshness.Key{}, false
	}
	return h.forgeListKeyFor(h.forge.tab)
}

// forgeVisibleKeys are the keys on screen: the list, and the open detail
// once the detail pane sets forge.detail.
func (h *Home) forgeVisibleKeys() []freshness.Key {
	k, ok := h.forgeListKey()
	if !ok {
		return nil
	}
	keys := []freshness.Key{k}
	if h.forge.detail.Kind != "" {
		keys = append(keys, h.forge.detail)
	}
	if ck, open := h.forgeOverlayKey(); open {
		keys = append(keys, ck)
	}
	return keys
}

// forgePaused reports whether automatic fetches wait: no forge tab shows,
// a session is attached or has the keyboard, or the terminal is in the
// background.
func (h *Home) forgePaused() bool {
	return !h.forgeTabActive() || h.isAttaching.Load() || h.embeddedMode || h.forge.background
}

// forgeRefreshDue starts the fetches the visible keys are due for: a first
// open, a list older than the refresh interval, a retry after backoff.
func (h *Home) forgeRefreshDue() tea.Cmd {
	h.syncForgeLists()
	if h.forgePaused() {
		return nil
	}
	now := h.forgeNow()
	cmds := []tea.Cmd{h.syncForgeDetail(), h.forgePRLocalDue(false)}
	for _, k := range h.forgeVisibleKeys() {
		if h.forgeCache().Due(k, now, h.forge.interval) {
			cmds = append(cmds, h.forgeFetch(k))
		}
	}
	return tea.Batch(cmds...)
}

// forgeOnTick is the main tick's poll. It also hands the Attention signal
// poller the current links.
func (h *Home) forgeOnTick() tea.Cmd {
	h.signalsOnTick()
	return h.forgeRefreshDue()
}

// forgeManualRefresh is r: fetch the visible keys now, joining a fetch
// already in flight; nothing while the host is rate-limited. The poll timer
// restarts when the fetch lands.
func (h *Home) forgeManualRefresh() tea.Cmd {
	h.forgeExpirePRLocal()
	now := h.forgeNow()
	var cmds []tea.Cmd
	for _, k := range h.forgeVisibleKeys() {
		if _, limited := h.forgeCache().RateLimited(k.Repo.Host, now); limited {
			continue
		}
		cmds = append(cmds, h.forgeFetch(k))
	}
	return tea.Batch(cmds...)
}

// forgeFetch marks k in flight and returns the command that fetches it;
// nil when a fetch of k is already in flight or k's kind has no fetcher.
func (h *Home) forgeFetch(k freshness.Key) tea.Cmd {
	if h.forge.fetchers == nil {
		h.forge.fetchers = defaultForgeFetchers()
	}
	fetch := h.forge.fetchers[k.Kind]
	if fetch == nil || !h.forgeCache().Begin(k) {
		return nil
	}
	client := h.forgeClient()
	return h.forgeJobs().run(func(ctx context.Context) tea.Msg {
		v, err := fetch(ctx, client, k)
		return forgeFetchedMsg{key: k, value: v, err: err}
	})
}

// applyForgeFetched writes a result under its key and settles a filter
// submitted for it. A default PRs list that arrives starts Link
// auto-detect.
func (h *Home) applyForgeFetched(msg forgeFetchedMsg) tea.Cmd {
	h.forgeCache().Done(msg.key, msg.value, msg.err, h.forgeNow())
	h.forgeOverlayFetched(msg.key)
	if msg.err == nil {
		h.markForgeRowsSeen(msg.key, msg.value)
	}
	h.applyForgeFilterFetched(msg)
	h.syncForgeLists()
	open := h.syncForgeDetail()
	if _, ok := msg.value.(github.PRDetail); ok && msg.err == nil && msg.key == h.forge.detail {
		open = tea.Batch(open, h.forgePRLocalDue(true))
	}
	// Only the default list links sessions: a filtered one may hold
	// merged or closed PRs.
	if l, ok := msg.value.(github.PRList); ok && msg.err == nil && msg.key.Filter == github.DefaultFilter {
		return tea.Batch(open, h.forgeAutoLinkCmd(msg.key.Repo, l))
	}
	return open
}

// forgeVisibleEntry is the cache entry of the list on screen.
func (h *Home) forgeVisibleEntry() (freshness.Key, freshness.Entry, bool) {
	k, ok := h.forgeListKey()
	if !ok || h.forge.fresh == nil {
		return k, freshness.Entry{}, false
	}
	e, ok := h.forge.fresh.Get(k)
	return k, e, ok
}

// forgeListStale reports whether the list on screen is the last good data
// after a failed refetch: the list slices dim its rows.
func (h *Home) forgeListStale() bool {
	_, e, ok := h.forgeVisibleEntry()
	return ok && e.Stale()
}

var forgeSpinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// forgeFreshText is the Project line's right side (spec #18 §3): the open
// count when known, then the spinner while fetching, ⚠ stale after a failed
// refetch, or the data's age. Empty before the first fetch.
func (h *Home) forgeFreshText() string { return h.forgeFreshTextOf(true) }

// forgeFreshTextOf is forgeFreshText, without the age unless age is set
// (a narrow Project line keeps the filter chip whole first).
func (h *Home) forgeFreshTextOf(age bool) string {
	_, e, ok := h.forgeVisibleEntry()
	if !ok {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	var parts []string
	if e.HasData() && forgeCounted(e.Value) {
		parts = append(parts, dim.Render(h.forgeCountText()))
	}
	switch {
	case e.Fetching:
		parts = append(parts, lipgloss.NewStyle().Foreground(ColorCyan).Render(forgeSpinnerFrames[h.animationFrame%len(forgeSpinnerFrames)]))
	case e.Stale():
		parts = append(parts, lipgloss.NewStyle().Foreground(ColorYellow).Render("⚠ stale"))
	case e.HasData() && age:
		text := humanizeSince(h.forgeNow().Sub(e.FetchedAt))
		if len(parts) == 0 {
			text = "updated " + text
		}
		parts = append(parts, dim.Render(text))
	}
	return strings.Join(parts, dim.Render(" · "))
}

// renderForgeListBody is what the pane shows under the Project line when
// it has no list to show: loading, or the failure message. An error takes
// over the pane only when there is no cached list.
func (h *Home) renderForgeListBody(width int) []string {
	k, e, ok := h.forgeVisibleEntry()
	if !ok || e.HasData() {
		return nil
	}
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	if e.Err == nil {
		if e.Fetching {
			return []string{dim.Render("loading…")}
		}
		return nil
	}
	f := forgeFailureFor(e.Err, k, h.forgeNow())
	style := lipgloss.NewStyle().Foreground(ColorYellow)
	if f.empty {
		style = dim
	}
	var out []string
	for i, line := range f.lines {
		if i > 0 {
			style = dim
		}
		for _, w := range strings.Split(ansi.Wrap(line, max(1, width), ""), "\n") {
			out = append(out, style.Render(w))
		}
	}
	return out
}

// forgeFailure is the message a failed fetch shows in place of its view.
type forgeFailure struct {
	lines []string
	// url is the SAML authorize URL that o opens and y copies.
	url string
	// empty marks a normal empty state rather than a failure.
	empty bool
}

// forgeFailureFor is the message for err, the failure of k's fetch, by
// error class (spec #18 §5).
func forgeFailureFor(err error, k freshness.Key, now time.Time) forgeFailure {
	if errors.Is(err, github.ErrIssuesDisabled) {
		return forgeFailure{lines: []string{"Issues are disabled for " + repoLabel(k.Repo)}, empty: true}
	}
	var qe *github.QueryError
	if errors.As(err, &qe) {
		return forgeFailure{lines: []string{"⚠ GitHub rejected the filter: " + qe.Message, "/ edit · esc reset"}}
	}
	var ge *github.Error
	if !errors.As(err, &ge) {
		return forgeFailure{lines: []string{"⚠ " + err.Error()}}
	}
	host := ge.Host
	if host == "" {
		host = k.Repo.HostOrDefault()
	}
	login := "run gh auth login -h " + host
	switch ge.Class {
	case github.ClassGhMissing:
		return forgeFailure{lines: []string{"⚠ gh is not installed", "install it from https://cli.github.com"}}
	case github.ClassNotLoggedIn:
		return forgeFailure{lines: []string{"⚠ gh is not logged in to " + host, login}}
	case github.ClassBadToken:
		return forgeFailure{lines: []string{"⚠ GitHub rejected gh's token for " + host, login}}
	case github.ClassSAML:
		return forgeFailure{
			lines: []string{"⚠ SAML SSO: authorize gh for this organization", ge.AuthorizeURL, "o open · y copy"},
			url:   ge.AuthorizeURL,
		}
	case github.ClassUnknownHost:
		return forgeFailure{lines: []string{"⚠ gh has no login for " + host, login}}
	case github.ClassNetwork:
		return forgeFailure{lines: []string{"⚠ offline · retrying"}}
	case github.ClassRateLimited:
		if ge.Reset.After(now) {
			return forgeFailure{lines: []string{"⚠ rate-limited until " + ge.Reset.Local().Format("15:04")}}
		}
		return forgeFailure{lines: []string{"⚠ rate-limited · retrying"}}
	case github.ClassNotFound:
		if k.Kind == freshness.IssueList || k.Kind == freshness.PRList {
			res := github.Resolution{Repo: k.Repo, Reason: github.ReasonNotFound}
			return forgeFailure{lines: []string{"No GitHub project: " + res.Message()}, empty: true}
		}
		return forgeFailure{lines: []string{fmt.Sprintf("⚠ #%d not found in %s", k.Number, repoLabel(k.Repo))}}
	case github.ClassServer:
		return forgeFailure{lines: []string{"⚠ GitHub is not answering · retrying"}}
	}
	msg := ge.Message
	if msg == "" {
		msg = ge.Error()
	}
	return forgeFailure{lines: []string{"⚠ gh failed: " + msg}}
}

// forgeVisibleURL is the URL o and y act on: the selected row's, or the
// SAML authorize URL while its message shows.
func (h *Home) forgeVisibleURL() string {
	if r, ok := h.forgeSelectedRow(); ok {
		return r.url
	}
	k, e, ok := h.forgeVisibleEntry()
	if !ok || e.HasData() || e.Err == nil {
		return ""
	}
	return forgeFailureFor(e.Err, k, h.forgeNow()).url
}

// forgeOpenURL is o: open the visible URL in the browser, in a tea.Cmd.
func (h *Home) forgeOpenURL() tea.Cmd {
	url := h.forgeVisibleURL()
	if url == "" {
		return nil
	}
	open := h.forge.openURL
	if open == nil {
		open = feedback.NewSender().BrowserCmd
	}
	return h.forgeJobs().run(func(context.Context) tea.Msg {
		if err := open(url); err != nil {
			return forgeURLDoneMsg{err: fmt.Errorf("opening %s: %w", url, err)}
		}
		return forgeURLDoneMsg{done: "opened " + url}
	})
}

// forgeCopyURL is y: copy the visible URL, in a tea.Cmd.
func (h *Home) forgeCopyURL() tea.Cmd {
	url := h.forgeVisibleURL()
	if url == "" {
		return nil
	}
	cp := h.forge.copyURL
	if cp == nil {
		cp = func(s string) error {
			_, err := clipboard.Copy(s, tmux.GetTerminalInfo().SupportsOSC52)
			return err
		}
	}
	return h.forgeJobs().run(func(context.Context) tea.Msg {
		if err := cp(url); err != nil {
			return forgeURLDoneMsg{err: fmt.Errorf("copying %s: %w", url, err)}
		}
		return forgeURLDoneMsg{done: "copied " + url}
	})
}

func (h *Home) applyForgeURLDone(msg forgeURLDoneMsg) {
	if msg.err != nil {
		h.setError(msg.err)
		return
	}
	h.setError(errors.New(msg.done))
}
