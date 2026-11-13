package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Launch from an Issue or PR (spec #18 §13): n on a row, the chooser when
// the row has linked sessions, the prefilled NewDialog with its Template
// field, and the launch. gh is the fake runner; the agent start is
// replaced, so no tmux and no agent CLI run.

// serveItemText answers every launch text query for me/widgets with f.
func serveItemText(fake *githubtest.Fake, f githubtest.Fixture) {
	fake.ServeFunc(f, func(args []string) bool { return slices.Contains(args, "query="+github.ItemTextQuery) })
}

// launchHome is a gate-on Home with extra config on the Issues tab (or PRs
// with pr set, on PR #14927), the list and detail served, the launch text
// served from fixture text, and local as its checkouts.
func launchHome(t *testing.T, extra string, pr bool, local map[string]github.Local, text string, instances ...*session.Instance) (*Home, *githubtest.Fake) {
	t.Helper()
	fake := githubtest.NewFake()
	serveIssueList(fake, githubtest.MustLoad(t, githubtest.IssueList))
	servePRList(fake, githubtest.MustLoad(t, githubtest.PRList))
	serveDetail(fake, 14822, githubtest.MustLoad(t, githubtest.IssueDetail))
	serveDetail(fake, 14927, githubtest.MustLoad(t, githubtest.PRDetail))
	if text != "" {
		serveItemText(fake, githubtest.MustLoad(t, text))
	}
	home := forgeFrameHome(t, 120, 40, false, gateOn+extra)
	home.forge.resolver = github.NewResolver(github.NewClient(fake), forgeLocal(local))
	home.forge.client = github.NewClient(fake)
	home.forge.fetchers = defaultForgeFetchers()
	home.forge.now = func() time.Time { return issuesT0 }
	home.forge.interval = time.Minute
	home.forge.detailDelay = -1
	if instances != nil {
		setForgeInstances(home, instances)
	}
	if pr {
		home = launchDrive(t, home, altKeyMsg('p'))
		return launchDrive(t, home, runeKey('j')), fake
	}
	return launchDrive(t, home, altKeyMsg('i')), fake
}

// launchDrive is drive that also feeds back the launch's messages.
func launchDrive(t *testing.T, h *Home, msg tea.Msg) *Home {
	t.Helper()
	model, cmd := h.Update(msg)
	return launchCmds(t, model.(*Home), cmd)
}

func launchCmds(t *testing.T, h *Home, cmd tea.Cmd) *Home {
	t.Helper()
	if cmd == nil {
		return h
	}
	switch m := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			h = launchCmds(t, h, c)
		}
	case forgeGroupResolvedMsg, forgeParentResolvedMsg, forgeFetchedMsg, forgeAutoLinkedMsg, forgeDetailOpenMsg,
		forgeLaunchTextMsg, forgeLaunchedMsg, sessionCreatedMsg:
		model, next := h.Update(m)
		h = launchCmds(t, model.(*Home), next)
	}
	return h
}

func enterKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }

// focusTemplateField moves the dialog's focus to the Template field.
func focusTemplateField(t *testing.T, h *Home) {
	t.Helper()
	idx := h.newDialog.indexOf(focusTemplate)
	if idx < 0 {
		t.Fatalf("no Template field in %v", h.newDialog.focusTargets)
	}
	h.newDialog.focusIndex = idx
	h.newDialog.updateFocus()
}

// TestForgeLaunchIssueDialogPrefill: n on an Issue opens the NewDialog in
// the tab's group on the Project's checkout, worktree ticked on the issue
// branch, titled #n <title>, with the issue template filled from the
// detail and the fetched body and comments.
func TestForgeLaunchIssueDialogPrefill(t *testing.T) {
	h, fake := launchHome(t, "", false, widgetsCheckout, githubtest.ItemTextIssue)
	h = launchDrive(t, h, runeKey('n'))
	d := h.newDialog
	if !d.IsVisible() || d.forge == nil {
		t.Fatal("n did not open the launch dialog")
	}
	name, path, _, branch, wt := d.GetValuesWithWorktree()
	if !strings.HasPrefix(name, "#14822 gh run view") || len(name) > MaxNameLength {
		t.Fatalf("name %q", name)
	}
	if path != "/src/widgets" || d.GetSelectedGroup() != "widgets" {
		t.Fatalf("path %q group %q", path, d.GetSelectedGroup())
	}
	if !wt || branch != "feature/14822-gh-run-view-job-log-failed-fails-on" {
		t.Fatalf("worktree %v branch %q", wt, branch)
	}
	if got := d.forge.name(); got != "issue" {
		t.Fatalf("template %q, want the issue built-in", got)
	}
	msg := d.forge.message()
	for _, want := range []string{"#14822", "me/widgets", "bug, priority-3, gh-run", "Pressing n on an empty list crashes.", "@alice (2026-11-10)"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q:\n%s", want, msg)
		}
	}
	calls := 0
	for _, c := range fake.Calls() {
		if slices.Contains(c.Args, "query="+github.ItemTextQuery) {
			calls++
			if !slices.Contains(c.Args, "comments=true") || !slices.Contains(c.Args, "review=false") {
				t.Fatalf("text query args %v", c.Args)
			}
		}
	}
	if calls != 1 {
		t.Fatalf("%d launch text queries, want 1", calls)
	}
	view := stripAnsi(d.View())
	if !strings.Contains(view, "Template") || !strings.Contains(view, "issue") {
		t.Fatalf("dialog has no Template field:\n%s", view)
	}
}

