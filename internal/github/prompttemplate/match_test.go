package prompttemplate

import (
	"strings"
	"testing"
)

func TestSelect(t *testing.T) {
	const project = "acme/app"
	tests := []struct {
		name        string
		defs        map[string]Def
		kind        Kind
		labels      []string
		wantChosen  string
		wantOrder   string
		wantMatches int
	}{
		{name: "built-in default when nothing is configured", kind: KindIssue,
			wantChosen: "issue", wantOrder: "issue"},
		{name: "pr built-in default", kind: KindPR,
			wantChosen: "pr-review", wantOrder: "pr-review"},
		{name: "one label match is used, case-insensitive", kind: KindIssue, labels: []string{"Bug"},
			defs:       map[string]Def{"bug": {Labels: []string{"BUG"}, Text: "x"}, "feat": {Labels: []string{"feature"}, Text: "y"}},
			wantChosen: "bug", wantOrder: "bug,feat,issue", wantMatches: 1},
		{name: "labels match exactly, no substrings", kind: KindIssue, labels: []string{"bugfix"},
			defs:       map[string]Def{"bug": {Labels: []string{"bug"}, Text: "x"}},
			wantChosen: "issue", wantOrder: "bug,issue"},
		{name: "several matches open the picker in name order", kind: KindIssue, labels: []string{"bug", "ui"},
			defs:       map[string]Def{"z-ui": {Labels: []string{"ui"}, Text: "x"}, "a-bug": {Labels: []string{"bug"}, Text: "y"}, "other": {Text: "z"}},
			wantChosen: "", wantOrder: "a-bug,z-ui,issue,other", wantMatches: 2},
		{name: "repo-scoped templates rank first", kind: KindIssue, labels: []string{"bug"},
			defs: map[string]Def{
				"a-global": {Labels: []string{"bug"}, Text: "x"},
				"z-mine":   {Labels: []string{"bug"}, Text: "y", Repos: []string{"Acme/App"}},
				"m-rest":   {Text: "r", Repos: []string{"acme/app"}}},
			wantChosen: "", wantOrder: "z-mine,a-global,m-rest,issue", wantMatches: 2},
		{name: "templates for other repos do not apply", kind: KindIssue, labels: []string{"bug"},
			defs:       map[string]Def{"theirs": {Labels: []string{"bug"}, Text: "x", Repos: []string{"other/repo"}}},
			wantChosen: "issue", wantOrder: "issue"},
		{name: "other kind does not apply", kind: KindPR, labels: []string{"bug"},
			defs:       map[string]Def{"bug": {Labels: []string{"bug"}, Text: "x"}},
			wantChosen: "pr-review", wantOrder: "pr-review"},
		{name: "user default beats the built-in default", kind: KindIssue,
			defs:       map[string]Def{"mine": {Default: true, Text: "x"}},
			wantChosen: "mine", wantOrder: "issue,mine"},
		{name: "several user defaults: first by name wins", kind: KindIssue,
			defs:       map[string]Def{"b": {Default: true, Text: "x"}, "a": {Default: true, Text: "y"}},
			wantChosen: "a", wantOrder: "a,b,issue"},
		{name: "repo default beats a global default", kind: KindIssue,
			defs:       map[string]Def{"a": {Default: true, Text: "x"}, "z": {Default: true, Text: "y", Repos: []string{"acme/app"}}},
			wantChosen: "z", wantOrder: "z,a,issue"},
		{name: "no match and no default opens the picker", kind: KindIssue,
			defs:       map[string]Def{"issue": {Text: "mine, not default"}, "b": {Text: "y"}},
			wantChosen: "", wantOrder: "b,issue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, _ := Build(tt.defs, "/cfg", "/home/u")
			got := Select(ts, tt.kind, project, tt.labels)
			if got.Chosen != tt.wantChosen {
				t.Errorf("Chosen = %q, want %q", got.Chosen, tt.wantChosen)
			}
			if order := strings.Join(got.Order, ","); order != tt.wantOrder {
				t.Errorf("Order = %s, want %s", order, tt.wantOrder)
			}
			if got.Matches != tt.wantMatches {
				t.Errorf("Matches = %d, want %d", got.Matches, tt.wantMatches)
			}
		})
	}
}
