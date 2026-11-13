package github_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/ghexec"
	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	"github.com/asheshgoplani/agent-deck/internal/testutil"
)

// runnerUnderTest gives a Runner that answers `gh issue list` with fixture f.
type runnerUnderTest struct {
	name  string
	setup func(t *testing.T, f githubtest.Fixture) github.Runner
}

var contractArgs = []string{"issue", "list", "-R", "github.com/acme/widgets"}

var runnersUnderTest = []runnerUnderTest{
	{"fake", func(t *testing.T, f githubtest.Fixture) github.Runner {
		return githubtest.NewFake().Serve(f, contractArgs...)
	}},
	{"exec", func(t *testing.T, f githubtest.Fixture) github.Runner {
		switch {
		case errors.Is(f.Err, exec.ErrNotFound):
			t.Setenv("PATH", t.TempDir())
		case errors.Is(f.Err, context.DeadlineExceeded):
			testutil.InstallFakeGh(t, testutil.FakeGhHang)
			restore := ghexec.SetDefaultTimeoutForTest(300 * time.Millisecond)
			t.Cleanup(restore)
		default:
			dir := t.TempDir()
			for name, b := range map[string][]byte{"stdout": f.Result.Stdout, "stderr": f.Result.Stderr} {
				if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			testutil.InstallFakeGh(t, fmt.Sprintf("cat %q; cat %q >&2; exit %d",
				filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr"), f.Result.ExitCode))
		}
		return github.ExecRunner{}
	}},
}

// TestRunnerContract: the fake serving a fixture and the real gh printing
// the same output are indistinguishable to a caller, for every fixture.
func TestRunnerContract(t *testing.T) {
	for _, rut := range runnersUnderTest {
		for _, name := range githubtest.Names() {
			t.Run(rut.name+"/"+name, func(t *testing.T) {
				f := githubtest.MustLoad(t, name)
				r := rut.setup(t, f)
				res, err := r.Run(context.Background(), github.Call{Args: contractArgs})
				switch {
				case f.Err != nil:
					for _, sentinel := range []error{exec.ErrNotFound, context.DeadlineExceeded} {
						if errors.Is(f.Err, sentinel) != errors.Is(err, sentinel) {
							t.Fatalf("Run error = %v, want one wrapping the same sentinel as %v", err, f.Err)
						}
					}
				case err != nil:
					t.Fatalf("Run error = %v, want nil (a non-zero exit is a Result, not an error)", err)
				default:
					if !bytes.Equal(res.Stdout, f.Result.Stdout) || !bytes.Equal(res.Stderr, f.Result.Stderr) || res.ExitCode != f.Result.ExitCode {
						t.Fatalf("Run = {stdout %q stderr %q exit %d}, want {%q %q %d}",
							res.Stdout, res.Stderr, res.ExitCode, f.Result.Stdout, f.Result.Stderr, f.Result.ExitCode)
					}
				}
			})
		}
	}
}

func TestRunnerContractCancelledContext(t *testing.T) {
	for _, rut := range runnersUnderTest {
		t.Run(rut.name, func(t *testing.T) {
			r := rut.setup(t, githubtest.MustLoad(t, githubtest.OK))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := r.Run(ctx, github.Call{Args: contractArgs}); !errors.Is(err, context.Canceled) {
				t.Fatalf("Run with a cancelled ctx = %v, want context.Canceled", err)
			}
		})
	}
}

func TestExecRunnerPassesStdinAndArgs(t *testing.T) {
	testutil.InstallFakeGh(t, `printf '%s|' "$@"; cat`)
	res, err := github.ExecRunner{}.Run(context.Background(), github.Call{
		Args:  []string{"api", "graphql", "--input", "-"},
		Stdin: []byte(`{"query":"q"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(res.Stdout), `api|graphql|--input|-|{"query":"q"}`; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestExecRunnerCallTimeoutWins(t *testing.T) {
	testutil.InstallFakeGh(t, testutil.FakeGhHang)
	start := time.Now()
	_, err := github.ExecRunner{}.Run(context.Background(), github.Call{Args: []string{"pr", "merge"}, Timeout: 200 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run = %v, want context.DeadlineExceeded", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("Run took %s, want it bounded by the 200ms call timeout", took)
	}
}

func TestFakeRecordsCallsAndRejectsUnknownCalls(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.OK), "issue", "list")
	res, err := fake.Run(context.Background(), github.Call{Args: []string{"pr", "list"}, Stdin: []byte("x")})
	if err != nil || res.ExitCode == 0 || !bytes.Contains(res.Stderr, []byte("gh pr list")) {
		t.Fatalf("unmatched call = (%+v, %v), want a non-zero exit naming the call", res, err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || fmt.Sprint(calls[0].Args) != "[pr list]" || string(calls[0].Stdin) != "x" {
		t.Fatalf("Calls() = %+v, want the one call made", calls)
	}
}
