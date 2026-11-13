package session

import (
	"bytes"
	"strings"
	"testing"

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
