package ui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/desknotify"
	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/github/signals"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// signalBadgeSnapshot is the poll the badge goldens draw: acme/app#12
// closed, and ghe.corp.example/team/svc PR#34 failing with changes
// requested (the links of linkBadgeRows).
func signalBadgeSnapshot() *signals.Snapshot {
	app := github.Repo{Host: "github.com", Owner: "acme", Name: "app"}
	svc := github.Repo{Host: "ghe.corp.example", Owner: "team", Name: "svc"}
	return &signals.Snapshot{
		Items: map[signals.Target]signals.Item{
			{Repo: app, Number: 12}:           {Found: true, State: signals.StateClosed},
			{Repo: svc, Number: 34, PR: true}: {Found: true, PR: true, State: signals.StateOpen, Checks: signals.ChecksFailing, Review: signals.ReviewChanges},
		},
	}
}

// TestSignalBadgeRowsGolden freezes the live badge (spec #18 §15) on
// classic session rows at three terminal widths, selected and not: each
// link label followed by its glyphs, yielding to the title when narrow.
func TestSignalBadgeRowsGolden(t *testing.T) {
	for _, terminalWidth := range []int{80, 120, 200} {
		t.Run(fmt.Sprint(terminalWidth), func(t *testing.T) {
			h := NewHome()
			h.width, h.height = terminalWidth, 40
			h.forge.signals.snap.Store(signalBadgeSnapshot())
			width := h.sessionsPaneWidth()
			var b strings.Builder
			rows := linkBadgeRows()
			snapshot := map[string]sessionRenderState{}
			for _, inst := range rows {
				snapshot[inst.ID] = sessionRenderState{status: session.StatusIdle, tool: "claude", title: inst.Title}
			}
			for i, inst := range rows {
				item := session.Item{Type: session.ItemTypeSession, Session: inst, Level: 1, IsLastInGroup: i == len(rows)-1}
				h.renderSessionItem(&b, item, false, snapshot, width)
				h.renderSessionItem(&b, item, true, snapshot, width)
			}
			got := fmt.Sprintf("terminal_width=%d list_width=%d\n%s", terminalWidth, width, stripAnsi(b.String()))
			assertLinkGolden(t, fmt.Sprintf("signals_%d.txt", terminalWidth), got)
		})
	}
}

// TestSignalBadgeEmbeddedCardGolden: the embedded sidebar card carries the
// live badge on its metadata line.
func TestSignalBadgeEmbeddedCardGolden(t *testing.T) {
	h := NewHome()
	h.width, h.height = 120, 40
	h.embeddedLayout = true
	h.forge.signals.snap.Store(signalBadgeSnapshot())
	var b strings.Builder
	for _, inst := range linkBadgeRows() {
		state := sessionRenderState{status: session.StatusIdle, tool: "claude", title: inst.Title}
		h.renderSessionItem(&b, session.Item{Type: session.ItemTypeSession, Session: inst, Level: 1}, false, map[string]sessionRenderState{inst.ID: state}, 40)
	}
	assertLinkGolden(t, "signals_embedded.txt", stripAnsi(b.String()))
}

func TestSignalBadgeText(t *testing.T) {
	inst := &session.Instance{ID: "x"}
	inst.SetLink(session.LinkIssue, session.Link{Host: "github.com", Owner: "acme", Name: "app", Number: 12})
	inst.SetLink(session.LinkPR, session.Link{Host: "ghe.corp.example", Owner: "team", Name: "svc", Number: 34})
	h := NewHome()
	if got := h.linkBadgeText(inst); got != "#12 PR#34" {
		t.Fatalf("poller off: %q, want the static badge", got)
	}
	h.forge.signals.snap.Store(signalBadgeSnapshot())
	if got := h.linkBadgeText(inst); got != "#12 ✓ PR#34 ✗±" {
		t.Fatalf("badge = %q", got)
	}
	if got := h.linkBadgeFallbacks(inst); !slices.Equal(got, []string{"PR#34 ✗±", "PR#34"}) {
		t.Fatalf("narrow fallbacks = %q", got)
	}
	// A host gh is not logged in to shows ⚠ on its badges only.
	warn := signalBadgeSnapshot()
	warn.Warn = map[string]bool{"ghe.corp.example": true}
	h.forge.signals.snap.Store(warn)
	if got := h.linkBadgeText(inst); got != "#12 ✓ PR#34 ⚠" {
		t.Fatalf("badge with a logged-out host = %q", got)
	}
}

