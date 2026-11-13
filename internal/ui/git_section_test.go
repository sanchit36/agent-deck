package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/gitstate"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// gitSectionHome builds a Home with one selected session. A worktree
// session gets fixed worktree fields; a plain one only a project path.
func gitSectionHome(t *testing.T, worktree bool) (*Home, *session.Instance) {
	t.Helper()
	h := NewHome()
	h.width = 160
	h.height = 50
	h.initialLoading = false

	inst := session.NewInstance("git-section", "/work/repo")
	inst.Tool = "bash"
	if worktree {
		inst.ProjectPath = "/work/repo/.worktrees/feature-x"
		inst.WorktreePath = "/work/repo/.worktrees/feature-x"
		inst.WorktreeRepoRoot = "/work/repo"
		inst.WorktreeBranch = "feature/x"
	}

	h.instancesMu.Lock()
	h.instances = []*session.Instance{inst}
	h.instanceByID[inst.ID] = inst
	h.instancesMu.Unlock()
	h.flatItems = []session.Item{{Type: session.ItemTypeSession, Session: inst}}
	h.cursor = 0
	h.setHotkeys(resolveHotkeys(nil))
	return h, inst
}

// countingFetcher replaces the git reader with a fake that counts calls.
func countingFetcher(h *Home, st gitstate.State) *atomic.Int32 {
	var n atomic.Int32
	h.gitStateFetch = func(_ context.Context, _ string) gitstate.State {
		n.Add(1)
		return st
	}
	return &n
}

// runGitCmd executes cmd and every command of any batch it returns, feeding
// each message back into Update.
func runGitCmd(t *testing.T, h *Home, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runGitCmd(t, h, c)
		}
		return
	}
	if msg != nil {
		_, next := h.Update(msg)
		runGitCmd(t, h, next)
	}
}

// gitDebounce delivers a preview-debounce for the selected session with the
// preview fetch already marked in flight, so the only command left is the
// git-state fetch.
func gitDebounce(t *testing.T, h *Home, inst *session.Instance) {
	t.Helper()
	key := previewCacheKey(inst.ID, -1)
	h.previewDebounceMu.Lock()
	h.pendingPreviewKey = key
	h.previewDebounceMu.Unlock()
	h.previewCacheMu.Lock()
	h.previewFetchingID = key
	h.previewCacheMu.Unlock()
	_, cmd := h.Update(previewDebounceMsg{previewKey: key, sessionID: inst.ID, windowIndex: -1})
	runGitCmd(t, h, cmd)
}

func TestGitState_DebounceFetchesOncePerTTL(t *testing.T) {
	for _, worktree := range []bool{true, false} {
		h, inst := gitSectionHome(t, worktree)
		calls := countingFetcher(h, gitstate.State{Repo: "/work/repo", Branch: "main"})

		gitDebounce(t, h, inst)
		if got := calls.Load(); got != 1 {
			t.Fatalf("worktree=%v: first debounce made %d git calls, want 1", worktree, got)
		}
		st, ok := h.gitState.Get(h.gitCheckoutPath(inst))
		if !ok || st.Branch != "main" {
			t.Fatalf("worktree=%v: cache after fetch = %+v, %v", worktree, st, ok)
		}

		gitDebounce(t, h, inst)
		if got := calls.Load(); got != 1 {
			t.Fatalf("worktree=%v: a debounce within the TTL must not refetch, got %d calls", worktree, got)
		}
	}
}

func TestGitState_CheckoutPathKeysTheCache(t *testing.T) {
	h, wt := gitSectionHome(t, true)
	if got := h.gitCheckoutPath(wt); got != "/work/repo/.worktrees/feature-x" {
		t.Fatalf("worktree checkout path = %q", got)
	}
	plain := session.NewInstance("plain", "/work/other")
	if got := h.gitCheckoutPath(plain); got != "/work/other" {
		t.Fatalf("plain checkout path = %q", got)
	}
}

// TestGitState_RenderRunsNoGit pins the acceptance item: rendering the
// preview pane (session and group) never calls git, whatever the cache holds.
func TestGitState_RenderRunsNoGit(t *testing.T) {
	for _, worktree := range []bool{true, false} {
		h, inst := gitSectionHome(t, worktree)
		calls := countingFetcher(h, gitstate.State{})
		path := h.gitCheckoutPath(inst)

		_ = h.renderPreviewPane(100, 50) // empty cache
		h.gitState.Put(path, gitstate.State{Repo: "/work/repo", Branch: "main", Dirty: true}, time.Now())
		_ = h.renderPreviewPane(100, 50) // cached
		_ = h.renderPreviewPane(100, 50)

		if got := calls.Load(); got != 0 {
			t.Fatalf("worktree=%v: render made %d git calls, want 0", worktree, got)
		}
	}
}

