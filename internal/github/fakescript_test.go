package github_test

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

// TestFakeGhScriptServesFixtures: the fake gh on PATH answers a call with
// the first route whose arguments it holds, byte for byte as the fixture
// recorded (stdout, stderr, exit), fails a call no route matches, and logs
// every call with its arguments intact.
func TestFakeGhScriptServesFixtures(t *testing.T) {
	quoted := "query=it's {a} $x \"b\""
	gh := githubtest.InstallFakeGhScript(t,
		githubtest.ScriptRoute{Fixture: githubtest.NotFound, Args: []string{"issue", "view"}},
		githubtest.ScriptRoute{Fixture: githubtest.IssueList, Args: []string{quoted}},
	)
	run := func(args ...string) github.Result {
		t.Helper()
		res, err := github.ExecRunner{}.Run(context.Background(), github.Call{Args: args, Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	for _, c := range []struct {
		args    []string
		fixture string
	}{
		{[]string{"issue", "view", "7"}, githubtest.NotFound},
		{[]string{"api", "graphql", "-f", quoted}, githubtest.IssueList},
	} {
		want := githubtest.MustLoad(t, c.fixture).Result
		got := run(c.args...)
		if !bytes.Equal(got.Stdout, want.Stdout) || !bytes.Equal(got.Stderr, want.Stderr) || got.ExitCode != want.ExitCode {
			t.Fatalf("%q served exit %d stdout %d B stderr %q, want fixture %s (exit %d, %d B, %q)",
				c.args, got.ExitCode, len(got.Stdout), got.Stderr, c.fixture, want.ExitCode, len(want.Stdout), want.Stderr)
		}
	}
	if res := run("pr", "list"); res.ExitCode != 1 || !bytes.Contains(res.Stderr, []byte("no fixture for: pr list")) {
		t.Fatalf("unmatched call: exit %d stderr %q", res.ExitCode, res.Stderr)
	}
	calls := gh.Calls()
	if len(calls) != 3 || !slices.Equal(calls[1].Args, []string{"api", "graphql", "-f", quoted}) || calls[0].Env != "111" {
		t.Fatalf("logged calls %+v", calls)
	}
}
