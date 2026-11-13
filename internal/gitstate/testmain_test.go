package gitstate

import (
	"os"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(runTestMain(m))
}

// runTestMain holds the TestMain body so the cleanup defers run before
// os.Exit (which skips deferred functions).
func runTestMain(m *testing.M) int {
	cleanupHome := testutil.IsolateHome()
	defer cleanupHome()

	// Git hooks export GIT_DIR/GIT_WORK_TREE; clear them so the tests' git
	// commands operate on their temp repos instead of the real repository.
	testutil.UnsetGitRepoEnv()

	cleanupTmux := testutil.IsolateTmuxSocket()
	defer cleanupTmux()

	return m.Run()
}
