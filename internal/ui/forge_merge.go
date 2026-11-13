package ui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/gitstate"
	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

// Merge flow (spec #18 §14). m on a PR row, the detail pane or the overlay
// refetches the PR (github.MergeInfo) and opens the merge dialog. The
// dialog decides with github.GuardMerge and runs one `gh pr merge` pinned
// to the head SHA it shows (--match-head-commit), so a head that moved
// since is refused by GitHub; the dialog then stays open on the new head
// and asks again. Nothing offers --admin. A direct merge of a PR with
// linked sessions opens the follow-up (Keep all / Stop all / Delete all),
// where Delete never removes a dirty worktree without a separate yes.
//
// Both dialogs are modal: registered in renderFrame, updateInner's key
// ladder and hasModalVisible, above the overlay so m works from it.

// forgeMergeRetryDelay is the wait before the one refetch of a PR whose
// mergeability GitHub has not computed yet (UNKNOWN).
const forgeMergeRetryDelay = 2 * time.Second

// forgeMergeChangedText is the dialog's note after GitHub refused a merge
// because the head moved.
const forgeMergeChangedText = "PR changed since you looked: review and confirm again"

// forgeMergeDialog is the merge dialog's state.
type forgeMergeDialog struct {
	visible bool
	repo    github.Repo
	number  int
	// seq tells a reply from an earlier fetch or merge of the dialog apart.
	seq int
	// loading is true while the PR is being read; refetched once an
	// UNKNOWN merge state was read again.
	loading   bool
	refetched bool
	has       bool
	info      github.MergeInfo
	err       error
	// method and deleteBranch are the choices; they survive a refresh
	// while still allowed.
	method       github.MergeMethod
	deleteBranch bool
	// running is true while gh pr merge runs (up to 60 s, no cancel).
	running bool
	// notice (yellow) and failure (red) report the last refused merge.
	notice  string
	failure string
}

// forgeMergeFollow is the linked-sessions follow-up after a direct merge.
type forgeMergeFollow struct {
	visible bool
	number  int
	method  github.MergeMethod
	ids     []string
	titles  []string
	// choice is 0 Keep all, 1 Stop all, 2 Delete all.
	choice int
	// checking is true while the worktrees are read for uncommitted
	// changes; dirty are the sessions Delete all kept for a separate yes.
	checking bool
	dirty    []string
}

var forgeFollowChoices = []string{"Keep all", "Stop all", "Delete all"}

type forgeMergeInfoMsg struct {
	seq  int
	info github.MergeInfo
	err  error
}

type forgeMergeDoneMsg struct {
	seq  int
	repo github.Repo
	req  github.MergeRequest
	err  error
}

// forgeMergeDirtyMsg is the follow-up's worktree check: the sessions safe
// to delete and those with uncommitted changes (or unreadable).
type forgeMergeDirtyMsg struct {
	number int
	clean  []string
	dirty  []string
}

func (h *Home) forgeMergeVisible() bool       { return h.forge.merge.visible || h.forge.follow.visible }
func (h *Home) forgeMergeFollowVisible() bool { return h.forge.follow.visible }

// closeForgeMerge closes both dialogs (the gate turned off).
func (h *Home) closeForgeMerge() {
	h.forge.merge = forgeMergeDialog{}
	h.forge.follow = forgeMergeFollow{}
}

// forgeMergeTarget is the PR m acts on: the overlay's item, else the
// selected row of the PRs tab.
func (h *Home) forgeMergeTarget() (github.Repo, int, bool) {
	if o := &h.forge.overlay; o.visible {
		return o.repo, o.row.number, o.pr
	}
	if h.forge.tab != tabPRs {
		return github.Repo{}, 0, false
	}
	k, r, ok := h.forgeSelectedKey()
	return k.Repo, r.number, ok
}

