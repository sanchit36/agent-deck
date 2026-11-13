package github_test

import (
	"os"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(runTestMain(m))
}

// runTestMain returns the exit code so the cleanup defers run (TestMain's
// os.Exit skips them).
func runTestMain(m *testing.M) int {
	cleanupHome := testutil.IsolateHome()
	defer cleanupHome()
	cleanupTmux := testutil.IsolateTmuxSocket()
	defer cleanupTmux()
	os.Setenv("AGENTDECK_PROFILE", "_test")
	return m.Run()
}
