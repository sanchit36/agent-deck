package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/childenv"
	tea "github.com/charmbracelet/bubbletea"
)

// Diff pager (spec #18 §10): on the Files tab | sends the selected file's
// part of the diff and \ the whole PR's on stdin to [github] diff_pager,
// handing the terminal over until the pager quits. The overlay stays as
// it was.

// forgeDiffPagerFallback is the pager when nothing else is configured.
const forgeDiffPagerFallback = "less -R"

// forgePagerDoneMsg reports a pager that returned; err is its failure.
type forgePagerDoneMsg struct {
	pager string
	err   error
}

// forgeDiffPager resolves the pager command: configured ([github]
// diff_pager) when set, else delta when it is on PATH, else $PAGER, else
// less -R.
func forgeDiffPager(configured string, lookPath func(string) (string, error), getenv func(string) string) string {
	if s := strings.TrimSpace(configured); s != "" {
		return s
	}
	if _, err := lookPath("delta"); err == nil {
		return "delta"
	}
	if s := strings.TrimSpace(getenv("PAGER")); s != "" {
		return s
	}
	return forgeDiffPagerFallback
}

// forgeDiffPagerCommand is the pager's process: the command through the
// shell (it may carry flags), text on stdin, the environment childenv
// gives a child. The pager reads its keys from the terminal itself.
func forgeDiffPagerCommand(pager, text string) *exec.Cmd {
	// #nosec G204 -- [github] diff_pager / $PAGER is the user's own shell
	// command; the diff goes on stdin, never into the command line.
	cmd := exec.Command("sh", "-c", pager)
	cmd.Stdin = strings.NewReader(text)
	cmd.Env = childenv.ForLaunch("")
	return cmd
}

// forgeOpenPager is | (whole false: the selected file) and \ (whole: the
// PR): the diff on screen into the pager. Nothing before a diff arrives.
func (h *Home) forgeOpenPager(whole bool) tea.Cmd {
	d, ok := h.forgeFilesDiff()
	if !ok {
		return nil
	}
	text := d.Text
	if !whole {
		text = d.FileText(h.forge.overlay.files.file)
	}
	if text == "" {
		return nil
	}
	cfg := loadForgeConfig()
	pager := forgeDiffPager(cfg.GitHubDiffPager(), exec.LookPath, os.Getenv)
	cmd := forgeDiffPagerCommand(pager, text)
	if h.forge.runPager != nil {
		return h.forge.runPager(cmd)
	}
	return execProcessHoldingInput(cmd, func(err error) tea.Msg {
		return forgePagerDoneMsg{pager: pager, err: err}
	})
}

// applyForgePagerDone reports a pager that failed (a missing command, a
// non-zero exit): in the log and, since the overlay covers the footer's
// toasts, in the overlay's status row until the next Files key.
func (h *Home) applyForgePagerDone(msg forgePagerDoneMsg) {
	if msg.err == nil {
		return
	}
	h.setError(fmt.Errorf("running diff pager %q: %w", msg.pager, msg.err))
	if h.forgeOverlayVisible() {
		h.forge.overlay.files.pagerErr = fmt.Sprintf("diff pager %q: %v", msg.pager, msg.err)
	}
}
