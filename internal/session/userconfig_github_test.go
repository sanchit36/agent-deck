package session

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

// TestUserConfigGitHubEnabled pins the forge gate (spec #18 §0): [github]
// enabled decodes onto UserConfig, is off when unset, and an unset section
// is not written back.
func TestUserConfigGitHubEnabled(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      bool
	}{
		{"unset", ``, false},
		{"section without key", "[github]\n", false},
		{"on", "[github]\nenabled = true\n", true},
		{"off", "[github]\nenabled = false\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg UserConfig
			if _, err := toml.Decode(tc.src, &cfg); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := cfg.GitHubEnabled(); got != tc.want {
				t.Fatalf("GitHubEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
	var nilCfg *UserConfig
	if nilCfg.GitHubEnabled() {
		t.Fatal("nil config reports the gate on")
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(UserConfig{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "[github]") {
		t.Fatalf("an empty config writes a [github] section:\n%s", buf.String())
	}
}

// TestUserConfigGitHubRefreshInterval pins [github] refresh_interval (spec
// #18 §5): 60 s when unset, 0 = manual only, anything below 30 s raised to
// 30 s.
func TestUserConfigGitHubRefreshInterval(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      time.Duration
	}{
		{"unset", ``, 60 * time.Second},
		{"section without key", "[github]\n", 60 * time.Second},
		{"manual", "[github]\nrefresh_interval = 0\n", 0},
		{"below minimum", "[github]\nrefresh_interval = 10\n", 30 * time.Second},
		{"just below minimum", "[github]\nrefresh_interval = 29\n", 30 * time.Second},
		{"minimum", "[github]\nrefresh_interval = 30\n", 30 * time.Second},
		{"longer", "[github]\nrefresh_interval = 300\n", 300 * time.Second},
		{"negative", "[github]\nrefresh_interval = -5\n", 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg UserConfig
			if _, err := toml.Decode(tc.src, &cfg); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := cfg.GitHubRefreshInterval(); got != tc.want {
				t.Fatalf("GitHubRefreshInterval() = %v, want %v", got, tc.want)
			}
		})
	}
	var nilCfg *UserConfig
	if got := nilCfg.GitHubRefreshInterval(); got != 60*time.Second {
		t.Fatalf("nil config: %v, want 60s", got)
	}
}