// openForgeMerge is m: open the dialog on the PR and read it fresh.
func (h *Home) openForgeMerge() tea.Cmd {
	repo, number, ok := h.forgeMergeTarget()
	if !ok {
		return nil
	}
	h.forge.merge = forgeMergeDialog{visible: true, repo: repo, number: number, seq: h.forge.merge.seq + 1}
	return h.forgeMergeFetch(0)
}

// forgeMergeFetch reads the dialog's PR after delay.
func (h *Home) forgeMergeFetch(delay time.Duration) tea.Cmd {
	m := &h.forge.merge
	m.seq++
	m.loading = true
	seq, repo, number, client := m.seq, m.repo, m.number, h.forgeClient()
	return h.forgeJobs().run(func(ctx context.Context) tea.Msg {
		if delay > 0 {
			t := time.NewTimer(delay)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
			}
		}
		info, err := client.MergeInfo(ctx, repo, number)
		return forgeMergeInfoMsg{seq: seq, info: info, err: err}
	})
}

func (h *Home) forgeMergeRetryDelay() time.Duration {
	switch d := h.forge.mergeRetry; {
	case d < 0:
		return 0
	case d == 0:
		return forgeMergeRetryDelay
	default:
		return d
	}
}

// forgeMergeLocalDetail is the part of a PRDetail the local-state read
// keys on, from the dialog's fresh copy of the PR.
func forgeMergeLocalDetail(i github.MergeInfo) github.PRDetail {
	return github.PRDetail{Number: i.Number, HeadBranch: i.HeadBranch, HeadSHA: i.HeadSHA, HeadRepo: i.HeadRepo}
}

func (h *Home) applyForgeMergeInfo(msg forgeMergeInfoMsg) tea.Cmd {
	m := &h.forge.merge
	if !m.visible || msg.seq != m.seq {
		return nil
	}
	m.loading = false
	if msg.err != nil {
		m.err = msg.err
		return nil
	}
	first := !m.has
	m.err, m.has, m.info = nil, true, msg.info
	if !methodAllowed(msg.info.Methods, m.method) {
		m.method = msg.info.DefaultMethod
	}
	if first {
		m.deleteBranch = msg.info.DeleteBranchOnMerge
	}
	// The ⚠ of an unpushed or dirty checkout is read fresh for this head.
	cmds := []tea.Cmd{h.forgePRLocalCmd(m.repo, forgeMergeLocalDetail(msg.info), true)}
	if github.GuardMerge(msg.info).Refetch && !m.refetched {
		m.refetched = true
		cmds = append(cmds, h.forgeMergeFetch(h.forgeMergeRetryDelay()))
	}
	return tea.Batch(cmds...)
}

func methodAllowed(methods []github.MergeMethod, m github.MergeMethod) bool {
	for _, x := range methods {
		if x == m {
			return m != ""
		}
	}
	return false
}

// forgeMergeShowsMethod reports whether the dialog shows the method
// chooser: the action takes a method and the repository allows several.
func forgeMergeShowsMethod(g github.MergeGuard, i github.MergeInfo) bool {
	return (g.Action == github.ActionMerge || g.Action == github.ActionAutoMerge) && len(i.Methods) > 1
}

// forgeMergeShowsDelete reports whether the dialog shows the Delete
// branch toggle: a direct merge of a branch in the repository itself (gh
// never deletes a fork's branch, and -R keeps it off local branches).
func forgeMergeShowsDelete(g github.MergeGuard, i github.MergeInfo) bool {
	return g.Action == github.ActionMerge && !i.CrossRepo
}

// confirmForgeMerge is y: run the allowed action pinned to the head SHA
// the dialog shows. Nothing runs while the PR is being read or blocked.
func (h *Home) confirmForgeMerge() tea.Cmd {
	m := &h.forge.merge
	if !m.has || m.loading || m.running || m.err != nil {
		return nil
	}
	g := github.GuardMerge(m.info)
	if g.Action == "" {
		return nil
	}
	req := github.MergeRequest{Number: m.number, HeadSHA: m.info.HeadSHA, Action: g.Action}
	if g.Action == github.ActionMerge || g.Action == github.ActionAutoMerge {
		req.Method = m.method
	}
	if forgeMergeShowsDelete(g, m.info) {
		req.DeleteBranch = m.deleteBranch
	}
	m.running, m.notice, m.failure = true, "", ""
	seq, repo, client := m.seq, m.repo, h.forgeClient()
	return h.forgeJobs().run(func(ctx context.Context) tea.Msg {
		return forgeMergeDoneMsg{seq: seq, repo: repo, req: req, err: client.Merge(ctx, repo, req)}
	})
}

