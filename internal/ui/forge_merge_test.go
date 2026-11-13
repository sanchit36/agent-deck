package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/gitstate"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

// The merge flow (spec #18 §14) at the model level: the fake runner serves
// the merge dialog's query and `gh pr merge`; no test merges anything real.

const (
	cleanSHA = "f2b0c5d5e51351be27ca04e8b27f6be552d8de1e"
	movedSHA = "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567"
)

func mergeInfoQueryArgs(number int) []string {
	return []string{"api", "--hostname", "github.com", "graphql",
		"-f", "query=" + github.MergeInfoQuery, "-f", "owner=me", "-f", "name=widgets",
		"-F", fmt.Sprintf("number=%d", number)}
}

// serveMergeInfo answers the merge dialog's query for me/widgets#number.
func serveMergeInfo(fake *githubtest.Fake, number int, f githubtest.Fixture) {
	fake.Serve(f, mergeInfoQueryArgs(number)...)
}

func isMergeCall(args []string) bool { return len(args) > 1 && args[0] == "pr" && args[1] == "merge" }

// serveMerge answers every `gh pr merge` with f.
func serveMerge(fake *githubtest.Fake, f githubtest.Fixture) {
	fake.ServeFunc(f, isMergeCall)
}

func mergeCalls(fake *githubtest.Fake) [][]string {
	var out [][]string
	for _, c := range fake.Calls() {
		if isMergeCall(c.Args) {
			out = append(out, c.Args)
		}
	}
	return out
}

func mergeInfoCalls(fake *githubtest.Fake, number int) int {
	n := 0
	for _, c := range fake.Calls() {
		if slices.Equal(c.Args, mergeInfoQueryArgs(number)) {
			n++
		}
	}
	return n
}

func prListCalls(fake *githubtest.Fake) int {
	n := 0
	for _, c := range fake.Calls() {
		if slices.Contains(c.Args, "query="+github.PRListQuery) {
			n++
		}
	}
	return n
}

// mergeHome is the PRs tab on its first row, #15048, with the merge
// dialog's query of #15048 answered by fixture (none when empty) and no
// wait before the UNKNOWN refetch.
func mergeHome(t *testing.T, w, h int, fixture string, instances ...*session.Instance) (*Home, *githubtest.Fake) {
	t.Helper()
	home, fake := prsHome(t, w, h, githubtest.PRList, instances...)
	if fixture != "" {
		serveMergeInfo(fake, 15048, githubtest.MustLoad(t, fixture))
	}
	home.forge.mergeRetry = -1
	return home, fake
}

func screen(h *Home) string { return stripAnsi(h.View()) }

func keyLeft() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyLeft} }
func keyRight() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRight} }
func keySpace() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}} }

// TestForgeMergeOpensWithRefetch: m on a PR row refetches the PR and opens
// the dialog: title, number, base ← head, the short head SHA, the checks
// and review lines, the allowed methods with the viewer's default chosen,
// the Delete branch toggle at the repository's setting and GitHub's
// default commit title.
func TestForgeMergeOpensWithRefetch(t *testing.T) {
	h, fake := mergeHome(t, 120, 30, githubtest.MergeInfoClean)
	h = drive(t, h, runeKey('m'))
	if !h.forgeMergeVisible() {
		t.Fatal("m did not open the merge dialog")
	}
	if n := mergeInfoCalls(fake, 15048); n != 1 {
		t.Fatalf("%d merge info fetches, want 1", n)
	}
	s := screen(h)
	for _, want := range []string{
		"Merge PR #15048",
		"chore(deps): Bump golang.org/x/term from 0.47.0 to 0.48.0",
		"trunk ← dependabot/go_modules/golang.org/x/term-0.48.0",
		"f2b0c5d",
		"✓ 12 passed · 18 skipped",
		"✔ approved",
		"◀ squash ▶",
		"[ ] Delete branch",
		"chore(deps): Bump golang.org/x/term from 0.47.0 to 0.48.0 (#15048)",
		"y merge",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("dialog lacks %q:\n%s", want, s)
		}
	}
	if len(mergeCalls(fake)) != 0 {
		t.Fatal("opening the dialog merged")
	}
}

