package ui

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// TestForgeConfigLoadErrorIsLogged: a config.toml that does not parse is
// logged by the forge code (which then runs on the defaults) instead of
// being dropped.
func TestForgeConfigLoadErrorIsLogged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.MkdirAll(filepath.Join(home, ".agent-deck"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agent-deck", "config.toml"), []byte("[github\nenabled = ="), 0o600); err != nil {
		t.Fatal(err)
	}
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	var buf bytes.Buffer
	prev := uiLog
	uiLog = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { uiLog = prev })

	h := NewHome()
	h.reloadForgeFromConfig()
	t.Cleanup(func() { h.forgeShutdown()() })

	if !strings.Contains(buf.String(), `"msg":"forge_config_load_failed"`) || !strings.Contains(buf.String(), "config.toml parse error") {
		t.Fatalf("config load error not logged; log:\n%s", buf.String())
	}
	if !h.forge.enabled {
		t.Fatal("forge should fall back to the defaults (gate on) when config.toml does not parse")
	}
}