func (h *Home) applyForgeMergeDone(msg forgeMergeDoneMsg) tea.Cmd {
	m := &h.forge.merge
	current := m.visible && msg.seq == m.seq
	if current {
		m.running = false
	}
	if msg.err != nil {
		if !current {
			h.setError(fmt.Errorf("merging #%d: %s", msg.req.Number, github.MergeErrorText(msg.err)))
			return nil
		}
		if github.MergeHeadMoved(msg.err) {
			m.notice = forgeMergeChangedText
		} else {
			m.failure = github.MergeErrorText(msg.err)
		}
		// Whatever GitHub refused, the dialog shows the PR as it is now:
		// a new head needs a new y.
		m.refetched = false
		return h.forgeMergeFetch(0)
	}
	if current {
		h.forge.merge = forgeMergeDialog{seq: m.seq}
	}
	h.setError(errors.New(forgeMergeToast(msg.req)))
	cmds := []tea.Cmd{h.forgeManualRefresh()}
	if msg.req.Action == github.ActionMerge {
		h.openForgeMergeFollow(msg.repo, msg.req)
	}
	return tea.Batch(cmds...)
}

// forgeMergeToast is the toast of a done action.
func forgeMergeToast(req github.MergeRequest) string {
	switch req.Action {
	case github.ActionAutoMerge:
		return fmt.Sprintf("Auto-merge enabled for #%d (%s)", req.Number, req.Method.Word())
	case github.ActionQueue:
		return fmt.Sprintf("Added #%d to the merge queue", req.Number)
	case github.ActionDisableAuto:
		return fmt.Sprintf("Auto-merge disabled for #%d", req.Number)
	}
	return fmt.Sprintf("Merged #%d (%s)", req.Number, req.Method.Word())
}

// openForgeMergeFollow opens the follow-up when sessions are linked to
// the merged PR.
func (h *Home) openForgeMergeFollow(repo github.Repo, req github.MergeRequest) {
	insts := h.forgeLinkedSessions(repo, session.LinkPR, req.Number)
	if len(insts) == 0 {
		return
	}
	f := forgeMergeFollow{visible: true, number: req.Number, method: req.Method}
	for _, inst := range insts {
		f.ids = append(f.ids, inst.ID)
		f.titles = append(f.titles, inst.Title)
	}
	h.forge.follow = f
}

// handleForgeMergeKey routes keys while a merge dialog shows. ctrl+c
// still quits; nothing else leaves a running merge.
func (h *Home) handleForgeMergeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return h.handleMainKey(msg)
	}
	if h.forge.follow.visible {
		return h, h.handleForgeFollowKey(key)
	}
	m := &h.forge.merge
	if m.running {
		return h, nil
	}
	g := github.GuardMerge(m.info)
	switch key {
	case "esc", "q":
		h.forge.merge = forgeMergeDialog{seq: m.seq}
		return h, nil
	case "y":
		return h, h.confirmForgeMerge()
	case "r":
		if !m.loading {
			m.refetched = false
			return h, h.forgeMergeFetch(0)
		}
	case "left", "h", "shift+tab":
		if m.has && forgeMergeShowsMethod(g, m.info) {
			m.method = cycleMethod(m.info.Methods, m.method, -1)
		}
	case "right", "l", "tab":
		if m.has && forgeMergeShowsMethod(g, m.info) {
			m.method = cycleMethod(m.info.Methods, m.method, 1)
		}
	case " ", "space":
		if m.has && forgeMergeShowsDelete(g, m.info) {
			m.deleteBranch = !m.deleteBranch
		}
	}
	return h, nil
}