// TestForgeMergeSmoke: y merges with the chosen method pinned to the head
// SHA the dialog shows, closes the dialog, toasts and refreshes the list
// at once.
func TestForgeMergeSmoke(t *testing.T) {
	h, fake := mergeHome(t, 120, 30, githubtest.MergeInfoClean)
	serveMerge(fake, githubtest.MustLoad(t, githubtest.OK))
	h = drive(t, h, runeKey('m'))
	before := prListCalls(fake)
	h = drive(t, h, runeKey('y'))
	want := []string{"pr", "merge", "15048", "--squash", "--match-head-commit", cleanSHA, "-R", "github.com/me/widgets"}
	if got := mergeCalls(fake); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("merge calls %q, want one %q", got, want)
	}
	if h.forgeMergeVisible() {
		t.Fatal("the dialog stayed open after a merge")
	}
	if h.err == nil || h.err.Error() != "Merged #15048 (squash)" {
		t.Fatalf("toast = %v", h.err)
	}
	if prListCalls(fake) != before+1 {
		t.Fatalf("list fetched %d times after the merge, want one more than %d", prListCalls(fake), before)
	}
}

// TestForgeMergeMethodAndBranch: ←/→ cycle the allowed methods and space
// toggles Delete branch; both reach gh.
func TestForgeMergeMethodAndBranch(t *testing.T) {
	h, fake := mergeHome(t, 120, 30, githubtest.MergeInfoClean)
	serveMerge(fake, githubtest.MustLoad(t, githubtest.OK))
	h = drive(t, h, runeKey('m'))
	h = drive(t, h, keyRight())
	h = drive(t, h, keySpace())
	if s := screen(h); !strings.Contains(s, "◀ rebase ▶") || !strings.Contains(s, "[x] Delete branch") {
		t.Fatalf("dialog:\n%s", s)
	}
	h = drive(t, h, runeKey('y'))
	want := []string{"pr", "merge", "15048", "--rebase", "--match-head-commit", cleanSHA, "--delete-branch", "-R", "github.com/me/widgets"}
	if got := mergeCalls(fake); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("merge calls %q, want %q", got, want)
	}
	if h.err == nil || h.err.Error() != "Merged #15048 (rebase)" {
		t.Fatalf("toast = %v", h.err)
	}
}

// TestForgeMergeHeadMoved: a head that moved since the dialog read it is
// never merged. The dialog stays open, refreshes to the new head and asks
// to review and confirm again; only the next y merges, pinned to the new
// head.
func TestForgeMergeHeadMoved(t *testing.T) {
	h, fake := mergeHome(t, 120, 30, "")
	var infos atomic.Int32
	fake.ServeFunc(githubtest.MustLoad(t, githubtest.MergeInfoClean), func(args []string) bool {
		return slices.Equal(args, mergeInfoQueryArgs(15048)) && infos.Add(1) == 1
	})
	serveMergeInfo(fake, 15048, githubtest.MustLoad(t, githubtest.MergeInfoMoved))
	var merges atomic.Int32
	fake.ServeFunc(githubtest.MustLoad(t, githubtest.MergeHeadMoved), func(args []string) bool {
		return isMergeCall(args) && merges.Add(1) == 1
	})
	serveMerge(fake, githubtest.MustLoad(t, githubtest.OK))

	h = drive(t, h, runeKey('m'))
	h = drive(t, h, runeKey('y'))
	if !h.forgeMergeVisible() {
		t.Fatal("the dialog closed although the head moved")
	}
	s := screen(h)
	if !strings.Contains(s, "PR changed since you looked: review and confirm again") || !strings.Contains(s, "0a1b2c3") {
		t.Fatalf("dialog after the head moved:\n%s", s)
	}
	if h.err != nil && strings.HasPrefix(h.err.Error(), "Merged") {
		t.Fatalf("toast %v for a refused merge", h.err)
	}
	h = drive(t, h, runeKey('y'))
	got := mergeCalls(fake)
	if len(got) != 2 || !slices.Contains(got[0], cleanSHA) || !slices.Contains(got[1], movedSHA) {
		t.Fatalf("merge calls %q: want the shown head, then the new one", got)
	}
	if h.forgeMergeVisible() {
		t.Fatal("the dialog stayed open after the confirmed merge")
	}
}

// TestForgeMergeNoConfirmWhileRefreshing: y while the dialog is still
// reading the PR does nothing: there is no head to pin the merge to yet.
func TestForgeMergeNoConfirmWhileRefreshing(t *testing.T) {
	h, fake := mergeHome(t, 120, 30, githubtest.MergeInfoClean)
	serveMerge(fake, githubtest.MustLoad(t, githubtest.OK))
	model, _ := h.Update(runeKey('m')) // the fetch never runs
	h = drive(t, model.(*Home), runeKey('y'))
	if len(mergeCalls(fake)) != 0 || h.forge.merge.running {
		t.Fatal("y merged before the dialog had read the PR")
	}
}

