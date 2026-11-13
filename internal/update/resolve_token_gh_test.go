package update

import (
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/ghexec"
	"github.com/asheshgoplani/agent-deck/internal/testutil"
)

// resolveGitHubToken runs `gh auth token` with the gh child env and gives up
// at the gh timeout (falling back to anonymous requests) instead of hanging
// the update check.
func TestResolveGitHubTokenUsesGhEnvAndTimeout(t *testing.T) {
	t.Run("env", func(t *testing.T) {
		testutil.InstallFakeGh(t, testutil.FakeGhEnvProbe)
		if got := resolveGitHubToken(); got != "gh-111" {
			t.Fatalf("resolveGitHubToken() = %q, want gh-111 (gh run with ghexec.Env)", got)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		testutil.InstallFakeGh(t, testutil.FakeGhHang)
		defer ghexec.SetDefaultTimeoutForTest(200 * time.Millisecond)()
		start := time.Now()
		if got := resolveGitHubToken(); got != "" {
			t.Fatalf("resolveGitHubToken() = %q from a hung gh, want empty", got)
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Fatalf("resolveGitHubToken waited %v on a hung gh, want the timeout", el)
		}
	})
}