func cycleMethod(methods []github.MergeMethod, cur github.MergeMethod, step int) github.MergeMethod {
	n := len(methods)
	for i, m := range methods {
		if m == cur {
			return methods[((i+step)%n+n)%n]
		}
	}
	return methods[0]
}

func (h *Home) handleForgeFollowKey(key string) tea.Cmd {
	f := &h.forge.follow
	if f.checking {
		return nil
	}
	if len(f.dirty) > 0 {
		switch key {
		case "y":
			ids := f.dirty
			h.forge.follow = forgeMergeFollow{}
			return h.forgeMergeActOn(ids, true)
		case "n", "esc", "q":
			h.forge.follow = forgeMergeFollow{}
		}
		return nil
	}
	n := len(forgeFollowChoices)
	switch key {
	case "left", "h", "shift+tab", "up", "k":
		f.choice = (f.choice + n - 1) % n
	case "right", "l", "tab", "down", "j":
		f.choice = (f.choice + 1) % n
	case "esc", "q":
		h.forge.follow = forgeMergeFollow{}
	case "enter":
		switch f.choice {
		case 1:
			ids := f.ids
			h.forge.follow = forgeMergeFollow{}
			return h.forgeMergeActOn(ids, false)
		case 2:
			return h.forgeMergeCheckDirty()
		}
		h.forge.follow = forgeMergeFollow{}
	}
	return nil
}

// forgeMergeActOn stops (del false) or deletes the sessions ids that
// still exist, as their own keys do: deleting a worktree session removes
// its worktree through the session guard.
func (h *Home) forgeMergeActOn(ids []string, del bool) tea.Cmd {
	act := h.forge.mergeAct
	if act == nil {
		act = func(inst *session.Instance, del bool) tea.Cmd {
			if del {
				return h.deleteSession(inst)
			}
			return h.closeSession(inst)
		}
	}
	var cmds []tea.Cmd
	for _, id := range ids {
		if inst := h.getInstanceByID(id); inst != nil {
			cmds = append(cmds, act(inst, del))
		}
	}
	return tea.Batch(cmds...)
}

// forgeMergeCheckDirty is Delete all: read every linked worktree for
// uncommitted changes first. A worktree that cannot be read counts as
// dirty.
func (h *Home) forgeMergeCheckDirty() tea.Cmd {
	f := &h.forge.follow
	f.checking = true
	type target struct{ id, path string }
	var targets []target
	for _, id := range f.ids {
		inst := h.getInstanceByID(id)
		if inst == nil {
			continue
		}
		path := ""
		if inst.IsWorktree() {
			path = inst.WorktreePath
		}
		targets = append(targets, target{id: id, path: path})
	}
	fetch := h.gitStateFetch
	if fetch == nil {
		fetch = gitstate.Fetch
	}
	number := f.number
	return h.forgeJobs().run(func(ctx context.Context) tea.Msg {
		msg := forgeMergeDirtyMsg{number: number}
		for _, t := range targets {
			if t.path == "" {
				// Not a worktree: deleting the session removes no files.
				msg.clean = append(msg.clean, t.id)
				continue
			}
			if st := fetch(ctx, t.path); st.Dirty || st.Err != nil || st.NotRepo {
				msg.dirty = append(msg.dirty, t.id)
				continue
			}
			msg.clean = append(msg.clean, t.id)
		}
		return msg
	})
}

func (h *Home) applyForgeMergeDirty(msg forgeMergeDirtyMsg) tea.Cmd {
	f := &h.forge.follow
	if !f.visible || !f.checking || f.number != msg.number {
		return nil
	}
	f.checking = false
	cmd := h.forgeMergeActOn(msg.clean, true)
	if len(msg.dirty) == 0 {
		h.forge.follow = forgeMergeFollow{}
		return cmd
	}
	f.dirty = msg.dirty
	return cmd
}