// TestForgeMergeBlocked: a blocked PR shows why and y does nothing; no
// admin bypass is offered.
func TestForgeMergeBlocked(t *testing.T) {
	h, fake := mergeHome(t, 120, 30, "")
	serveMergeInfo(fake, 14927, githubtest.MustLoad(t, githubtest.MergeInfoDraft))
	serveMerge(fake, githubtest.MustLoad(t, githubtest.OK))
	h = drive(t, h, runeKey('j'))
	h = drive(t, h, runeKey('m'))
	h = drive(t, h, runeKey('y'))
	if len(mergeCalls(fake)) != 0 {
		t.Fatal("a draft was merged")
	}
	s := screen(h)
	if !strings.Contains(s, "draft: mark it ready on GitHub") || strings.Contains(s, "admin") {
		t.Fatalf("dialog:\n%s", s)
	}
	if !h.forgeMergeVisible() {
		t.Fatal("y closed the blocked dialog")
	}
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEsc})
	if h.forgeMergeVisible() {
		t.Fatal("esc did not close the dialog")
	}
}

// TestForgeMergeAutoMerge: a PR blocked only on review enables auto-merge
// with the chosen method, pinned to the shown head.
func TestForgeMergeAutoMerge(t *testing.T) {
	h, fake := mergeHome(t, 120, 30, githubtest.MergeInfoBlockedReview)
	serveMerge(fake, githubtest.MustLoad(t, githubtest.OK))
	h = drive(t, h, runeKey('m'))
	s := screen(h)
	if !strings.Contains(s, "y enable auto-merge") || strings.Contains(s, "Delete branch") {
		t.Fatalf("dialog:\n%s", s)
	}
	h = drive(t, h, runeKey('y'))
	want := []string{"pr", "merge", "15048", "--auto", "--merge", "--match-head-commit", cleanSHA, "-R", "github.com/me/widgets"}
	if got := mergeCalls(fake); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("merge calls %q, want %q", got, want)
	}
	if h.err == nil || h.err.Error() != "Auto-merge enabled for #15048 (merge)" {
		t.Fatalf("toast = %v", h.err)
	}
}

// TestForgeMergeUnknownRefetchesOnce: GitHub still computing mergeability
// (UNKNOWN) is refetched once, then shown as blocked.
func TestForgeMergeUnknownRefetchesOnce(t *testing.T) {
	h, fake := mergeHome(t, 120, 30, "")
	f := githubtest.MustLoad(t, githubtest.MergeInfoClean)
	f.Result.Stdout = []byte(strings.Replace(string(f.Result.Stdout), `"CLEAN"`, `"UNKNOWN"`, 1))
	serveMergeInfo(fake, 15048, f)
	serveMerge(fake, githubtest.MustLoad(t, githubtest.OK))
	h = drive(t, h, runeKey('m'))
	h = drive(t, h, runeKey('y'))
	if n := mergeInfoCalls(fake, 15048); n != 2 {
		t.Fatalf("%d merge info fetches, want 2 (one refetch)", n)
	}
	if len(mergeCalls(fake)) != 0 || !strings.Contains(screen(h), "GitHub is still checking") {
		t.Fatalf("dialog:\n%s", screen(h))
	}
}

// TestForgeMergeFailureStaysOpen: a refused merge keeps the dialog open
// with gh's reason, without gh's --admin hint.
func TestForgeMergeFailureStaysOpen(t *testing.T) {
	h, fake := mergeHome(t, 120, 30, githubtest.MergeInfoClean)
	serveMerge(fake, githubtest.MustLoad(t, githubtest.MergeNotMergeable))
	h = drive(t, h, runeKey('m'))
	h = drive(t, h, runeKey('y'))
	s := screen(h)
	// The reason wraps in the dialog; read it as words.
	words := strings.Join(strings.Fields(strings.ReplaceAll(s, "│", " ")), " ")
	if !h.forgeMergeVisible() || !strings.Contains(words, "the base branch policy prohibits the merge") || strings.Contains(s, "admin") {
		t.Fatalf("dialog:\n%s", s)
	}
}

// TestForgeMergeLocalWarning: the dialog reads the PR's local checkouts
// fresh and shows a yellow ⚠ when one is unpushed or dirty.
func TestForgeMergeLocalWarning(t *testing.T) {
	h, _ := mergeHome(t, 120, 30, githubtest.MergeInfoClean)
	var reads atomic.Int32
	h.forge.localRead = func(_ context.Context, q gitstate.PRQuery) gitstate.PRLocal {
		reads.Add(1)
		if q.SHA != cleanSHA {
			t.Errorf("local read for head %q, want the dialog's", q.SHA)
		}
		return gitstate.PRLocal{Checkouts: []gitstate.Checkout{{Path: "/wt/s1", Branch: "term-bump",
			Compare: gitstate.Compare{Kind: gitstate.Unpushed, Ahead: 2}, Dirty: true}}}
	}
	h = drive(t, h, runeKey('m'))
	if reads.Load() == 0 {
		t.Fatal("the dialog did not read the local checkouts")
	}
	if s := screen(h); !strings.Contains(s, "⚠ local term-bump: ↑2 unpushed · dirty") {
		t.Fatalf("dialog:\n%s", s)
	}
}

