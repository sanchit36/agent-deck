package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// linkBadgeRows are the sessions the link-badge goldens draw: no link, an
// Issue, a PR, both, and both next to a worktree branch badge.
func linkBadgeRows() []*session.Instance {
	issue := session.Link{Host: "github.com", Owner: "acme", Name: "app", Number: 12}
	pr := session.Link{Host: "ghe.corp.example", Owner: "team", Name: "svc", Number: 34}
	mk := func(id, title string, i, p *session.Link, branch string) *session.Instance {
		inst := &session.Instance{ID: id, Title: title, Tool: "claude", Status: session.StatusIdle}
		if i != nil {
			inst.SetLink(session.LinkIssue, *i)
		}
		if p != nil {
			inst.SetLink(session.LinkPR, *p)
		}
		if branch != "" {
			inst.WorktreePath, inst.WorktreeBranch = "/tmp/"+id, branch
		}
		return inst
	}
	return []*session.Instance{
		mk("none", "no link", nil, nil, ""),
		mk("issue", "fix login", &issue, nil, ""),
		mk("pr", "review svc", nil, &pr, ""),
		mk("both", "issue and pr", &issue, &pr, ""),
		mk("wt", "a long worktree session title", &issue, &pr, "feat/login"),
	}
}

func assertLinkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "link_badge", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s differs\nwant:\n%s\ngot:\n%s", path, want, got)
	}
}

// TestLinkBadgeRowsGolden freezes the static `#12` / `PR#34` badge on classic
// session rows at three terminal widths, selected and not.
func TestLinkBadgeRowsGolden(t *testing.T) {
	for _, terminalWidth := range []int{80, 120, 200} {
		t.Run(fmt.Sprint(terminalWidth), func(t *testing.T) {
			h := NewHome()
			h.width, h.height = terminalWidth, 40
			width := h.sessionsPaneWidth()
			var b strings.Builder
			rows := linkBadgeRows()
			snapshot := map[string]sessionRenderState{}
			for _, inst := range rows {
				snapshot[inst.ID] = sessionRenderState{status: session.StatusIdle, tool: "claude", title: inst.Title}
			}
			for i, inst := range rows {
				item := session.Item{Type: session.ItemTypeSession, Session: inst, Level: 1, IsLastInGroup: i == len(rows)-1}
				h.renderSessionItem(&b, item, false, snapshot, width)
				h.renderSessionItem(&b, item, true, snapshot, width)
			}
			got := fmt.Sprintf("terminal_width=%d list_width=%d\n%s", terminalWidth, width, stripAnsi(b.String()))
			assertLinkGolden(t, fmt.Sprintf("%d.txt", terminalWidth), got)
		})
	}
}

// TestLinkBadgeEmbeddedCardGolden: the embedded sidebar card carries the
// links on its metadata line.
func TestLinkBadgeEmbeddedCardGolden(t *testing.T) {
	h := NewHome()
	h.width, h.height = 120, 40
	h.embeddedLayout = true
	var b strings.Builder
	for _, inst := range linkBadgeRows() {
		state := sessionRenderState{status: session.StatusIdle, tool: "claude", title: inst.Title}
		h.renderSessionItem(&b, session.Item{Type: session.ItemTypeSession, Session: inst, Level: 1}, false, map[string]sessionRenderState{inst.ID: state}, 40)
	}
	assertLinkGolden(t, "embedded.txt", stripAnsi(b.String()))
}

// TestLinkBadgeRenderDoesNoIO: the badge is drawn from the stored link only.
// Its helper may not reach gh, git, the filesystem or the state DB, and the
// row renderer may only take it from that helper.
func TestLinkBadgeRenderDoesNoIO(t *testing.T) {
	for _, file := range []string{"home.go", "embedded_sidebar.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !strings.Contains(string(src), "sessionLinkBadgeText(inst)") {
			t.Errorf("%s does not draw the link badge through sessionLinkBadgeText", file)
		}
	}
	src, err := os.ReadFile("link_badge.go")
	if err != nil {
		t.Fatalf("read link_badge.go: %v", err)
	}
	body := funcBody(t, string(src), "func sessionLinkBadgeText(")
	for _, banned := range []string{"exec.", "os.", "github.", "gitstate", "storage", "Load", "Fetch", "http"} {
		if strings.Contains(body, banned) {
			t.Errorf("sessionLinkBadgeText mentions %q: the row badge must come from the stored link only", banned)
		}
	}
	inst := &session.Instance{ID: "x"}
	if got := sessionLinkBadgeText(inst); got != "" {
		t.Fatalf("unlinked badge = %q, want empty", got)
	}
	inst.SetLink(session.LinkPR, session.Link{Host: "h", Owner: "o", Name: "n", Number: 34})
	inst.SetLink(session.LinkIssue, session.Link{Host: "h", Owner: "o", Name: "n", Number: 12})
	if got := sessionLinkBadgeText(inst); got != "#12 PR#34" {
		t.Fatalf("badge = %q, want %q", got, "#12 PR#34")
	}
}
