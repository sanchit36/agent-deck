package prompttemplate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Def is one [prompt_templates.<name>] table in config.toml.
type Def struct {
	// AppliesTo is "issue" (default) or "pr".
	AppliesTo string `toml:"applies_to,omitempty"`
	// Labels select the template for an item carrying any of them (exact,
	// case-insensitive). Empty: never selected by label.
	Labels []string `toml:"labels,omitempty"`
	// Default makes this the fallback for its kind when no label matches.
	Default bool `toml:"default,omitempty"`
	// Repos limits the template to these owner/name Projects. Empty: all.
	Repos []string `toml:"repos,omitempty"`
	// Text is the inline template. Set exactly one of Text and File.
	Text string `toml:"text,omitempty"`
	// File is a template file, read at launch. Relative paths resolve
	// against the config dir; ~ is expanded.
	File string `toml:"file,omitempty"`
}

// Template is a validated user or built-in template.
type Template struct {
	Name    string
	Kind    Kind
	Labels  []string
	Default bool
	Repos   []string
	Text    string
	File    string
	Builtin bool
}

// Warning is a config-load problem with one template (or, for clashing
// defaults, a kind).
type Warning struct {
	Template string
	Message  string
}

func (w Warning) String() string {
	if w.Template == "" {
		return "prompt_templates: " + w.Message
	}
	return fmt.Sprintf("prompt_templates %q: %s", w.Template, w.Message)
}

const builtinIssueText = `Work on GitHub issue #{number} in {repo}: {title}
{url}
Labels: {labels}
Opened by: {author}

{body}

Comments:
{comments}

Investigate the issue, make the change in this worktree, and add or update tests that cover it.`

const builtinPRReviewText = `Address the review feedback on pull request #{number} in {repo}: {title}
{url}
Branch {head-branch} into {base-branch}, opened by {author}.

{body}

Review comments:
{review-comments}

Comments:
{comments}

Work through each review comment: make the change it asks for, or explain why not. Then run the tests.`

// Builtins returns the templates Agent Deck ships: issue and pr-review.
func Builtins() []Template {
	return []Template{
		{Name: "issue", Kind: KindIssue, Default: true, Text: builtinIssueText, Builtin: true},
		{Name: "pr-review", Kind: KindPR, Default: true, Text: builtinPRReviewText, Builtin: true},
	}
}

// Build merges the user's templates over the built-ins and checks them. A
// user template replaces the built-in of the same name. A template whose
// shape is invalid (bad applies_to, not exactly one of text/file) is dropped;
// every other problem only warns. File templates are read best-effort to
// check their variables; they are read again at launch. The result is
// sorted by name.
func Build(defs map[string]Def, configDir, home string) ([]Template, []Warning) {
	byName := map[string]Template{}
	for _, b := range Builtins() {
		byName[b.Name] = b
	}
	var warnings []Warning
	userNames := make([]string, 0, len(defs))
	for name := range defs {
		userNames = append(userNames, name)
	}
	sort.Strings(userNames)
	for _, name := range userNames {
		t, ws, ok := buildOne(name, defs[name], configDir, home)
		warnings = append(warnings, ws...)
		if ok {
			byName[name] = t
		}
	}
	out := make([]Template, 0, len(byName))
	for _, t := range byName {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	warnings = append(warnings, defaultClashes(out)...)
	return out, warnings
}

func buildOne(name string, d Def, configDir, home string) (Template, []Warning, bool) {
	warn := func(format string, args ...any) Warning {
		return Warning{Template: name, Message: fmt.Sprintf(format, args...)}
	}
	var ws []Warning
	kind := KindIssue
	switch strings.ToLower(strings.TrimSpace(d.AppliesTo)) {
	case "", string(KindIssue):
	case string(KindPR):
		kind = KindPR
	default:
		return Template{}, []Warning{warn("applies_to %q is not \"issue\" or \"pr\"; template ignored", d.AppliesTo)}, false
	}
	if (d.Text == "") == (d.File == "") {
		return Template{}, []Warning{warn("set exactly one of text or file; template ignored")}, false
	}
	for _, r := range d.Repos {
		if !validRepo(r) {
			ws = append(ws, warn("repos entry %q is not owner/name", r))
		}
	}
	t := Template{Name: name, Kind: kind, Labels: d.Labels, Default: d.Default, Repos: d.Repos, Text: d.Text, File: d.File}
	text, err := t.Load(configDir, home)
	if err != nil {
		ws = append(ws, warn("reading file: %v (it is read again at launch)", errors.Unwrap(err)))
		return t, ws, true
	}
	for _, v := range Variables(text) {
		switch {
		case !isKnownVariable(v):
			ws = append(ws, warn("unknown variable {%s} stays as written", v))
		case kind == KindIssue && isPRVariable(v):
			ws = append(ws, warn("{%s} is a pull request variable and renders empty in an issue template", v))
		}
	}
	return t, ws, true
}

func validRepo(r string) bool {
	owner, name, ok := strings.Cut(strings.TrimSpace(r), "/")
	return ok && owner != "" && name != "" && !strings.Contains(name, "/")
}

// defaultClashes warns when two user defaults of a kind can apply to the same
// Project: both global, or both listing a common repo. The first by name wins.
func defaultClashes(ts []Template) []Warning {
	var ws []Warning
	for _, kind := range []Kind{KindIssue, KindPR} {
		var defaults []Template
		for _, t := range ts {
			if t.Kind == kind && t.Default && !t.Builtin {
				defaults = append(defaults, t)
			}
		}
		clashing := map[string]bool{}
		for i := range defaults {
			for j := i + 1; j < len(defaults); j++ {
				if scopesOverlap(defaults[i], defaults[j]) {
					clashing[defaults[i].Name] = true
					clashing[defaults[j].Name] = true
				}
			}
		}
		if len(clashing) == 0 {
			continue
		}
		var list []string
		for _, t := range defaults {
			if clashing[t.Name] {
				list = append(list, t.Name)
			}
		}
		ws = append(ws, Warning{Message: fmt.Sprintf("several default templates for %s (%s); the first by name wins", kind, strings.Join(list, ", "))})
	}
	return ws
}

func scopesOverlap(a, b Template) bool {
	if len(a.Repos) == 0 && len(b.Repos) == 0 {
		return true
	}
	for _, ra := range a.Repos {
		for _, rb := range b.Repos {
			if strings.EqualFold(strings.TrimSpace(ra), strings.TrimSpace(rb)) {
				return true
			}
		}
	}
	return false
}

// Path resolves a file template's path: ~ expands to home, and a relative
// path resolves against configDir.
func (t Template) Path(configDir, home string) string {
	p := strings.TrimSpace(t.File)
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	case filepath.IsAbs(p):
		return p
	default:
		return filepath.Join(configDir, p)
	}
}

// Load returns the template text: the inline text, or the file read now (at
// launch, so edits need no restart).
func (t Template) Load(configDir, home string) (string, error) {
	if t.File == "" {
		return t.Text, nil
	}
	path := t.Path(configDir, home)
	data, err := os.ReadFile(path) //nolint:gosec // the user's own configured template file
	if err != nil {
		return "", fmt.Errorf("reading prompt template %q file %s: %w", t.Name, path, err)
	}
	return string(data), nil
}