// TestForgeMergeOnlyOnPRs: m on the Issues tab does nothing.
func TestForgeMergeOnlyOnPRs(t *testing.T) {
	h, fake := detailIssuesHome(t, 120, 30)
	h = drive(t, h, runeKey('m'))
	if h.forgeMergeVisible() {
		t.Fatal("m opened the merge dialog on an Issue")
	}
	for _, c := range fake.Calls() {
		if slices.Contains(c.Args, "query="+github.MergeInfoQuery) {
			t.Fatal("m fetched merge info on an Issue")
		}
	}
}

// TestForgeMergeFromOverlay: m in the overlay of a PR opens the dialog
// over it for that PR.
func TestForgeMergeFromOverlay(t *testing.T) {
	h, fake := overlayPRHome(t, 120, 30)
	serveMergeInfo(fake, 14927, githubtest.MustLoad(t, githubtest.MergeInfoDraft))
	h = openOverlay(t, h)
	h = drive(t, h, runeKey('m'))
	if !h.forgeMergeVisible() || !strings.Contains(screen(h), "Merge PR #14927") {
		t.Fatalf("overlay m:\n%s", screen(h))
	}
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEsc})
	if h.forgeMergeVisible() || !h.forgeOverlayVisible() {
		t.Fatal("esc should close the dialog back to the overlay")
	}
}

// linkedWorktree is a worktree session linked to me/widgets PR number.
func linkedWorktree(id, title string, number int) *session.Instance {
	inst := worktreeSession(id, title, "/src/widgets", "term-bump")
	inst.SetLink(session.LinkPR, session.Link{Host: "github.com", Owner: "me", Name: "widgets", Number: number})
	return inst
}

type sessionActs struct{ stopped, deleted []string }

// followHome merges #15048, which has the linked worktree sessions s1
// (dirty) and s2 (clean), and records what the follow-up does to them.
func followHome(t *testing.T) (*Home, *sessionActs) {
	t.Helper()
	return followHomeAt(t, 120, 30)
}

func followHomeAt(t *testing.T, w, ht int) (*Home, *sessionActs) {
	t.Helper()
	h, fake := mergeHome(t, w, ht, githubtest.MergeInfoClean,
		linkedWorktree("s1", "bump term", 15048), linkedWorktree("s2", "review bump", 15048))
	serveMerge(fake, githubtest.MustLoad(t, githubtest.OK))
	acts := &sessionActs{}
	h.forge.mergeAct = func(inst *session.Instance, del bool) tea.Cmd {
		if del {
			acts.deleted = append(acts.deleted, inst.ID)
		} else {
			acts.stopped = append(acts.stopped, inst.ID)
		}
		return nil
	}
	h.gitStateFetch = func(_ context.Context, path string) gitstate.State {
		return gitstate.State{Dirty: path == "/wt/s1"}
	}
	h = drive(t, h, runeKey('m'))
	h = drive(t, h, runeKey('y'))
	if !h.forgeMergeFollowVisible() {
		t.Fatalf("no follow-up for linked sessions:\n%s", screen(h))
	}
	s := screen(h)
	for _, want := range []string{"Merged #15048 (squash)", "bump term", "review bump", "Keep all", "Stop all", "Delete all"} {
		if !strings.Contains(s, want) {
			t.Fatalf("follow-up lacks %q:\n%s", want, s)
		}
	}
	return h, acts
}

