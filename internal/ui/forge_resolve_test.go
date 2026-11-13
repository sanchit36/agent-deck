package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

// changingLocal is a github.LocalReader whose checkouts can fail for a
// while; it counts reads.
type changingLocal struct {
	mu        sync.Mutex
	checkouts map[string]github.Local
	fail      error
	reads     int
}

func (c *changingLocal) read(_ context.Context, path string) (github.Local, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	if c.fail != nil {
		return github.Local{}, c.fail
	}
	l, ok := c.checkouts[path]
	if !ok {
		return github.Local{}, fmt.Errorf("reading %s: %w", path, github.ErrNotGit)
	}
	return l, nil
}

func (c *changingLocal) set(fail error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fail = fail
}

func (c *changingLocal) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func forgeProjectLine(h *Home) string { return frameLines(h.View())[h.getListContentStartY()] }

// TestForgeRefreshReResolvesProject: r resolves the group's Project again,
// asking gh about hosts afresh, so a GHE host found "unknown" shows its
// Project once `gh auth login` knows it, without a restart.
func TestForgeRefreshReResolvesProject(t *testing.T) {
	var mu sync.Mutex
	loggedIn := false
	isLoggedIn := func() bool { mu.Lock(); defer mu.Unlock(); return loggedIn }
	isAuth := func(args []string) bool { return len(args) > 1 && args[0] == "auth" && args[1] == "status" }
	fake := githubtest.NewFake().
		ServeFunc(githubtest.MustLoad(t, githubtest.AuthStatusNoHost), func(args []string) bool { return isAuth(args) && !isLoggedIn() }).
		ServeFunc(githubtest.MustLoad(t, githubtest.AuthStatusOK), func(args []string) bool { return isAuth(args) && isLoggedIn() })
	h := forgeHome(t, 120, 30, false, fake, map[string]github.Local{
		"/src/widgets": checkout("/src/widgets", "https://ghe.corp.example/me/widgets.git"),
	})
	h = drive(t, h, altKeyMsg('i'))
	if line := forgeProjectLine(h); !strings.Contains(line, "No GitHub project") {
		t.Fatalf("before login: Project line = %q, want the no-Project state", line)
	}
	mu.Lock()
	loggedIn = true
	mu.Unlock()
	h = drive(t, h, runeKey('r'))
	if line := forgeProjectLine(h); !strings.Contains(line, "ghe.corp.example/me/widgets ▾") {
		t.Fatalf("r after gh auth login: Project line = %q, want ghe.corp.example/me/widgets", line)
	}

	// r while a resolution runs: that one's answer predates r, so the
	// group resolves again from scratch when it lands.
	mu.Lock()
	loggedIn = false
	mu.Unlock()
	h2 := forgeHome(t, 120, 30, false, fake, map[string]github.Local{
		"/src/widgets": checkout("/src/widgets", "https://ghe.corp.example/me/widgets.git"),
	})
	model, first := h2.Update(altKeyMsg('i'))
	h2 = model.(*Home)
	early := first() // answered before the login
	mu.Lock()
	loggedIn = true
	mu.Unlock()
	model, cmd := h2.Update(runeKey('r'))
	h2 = runCmds(t, model.(*Home), cmd)
	h2 = drive(t, h2, early)
	if line := forgeProjectLine(h2); !strings.Contains(line, "ghe.corp.example/me/widgets ▾") {
		t.Fatalf("r during a resolution: Project line = %q, want ghe.corp.example/me/widgets", line)
	}
}

// TestForgeFailedResolveRetriedOnTabOpen: a resolution that could not
// finish (git timed out) is tried again the next time a forge tab opens; a
// finished one is not.
func TestForgeFailedResolveRetriedOnTabOpen(t *testing.T) {
	loc := &changingLocal{checkouts: widgetsCheckout, fail: fmt.Errorf("running git: %w", context.DeadlineExceeded)}
	h := forgeHome(t, 120, 30, false, nil, nil)
	h.forge.resolver = github.NewResolver(github.NewClient(githubtest.NewFake()), loc.read)

	h = drive(t, h, altKeyMsg('i'))
	if line := forgeProjectLine(h); !strings.Contains(line, "⚠") {
		t.Fatalf("failed resolution: Project line = %q, want the warning", line)
	}
	loc.set(nil)
	h = drive(t, h, altKeyMsg('s'))
	h = drive(t, h, altKeyMsg('i'))
	if line := forgeProjectLine(h); !strings.Contains(line, "me/widgets ▾") {
		t.Fatalf("reopening the tab did not retry: Project line = %q", line)
	}
	reads := loc.count()
	h = drive(t, h, altKeyMsg('s'))
	h = drive(t, h, altKeyMsg('p'))
	if got := loc.count(); got != reads {
		t.Fatalf("reopening a tab resolved a resolved group again (%d git reads, want %d)", got, reads)
	}
	// r resolves again; when that cannot finish, the Project stays and
	// the next tab open tries again.
	loc.set(fmt.Errorf("running git: %w", context.DeadlineExceeded))
	h = drive(t, h, runeKey('r'))
	if got := loc.count(); got == reads {
		t.Fatal("r did not resolve the group again")
	}
	if line := forgeProjectLine(h); !strings.Contains(line, "me/widgets ▾") {
		t.Fatalf("a failed re-resolve hid the Project: %q", line)
	}
	loc.set(nil)
	reads = loc.count()
	h = drive(t, h, altKeyMsg('i'))
	if got := loc.count(); got == reads || h.forgeView().err != nil {
		t.Fatalf("reopening the tab after a failed re-resolve did not retry (reads %d -> %d, err %v)", reads, got, h.forgeView().err)
	}
}

