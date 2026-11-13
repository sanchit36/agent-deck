package ui

import (
	"log/slog"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// loadForgeConfig is session.LoadUserConfig for the forge code. A config.toml
// that cannot be read or parsed is logged; the defaults LoadUserConfig falls
// back to (nil-safe getters) are used, so the forge keeps working.
func loadForgeConfig() *session.UserConfig {
	cfg, err := session.LoadUserConfig()
	if err != nil {
		uiLog.Warn("forge_config_load_failed", slog.String("error", err.Error()))
	}
	return cfg
}