// TestForgeMergeFollowUp: after a direct merge with linked sessions, the
// follow-up offers Keep all (the default), Stop all and Delete all; Delete
// skips dirty worktrees unless confirmed separately.
func TestForgeMergeFollowUp(t *testing.T) {
	t.Run("keep all is the default", func(t *testing.T) {
		h, acts := followHome(t)
		h = drive(t, h, enterKey())
		if h.forgeMergeFollowVisible() || len(acts.stopped)+len(acts.deleted) != 0 {
			t.Fatalf("keep all acted: %+v", acts)
		}
	})
	t.Run("stop all", func(t *testing.T) {
		h, acts := followHome(t)
		h = drive(t, h, keyRight())
		h = drive(t, h, enterKey())
		if h.forgeMergeFollowVisible() || !slices.Equal(acts.stopped, []string{"s1", "s2"}) || len(acts.deleted) != 0 {
			t.Fatalf("stop all: %+v", acts)
		}
	})
	t.Run("delete all keeps a dirty worktree", func(t *testing.T) {
		h, acts := followHome(t)
		h = drive(t, h, keyRight())
		h = drive(t, h, keyRight())
		h = drive(t, h, enterKey())
		if !slices.Equal(acts.deleted, []string{"s2"}) {
			t.Fatalf("deleted %v before the dirty one was confirmed, want only s2", acts.deleted)
		}
		s := screen(h)
		if !h.forgeMergeFollowVisible() || !strings.Contains(s, "uncommitted changes") || !strings.Contains(s, "bump term") {
			t.Fatalf("no separate confirmation for the dirty worktree:\n%s", s)
		}
		h = drive(t, h, runeKey('n'))
		if h.forgeMergeFollowVisible() || !slices.Equal(acts.deleted, []string{"s2"}) {
			t.Fatalf("n: deleted %v", acts.deleted)
		}
	})
	t.Run("a dirty worktree is deleted only when confirmed", func(t *testing.T) {
		h, acts := followHome(t)
		h = drive(t, h, keyLeft()) // wraps to Delete all
		h = drive(t, h, enterKey())
		h = drive(t, h, runeKey('y'))
		if h.forgeMergeFollowVisible() || !slices.Equal(acts.deleted, []string{"s2", "s1"}) {
			t.Fatalf("y: deleted %v", acts.deleted)
		}
	})
}

// TestForgeMergeFollowUpUnreadableIsDirty: a worktree that is not provably
// clean (unreadable, a jj workspace, not a repository) is protected like a
// dirty one and needs its own y.
func TestForgeMergeFollowUpUnreadableIsDirty(t *testing.T) {
	for name, st := range map[string]gitstate.State{
		"unreadable":   {Err: fmt.Errorf("git status timed out")},
		"jj workspace": {Repo: "/wt/s1", JJ: true},
		"not a repo":   {NotRepo: true},
	} {
		t.Run(name, func(t *testing.T) {
			h, acts := followHome(t)
			h.gitStateFetch = func(context.Context, string) gitstate.State { return st }
			h = drive(t, h, keyLeft())
			h = drive(t, h, enterKey())
			if len(acts.deleted) != 0 || !strings.Contains(screen(h), "uncommitted changes") {
				t.Fatalf("deleted %v without confirmation", acts.deleted)
			}
		})
	}
}

// TestForgeMergeNoFollowUpWithoutLinks: a merge with no linked sessions
// only toasts.
func TestForgeMergeNoFollowUpWithoutLinks(t *testing.T) {
	h, fake := mergeHome(t, 120, 30, githubtest.MergeInfoClean)
	serveMerge(fake, githubtest.MustLoad(t, githubtest.OK))
	h = drive(t, h, runeKey('m'))
	h = drive(t, h, runeKey('y'))
	if h.forgeMergeFollowVisible() {
		t.Fatal("follow-up without linked sessions")
	}
}

// TestForgeMergeGolden freezes the merge dialog (ready, blocked) and the
// linked-sessions follow-up at dual, stacked and single widths.
func TestForgeMergeGolden(t *testing.T) {
	for _, sz := range forgeFrameSizes {
		for _, c := range []string{"ready", "blocked", "followup"} {
			name := fmt.Sprintf("forge_merge_%dx%d_%s", sz[0], sz[1], c)
			t.Run(name, func(t *testing.T) {
				var h *Home
				switch c {
				case "ready":
					h, _ = mergeHome(t, sz[0], sz[1], githubtest.MergeInfoClean)
					h.forge.localRead = func(context.Context, gitstate.PRQuery) gitstate.PRLocal {
						return gitstate.PRLocal{Checkouts: []gitstate.Checkout{{Path: "/wt/s1", Branch: "term-bump", Dirty: true,
							Compare: gitstate.Compare{Kind: gitstate.InSync}}}}
					}
					h = drive(t, h, runeKey('m'))
				case "blocked":
					var fake *githubtest.Fake
					h, fake = mergeHome(t, sz[0], sz[1], "")
					serveMergeInfo(fake, 14927, githubtest.MustLoad(t, githubtest.MergeInfoDraft))
					h = drive(t, h, runeKey('j'))
					h = drive(t, h, runeKey('m'))
				case "followup":
					h, _ = followHomeAt(t, sz[0], sz[1])
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
