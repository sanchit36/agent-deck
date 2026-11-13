package session

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

func linkTestInstance(id string) *Instance {
	return &Instance{
		ID: id, Title: id, ProjectPath: "/tmp/" + id, GroupPath: "work",
		Tool: "claude", Command: "claude", Status: StatusIdle, CreatedAt: time.Now(),
	}
}

func loadByID(t *testing.T, s *Storage) map[string]*Instance {
	t.Helper()
	loaded, _, err := s.LoadWithGroups()
	if err != nil {
		t.Fatalf("LoadWithGroups: %v", err)
	}
	out := map[string]*Instance{}
	for _, inst := range loaded {
		out[inst.ID] = inst
	}
	return out
}

// Links survive a save and a fresh load (a TUI or CLI restart), one Issue
// and one PR per session, each with its own repo.
func TestLinkStateDBRoundTrip(t *testing.T) {
	s := newTestStorage(t)
	a, b := linkTestInstance("a"), linkTestInstance("b")
	a.SetLink(LinkIssue, Link{Host: "github.com", Owner: "acme", Name: "app", Number: 12})
	a.SetLink(LinkPR, Link{Host: "ghe.corp.example", Owner: "team", Name: "svc", Number: 34})
	if err := s.SaveWithGroups([]*Instance{a, b}, nil); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := loadByID(t, s)
	wantIssue := Link{Host: "github.com", Owner: "acme", Name: "app", Number: 12}
	wantPR := Link{Host: "ghe.corp.example", Owner: "team", Name: "svc", Number: 34}
	if l := got["a"].LinkOf(LinkIssue); l == nil || *l != wantIssue {
		t.Fatalf("issue link = %+v, want %+v", l, wantIssue)
	}
	if l := got["a"].LinkOf(LinkPR); l == nil || *l != wantPR {
		t.Fatalf("pr link = %+v, want %+v", l, wantPR)
	}
	if got["b"].LinkOf(LinkIssue) != nil || got["b"].LinkOf(LinkPR) != nil {
		t.Fatalf("unlinked session gained a link: %+v %+v", got["b"].IssueLink, got["b"].PRLink)
	}

	// A second Issue replaces the first: at most one per kind.
	a2 := got["a"]
	a2.SetLink(LinkIssue, Link{Host: "github.com", Owner: "acme", Name: "app", Number: 99})
	if err := s.SaveWithGroups([]*Instance{a2, got["b"]}, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	if l := loadByID(t, s)["a"].LinkOf(LinkIssue); l == nil || l.Number != 99 {
		t.Fatalf("issue link after replace = %+v, want #99", l)
	}
}

// Unlinking writes an explicit clear, so the extras merge on the next save
// cannot carry the old link forward from the stored row.
func TestLinkUnlinkSurvivesSave(t *testing.T) {
	s := newTestStorage(t)
	a := linkTestInstance("a")
	a.SetLink(LinkIssue, Link{Host: "github.com", Owner: "acme", Name: "app", Number: 12})
	a.SetLink(LinkPR, Link{Host: "github.com", Owner: "acme", Name: "app", Number: 34})
	if err := s.SaveWithGroups([]*Instance{a}, nil); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded := loadByID(t, s)["a"]
	if !loaded.ClearLink(LinkPR) {
		t.Fatal("ClearLink reported no PR link")
	}
	if loaded.ClearLink(LinkPR) {
		t.Fatal("second ClearLink reported a PR link")
	}
	if err := s.SaveWithGroups([]*Instance{loaded}, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	again := loadByID(t, s)["a"]
	if again.LinkOf(LinkPR) != nil {
		t.Fatalf("pr link came back after unlink: %+v", again.PRLink)
	}
	if l := again.LinkOf(LinkIssue); l == nil || l.Number != 12 {
		t.Fatalf("issue link lost by pr unlink: %+v", l)
	}

	// An older writer that never knew the link (omits the key) keeps it.
	td := WriteLinksToToolData(json.RawMessage(`{}`), nil, nil, false, false)
	merged := statedb.MergeToolDataExtras(WriteLinksToToolData(nil, &Link{Host: "github.com", Owner: "o", Name: "r", Number: 1}, nil, false, false), td)
	if issue, _ := ReadLinksFromToolData(merged); issue == nil || issue.Number != 1 {
		t.Fatalf("link dropped by an unaware writer: %s", merged)
	}
	if got := WriteLinksToToolData(json.RawMessage(`{"a":1}`), nil, nil, false, false); string(got) != `{"a":1}` {
		t.Fatalf("never-linked row gained bytes: %s", got)
	}
}

// Links are deleted with their session: the row goes, and a new session
// that happens to reuse nothing of it starts unlinked.
func TestLinkDeletedWithSession(t *testing.T) {
	s := newTestStorage(t)
	a, b := linkTestInstance("a"), linkTestInstance("b")
	a.SetLink(LinkIssue, Link{Host: "github.com", Owner: "acme", Name: "app", Number: 12})
	if err := s.SaveWithGroups([]*Instance{a, b}, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.DeleteInstance("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := loadByID(t, s)["a"]; ok {
		t.Fatal("deleted session still loads")
	}
	// Re-creating a session with the same id must not resurrect the link.
	if err := s.SaveWithGroups([]*Instance{linkTestInstance("a"), b}, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	if l := loadByID(t, s)["a"].LinkOf(LinkIssue); l != nil {
		t.Fatalf("link outlived its session: %+v", l)
	}
}

func TestParseLinkRepo(t *testing.T) {
	tests := []struct {
		in      string
		want    Link
		wantErr bool
	}{
		{in: "acme/app", want: Link{Host: "github.com", Owner: "acme", Name: "app"}},
		{in: "ghe.corp.example/team/svc", want: Link{Host: "ghe.corp.example", Owner: "team", Name: "svc"}},
		{in: "GitHub.com/acme/app", want: Link{Host: "github.com", Owner: "acme", Name: "app"}},
		{in: "https://github.com/acme/app.git", want: Link{Host: "github.com", Owner: "acme", Name: "app"}},
		{in: "git@github.com:acme/app.git", want: Link{Host: "github.com", Owner: "acme", Name: "app"}},
		{in: "ssh://git@ghe.corp.example/team/svc", want: Link{Host: "ghe.corp.example", Owner: "team", Name: "svc"}},
		{in: "", wantErr: true},
		{in: "app", wantErr: true},
		{in: "a/b/c/d", wantErr: true},
		{in: "acme/", wantErr: true},
		{in: "/tmp/local/path", wantErr: true},
		// Same semantics as github.ParseRemoteURL (spec §2).
		{in: "git@ssh.github.com:acme/app.git", want: Link{Host: "github.com", Owner: "acme", Name: "app"}},
		{in: "ssh://git@ssh.github.com:443/acme/app.git", want: Link{Host: "github.com", Owner: "acme", Name: "app"}},
		{in: "https://www.github.com/acme/app", want: Link{Host: "github.com", Owner: "acme", Name: "app"}},
		{in: "www.github.com/acme/app", want: Link{Host: "github.com", Owner: "acme", Name: "app"}},
		{in: "github.com:acme/app.git", want: Link{Host: "github.com", Owner: "acme", Name: "app"}},
		{in: "GHE.Corp.Example:team/svc", want: Link{Host: "ghe.corp.example", Owner: "team", Name: "svc"}},
		{in: "file:///tmp/acme/app", wantErr: true},
		{in: "https://github.com/acme/app/pulls", wantErr: true},
		{in: "git@github.com:acme/.git", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseLinkRepo(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseLinkRepo(%q) = %+v, want error", tc.in, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ParseLinkRepo(%q) = %+v, %v; want %+v", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestParseLinkKind(t *testing.T) {
	for in, want := range map[string]LinkKind{"issue": LinkIssue, "pr": LinkPR, "PR": LinkPR} {
		if got, err := ParseLinkKind(in); err != nil || got != want {
			t.Fatalf("ParseLinkKind(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseLinkKind("pull"); err == nil {
		t.Fatal("ParseLinkKind accepted pull")
	}
}

func TestLinkBadgeLabel(t *testing.T) {
	l := Link{Host: "github.com", Owner: "acme", Name: "app", Number: 12}
	if got := l.Label(LinkIssue); got != "#12" {
		t.Fatalf("issue label = %q", got)
	}
	if got := l.Label(LinkPR); got != "PR#12" {
		t.Fatalf("pr label = %q", got)
	}
	if got := l.Repo(); got != "github.com/acme/app" {
		t.Fatalf("repo = %q", got)
	}
}