// TestForgeLaunchPRDialogPrefill: a PR launch is titled PR#n, uses
// pr-review, and its branch field shows gh's head branch, read-only.
func TestForgeLaunchPRDialogPrefill(t *testing.T) {
	h, _ := launchHome(t, "", true, widgetsCheckout, githubtest.ItemTextPR)
	h = launchDrive(t, h, runeKey('n'))
	d := h.newDialog
	if !d.IsVisible() || d.forge == nil {
		t.Fatal("n did not open the launch dialog")
	}
	name, _, _, branch, wt := d.GetValuesWithWorktree()
	if !strings.HasPrefix(name, "PR#14927 Add guided") || !wt {
		t.Fatalf("name %q worktree %v", name, wt)
	}
	if branch != "williammartin-multiaccount-draft-08-evaluation" {
		t.Fatalf("branch %q", branch)
	}
	if d.forge.name() != "pr-review" {
		t.Fatalf("template %q", d.forge.name())
	}
	if !strings.Contains(d.forge.message(), "widget.go:42") {
		t.Fatalf("review comments missing:\n%s", d.forge.message())
	}
	// The branch is gh's: typing into it changes nothing.
	idx := d.indexOf(focusBranch)
	if idx < 0 {
		t.Fatal("no branch field")
	}
	d.focusIndex = idx
	d.updateFocus()
	h = launchDrive(t, h, runeKey('x'))
	if _, _, _, b, _ := h.newDialog.GetValuesWithWorktree(); b != branch {
		t.Fatalf("read-only branch became %q", b)
	}
}

// TestForgeLaunchChooser: a row with linked sessions opens the chooser
// first; enter attaches, n opens the dialog anyway, esc cancels.
func TestForgeLaunchChooser(t *testing.T) {
	linked := linkedTo("s1", "triage run", session.LinkIssue, 14822)
	setup := func(t *testing.T) (*Home, *[]string) {
		h, _ := launchHome(t, "", false, widgetsCheckout, githubtest.ItemTextIssue, linked)
		var attached []string
		h.forge.attach = func(id string) tea.Cmd { attached = append(attached, id); return nil }
		h = launchDrive(t, h, runeKey('n'))
		if h.newDialog.IsVisible() || h.forge.sessionPick == nil || !h.forge.sessionPick.launch {
			t.Fatal("n on a linked row did not open the chooser")
		}
		if pane := listPane(h); !strings.Contains(pane, "triage run") || !strings.Contains(pane, "n new ·") {
			t.Fatalf("chooser:\n%s", pane)
		}
		return h, &attached
	}
	t.Run("enter attaches", func(t *testing.T) {
		h, attached := setup(t)
		h = launchDrive(t, h, enterKey())
		if !slices.Equal(*attached, []string{"s1"}) || h.newDialog.IsVisible() {
			t.Fatalf("attached %v, dialog %v", *attached, h.newDialog.IsVisible())
		}
	})
	t.Run("n starts a new one", func(t *testing.T) {
		h, attached := setup(t)
		h = launchDrive(t, h, runeKey('n'))
		if len(*attached) != 0 || !h.newDialog.IsVisible() || h.forge.sessionPick != nil {
			t.Fatalf("attached %v, dialog %v", *attached, h.newDialog.IsVisible())
		}
	})
	t.Run("esc cancels", func(t *testing.T) {
		h, attached := setup(t)
		h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyEsc})
		if len(*attached) != 0 || h.newDialog.IsVisible() || h.forge.sessionPick != nil {
			t.Fatal("esc did not just close the chooser")
		}
	})
}

