// Package ghexec builds `gh` CLI commands the one way agent-deck runs them:
// with the childenv-based environment (no prompts, no color, no update
// notice) and bound to a timeout, so a gh that waits on a prompt or a stuck
// network call can never hang the caller.
//
// It sits below every package that runs gh (internal/feedback,
// internal/update, internal/issuereport, internal/ui, cmd/agent-deck) so all
// of them can share it without an import cycle.
package ghexec

import (
	"context"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/childenv"
)

// MergeTimeout bounds `gh pr merge`, which waits on GitHub's merge.
const MergeTimeout = 60 * time.Second

const defaultTimeout = 20 * time.Second

// waitDelay bounds how long Wait waits for gh's output pipes after the
// timeout kills gh, in case a gh child process still holds them.
const waitDelay = 2 * time.Second

var defaultTimeoutNanos atomic.Int64

func init() { defaultTimeoutNanos.Store(int64(defaultTimeout)) }

// DefaultTimeout is the timeout of a gh call that names none (20 s).
func DefaultTimeout() time.Duration { return time.Duration(defaultTimeoutNanos.Load()) }

// SetDefaultTimeoutForTest sets DefaultTimeout and returns a func that
// restores it. Tests only.
func SetDefaultTimeoutForTest(d time.Duration) (restore func()) {
	prev := defaultTimeoutNanos.Swap(int64(d))
	return func() { defaultTimeoutNanos.Store(prev) }
}

// Env is the environment gh runs with: childenv.ForLaunch plus
// GH_PROMPT_DISABLED=1, NO_COLOR=1 and GH_NO_UPDATE_NOTIFIER=1.
func Env() []string {
	return append(childenv.ForLaunch(""), "GH_PROMPT_DISABLED=1", "NO_COLOR=1", "GH_NO_UPDATE_NOTIFIER=1")
}

// Command returns `gh args...` (gh found on PATH) running with Env and
// killed when timeout elapses (DefaultTimeout when timeout <= 0) or ctx is
// done. The caller must call cancel once the command has finished.
func Command(ctx context.Context, timeout time.Duration, args ...string) (*exec.Cmd, context.CancelFunc) {
	if timeout <= 0 {
		timeout = DefaultTimeout()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	cmd := exec.CommandContext(ctx, "gh", args...) // #nosec G204 -- fixed binary; args are the caller's gh arguments, not a shell line
	cmd.Env = Env()
	cmd.WaitDelay = waitDelay
	return cmd, cancel
}