// TestForgeReResolvesWhenSessionPathsChange: a session added to the group
// with a new repository becomes one of its Projects without a restart; the
// Project on screen stays while the group resolves again.
func TestForgeReResolvesWhenSessionPathsChange(t *testing.T) {
	loc := &changingLocal{checkouts: map[string]github.Local{
		"/src/widgets": checkout("/src/widgets", "git@github.com:me/widgets.git"),
		"/src/gadgets": checkout("/src/gadgets", "git@github.com:acme/gadgets.git"),
	}}
	h := forgeHome(t, 120, 30, false, nil, nil)
	h.forge.resolver = github.NewResolver(github.NewClient(githubtest.NewFake()), loc.read)
	grp := h.groupTree.Groups["widgets"]
	grp.DefaultPath = ""

	h = drive(t, h, altKeyMsg('i'))
	if n := len(h.forgeView().group.Projects); n != 1 {
		t.Fatalf("Projects = %d, want 1", n)
	}
	reads := loc.count()
	h = runCmds(t, h, h.forgeOnTick())
	if got := loc.count(); got != reads {
		t.Fatalf("a tick with the same sessions resolved again (%d git reads, want %d)", got, reads)
	}

	grp.Sessions = append(grp.Sessions, &session.Instance{ID: "c", Title: "gadgets", Tool: "claude", Status: session.StatusStopped, GroupPath: "widgets", ProjectPath: "/src/gadgets"})
	cmd := h.forgeOnTick()
	if line := forgeProjectLine(h); !strings.Contains(line, "me/widgets ▾") {
		t.Fatalf("while resolving again the Project line = %q, want me/widgets kept", line)
	}
	h = runCmds(t, h, cmd)
	var got []string
	for _, p := range h.forgeView().group.Projects {
		got = append(got, repoLabel(p.Repo))
	}
	if fmt.Sprint(got) != "[me/widgets acme/gadgets]" {
		t.Fatalf("Projects after adding a session = %v, want [me/widgets acme/gadgets]", got)
	}
	if line := forgeProjectLine(h); !strings.Contains(line, "me/widgets ▾") {
		t.Fatalf("Project line = %q, want me/widgets (the cursor session's)", line)
	}
}

// TestForgeProjectFollowsCursorUntilPicked: the selector follows the cursor
// session's Project (spec #18 §2) as the cursor moves in the group, until
// the user picks a Project with p; that pick then stays, r included.
func TestForgeProjectFollowsCursorUntilPicked(t *testing.T) {
	h := forgeHome(t, 120, 30, false, nil, map[string]github.Local{
		"/src/widgets": checkout("/src/widgets", "git@github.com:me/widgets.git"),
		"/src/gadgets": checkout("/src/gadgets", "git@github.com:acme/gadgets.git"),
	})
	grp := h.groupTree.Groups["widgets"]
	grp.DefaultPath = ""
	grp.Sessions[1].ProjectPath = "/src/gadgets"

	h = drive(t, h, altKeyMsg('i'))
	if line := forgeProjectLine(h); !strings.Contains(line, "me/widgets ▾") {
		t.Fatalf("cursor on the widgets session: Project line = %q", line)
	}
	h = drive(t, h, altKeyMsg('j'))
	if line := forgeProjectLine(h); !strings.Contains(line, "acme/gadgets ▾") {
		t.Fatalf("cursor moved to the gadgets session: Project line = %q, want acme/gadgets", line)
	}
	h = drive(t, h, altKeyMsg('k'))
	if line := forgeProjectLine(h); !strings.Contains(line, "me/widgets ▾") {
		t.Fatalf("cursor back on the widgets session: Project line = %q, want me/widgets", line)
	}

	// p picks acme/gadgets: the cursor no longer moves the selection.
	h = drive(t, h, runeKey('p'))
	h = drive(t, h, runeKey('j'))
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	for _, k := range []rune{'j', 'k'} {
		h = drive(t, h, altKeyMsg(k))
		if line := forgeProjectLine(h); !strings.Contains(line, "acme/gadgets ▾") {
			t.Fatalf("alt+%c after an explicit pick: Project line = %q, want acme/gadgets kept", k, line)
		}
	}
	h = drive(t, h, runeKey('r'))
	if line := forgeProjectLine(h); !strings.Contains(line, "acme/gadgets ▾") {
		t.Fatalf("r after an explicit pick: Project line = %q, want acme/gadgets kept", line)
	}
}

// TestForgeWorktreeSessionResolvesByMainRepo: a worktree session stands for
// its main repository (session.GroupProjectPaths), both as a Project and
// as the cursor session's Project.
func TestForgeWorktreeSessionResolvesByMainRepo(t *testing.T) {
	h := forgeHome(t, 120, 30, false, nil, map[string]github.Local{
		"/src/widgets": checkout("/src/widgets", "git@github.com:me/widgets.git"),
		"/src/gadgets": checkout("/src/gadgets", "git@github.com:acme/gadgets.git"),
	})
	grp := h.groupTree.Groups["widgets"]
	grp.DefaultPath = ""
	grp.Sessions[0].ProjectPath = "/src/gadgets"
	wt := grp.Sessions[1]
	wt.ProjectPath, wt.WorktreePath, wt.WorktreeRepoRoot = "/wt/widgets-fix", "/wt/widgets-fix", "/src/widgets"
	h.cursor = 2 // the worktree session

	h = drive(t, h, altKeyMsg('p'))
	if line := forgeProjectLine(h); !strings.Contains(line, "me/widgets ▾") {
		t.Fatalf("cursor on a widgets worktree: Project line = %q, want me/widgets", line)
	}
	if n := len(h.forgeView().group.Projects); n != 2 {
		t.Fatalf("Projects = %d, want 2 (gadgets and the worktree's main repo)", n)
	}
}
