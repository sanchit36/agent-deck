package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// TestSessionLinkCLIGolden drives `session link` / `session unlink` through the
// built binary against the seeded goldens store and freezes every step's
// stdout, stderr and exit code in one transcript golden, then checks the
// links really landed in (and left) the state DB.
func TestSessionLinkCLIGolden(t *testing.T) {
	bin := goldensBinary(t)
	home, env := goldensSandbox(t)

	steps := [][]string{
		{"session", "link", "golden-sess-1", "issue", "12", "--repo", "acme/app"},
		{"session", "link", "golden-sess-1", "pr", "34", "--repo", "ghe.corp.example/team/svc", "--json"},
		{"session", "link", "golden-sess-1", "issue", "#13", "--repo", "acme/app", "--json"},
		{"session", "link", "golden-sess-1", "pull", "3", "--repo", "acme/app"},
		{"session", "link", "golden-sess-1", "issue", "0", "--repo", "acme/app"},
		{"session", "link", "golden-sess-1", "issue", "5", "--repo", "not-a-repo"},
		{"session", "link", "golden-sess-1", "issue", "5"},
		{"session", "link", "no-such-session", "issue", "5", "--repo", "acme/app"},
		{"session", "link", "golden-sess-1"},
		{"session", "unlink", "golden-sess-1", "pr", "--json"},
		{"session", "unlink", "golden-sess-1", "pr"},
		{"session", "unlink", "golden-sess-1", "pull"},
	}
	var transcript strings.Builder
	for _, args := range steps {
		full := append([]string{"-p", goldensProfile}, args...)
		stdout, stderr, exit := runGoldensStreamsIn(t, bin, env, home, full)
		fmt.Fprintf(&transcript, "$ agent-deck %s\nexit %d\n--- stdout\n%s--- stderr\n%s\n", strings.Join(args, " "), exit, stdout, stderr)
	}
	assertGolden(t, "session_link_transcript", home, transcript.String())

	// The CLI wrote through the same storage the TUI reads.
	storage, err := session.NewStorageWithProfile(goldensProfile)
	if err != nil {
		t.Fatalf("opening goldens storage: %v", err)
	}
	defer storage.Close()
	instances, _, err := storage.LoadWithGroups()
	if err != nil {
		t.Fatalf("loading goldens storage: %v", err)
	}
	for _, inst := range instances {
		if inst.ID != "golden-sess-1" {
			if inst.IssueLink != nil || inst.PRLink != nil {
				t.Errorf("%s gained a link: %+v %+v", inst.ID, inst.IssueLink, inst.PRLink)
			}
			continue
		}
		want := session.Link{Host: "github.com", Owner: "acme", Name: "app", Number: 13}
		if inst.IssueLink == nil || *inst.IssueLink != want {
			t.Errorf("issue link = %+v, want %+v", inst.IssueLink, want)
		}
		if inst.PRLink != nil {
			t.Errorf("pr link survived unlink: %+v", inst.PRLink)
		}
	}
}

// Without --repo the link takes the repo from the session checkout's origin
// remote, read locally (no gh, no network).
func TestSessionLinkRepoFromOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if _, err := linkRepoFromCheckout(dir); err == nil {
		t.Fatal("repo with no remotes resolved")
	}
	git("remote", "add", "upstream", "https://github.com/acme/app.git")
	if _, err := linkRepoFromCheckout(dir); err == nil {
		t.Fatal("upstream alone resolved (spec §2: upstream never becomes the Project)")
	}
	// No origin: the only GitHub remote (spec §2), skipping other forges
	// and upstream, with the shared parser's host folding.
	git("remote", "add", "a-gitlab", "git@gitlab.com:me/app.git")
	git("remote", "add", "fork", "git@ssh.github.com:me/app.git")
	got, err := linkRepoFromCheckout(dir)
	if want := (session.Link{Host: "github.com", Owner: "me", Name: "app"}); err != nil || got != want {
		t.Fatalf("no origin: linkRepoFromCheckout = %+v, %v; want %+v", got, err, want)
	}
	// Several GitHub remotes and no origin: the first alphabetically.
	git("remote", "add", "b-mirror", "https://github.com/mirror/app.git")
	got, err = linkRepoFromCheckout(dir)
	if want := (session.Link{Host: "github.com", Owner: "mirror", Name: "app"}); err != nil || got != want {
		t.Fatalf("two GitHub remotes: linkRepoFromCheckout = %+v, %v; want %+v", got, err, want)
	}
	// origin wins, even on a GHE host gh was never asked about.
	git("remote", "add", "origin", "git@ghe.corp.example:team/svc.git")
	got, err = linkRepoFromCheckout(filepath.Join(dir, "."))
	if want := (session.Link{Host: "ghe.corp.example", Owner: "team", Name: "svc"}); err != nil || got != want {
		t.Fatalf("linkRepoFromCheckout = %+v, %v; want %+v", got, err, want)
	}
	// An origin on another forge is not a GitHub repo.
	git("remote", "set-url", "origin", "https://gitlab.com/team/svc.git")
	if got, err := linkRepoFromCheckout(dir); err == nil {
		t.Fatalf("gitlab origin resolved to %+v", got)
	}
	if _, err := linkRepoFromCheckout(filepath.Join(os.TempDir(), "agent-deck-no-such-dir")); err == nil {
		t.Fatal("missing checkout resolved")
	}
}
