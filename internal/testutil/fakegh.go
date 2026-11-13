package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Scripts for InstallFakeGh.
const (
	// FakeGhEnvProbe prints "gh-" followed by the values of
	// GH_PROMPT_DISABLED, NO_COLOR and GH_NO_UPDATE_NOTIFIER, so a caller
	// that runs gh with the gh child env sees "gh-111".
	FakeGhEnvProbe = `printf 'gh-%s%s%s\n' "$GH_PROMPT_DISABLED" "$NO_COLOR" "$GH_NO_UPDATE_NOTIFIER"`
	// FakeGhEnvGate exits 0 only when all three gh env vars are "1".
	FakeGhEnvGate = `[ "$GH_PROMPT_DISABLED$NO_COLOR$GH_NO_UPDATE_NOTIFIER" = "111" ] || exit 7`
	// FakeGhHang never answers within a test's patience: it stands in for
	// a gh call that hangs (a prompt, a stuck network request).
	FakeGhHang = `exec sleep 30`
)

// InstallFakeGh writes script as an executable `gh` in a temp dir, puts that
// dir first on PATH for the test and clears GH_PROMPT_DISABLED, NO_COLOR,
// GH_NO_UPDATE_NOTIFIER, GITHUB_TOKEN and GH_TOKEN, so only the caller's own
// child env can set them. It returns the dir. Skips on Windows.
func InstallFakeGh(t testing.TB, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake gh shell script requires a POSIX shell")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil { // #nosec G306 -- test executable
		t.Fatalf("writing fake gh: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, k := range []string{"GH_PROMPT_DISABLED", "NO_COLOR", "GH_NO_UPDATE_NOTIFIER", "GITHUB_TOKEN", "GH_TOKEN"} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unsetting %s: %v", k, err)
		}
	}
	return dir
}
