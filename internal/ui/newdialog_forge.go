package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
	"github.com/asheshgoplani/agent-deck/internal/github/prompttemplate"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// The NewDialog's Template field (spec #18 §12, §13), present only when the
// dialog was opened by n on an Issue or PR. It holds the item the launch is
// for, the Prompt Templates in cycle order (label matches, the rest, then
// none), the variables they are filled with, and the background fetch of
// the body and comments.

// forgeTemplateNone is the cycle's last entry: no first message.
const forgeTemplateNone = "none"

// forgeLaunchPreviewLines is how many lines of the message the field shows.
const forgeLaunchPreviewLines = 4

type forgeLaunchField struct {
	kind   session.LinkKind
	repo   github.Repo
	number int
	// repoRoot is the main worktree the session starts from.
	repoRoot   string
	headBranch string
	baseBranch string

	vars      prompttemplate.Vars
	templates map[string]prompttemplate.Template
	// order is the cycle: label matches first (matches of them), then the
	// rest; at == len(order) is none.
	order   []string
	matches int
	at      int
	// text is the selected template's text; loadErr why it could not be
	// read (a missing file).
	text    string
	loadErr error
	// open shows the template list under the field (the picker).
	open bool
	// edited is the message after ctrl+e; it replaces the rendered one.
	edited     bool
	editedText string
	// discardDir is the →/← that asked before discarding the edit.
	discardDir int

	// The background fetch of the body and, when used, the comments.
	fetchGen     int
	fetching     bool
	fetchedText  bool
	haveComments bool
	haveReview   bool
	fetchErr     error
	// waitLaunch: enter was pressed during the fetch; launch when it lands.
	waitLaunch bool
	// askEmpty: the fetch failed and the user was asked whether to launch
	// with those variables empty; the next enter does.
	askEmpty bool
	// launching is true from enter until the launch reports.
	launching bool

	// detailKey is the PR detail the launch fetched because it was not
	// cached (the fill falls back to the list row until it lands);
	// detailPending while it is in flight. enter waits for it.
	detailKey     freshness.Key
	detailPending bool
	// waitDetail: enter was pressed while the detail loaded; launch when
	// it lands.
	waitDetail bool
	// touched: the user changed the template, so a late detail does not
	// reselect it by its labels.
	touched bool

	// templateList is every template, for selectTemplates.
	templateList    []prompttemplate.Template
	configDir, home string
}

// applyDetail fills the variables from a cached or fetched detail and
// reports whether v was one.
func (f *forgeLaunchField) applyDetail(v any) bool {
	switch d := v.(type) {
	case github.IssueDetail:
		f.vars.Title, f.vars.URL, f.vars.Labels, f.vars.Author = d.Title, d.URL, d.Labels, d.Author
	case github.PRDetail:
		f.vars.Title, f.vars.URL, f.vars.Labels, f.vars.Author = d.Title, d.URL, d.Labels, d.Author
		f.headBranch, f.baseBranch = d.HeadBranch, d.BaseBranch
	default:
		return false
	}
	return true
}

// selectTemplates picks the template by §12's matching on the item's
// kind, Project and labels, and opens the picker when nothing was chosen
// or the chosen one cannot be read.
func (f *forgeLaunchField) selectTemplates() {
	sel := prompttemplate.Select(f.templateList, f.vars.Kind, f.vars.Repo, f.vars.Labels)
	f.order, f.matches = sel.Order, sel.Matches
	at := 0
	if i := indexOf(f.order, sel.Chosen); i >= 0 {
		at = i
	}
	f.selectAt(at)
	f.open = (sel.Chosen == "" && len(f.order) > 0) || f.loadErr != nil
}

// name is the selected template's name, or none.
func (f *forgeLaunchField) name() string {
	if f.at >= len(f.order) {
		return forgeTemplateNone
	}
	return f.order[f.at]
}

// select points the field at entry i of the cycle and loads its text.
func (f *forgeLaunchField) selectAt(i int) {
	n := len(f.order) + 1
	f.at = ((i % n) + n) % n
	f.text, f.loadErr = "", nil
	if t, ok := f.templates[f.name()]; ok {
		f.text, f.loadErr = t.Load(f.configDir, f.home)
	}
}

// message is the first message the launch sends: the edit, else the
// selected template filled with the item's variables ("" for none).
func (f *forgeLaunchField) message() string {
	if f.edited {
		return f.editedText
	}
	if f.at >= len(f.order) || f.loadErr != nil {
		return ""
	}
	return prompttemplate.Render(f.text, f.vars)
}

// needs reports which of the fetched variables the selected template uses.
func (f *forgeLaunchField) needs() (comments, review bool) {
	if f.edited || f.at >= len(f.order) {
		return false, false
	}
	return prompttemplate.Uses(f.text, prompttemplate.VarComments),
		f.vars.Kind == prompttemplate.KindPR && prompttemplate.Uses(f.text, prompttemplate.VarReviewComments)
}

// wantsFetch reports whether the selected template needs text not fetched
// yet (the body always comes with the first fetch). A failed fetch is not
// retried until the template changes.
func (f *forgeLaunchField) wantsFetch() bool {
	if f.fetching || f.fetchErr != nil {
		return false
	}
	c, r := f.needs()
	return !f.fetchedText || (c && !f.haveComments) || (r && !f.haveReview)
}

