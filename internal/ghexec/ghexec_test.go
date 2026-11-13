package ghexec_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/ghexec"
	"github.com/asheshgoplani/agent-deck/internal/testutil"
)

func TestEnvAddsGhSettingsToChildEnv(t *testing.T) {
	t.Setenv("AGENTDECK_GHEXEC_PROBE", "kept")
	env := ghexec.Env()
	for _, want := range []string{"GH_PROMPT_DISABLED=1", "NO_COLOR=1", "GH_NO_UPDATE_NOTIFIER=1", "AGENTDECK_GHEXEC_PROBE=kept"} {
		if !slices.Contains(env, want) {
			t.Errorf("Env() lacks %q", want)
		}
	}
}

func TestCommandRunsGhWithEnv(t *testing.T) {
	testutil.InstallFakeGh(t, testutil.FakeGhEnvProbe)
	cmd, cancel := ghexec.Command(context.Background(), 0, "api", "user")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running fake gh: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "gh-111" {
		t.Fatalf("gh env probe = %q, want gh-111", got)
	}
}

func TestCommandKillsHungGh(t *testing.T) {
	tests := []struct {
		name     string
		defaultT time.Duration // DefaultTimeout during the test
		timeout  time.Duration // Command's timeout argument
	}{
		{"zero uses DefaultTimeout", 200 * time.Millisecond, 0},
		{"explicit timeout wins over DefaultTimeout", time.Hour, 200 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.InstallFakeGh(t, testutil.FakeGhHang)
			defer ghexec.SetDefaultTimeoutForTest(tt.defaultT)()
			start := time.Now()
			cmd, cancel := ghexec.Command(context.Background(), tt.timeout, "api", "user")
			defer cancel()
			if err := cmd.Run(); err == nil {
				t.Fatal("hung gh returned no error")
			}
			if el := time.Since(start); el > 5*time.Second {
				t.Fatalf("hung gh ran %v, want it killed at the timeout", el)
			}
		})
	}
}

func TestTimeoutsMatchSpec(t *testing.T) {
	// Spec #18 §1: 20 s per gh call, 60 s for merge.
	if ghexec.DefaultTimeout() != 20*time.Second || ghexec.MergeTimeout != 60*time.Second {
		t.Fatalf("DefaultTimeout %v, MergeTimeout %v; want 20s and 60s", ghexec.DefaultTimeout(), ghexec.MergeTimeout)
	}
}
