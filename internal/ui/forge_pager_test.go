package ui

import (
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestForgeDiffPagerResolution pins the pager's resolution order (spec #18
// §10): [github] diff_pager when set, else delta when it is on PATH, else
// $PAGER, else less -R.
func TestForgeDiffPagerResolution(t *testing.T) {
	onPath := func(names ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			if slices.Contains(names, name) {
				return "/usr/bin/" + name, nil
			}
			return "", exec.ErrNotFound
		}
	}
	env := func(pager string) func(string) string {
		return func(k string) string {
			if k == "PAGER" {
				return pager
			}
			return ""
		}
	}
	for _, tc := range []struct {
		name, configured string
		lookPath         func(string) (string, error)
		getenv           func(string) string
		want             string
	}{
		{"configured wins over delta and PAGER", "bat --paging=always", onPath("delta"), env("most"), "bat --paging=always"},
		{"delta on PATH", "", onPath("delta"), env("most"), "delta"},
		{"PAGER without delta", "", onPath(), env("most -s"), "most -s"},
		{"blank PAGER falls through", "", onPath(), env("  "), "less -R"},
		{"nothing set", "", onPath(), env(""), "less -R"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := forgeDiffPager(tc.configured, tc.lookPath, tc.getenv); got != tc.want {
				t.Fatalf("forgeDiffPager = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestForgeDiffPagerCommand: the pager runs through the shell with the
// diff on stdin, and its environment comes from childenv (no TELEGRAM_*,
// no inherited CLAUDE_CONFIG_DIR).
func TestForgeDiffPagerCommand(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "secret")
	t.Setenv("CLAUDE_CONFIG_DIR", "/nonexistent/claude")
	cmd := forgeDiffPagerCommand("less -R", "diff --git a/x b/x\n")
	if !slices.Equal(cmd.Args, []string{"sh", "-c", "less -R"}) {
		t.Fatalf("args = %q, want sh -c 'less -R'", cmd.Args)
	}
	if cmd.Env == nil {
		t.Fatal("the pager inherits the TUI's environment instead of childenv's")
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "TELEGRAM_") || strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			t.Fatalf("the pager's env carries %q", kv)
		}
	}
	if !slices.ContainsFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") }) {
		t.Fatal("the pager's env has no PATH")
	}
	in, err := io.ReadAll(cmd.Stdin)
	if err != nil || string(in) != "diff --git a/x b/x\n" {
		t.Fatalf("stdin = %q (%v), want the diff", in, err)
	}
}

// TestForgeFilesPagerKeys: | hands the selected file's part of the diff
// to the pager and \ the whole PR's; a pager that fails says so.
func TestForgeFilesPagerKeys(t *testing.T) {
	h := navHome(t, 120, 30)
	var got []*exec.Cmd
	h.forge.runPager = func(c *exec.Cmd) tea.Cmd {
		got = append(got, c)
		return func() tea.Msg { return forgePagerDoneMsg{pager: "less -R", err: errors.New("exit status 2")} }
	}
	h = drive(t, h, runeKey(']'))
	h = drive(t, h, runeKey('|'))
	h = drive(t, h, runeKey('\\'))
	if len(got) != 2 {
		t.Fatalf("%d pager runs, want 2", len(got))
	}
	d, _ := h.forgeFilesDiff()
	for i, want := range []string{d.FileText(1), d.Text} {
		in, _ := io.ReadAll(got[i].Stdin)
		if string(in) != want {
			t.Fatalf("pager %d got %q, want %q", i, in, want)
		}
		if got[i].Args[0] != "sh" || got[i].Args[1] != "-c" {
			t.Fatalf("pager %d args %q", i, got[i].Args)
		}
	}
	if !h.forgeOverlayVisible() || h.forge.overlay.tab != forgeOverlayFiles {
		t.Fatal("the overlay did not stay on Files after the pager")
	}
	// The overlay covers the footer toasts: the failure shows in its own
	// status row until the next key.
	if frame := filesFrame(h); !strings.Contains(frame, `⚠ diff pager "less -R": exit status 2`) {
		t.Fatalf("a failed pager is not reported on the overlay:\n%s", frame)
	}
	h = drive(t, h, runeKey('j'))
	if frame := filesFrame(h); strings.Contains(frame, "diff pager") {
		t.Fatalf("the pager failure outlived the next key:\n%s", frame)
	}
}