// applyText fills the variables from a fetch.
func (f *forgeLaunchField) applyText(t github.ItemText, comments, review bool) {
	f.vars.Body = t.Body
	f.fetchedText = true
	if comments {
		f.vars.Comments, f.haveComments = t.CommentsText(), true
	}
	if review {
		f.vars.ReviewComments, f.haveReview = t.ReviewText(), true
	}
}

// cycle moves the selection by dir. An edited message is discarded only
// on the second press in the same direction.
func (f *forgeLaunchField) cycle(dir int) {
	if f.edited && f.discardDir != dir {
		f.discardDir = dir
		return
	}
	f.edited, f.editedText, f.discardDir = false, "", 0
	f.askEmpty, f.fetchErr = false, nil
	f.touched = true
	f.selectAt(f.at + dir)
}

// setEdited takes the editor's text as the message.
func (f *forgeLaunchField) setEdited(text string) {
	f.edited, f.editedText, f.discardDir = true, strings.TrimRight(text, "\n"), 0
	f.touched = true
}

func (f *forgeLaunchField) label() string {
	name := f.name()
	if f.edited {
		return name + " (edited)"
	}
	return name
}

// forgeKey handles the keys the launch adds to the dialog: ctrl+e
// anywhere, ←/→ and enter on the Template field, and a read-only PR branch.
func (d *NewDialog) forgeKey(msg tea.KeyMsg, cur focusTarget) (bool, tea.Cmd) {
	f := d.forge
	key := msg.String()
	if key != "left" && key != "right" {
		f.discardDir = 0
	}
	if key == "ctrl+e" {
		return true, editPrompt(f.message())
	}
	switch cur {
	case focusTemplate:
		switch key {
		case "left":
			f.cycle(-1)
			return true, nil
		case "right":
			f.cycle(1)
			return true, nil
		case "enter":
			if f.open {
				f.open = false
				return true, nil
			}
		}
	case focusBranch:
		if f.kind == session.LinkPR && forgeEditsText(msg) {
			return true, nil
		}
	}
	return false, nil
}

// forgeEditsText reports whether msg would change a text input's value.
func forgeEditsText(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace, tea.KeyBackspace, tea.KeyDelete, tea.KeyCtrlW, tea.KeyCtrlU, tea.KeyCtrlK, tea.KeyCtrlH:
		return true
	}
	return false
}

// renderForgeTemplate writes the Template field: the name (with ‹ ›
// while focused), the list when open, its state (loading, errors, the
// discard question) and a preview of the message with its size.
func (d *NewDialog) renderForgeTemplate(content *strings.Builder, cur focusTarget, width int) {
	f := d.forge
	labelStyle := lipgloss.NewStyle().Foreground(ColorText)
	active := lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)
	dim := lipgloss.NewStyle().Foreground(ColorComment)
	warn := lipgloss.NewStyle().Foreground(ColorYellow)
	inner := max(10, width-8)
	value := f.label()
	if cur == focusTemplate {
		content.WriteString(active.Render("▶ " + newDialogInlineLabel("Template:")))
		content.WriteString(active.Render("‹ " + value + " ›"))
	} else {
		content.WriteString(labelStyle.Render("  " + newDialogInlineLabel("Template:")))
		content.WriteString(labelStyle.Render(value))
	}
	content.WriteString("\n")
	if f.open {
		names := append(append([]string{}, f.order...), forgeTemplateNone)
		for i, n := range names {
			mark := "    "
			if i == f.at {
				mark = "  ▸ "
			}
			if i < f.matches {
				n += "  (label match)"
			}
			content.WriteString(dim.Render(truncateDisplay(mark+n, inner, "…")) + "\n")
		}
	}
	note := ""
	switch {
	case f.loadErr != nil:
		note = warn.Render(truncateDisplay("  "+f.loadErr.Error(), inner, "…"))
	case f.discardDir != 0:
		note = warn.Render("  press again to discard the edit")
	case f.askEmpty:
		note = warn.Render(truncateDisplay("  comments did not load: enter to launch with them empty", inner, "…"))
	case f.waitDetail:
		note = dim.Render("  waiting for the PR details…")
	case f.waitLaunch:
		note = dim.Render("  waiting for comments…")
	case f.fetching:
		note = dim.Render("  loading comments…")
	}
	if note != "" {
		content.WriteString(note + "\n")
	}
	if msg := f.message(); msg != "" {
		lines := strings.Split(msg, "\n")
		shown := lines
		if len(shown) > forgeLaunchPreviewLines {
			shown = shown[:forgeLaunchPreviewLines]
		}
		for _, l := range shown {
			content.WriteString(dim.Render(truncateDisplay("  │ "+l, inner, "…")) + "\n")
		}
		content.WriteString(dim.Render(fmt.Sprintf("  %d lines, %s · ctrl+e edit", len(lines), forgeSize(len(msg)))) + "\n")
	} else if f.at >= len(f.order) {
		content.WriteString(dim.Render("  no first message; the session is still linked") + "\n")
	}
	content.WriteString("\n")
}

// forgeSize is a byte count as people read it.
func forgeSize(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}
