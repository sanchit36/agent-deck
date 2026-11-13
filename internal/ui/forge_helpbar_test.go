package ui

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

// TestForgeHelpBarPriority: when the forge bar is too narrow for every key
// the most important ones stay (view, refresh, filter, new) and ? help is
// always there; the kept keys keep their usual order.
func TestForgeHelpBarPriority(t *testing.T) {
	for _, c := range []struct {
		w, h int
		want []string
	}{
		{120, 30, []string{"[/] tabs", "enter view", "n new", "o open", "y copy url", "/ filter", "r refresh", "p project", "P parent", "S settings", "? help"}},
		{70, 24, []string{"enter view", "n new", "/ filter", "r refresh", "? help"}},
		{50, 20, []string{"enter view", "? help"}},
	} {
		h, _ := issuesHome(t, c.w, c.h, githubtest.IssueList)
		lines := frameLines(h.View())
		bar := lines[len(lines)-1]
		at := -1
		for _, w := range c.want {
			i := strings.Index(bar, w)
			if i < 0 {
				t.Errorf("%dx%d: bar %q lacks %q", c.w, c.h, bar, w)
				continue
			}
			if i < at {
				t.Errorf("%dx%d: bar %q has %q out of order", c.w, c.h, bar, w)
			}
			at = i
		}
	}
}