func TestGitState_FinishDialogGetsDirtyFromFetch(t *testing.T) {
	h, inst := gitSectionHome(t, true)
	calls := countingFetcher(h, gitstate.State{Repo: "/work/repo", Branch: "feature/x", Dirty: true})
	// A fresh cache entry must not stop the dialog from asking again.
	h.gitState.Put(h.gitCheckoutPath(inst), gitstate.State{Branch: "feature/x"}, time.Now())

	h.worktreeFinishDialog.Show(inst.ID, inst.Title, inst.WorktreeBranch, inst.WorktreeRepoRoot, inst.WorktreePath, "main")
	runGitCmd(t, h, h.gitStateCmd(inst, true))

	if calls.Load() != 1 {
		t.Fatalf("finish dialog must force a fetch, got %d calls", calls.Load())
	}
	if !h.worktreeFinishDialog.dirtyChecked || !h.worktreeFinishDialog.isDirty {
		t.Fatal("finish dialog must receive the dirty result")
	}
}

func TestGitState_GroupWorktreeInfoReadsCacheByPath(t *testing.T) {
	h := NewHome()
	a := session.NewInstance("a", "/r/.worktrees/a")
	a.WorktreePath, a.WorktreeRepoRoot, a.WorktreeBranch = "/r/.worktrees/a", "/r", "a"
	b := session.NewInstance("b", "/r/.worktrees/b")
	b.WorktreePath, b.WorktreeRepoRoot, b.WorktreeBranch = "/r/.worktrees/b", "/r", "b"
	h.gitState.Put("/r/.worktrees/a", gitstate.State{Dirty: true}, time.Now())

	info := h.getGroupWorktreeInfo(&session.Group{Sessions: []*session.Instance{a, b}})
	if info == nil || len(info.branches) != 2 {
		t.Fatalf("info = %+v", info)
	}
	if !info.branches[0].dirtyChecked || !info.branches[0].isDirty {
		t.Fatalf("branch a = %+v, want checked dirty", info.branches[0])
	}
	if info.branches[1].dirtyChecked {
		t.Fatalf("branch b = %+v, want unchecked", info.branches[1])
	}
}

// TestGitSection_Goldens pins the rendered Git section for each state.
func TestGitSection_Goldens(t *testing.T) {
	errTimeout := errors.New("reading git status: context deadline exceeded")
	cases := []struct {
		golden   string
		worktree bool
		jjType   bool
		state    *gitstate.State // nil: nothing cached yet
	}{
		{"git_section_worktree_checking.golden", true, false, nil},
		{"git_section_worktree_ahead_behind.golden", true, false,
			&gitstate.State{Repo: "/work/repo/.worktrees/feature-x", Branch: "feature/x", Upstream: "origin/feature/x", HasCounts: true, Ahead: 2, Behind: 1}},
		{"git_section_worktree_dirty_no_upstream.golden", true, false,
			&gitstate.State{Repo: "/work/repo/.worktrees/feature-x", Branch: "feature/x", Dirty: true}},
		{"git_section_worktree_unavailable.golden", true, false,
			&gitstate.State{Repo: "/work/repo/.worktrees/feature-x", Err: errTimeout}},
		{"git_section_worktree_jj.golden", true, true, nil},
		{"git_section_plain_in_sync.golden", false, false,
			&gitstate.State{Repo: "/work/repo", Branch: "main", Upstream: "origin/main", HasCounts: true}},
		{"git_section_plain_upstream_gone.golden", false, false,
			&gitstate.State{Repo: "/work/repo", Branch: "main", Upstream: "origin/main", Dirty: true}},
		{"git_section_plain_jj.golden", false, false,
			&gitstate.State{Repo: "/work/repo", JJ: true}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			h, inst := gitSectionHome(t, tc.worktree)
			if tc.jjType {
				inst.WorktreeType = "jujutsu"
			}
			if tc.state != nil {
				h.gitState.Put(h.gitCheckoutPath(inst), *tc.state, time.Now())
			}
			assertTextGolden(t, ansi.Strip(h.renderGitSection(inst, 80)), tc.golden)
		})
	}
}

// TestGitSection_HiddenOutsideARepo: a plain session shows no Git section
// until the cache says its path is in a repo, and none when it is not.
func TestGitSection_HiddenOutsideARepo(t *testing.T) {
	h, inst := gitSectionHome(t, false)
	if got := h.renderGitSection(inst, 80); got != "" {
		t.Fatalf("unfetched plain session must render no Git section, got %q", got)
	}
	h.gitState.Put(h.gitCheckoutPath(inst), gitstate.State{NotRepo: true}, time.Now())
	if got := h.renderGitSection(inst, 80); got != "" {
		t.Fatalf("non-repo session must render no Git section, got %q", got)
	}
	h.gitState.Put(h.gitCheckoutPath(inst), gitstate.State{Err: errors.New("boom")}, time.Now())
	if got := h.renderGitSection(inst, 80); got != "" {
		t.Fatalf("a plain session whose repo status is unknown renders nothing, got %q", got)
	}
}

func assertTextGolden(t *testing.T, got, name string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s golden mismatch\nwant:\n%s\ngot:\n%s", name, want, got)
	}
	if strings.Contains(got, "\t") {
		t.Fatalf("%s: tab in rendered section", name)
	}
}
