package feedback_test

import (
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/feedback"
	"github.com/asheshgoplani/agent-deck/internal/ghexec"
	"github.com/asheshgoplani/agent-deck/internal/testutil"
)

// The real GhCmd runs gh with the gh child env and gives up at the gh
// timeout, so a hung gh falls through to the clipboard/browser path.
func TestNewSenderGhCmdUsesGhEnvAndTimeout(t *testing.T) {
	t.Run("env", func(t *testing.T) {
		testutil.InstallFakeGh(t, testutil.FakeGhEnvGate)
		if err := feedback.NewSender().GhCmd("api", "graphql"); err != nil {
			t.Fatalf("GhCmd: %v (gh not run with ghexec.Env)", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		testutil.InstallFakeGh(t, testutil.FakeGhHang)
		defer ghexec.SetDefaultTimeoutForTest(200 * time.Millisecond)()
		start := time.Now()
		if err := feedback.NewSender().GhCmd("api", "graphql"); err == nil {
			t.Fatal("GhCmd on a hung gh returned no error")
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Fatalf("GhCmd waited %v on a hung gh, want the timeout", el)
		}
	})
}