const bugTemplate = `
[prompt_templates.bug]
labels = ["bug"]
text = "Fix bug {number}: {title}"
`

// TestForgeLaunchTemplateCycle: the label match is chosen; ←/→ cycle the
// matches, then the rest, then none, wrapping.
func TestForgeLaunchTemplateCycle(t *testing.T) {
	h, _ := launchHome(t, bugTemplate, false, widgetsCheckout, githubtest.ItemTextIssue)
	h = launchDrive(t, h, runeKey('n'))
	f := h.newDialog.forge
	if f.name() != "bug" || !strings.HasPrefix(f.message(), "Fix bug 14822: gh run view") {
		t.Fatalf("template %q message %q", f.name(), f.message())
	}
	focusTemplateField(t, h)
	var seen []string
	for range 3 {
		h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyRight})
		seen = append(seen, h.newDialog.forge.name())
	}
	if !slices.Equal(seen, []string{"issue", "none", "bug"}) {
		t.Fatalf("→ cycled %v", seen)
	}
	h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyLeft})
	if h.newDialog.forge.name() != "none" || h.newDialog.forge.message() != "" {
		t.Fatalf("← gave %q, message %q", h.newDialog.forge.name(), h.newDialog.forge.message())
	}
}

// TestForgeLaunchEditedTemplate: ctrl+e's result becomes the message and
// the field reads "(edited)"; changing the template asks first.
func TestForgeLaunchEditedTemplate(t *testing.T) {
	h, _ := launchHome(t, "", false, widgetsCheckout, githubtest.ItemTextIssue)
	h = launchDrive(t, h, runeKey('n'))
	model, cmd := h.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	h = model.(*Home)
	if cmd == nil {
		t.Fatal("ctrl+e did not open the editor")
	}
	h = launchDrive(t, h, promptEditorMsg{text: "my own words"})
	f := h.newDialog.forge
	if f.message() != "my own words" || !strings.Contains(stripAnsi(h.newDialog.View()), "issue (edited)") {
		t.Fatalf("message %q\n%s", f.message(), stripAnsi(h.newDialog.View()))
	}
	focusTemplateField(t, h)
	h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyRight})
	if f.name() != "issue" || f.message() != "my own words" || !strings.Contains(stripAnsi(h.newDialog.View()), "discard") {
		t.Fatalf("the edit was dropped without asking: %q %q", f.name(), f.message())
	}
	launchDrive(t, h, tea.KeyMsg{Type: tea.KeyRight})
	if f.name() != "none" || f.edited {
		t.Fatalf("second → did not switch: %q edited %v", f.name(), f.edited)
	}
}

// TestForgeLaunchMissingTemplateFile: a template file that is gone is an
// error on the field, which opens its list; enter refuses to launch.
func TestForgeLaunchMissingTemplateFile(t *testing.T) {
	h, _ := launchHome(t, "\n[prompt_templates.bug]\nlabels = [\"bug\"]\nfile = \"/nonexistent/bug.md\"\n", false, widgetsCheckout, githubtest.ItemTextIssue)
	h = launchDrive(t, h, runeKey('n'))
	d := h.newDialog
	if d.currentTarget() != focusTemplate || !d.forge.open || d.forge.loadErr == nil {
		t.Fatalf("focus %v open %v err %v", d.currentTarget(), d.forge.open, d.forge.loadErr)
	}
	h = launchDrive(t, h, enterKey()) // closes the list
	h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !h.newDialog.IsVisible() || h.newDialog.forge.launching {
		t.Fatal("launched with a missing template file")
	}
}

// TestForgeLaunchCommentsFailureAsks: when the comments do not load,
// enter asks before launching with them empty.
func TestForgeLaunchCommentsFailureAsks(t *testing.T) {
	_, local := launchRepoCheckout(t)
	h, fake := launchHome(t, "", false, local, githubtest.Network)
	h = launchDrive(t, h, runeKey('n'))
	starts := 0
	h.forge.launcher = forgeLauncher{
		start:   func(*session.Instance, string) error { starts++; return nil },
		running: func(*session.Instance) bool { return true },
	}
	// Typing does not retry the failed fetch.
	h = launchDrive(t, h, runeKey('x'))
	h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyBackspace})
	queries := 0
	for _, c := range fake.Calls() {
		if slices.Contains(c.Args, "query="+github.ItemTextQuery) {
			queries++
		}
	}
	if queries != 1 {
		t.Fatalf("%d launch text queries, want 1 (no retry per key)", queries)
	}
	h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyCtrlS})
	if starts != 0 || !h.newDialog.IsVisible() || !strings.Contains(stripAnsi(h.newDialog.View()), "launch with them empty") {
		t.Fatalf("starts %d\n%s", starts, stripAnsi(h.newDialog.View()))
	}
	h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyCtrlS})
	if starts != 1 || h.newDialog.IsVisible() {
		t.Fatalf("the second enter did not launch: starts %d", starts)
	}
}

