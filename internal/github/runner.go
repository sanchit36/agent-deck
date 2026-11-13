// Package github is agent-deck's only way to talk to GitHub for the forge
// tabs: it shells out to the `gh` CLI through a Runner, always names the
// repository with `-R HOST/OWNER/REPO`, bounds every call with a timeout and
// turns a failed call into an *Error with a Class the UI can act on.
//
// Tests replace the Runner with githubtest.Fake, which serves recorded
// fixtures; nothing here touches the network or real gh auth in tests.
package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/ghexec"
)

// Call is one gh invocation: the arguments after `gh`, optional stdin and a
// timeout (0 means ghexec.DefaultTimeout, 20 s; merge passes
// ghexec.MergeTimeout).
type Call struct {
	Args    []string
	Stdin   []byte
	Timeout time.Duration
	// Dir is the working directory gh runs in; empty means this process's.
	// Only commands that act on the local checkout (gh pr checkout) set it.
	Dir string
}

// Result is what a gh process that ran to completion produced. A non-zero
// ExitCode is not a Runner error: Classify turns it into one.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner runs gh. Run returns an error only when gh did not run to
// completion: the error wraps exec.ErrNotFound when gh is not installed,
// context.DeadlineExceeded when the call's timeout elapsed and ctx's own
// error when ctx was cancelled. A gh that exited non-zero is a nil error and
// a Result with that ExitCode.
type Runner interface {
	Run(ctx context.Context, call Call) (Result, error)
}

// ExecRunner runs the real gh found on PATH, with the gh child env and
// timeout from internal/ghexec.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, call Call) (Result, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return Result{}, fmt.Errorf("looking up gh: %w", err)
	}
	timeout := call.Timeout
	if timeout <= 0 {
		timeout = ghexec.DefaultTimeout()
	}
	// Our own deadline is set before ghexec's, so it is the one that has
	// expired when the call is killed for taking too long.
	callCtx, cancelCall := context.WithTimeout(ctx, timeout)
	defer cancelCall()
	cmd, cancel := ghexec.Command(callCtx, timeout, call.Args...)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = call.Dir
	if call.Stdin != nil {
		cmd.Stdin = bytes.NewReader(call.Stdin)
	}
	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("running gh: %w", ctxErr)
	}
	if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return res, fmt.Errorf("running gh: timed out after %s: %w", timeout, context.DeadlineExceeded)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return res, fmt.Errorf("running gh: %w", err)
	}
	return res, nil
}
