package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/gitstate"
	tea "github.com/charmbracelet/bubbletea"
)

// TestForgeSmokeDefaultConfig is the end-to-end smoke of the shipped
// integration (spec #18 §0, slice 21): with no [github] section at all,
// the tab keys open Issues and PRs, a PR row's detail fills the right
// pane, enter opens the overlay on its Conversation and d moves it to the
// diff. Every gh call goes through the fake runner; the frames prove the
// recorded fixtures of each step landed (the PRs tab's first row, #15048,
// has no detail fixture and is never looked at).
func TestForgeSmokeDefaultConfig(t *testing.T) {
	fake := githubtest.NewFake()
	serveIssueList(fake, githubtest.MustLoad(t, githubtest.IssueList))
	servePRList(fake, githubtest.MustLoad(t, githubtest.PRList))
	serveDetail(fake, 14822, githubtest.MustLoad(t, githubtest.IssueDetail))
	serveDetail(fake, 14927, githubtest.MustLoad(t, githubtest.PRDetail))
	serveConversation(fake, 14927, githubtest.MustLoad(t, githubtest.PRRowConversation))
	serveDiff(fake, 14927, githubtest.MustLoad(t, githubtest.PRDiff))

	h := forgeFrameHome(t, 120, 30, false, "")
	if !h.forge.enabled {
		t.Fatal("no [github] section: the integration is off")
	}
	h.forge.resolver = github.NewResolver(github.NewClient(fake), forgeLocal(widgetsCheckout))
	h.forge.client = github.NewClient(fake)
	h.forge.fetchers = defaultForgeFetchers()
	h.forge.localRead = func(context.Context, gitstate.PRQuery) gitstate.PRLocal { return gitstate.PRLocal{} }
	h.forge.now = func() time.Time { return prsT0 }
	h.forge.interval = time.Minute
	h.forge.detailDelay = -1

	frame := func() string { return stripAnsi(h.View()) }
	expect := func(step string, wants ...string) {
		t.Helper()
		f := frame()
		for _, want := range wants {
			if !strings.Contains(f, want) {
				t.Fatalf("%s: frame lacks %q:\n%s", step, want, f)
			}
		}
	}

	expect("sessions", "SESSIONS │ ISSUES │ PRS", "fix login")

	// ] opens Issues: the list arrives and the first row's detail fills
	// the right pane.
	h = drive(t, h, runeKey(']'))
	if h.forge.tab != tabIssues {
		t.Fatalf("] = %v, want Issues", h.forge.tab)
	}
	expect("issues", "#14822 · henrymodisett", "ISSUE #14822", "state     ○ open")

	// ] again opens PRs; j selects #14927 and its detail replaces the
	// first row's.
	h = drive(t, h, runeKey(']'))
	if h.forge.tab != tabPRs {
		t.Fatalf("] = %v, want PRs", h.forge.tab)
	}
	h = drive(t, h, runeKey('j'))
	expect("pr detail", "PR #14927", "Add guided multi-account setup and evaluation")

	// enter opens the overlay on the PR's Conversation.
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	if !h.forgeOverlayVisible() || h.forge.overlay.tab != forgeOverlayConversation {
		t.Fatal("enter did not open the overlay on Conversation")
	}
	expect("overlay", "◌ #14927", "Conversation", "Files")
	if f := frame(); strings.Contains(f, "loading…") {
		t.Fatalf("overlay still loading after the fetch:\n%s", f)
	}

	// d moves to Files: the recorded diff's file list.
	h = drive(t, h, runeKey('d'))
	if h.forge.overlay.tab != forgeOverlayFiles {
		t.Fatal("d did not move the overlay to Files")
	}
	if n := diffCalls(fake, 14927); n != 1 {
		t.Fatalf("%d diff requests, want 1", n)
	}
	if f := frame(); strings.Contains(f, "loading…") {
		t.Fatalf("Files still loading after the fetch:\n%s", f)
	}

	// esc closes the overlay back to the list.
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEsc})
	if h.forgeOverlayVisible() || h.forge.tab != tabPRs {
		t.Fatal("esc did not close the overlay back to PRs")
	}
}