// launchRepoCheckout is a git repository at a temp path that resolves to
// me/widgets.
func launchRepoCheckout(t *testing.T) (string, map[string]github.Local) {
	repo := launchTestRepo(t)
	return repo, map[string]github.Local{"/src/widgets": checkout(repo, "git@github.com:me/widgets.git")}
}

// TestForgeLaunchFailureKeepsDialog: a failed start leaves no session and
// no worktree; the error shows in the dialog, which stays open.
func TestForgeLaunchFailureKeepsDialog(t *testing.T) {
	repo, local := launchRepoCheckout(t)
	h, _ := launchHome(t, "", false, local, githubtest.ItemTextIssue)
	h = launchDrive(t, h, runeKey('n'))
	h.forge.launcher = forgeLauncher{
		start:   func(*session.Instance, string) error { return errors.New("tmux refused") },
		running: func(*session.Instance) bool { return false },
	}
	before := len(h.instances)
	h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !h.newDialog.IsVisible() || !strings.Contains(stripAnsi(h.newDialog.View()), "tmux refused") {
		t.Fatalf("dialog closed or no error:\n%s", stripAnsi(h.newDialog.View()))
	}
	if len(h.instances) != before {
		t.Fatal("a failed launch added a session")
	}
	if n := worktreeCount(t, repo); n != 1 {
		t.Fatalf("%d worktrees after a failed launch", n)
	}
}

// TestForgeLaunchIssueSmoke: n, enter: the session starts in a new
// worktree on the issue branch with the filled template, Linked to the
// Issue, and the dialog closes.
func TestForgeLaunchIssueSmoke(t *testing.T) {
	repo, local := launchRepoCheckout(t)
	h, _ := launchHome(t, "", false, local, githubtest.ItemTextIssue)
	var sent []string
	h.forge.launcher = forgeLauncher{
		start:   func(_ *session.Instance, m string) error { sent = append(sent, m); return nil },
		running: func(*session.Instance) bool { return true },
	}
	h = launchDrive(t, h, runeKey('n'))
	h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyCtrlS})
	if h.newDialog.IsVisible() {
		t.Fatalf("dialog still open: %s", h.newDialog.validationErr)
	}
	inst := findLaunched(t, h, session.LinkIssue, 14822)
	if inst.WorktreeRepoRoot != repo || inst.WorktreeBranch != "feature/14822-gh-run-view-job-log-failed-fails-on" {
		t.Fatalf("worktree %q branch %q", inst.WorktreeRepoRoot, inst.WorktreeBranch)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "Pressing n on an empty list crashes.") {
		t.Fatalf("first message %q", sent)
	}
	if _, err := os.Stat(filepath.Join(inst.WorktreePath, "seed")); err != nil {
		t.Fatalf("worktree not checked out: %v", err)
	}
	if h.forgeLinkedSessions(github.Repo{Host: "github.com", Owner: "me", Name: "widgets"}, session.LinkIssue, 14822) == nil {
		t.Fatal("the row does not see its new session")
	}
}

// TestForgeLaunchPRSmoke: a PR launch checks the PR out with gh in a new
// worktree and Links the session to the PR.
func TestForgeLaunchPRSmoke(t *testing.T) {
	_, local := launchRepoCheckout(t)
	h, fake := launchHome(t, "", true, local, githubtest.ItemTextPR)
	fake.ServeFunc(githubtest.MustLoad(t, githubtest.OK), func(args []string) bool {
		return len(args) > 2 && args[0] == "pr" && args[1] == "checkout" && args[2] == "14927"
	})
	head := "williammartin-multiaccount-draft-08-evaluation"
	h.forge.launcher = forgeLauncher{
		client:  github.NewClient(checkoutGh{t: t, head: head, fake: fake}),
		start:   func(*session.Instance, string) error { return nil },
		running: func(*session.Instance) bool { return true },
	}
	h = launchDrive(t, h, runeKey('n'))
	h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyCtrlS})
	if h.newDialog.IsVisible() {
		t.Fatalf("dialog still open: %s", h.newDialog.validationErr)
	}
	inst := findLaunched(t, h, session.LinkPR, 14927)
	if inst.WorktreeBranch != head {
		t.Fatalf("branch %q", inst.WorktreeBranch)
	}
}

