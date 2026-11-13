package main

import (
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/ghexec"
	"github.com/asheshgoplani/agent-deck/internal/testutil"
)

// ghUserLogin runs gh with the gh child env and gives up at the gh timeout
// instead of hanging the feedback prompt.
func TestFeedbackCmdGhUserLoginUsesGhEnvAndTimeout(t *testing.T) {
	t.Run("env", func(t *testing.T) {
		testutil.InstallFakeGh(t, testutil.FakeGhEnvProbe)
		if got := ghUserLogin(); got != "gh-111" {
			t.Fatalf("ghUserLogin() = %q, want gh-111 (gh run with ghexec.Env)", got)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		testutil.InstallFakeGh(t, testutil.FakeGhHang)
		defer ghexec.SetDefaultTimeoutForTest(200 * time.Millisecond)()
		start := time.Now()
		if got := ghUserLogin(); got != "" {
			t.Fatalf("ghUserLogin() = %q from a hung gh, want empty", got)
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Fatalf("ghUserLogin waited %v on a hung gh, want the timeout", el)
		}
	})
}
