package githubtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/testutil"
)

// The fake gh on PATH (spec #18 §17.3): a shell script that serves the same
// recorded fixtures the fake Runner does, for smoke tests that run the real
// github.ExecRunner end to end.

// ScriptRoute answers a gh call whose arguments include every one of Args
// (each matched as a whole argument, in any order) with the fixture named
// Fixture. Then, when set, is shell run first in the call's directory (gh
// pr checkout making its branch, say). Routes are tried in order.
type ScriptRoute struct {
	Fixture string
	Args    []string
	Then    string
}

// ScriptCall is one call the fake gh script saw.
type ScriptCall struct {
	// Env is GH_PROMPT_DISABLED, NO_COLOR and GH_NO_UPDATE_NOTIFIER as one
	// string: "111" under the gh child env.
	Env string
	// Dir is the directory gh ran in.
	Dir  string
	Args []string
}

// FakeGhScript is a fake gh installed on PATH by InstallFakeGhScript.
type FakeGhScript struct {
	t   testing.TB
	log string
}

// InstallFakeGhScript writes each route's fixture (stdout, stderr, exit) to
// disk and installs, through testutil.InstallFakeGh, a gh script that
// answers a call with the first matching route: the fixture's stdout and
// stderr, then its exit code. A call no route matches exits 1 with a stderr
// naming it. Every call is logged for Calls. A fixture of a gh that never
// ran (an error file) cannot be served by a script and fails the test.
func InstallFakeGhScript(t testing.TB, routes ...ScriptRoute) *FakeGhScript {
	t.Helper()
	root := t.TempDir()
	log := filepath.Join(root, "calls")
	var b strings.Builder
	fmt.Fprintf(&b, "log=%s\n", shellQuote(log))
	b.WriteString(`{ printf '%s\037' "$GH_PROMPT_DISABLED$NO_COLOR$GH_NO_UPDATE_NOTIFIER" "$(pwd -P)" "$@"; printf '\036'; } >> "$log"` + "\n")
	b.WriteString(`has() { w=$1; shift; for a in "$@"; do [ "$a" = "$w" ] && return 0; done; return 1; }` + "\n")
	b.WriteString(`serve() { cat "$1/stdout"; cat "$1/stderr" >&2; exit "$(cat "$1/exit")"; }` + "\n")
	for i, r := range routes {
		f := MustLoad(t, r.Fixture)
		if f.Err != nil {
			t.Fatalf("gh fixture %q is a gh that never ran: a script cannot serve it", r.Fixture)
		}
		dir := filepath.Join(root, strconv.Itoa(i))
		writeFixtureFiles(t, dir, f)
		cond := []string{"true"}
		for _, a := range r.Args {
			cond = append(cond, "has "+shellQuote(a)+` "$@"`)
		}
		fmt.Fprintf(&b, "if %s; then\n", strings.Join(cond, " && "))
		if r.Then != "" {
			b.WriteString(r.Then + "\n")
		}
		fmt.Fprintf(&b, "serve %s\nfi\n", shellQuote(dir))
	}
	b.WriteString(`printf 'fake gh: no fixture for: %s\n' "$*" >&2` + "\nexit 1\n")
	testutil.InstallFakeGh(t, b.String())
	return &FakeGhScript{t: t, log: log}
}

func writeFixtureFiles(t testing.TB, dir string, f Fixture) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"stdout": f.Result.Stdout,
		"stderr": f.Result.Stderr,
		"exit":   []byte(strconv.Itoa(f.Result.ExitCode)),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatalf("writing gh fixture %q: %v", f.Name, err)
		}
	}
}

// shellQuote quotes s as one POSIX shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Calls are the calls the script saw, in order.
func (s *FakeGhScript) Calls() []ScriptCall {
	s.t.Helper()
	data, err := os.ReadFile(s.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		s.t.Fatalf("reading the fake gh log: %v", err)
	}
	var calls []ScriptCall
	for _, rec := range strings.Split(string(data), "\x1e") {
		if rec == "" {
			continue
		}
		fields := strings.Split(strings.TrimSuffix(rec, "\x1f"), "\x1f")
		if len(fields) < 2 {
			s.t.Fatalf("fake gh log record %q", rec)
		}
		calls = append(calls, ScriptCall{Env: fields[0], Dir: fields[1], Args: fields[2:]})
	}
	return calls
}