// TestForgeLaunchNoDuplicateWhileLaunching: enter again while a launch
// runs starts nothing more.
func TestForgeLaunchNoDuplicateWhileLaunching(t *testing.T) {
	h, _ := launchHome(t, "", false, widgetsCheckout, githubtest.ItemTextIssue)
	h = launchDrive(t, h, runeKey('n'))
	model, cmd := h.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	h = model.(*Home)
	if cmd == nil || !h.newDialog.forge.launching {
		t.Fatal("ctrl+s did not start the launch")
	}
	if _, again := h.Update(tea.KeyMsg{Type: tea.KeyCtrlS}); again != nil {
		t.Fatal("a second submit during the launch returned a command")
	}
}

func findLaunched(t *testing.T, h *Home, kind session.LinkKind, n int) *session.Instance {
	t.Helper()
	for _, inst := range h.instances {
		if l := inst.LinkOf(kind); l != nil && l.Number == n {
			return inst
		}
	}
	t.Fatalf("no session linked to %s %d", kind, n)
	return nil
}

// TestForgeLaunchTitleAndTool: the title fits the dialog's name limit in
// bytes and display width without cutting a rune; the tool falls back
// from the group's to default_tool to claude.
func TestForgeLaunchTitleAndTool(t *testing.T) {
	for _, title := range []string{"short", strings.Repeat("word ", 20), strings.Repeat("日本語", 20), "tab\there\nnewline"} {
		got := forgeLaunchTitle("#123", title)
		if len(got) > MaxNameLength || !strings.HasPrefix(got, "#123 ") || strings.ContainsAny(got, "\t\n") || !utf8.ValidString(got) {
			t.Fatalf("forgeLaunchTitle(%q) = %q (%d bytes)", title, got, len(got))
		}
	}
	tools := []struct{ group, def, want string }{
		{"codex", "gemini", "codex"},
		{"", "gemini", "gemini"},
		{"", " ", "claude"},
	}
	for _, tt := range tools {
		if got := forgeLaunchTool(tt.group, tt.def); got != tt.want {
			t.Fatalf("forgeLaunchTool(%q, %q) = %q, want %q", tt.group, tt.def, got, tt.want)
		}
	}
}

// TestForgeLaunchFromOverlay: n in the overlay launches on the overlay's
// item (not the list cursor's row) and the dialog replaces the overlay.
func TestForgeLaunchFromOverlay(t *testing.T) {
	h, fake := overlayIssueHome(t, 120, 30)
	serveItemText(fake, githubtest.MustLoad(t, githubtest.ItemTextIssue))
	h = openOverlay(t, h)
	k, _ := h.forgeListKey()
	e, _ := h.forge.fresh.Get(k)
	l := e.Value.(github.IssueList)
	l.Issues = append(slices.Clone(l.Issues[1:]), l.Issues[0])
	h = drive(t, h, forgeFetchedMsg{key: k, value: l})

	h = launchDrive(t, h, runeKey('n'))
	if !h.newDialog.IsVisible() || h.newDialog.forge == nil || h.newDialog.forge.number != 14822 {
		t.Fatalf("n in the overlay did not open the launch dialog for #14822: %+v", h.newDialog.forge)
	}
	if h.forgeOverlayVisible() {
		t.Fatal("the overlay stayed open under the dialog")
	}
}

// TestForgeLaunchDialogGolden freezes the launch dialog for an Issue and a
// PR, and the chooser, at dual, stacked and single widths.
func TestForgeLaunchDialogGolden(t *testing.T) {
	for _, sz := range forgeFrameSizes {
		for _, kind := range []string{"issue", "pr", "chooser"} {
			golden := fmt.Sprintf("forge_launch_%dx%d_%s", sz[0], sz[1], kind)
			t.Run(golden, func(t *testing.T) {
				var instances []*session.Instance
				if kind == "chooser" {
					instances = append(instances, linkedTo("s1", "triage run", session.LinkIssue, 14822))
				}
				text := githubtest.ItemTextIssue
				if kind == "pr" {
					text = githubtest.ItemTextPR
				}
				h, _ := launchHome(t, "", kind == "pr", widgetsCheckout, text, instances...)
				h = launchDrive(t, h, tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
				h = launchDrive(t, h, runeKey('n'))
				frame := h.View()
				if got := len(frameLines(frame)); got != sz[1] {
					t.Fatalf("frame has %d rows, want %d", got, sz[1])
				}
				assertFrameGolden(t, golden+".golden", stripAnsi(frame))
			})
		}
	}
}
