package prompttemplate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func names(ts []Template) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func find(ts []Template, name string) (Template, bool) {
	for _, t := range ts {
		if t.Name == name {
			return t, true
		}
	}
	return Template{}, false
}

func TestDefDecodesFromTOML(t *testing.T) {
	src := `
[prompt_templates.bug]
labels = ["bug", "regression"]
repos = ["acme/app"]
text = "Fix #{number}"

[prompt_templates.review]
applies_to = "pr"
default = true
file = "templates/review.md"
`
	var cfg struct {
		PromptTemplates map[string]Def `toml:"prompt_templates"`
	}
	if _, err := toml.Decode(src, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	bug := cfg.PromptTemplates["bug"]
	if strings.Join(bug.Labels, ",") != "bug,regression" || bug.Repos[0] != "acme/app" || bug.Text != "Fix #{number}" || bug.AppliesTo != "" {
		t.Errorf("bug = %+v", bug)
	}
	review := cfg.PromptTemplates["review"]
	if review.AppliesTo != "pr" || !review.Default || review.File != "templates/review.md" {
		t.Errorf("review = %+v", review)
	}
}

func TestBuildWithNoUserTemplatesGivesTheBuiltins(t *testing.T) {
	ts, warnings := Build(nil, "/cfg", "/home/u")
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
	if got := strings.Join(names(ts), ","); got != "issue,pr-review" {
		t.Fatalf("templates = %s, want issue,pr-review", got)
	}
	issue, _ := find(ts, "issue")
	if issue.Kind != KindIssue || !issue.Default || !issue.Builtin || issue.Text == "" {
		t.Errorf("issue built-in = %+v", issue)
	}
	review, _ := find(ts, "pr-review")
	if review.Kind != KindPR || !review.Default || !review.Builtin || !Uses(review.Text, VarReviewComments) {
		t.Errorf("pr-review built-in = %+v", review)
	}
}

func TestBuildUserTemplateReplacesBuiltinOfTheSameName(t *testing.T) {
	ts, warnings := Build(map[string]Def{"issue": {Text: "mine #{number}"}}, "/cfg", "/home/u")
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
	issue, _ := find(ts, "issue")
	if issue.Builtin || issue.Text != "mine #{number}" || issue.Default {
		t.Errorf("issue = %+v, want the user's template (not default unless it says so)", issue)
	}
	if len(ts) != 2 {
		t.Errorf("templates = %v", names(ts))
	}
}

func TestBuildWarnings(t *testing.T) {
	cfgDir := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "rel.md"), []byte("rel {bogus}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "tilde.md"), []byte("{head-branch}"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		defs     map[string]Def
		want     []string // substrings, one per expected warning, in order
		dropped  []string // templates missing from the result
		kept     []string
		wantKind map[string]Kind
	}{
		{name: "clean", defs: map[string]Def{"a": {Text: "{title} {comments}"}, "b": {AppliesTo: "pr", Text: "{review-comments}"}},
			kept: []string{"a", "b"}, wantKind: map[string]Kind{"a": KindIssue, "b": KindPR}},
		{name: "unknown variable", defs: map[string]Def{"a": {Text: "{titel} and {nope}"}},
			want: []string{`"a": unknown variable {titel}`, `"a": unknown variable {nope}`}, kept: []string{"a"}},
		{name: "pr variable in an issue template", defs: map[string]Def{"a": {Text: "{head-branch} {review-comments}"}},
			want: []string{`"a": {head-branch} is a pull request variable`, `"a": {review-comments} is a pull request variable`}, kept: []string{"a"}},
		{name: "bad applies_to is dropped", defs: map[string]Def{"a": {AppliesTo: "commit", Text: "x"}},
			want: []string{`"a": applies_to "commit"`}, dropped: []string{"a"}},
		{name: "applies_to is case-insensitive", defs: map[string]Def{"a": {AppliesTo: " PR ", Text: "{head-branch}"}},
			kept: []string{"a"}, wantKind: map[string]Kind{"a": KindPR}},
		{name: "both text and file", defs: map[string]Def{"a": {Text: "x", File: "rel.md"}},
			want: []string{`"a": set exactly one of text or file`}, dropped: []string{"a"}},
		{name: "neither text nor file", defs: map[string]Def{"a": {Labels: []string{"bug"}}},
			want: []string{`"a": set exactly one of text or file`}, dropped: []string{"a"}},
		{name: "bad repos entry", defs: map[string]Def{"a": {Text: "x", Repos: []string{"acme/app", "justname", "a/b/c"}}},
			want: []string{`"a": repos entry "justname"`, `"a": repos entry "a/b/c"`}, kept: []string{"a"}},
		{name: "relative file is checked against the config dir", defs: map[string]Def{"a": {File: "rel.md"}},
			want: []string{`"a": unknown variable {bogus}`}, kept: []string{"a"}},
		{name: "tilde file is checked against home", defs: map[string]Def{"a": {File: "~/tilde.md"}},
			want: []string{`"a": {head-branch} is a pull request variable`}, kept: []string{"a"}},
		{name: "missing file warns but is kept (read again at launch)", defs: map[string]Def{"a": {File: "gone.md"}},
			want: []string{`"a": reading file`}, kept: []string{"a"}},
		{name: "two global defaults for a kind", defs: map[string]Def{"b": {Default: true, Text: "x"}, "a": {Default: true, Text: "y"}},
			want: []string{`several default templates for issue (a, b)`}, kept: []string{"a", "b"}},
		{name: "defaults for different repos do not clash", defs: map[string]Def{
			"a": {Default: true, Text: "x", Repos: []string{"acme/app"}},
			"b": {Default: true, Text: "y", Repos: []string{"acme/lib"}},
			"c": {Default: true, Text: "z"}}},
		{name: "defaults sharing a repo clash", defs: map[string]Def{
			"a": {Default: true, Text: "x", Repos: []string{"Acme/App"}},
			"b": {Default: true, Text: "y", Repos: []string{"acme/app"}}},
			want: []string{`several default templates for issue (a, b)`}},
		{name: "defaults of different kinds do not clash", defs: map[string]Def{
			"a": {Default: true, Text: "x"}, "b": {AppliesTo: "pr", Default: true, Text: "y"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, warnings := Build(tt.defs, cfgDir, home)
			if len(warnings) != len(tt.want) {
				t.Fatalf("warnings = %q, want %d matching %q", warnings, len(tt.want), tt.want)
			}
			for i, w := range tt.want {
				if !strings.Contains(warnings[i].String(), w) {
					t.Errorf("warning %d = %q, want it to contain %q", i, warnings[i].String(), w)
				}
			}
			for _, n := range tt.dropped {
				if _, ok := find(ts, n); ok {
					t.Errorf("template %q should be dropped", n)
				}
			}
			for _, n := range tt.kept {
				if _, ok := find(ts, n); !ok {
					t.Errorf("template %q should be kept", n)
				}
			}
			for n, k := range tt.wantKind {
				if got, _ := find(ts, n); got.Kind != k {
					t.Errorf("template %q kind = %q, want %q", n, got.Kind, k)
				}
			}
		})
	}
}

func TestTemplateLoad(t *testing.T) {
	cfgDir := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "rel.md"), []byte("from config dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "tilde.md"), []byte("from home"), 0o600); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(t.TempDir(), "abs.md")
	if err := os.WriteFile(abs, []byte("absolute"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		tpl     Template
		want    string
		wantErr string
	}{
		{"inline text", Template{Name: "a", Text: "inline"}, "inline", ""},
		{"relative file", Template{Name: "a", File: "rel.md"}, "from config dir", ""},
		{"tilde file", Template{Name: "a", File: "~/tilde.md"}, "from home", ""},
		{"absolute file", Template{Name: "a", File: abs}, "absolute", ""},
		{"missing file", Template{Name: "a", File: "gone.md"}, "", `reading prompt template "a" file`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.tpl.Load(cfgDir, home)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Load() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
