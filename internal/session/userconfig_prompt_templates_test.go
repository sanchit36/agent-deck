package session

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestUserConfigPromptTemplates pins that [prompt_templates.<name>] decodes
// onto UserConfig and that BuildPromptTemplates merges it over the built-ins
// and reports the config-load warnings (spec #18 §12).
func TestUserConfigPromptTemplates(t *testing.T) {
	src := `
[prompt_templates.bug]
labels = ["bug"]
text = "Fix #{number}: {title} {head-brnch}"

[prompt_templates.review]
applies_to = "pr"
text = "{review-comments}"

[prompt_templates.broken]
applies_to = "commit"
text = "x"
`
	var cfg UserConfig
	if _, err := toml.Decode(src, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cfg.PromptTemplates) != 3 {
		t.Fatalf("PromptTemplates = %+v", cfg.PromptTemplates)
	}

	ts, warnings := cfg.BuildPromptTemplates(t.TempDir(), t.TempDir())
	var got []string
	for _, tpl := range ts {
		got = append(got, tpl.Name)
	}
	if strings.Join(got, ",") != "bug,issue,pr-review,review" {
		t.Errorf("templates = %v, want bug,issue,pr-review,review", got)
	}
	var msgs []string
	for _, w := range warnings {
		msgs = append(msgs, w.String())
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{`"broken": applies_to "commit"`, `"bug": unknown variable {head-brnch}`} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q missing %q", joined, want)
		}
	}
	if len(warnings) != 2 {
		t.Errorf("warnings = %q, want 2", msgs)
	}
}

func TestUserConfigPromptTemplatesUnsetGivesBuiltins(t *testing.T) {
	var cfg UserConfig
	ts, warnings := cfg.BuildPromptTemplates(t.TempDir(), t.TempDir())
	if len(ts) != 2 || len(warnings) != 0 {
		t.Errorf("templates = %d, warnings = %v; want the 2 built-ins and no warnings", len(ts), warnings)
	}
}
