package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/prompttemplate"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Launch from an Issue or PR (spec #18 §13), the UI side: n on a row opens
// the chooser when sessions are linked to it, else the NewDialog prefilled
// for the item (group, the Project's checkout, worktree on the issue
// branch or the PR's, the tool, the title and the Template field). The
// body and comments load in the background. enter launches through
// forgeLauncher (forge_launch_run.go) off the UI goroutine; the dialog
// stays open until the launch reports, and shows a failure inline.

// forgeLaunchTextMsg delivers a launch text fetch.
type forgeLaunchTextMsg struct {
	gen              int
	text             github.ItemText
	comments, review bool
	err              error
}

// forgeLaunchedMsg is a launch's outcome.
type forgeLaunchedMsg struct {
	res forgeLaunchResult
}

// forgeNew is n on a forge list: the chooser when sessions are linked to
// the selected row, else the launch dialog.
func (h *Home) forgeNew() tea.Cmd {
	insts, r, ok := h.forgeSelectedSessions()
	if !ok {
		return nil
	}
	if len(insts) == 0 {
		return h.forgeOpenLaunch()
	}
	p := &forgeSessionPick{number: r.number, launch: true}
	for _, inst := range insts {
		p.ids = append(p.ids, inst.ID)
		p.titles = append(p.titles, inst.Title)
	}
	h.forge.sessionPick = p
	return nil
}

// forgeLaunchTitle is the session title: prefix and the item's title, cut
// to the dialog's name limit (display width, and bytes, which it checks).
func forgeLaunchTitle(prefix, title string) string {
	full := strings.TrimSpace(prefix + " " + forgeOneLine(title))
	s := full
	for w := MaxNameLength; len(s) > MaxNameLength && w > 1; w-- {
		s = truncateDisplay(full, w, "…")
	}
	return s
}

// forgeLaunchTool is the tool a launch preselects: the group's (its newest
// session's tool), then [default_tool], then claude.
func forgeLaunchTool(groupTool, defaultTool string) string {
	for _, t := range []string{groupTool, defaultTool} {
		if t = strings.TrimSpace(t); t != "" {
			return t
		}
	}
	return "claude"
}

// forgeGroupTool is the tool of the newest session in group, "" when it
// has none.
func (h *Home) forgeGroupTool(group string) string {
	h.instancesMu.RLock()
	defer h.instancesMu.RUnlock()
	var newest *session.Instance
	for _, inst := range h.instances {
		if inst == nil || inst.GroupPath != group || inst.Tool == "" {
			continue
		}
		if newest == nil || inst.CreatedAt.After(newest.CreatedAt) {
			newest = inst
		}
	}
	if newest == nil {
		return ""
	}
	return newest.Tool
}