// TestSignalBadgeRenderDoesNoIO covers the acceptance line: the session
// list draws the live badge from the published snapshot only, with no gh,
// git, file, config or lock on the render path.
func TestSignalBadgeRenderDoesNoIO(t *testing.T) {
	src, err := os.ReadFile("forge_signals.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []string{
		"func (h *Home) linkBadgeText(",
		"func (h *Home) linkBadgeFallbacks(",
		"func linkBadgeLabel(",
		"func (h *Home) reviewCountSegment(",
		"func (h *Home) prsReviewShare(",
	} {
		body := funcBody(t, string(src), fn)
		for _, banned := range []string{"exec.", "os.", "gitstate", "storage", "LoadUserConfig", "Fetch", "http", ".API(", "Poll(", "Lock()", "Settings()"} {
			if strings.Contains(body, banned) {
				t.Errorf("%s mentions %q: the row render path must only read the snapshot", fn, banned)
			}
		}
	}
}

// TestSignalsReviewCount: the header shows `⇠ n reviews` and the PRS chip
// the on-screen Project's share; neither shows with the poller off.
func TestSignalsReviewCount(t *testing.T) {
	h := forgeHome(t, 140, 30, false, nil, widgetsCheckout)
	if strings.Contains(stripAnsi(h.View()), "⇠") {
		t.Fatal("review count shown with the poller off")
	}
	h = drive(t, h, altKeyMsg('p'))
	h.forge.signals.snap.Store(&signals.Snapshot{Reviews: map[string]signals.HostReviews{
		"github.com": {Requests: []signals.ReviewRequest{
			{Repo: widgetsRepo, Number: 7, Title: "a"},
			{Repo: github.Repo{Host: "github.com", Owner: "acme", Name: "gadgets"}, Number: 81, Title: "b"},
		}},
	}})
	if got := stripAnsi(h.reviewCountSegment()); got != "⇠ 2 reviews" {
		t.Fatalf("header segment = %q", got)
	}
	view := stripAnsi(h.View())
	if !strings.Contains(view, "⇠ 2 reviews") {
		t.Fatalf("header has no review count:\n%s", view)
	}
	if !strings.Contains(view, "PRS ⇠1") {
		t.Fatalf("PRS chip has no share of the review requests:\n%s", view)
	}
}

// signalsRunner answers graphql with the fixture of the moment and records
// the calls.
type signalsRunner struct {
	mu      sync.Mutex
	fixture githubtest.Fixture
	n       int
}

func (r *signalsRunner) set(f githubtest.Fixture) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fixture = f
}

func (r *signalsRunner) Run(_ context.Context, call github.Call) (github.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(call.Args, "graphql") {
		return github.Result{ExitCode: 1}, nil
	}
	r.n++
	return r.fixture.Result, r.fixture.Err
}

type lockedClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *lockedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *lockedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func signalsWaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSignalsPollerEndToEnd drives the TUI's poller through a fake runner
// and clock: no notification on the first load, then each transition marks
// the linked session on the notification bar and raises one desktop
// notification; quitting joins the poller.
func TestSignalsPollerEndToEnd(t *testing.T) {
	h := forgeFrameHome(t, 120, 30, false, gateOn)
	runner := &signalsRunner{fixture: githubtest.MustLoad(t, githubtest.SignalsPoll)}
	clk := &lockedClock{now: freshT0}
	ticks := make(chan time.Time)
	h.forge.client = github.NewClient(runner)
	h.forge.now = clk.Now
	h.forge.signals.ticks = ticks
	var mu sync.Mutex
	var notes []desknotify.Notification
	h.forge.signals.notify = func(n desknotify.Notification) {
		mu.Lock()
		defer mu.Unlock()
		notes = append(notes, n)
	}
	h.notificationManager = session.NewNotificationManager(6, false, false)
	inst := &session.Instance{ID: "a", Title: "fix login", Tool: "claude", Status: session.StatusIdle}
	inst.SetLink(session.LinkIssue, session.Link{Host: "github.com", Owner: "acme", Name: "widgets", Number: 12})
	inst.SetLink(session.LinkPR, session.Link{Host: "github.com", Owner: "acme", Name: "widgets", Number: 34})
	h.instances = []*session.Instance{inst}

	h.startSignals()
	t.Cleanup(h.stopForge)
	poller := h.forge.signals.poller
	if poller == nil {
		t.Fatal("no poller with the gate on")
	}
	signalsWaitFor(t, "the first poll", func() bool { return h.forge.signals.snap.Load() != nil })
	if got := h.linkBadgeText(inst); got != "#12 PR#34" {
		t.Fatalf("baseline badge = %q", got)
	}
	mu.Lock()
	if len(notes) != 0 {
		t.Fatalf("first load notified: %+v", notes)
	}
	mu.Unlock()

	runner.set(githubtest.MustLoad(t, githubtest.SignalsPollChanged))
	clk.advance(time.Minute)
	ticks <- clk.Now()
	signalsWaitFor(t, "the transitions", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(notes) == 3
	})
	if got := h.linkBadgeText(inst); got != "#12 ✓ PR#34 ✗±" {
		t.Fatalf("badge after the change = %q", got)
	}
	mu.Lock()
	var details []string
	for _, n := range notes {
		details = append(details, n.Title()+": "+n.Detail)
	}
	mu.Unlock()
	want := []string{
		"fix login: PR#34 checks failing",
		"fix login: PR#34 changes requested",
		"GitHub: review requested: acme/gadgets#81 Speed up the gadget cache",
	}
	if !slices.Equal(details, want) {
		t.Fatalf("notifications = %q, want %q", details, want)
	}

	// The session is marked on the bar until the user looks at it.
	h.notificationManager.SyncFromInstances(h.instances, "")
	if bar := h.notificationManager.FormatBar(); !strings.Contains(bar, "± fix login") {
		t.Fatalf("bar = %q, want the session marked", bar)
	}
	h.clearSignalMark("a")
	h.notificationManager.SyncFromInstances(h.instances, "")
	if bar := h.notificationManager.FormatBar(); bar != "" {
		t.Fatalf("bar after a visit = %q, want empty", bar)
	}

	h.stopForge()
	if poller.Running() || h.forge.signals.poller != nil {
		t.Fatal("poller not joined on stop")
	}
	if h.forge.signals.snap.Load() != nil {
		t.Fatal("badges stay after the poller stopped")
	}
}

// TestSignalsPollProjectHosts: the hosts of the resolved Projects reach the
// poller, so a GHE host gets its review search before any session links an
// item there.
func TestSignalsPollProjectHosts(t *testing.T) {
	h := forgeFrameHome(t, 120, 30, false, gateOn)
	forgeShowRepo(h, github.Repo{Host: "ghe.example.com", Owner: "acme", Name: "tools"})
	h.refreshSignalLinks()
	if got := h.forge.signals.hosts(); !slices.Contains(got, "ghe.example.com") {
		t.Fatalf("hosts for the poller = %q, want ghe.example.com", got)
	}
}

// TestSignalsOffSwitch: [github] signals = false, the gate off, or
// refresh_interval = 0 start no poller; a Home that is not running (no
// Init) never polls.
func TestSignalsOffSwitch(t *testing.T) {
	for _, cfg := range []string{gateOff, gateOn + "signals = false\n", gateOn + "refresh_interval = 0\n"} {
		h := forgeFrameHome(t, 120, 30, false, cfg)
		h.startSignals()
		if h.forge.signals.poller != nil {
			h.stopForge()
			t.Fatalf("config %q started the poller", cfg)
		}
	}
	h := forgeFrameHome(t, 120, 30, false, gateOn)
	h.reloadForgeFromConfig()
	if h.forge.signals.poller != nil {
		h.stopForge()
		t.Fatal("a Home that never ran Init started the poller")
	}
}