// forgeOpenLaunch opens the NewDialog for the selected row.
func (h *Home) forgeOpenLaunch() tea.Cmd {
	k, r, ok := h.forgeSelectedKey()
	if !ok {
		return nil
	}
	v := h.forgeView()
	if v == nil || v.selected >= len(v.group.Projects) || v.group.Projects[v.selected].Root == "" {
		h.setError(fmt.Errorf("no local checkout of %s to start a session in", k.Repo.Owner+"/"+k.Repo.Name))
		return nil
	}
	root := v.group.Projects[v.selected].Root
	group := h.forge.groupPath
	groupName := group
	if h.groupTree != nil {
		if g, ok := h.groupTree.Groups[group]; ok {
			groupName = g.Name
		}
	}

	f := &forgeLaunchField{kind: h.forgeLinkKind(), repo: k.Repo, number: r.number, repoRoot: root}
	f.vars = prompttemplate.Vars{
		Kind: prompttemplate.KindIssue, Number: r.number, Title: r.title, URL: r.url,
		Repo: k.Repo.Owner + "/" + k.Repo.Name, Host: k.Repo.HostOrDefault(),
	}
	if e, ok := h.forgeCache().Get(k); ok {
		switch d := e.Value.(type) {
		case github.IssueDetail:
			f.vars.Title, f.vars.URL, f.vars.Labels, f.vars.Author = d.Title, d.URL, d.Labels, d.Author
		case github.PRDetail:
			f.vars.Title, f.vars.URL, f.vars.Labels, f.vars.Author = d.Title, d.URL, d.Labels, d.Author
			f.headBranch, f.baseBranch = d.HeadBranch, d.BaseBranch
		}
	}
	title := forgeLaunchTitle(fmt.Sprintf("#%d", r.number), f.vars.Title)
	if f.kind == session.LinkPR {
		f.vars.Kind = prompttemplate.KindPR
		f.vars.HeadBranch, f.vars.BaseBranch = f.headBranch, f.baseBranch
		title = forgeLaunchTitle(fmt.Sprintf("PR#%d", r.number), f.vars.Title)
	}

	cfg, _ := session.LoadUserConfig()
	ts := prompttemplate.Builtins()
	if dir, home, err := session.PromptTemplateDirs(); err == nil {
		f.configDir, f.home = dir, home
		if cfg != nil {
			ts, _ = cfg.BuildPromptTemplates(dir, home)
		}
	}
	f.templates = make(map[string]prompttemplate.Template, len(ts))
	for _, t := range ts {
		f.templates[t.Name] = t
	}
	sel := prompttemplate.Select(ts, f.vars.Kind, f.vars.Repo, f.vars.Labels)
	f.order, f.matches = sel.Order, sel.Matches
	at := 0
	if i := indexOf(f.order, sel.Chosen); i >= 0 {
		at = i
	}
	f.selectAt(at)
	f.open = (sel.Chosen == "" && len(f.order) > 0) || f.loadErr != nil

	d := h.newDialog
	h.pendingRemoteName = ""
	d.restoreLocalPresets()
	d.RefreshInstalledTools()
	d.SetDefaultTool(forgeLaunchTool(h.forgeGroupTool(group), session.GetDefaultTool()))
	d.ShowInGroup(group, groupName, root, nil, "")
	d.SetClaudeHooksUndecided(h.pendingHooksPrompt)
	d.nameInput.SetValue(title)
	d.nameInput.CursorStart()
	d.forge = f
	d.worktreeEnabled, d.worktreeToggled = true, true
	branch := github.IssueBranch(d.branchPrefix, r.number, f.vars.Title)
	if f.kind == session.LinkPR {
		branch = f.headBranch
	}
	d.branchInput.SetValue(branch)
	d.branchAutoSet = false
	d.rebuildFocusTargets()
	if f.open {
		if i := d.indexOf(focusTemplate); i >= 0 {
			d.focusIndex = i
			d.updateFocus()
		}
	}
	return h.forgeLaunchFetch()
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// forgeLaunchFetch fetches the body, and the comments and review threads
// the selected template uses, when it still needs them.
func (h *Home) forgeLaunchFetch() tea.Cmd {
	f := h.newDialog.forge
	if f == nil || !f.wantsFetch() {
		return nil
	}
	comments, review := f.needs()
	f.fetchGen++
	f.fetching, f.fetchErr = true, nil
	gen, repo, number, client := f.fetchGen, f.repo, f.number, h.forgeClient()
	return h.forgeJobs().run(func(ctx context.Context) tea.Msg {
		t, err := client.ItemText(ctx, repo, number, comments, review)
		return forgeLaunchTextMsg{gen: gen, text: t, comments: comments, review: review, err: err}
	})
}

// applyForgeLaunchText fills the dialog with a fetch, and goes on with a
// launch that was waiting for it.
func (h *Home) applyForgeLaunchText(msg forgeLaunchTextMsg) tea.Cmd {
	f := h.newDialog.forge
	if f == nil || !h.newDialog.IsVisible() || msg.gen != f.fetchGen {
		return nil
	}
	f.fetching = false
	wait := f.waitLaunch
	f.waitLaunch = false
	if msg.err != nil {
		f.fetchErr = msg.err
		f.askEmpty = wait
		return nil
	}
	f.applyText(msg.text, msg.comments, msg.review)
	if wait {
		return h.submitForgeLaunch()
	}
	return h.forgeLaunchFetch()
}

// submitForgeLaunch is enter in the launch dialog once it validated.
func (h *Home) submitForgeLaunch() tea.Cmd {
	d := h.newDialog
	f := d.forge
	if f.launching {
		return nil
	}
	if f.loadErr != nil {
		d.SetError(f.loadErr.Error())
		if i := d.indexOf(focusTemplate); i >= 0 {
			d.focusIndex = i
			d.updateFocus()
		}
		return nil
	}
	usesText := !f.edited && f.at < len(f.order)
	if usesText && f.fetching {
		// The fetch's gh call times out on its own (20 s).
		f.waitLaunch = true
		return nil
	}
	if usesText && f.fetchErr != nil && !f.askEmpty {
		f.askEmpty = true
		return nil
	}

	name, path, command, branch, worktree := d.GetValuesWithWorktree()
	abs, err := absLocalProjectPath(path)
	if err != nil {
		d.SetError("Cannot resolve a relative path here — enter an absolute path")
		return nil
	}
	if name == "" {
		prefix := fmt.Sprintf("#%d", f.number)
		if f.kind == session.LinkPR {
			prefix = "PR" + prefix
		}
		name = forgeLaunchTitle(prefix, f.vars.Title)
	}
	rememberTool(h.stateDB(), command)
	if asked, install := d.HooksDecision(); asked {
		h.applyClaudeHooksDecision(install)
	}
	plan := forgeLaunchPlan{
		kind: f.kind, repo: f.repo, number: f.number, repoRoot: abs,
		worktree: worktree, branch: branch, baseBranch: f.baseBranch,
		message:     f.message(),
		newInstance: h.forgeLaunchInstance(name, d.GetSelectedGroup(), command),
	}
	f.launching, f.askEmpty = true, false
	d.ClearError()
	launcher := h.forge.launcher
	if launcher.client == nil {
		launcher.client = h.forgeClient()
	}
	return h.forgeJobs().run(func(ctx context.Context) tea.Msg {
		return forgeLaunchedMsg{res: launcher.launch(ctx, plan)}
	})
}

// forgeLaunchInstance reads the dialog's session options now (on the UI
// goroutine) and returns the builder the launch calls with its directory.
func (h *Home) forgeLaunchInstance(name, group, command string) func(path string) (*session.Instance, error) {
	d := h.newDialog
	tool, command := createSessionTool(command)
	gemini := d.IsGeminiYoloMode()
	model := d.GetLaunchModelID()
	var toolOptions json.RawMessage
	var extraArgs []string
	switch {
	case tool == "claude":
		if opts := d.GetClaudeOptions(); opts != nil {
			toolOptions, _ = session.MarshalToolOptions(opts)
			extraArgs = d.GetClaudeExtraArgs()
		}
	case command == "codex":
		yolo := d.GetCodexYoloMode()
		toolOptions, _ = session.MarshalToolOptions(&session.CodexOptions{YoloMode: &yolo, ReasoningEffort: d.GetLaunchReasoningEffort()})
	case command == "hermes":
		yolo := d.GetHermesYoloMode()
		toolOptions, _ = session.MarshalToolOptions(&session.HermesOptions{YoloMode: &yolo})
	case command == "omp":
		toolOptions, _ = session.MarshalToolOptions(d.GetOMPOptions())
	}
	account := ""
	if session.IsClaudeCompatible(command) {
		account = d.GetClaudeAccount()
	}
	return func(path string) (*session.Instance, error) {
		inst := session.NewInstanceWithGroupAndTool(name, path, group, tool)
		inst.Command = command
		inst.TitleLocked = true
		applyCreateSessionToolOverrides(inst, tool, gemini)
		if len(toolOptions) > 0 {
			inst.ToolOptionsJSON = toolOptions
		}
		if model != "" {
			if err := inst.ApplyLaunchModel(model); err != nil {
				return nil, fmt.Errorf("applying the model override: %w", err)
			}
		}
		if tool == "claude" && len(extraArgs) > 0 {
			inst.ExtraArgs = extraArgs
		}
		if account != "" {
			inst.Account = account
		}
		return inst, nil
	}
}

// applyForgeLaunched ends a launch: a failure shows in the dialog, which
// stays open; a session joins the list as a created one does, with a
// failed first message as its warning.
func (h *Home) applyForgeLaunched(msg forgeLaunchedMsg) tea.Cmd {
	d := h.newDialog
	if f := d.forge; f != nil {
		f.launching = false
	}
	if msg.res.err != nil {
		if d.forge != nil && d.IsVisible() {
			d.SetError(msg.res.err.Error())
		} else {
			h.setError(msg.res.err)
		}
		return nil
	}
	if d.forge != nil {
		d.Hide()
	}
	var warn []string
	if msg.res.sendErr != nil {
		warn = append(warn, "first message not sent: "+msg.res.sendErr.Error())
	}
	if msg.res.setupWarning != "" {
		warn = append(warn, msg.res.setupWarning)
	}
	created := sessionCreatedMsg{instance: msg.res.inst, setupWarning: strings.Join(warn, " · ")}
	return func() tea.Msg { return created }
}

// forgeLaunchBusy reports whether the launch dialog owns every key: its
// launch is running.
func (h *Home) forgeLaunchBusy() bool {
	return h.newDialog.forge != nil && h.newDialog.forge.launching
}
