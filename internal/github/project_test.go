package github_test

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
)

// hostKinds is a fixed view of which hosts count as GitHub: github.com,
// one GHE host gh knows, the well-known other forges, anything else unknown.
func hostKinds(host string) github.HostKind {
	switch host {
	case "github.com", "ghe.example.com":
		return github.HostGitHub
	case "gitlab.com", "bitbucket.org":
		return github.HostOtherForge
	}
	return github.HostUnknown
}

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		url               string
		host, owner, name string
	}{
		{"https://github.com/acme/widgets.git", "github.com", "acme", "widgets"},
		{"https://github.com/acme/widgets", "github.com", "acme", "widgets"},
		{"https://github.com/acme/widgets/", "github.com", "acme", "widgets"},
		{"http://GitHub.com/Acme/Widgets.git", "github.com", "Acme", "Widgets"},
		{"https://x-access-token:secret@github.com/acme/widgets.git", "github.com", "acme", "widgets"},
		{"git@github.com:acme/widgets.git", "github.com", "acme", "widgets"},
		{"github.com:acme/widgets", "github.com", "acme", "widgets"},
		{"ssh://git@github.com/acme/widgets.git", "github.com", "acme", "widgets"},
		{"ssh://git@ssh.github.com:443/acme/widgets.git", "github.com", "acme", "widgets"},
		{"git://github.com/acme/widgets.git", "github.com", "acme", "widgets"},
		{"git+ssh://git@github.com/acme/widgets.git", "github.com", "acme", "widgets"},
		{"https://www.github.com/acme/widgets", "github.com", "acme", "widgets"},
		{"ssh://git@ghe.example.com:2222/acme/widgets.git", "ghe.example.com", "acme", "widgets"},
		{"https://ghe.example.com/acme/widgets", "ghe.example.com", "acme", "widgets"},
		// A host but no owner/name pair (a GitLab subgroup).
		{"https://gitlab.com/acme/team/widgets.git", "gitlab.com", "", ""},
		// No host at all.
		{"/srv/git/widgets.git", "", "", ""},
		{"../widgets", "", "", ""},
		{"file:///srv/git/widgets.git", "", "", ""},
		{"", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			host, repo := github.ParseRemoteURL(tt.url)
			if host != tt.host || repo.Owner != tt.owner || repo.Name != tt.name {
				t.Fatalf("ParseRemoteURL(%q) = %q, %+v; want host %q owner %q name %q", tt.url, host, repo, tt.host, tt.owner, tt.name)
			}
			if tt.owner != "" && repo.Host != tt.host {
				t.Fatalf("repo.Host = %q, want %q", repo.Host, tt.host)
			}
		})
	}
}

func TestPickRemote(t *testing.T) {
	const (
		fork     = "git@github.com:me/widgets.git"
		parent   = "https://github.com/acme/widgets.git"
		lab      = "git@gitlab.com:acme/widgets.git"
		ghe      = "https://ghe.example.com/corp/widgets.git"
		stranger = "https://git.internal.example/acme/widgets.git"
	)
	tests := []struct {
		name    string
		remotes []github.Remote
		want    string // HOST/OWNER/REPO, or "" for no Project
		reason  github.Reason
		host    string
	}{
		{"fork: origin wins over upstream", []github.Remote{{"upstream", parent}, {"origin", fork}}, "github.com/me/widgets", "", ""},
		{"origin on GHE", []github.Remote{{"origin", ghe}}, "ghe.example.com/corp/widgets", "", ""},
		{"no origin: the only GitHub remote", []github.Remote{{"lab", lab}, {"mine", fork}}, "github.com/me/widgets", "", ""},
		{"no origin: several GitHub remotes, first alphabetically", []github.Remote{{"zeta", parent}, {"alpha", fork}, {"mid", ghe}}, "github.com/me/widgets", "", ""},
		{"upstream only never becomes the Project", []github.Remote{{"upstream", parent}}, "", github.ReasonNoGitHubRemote, ""},
		{"no origin: upstream skipped for another GitHub remote", []github.Remote{{"upstream", parent}, {"mine", fork}}, "github.com/me/widgets", "", ""},
		{"no remotes", nil, "", github.ReasonNoGitHubRemote, ""},
		{"origin on another forge", []github.Remote{{"origin", lab}, {"mirror", parent}}, "", github.ReasonOtherForge, "gitlab.com"},
		{"origin on an unknown host", []github.Remote{{"origin", stranger}}, "", github.ReasonUnknownHost, "git.internal.example"},
		{"no origin: other forge only", []github.Remote{{"lab", lab}}, "", github.ReasonOtherForge, "gitlab.com"},
		{"no origin: unknown host only", []github.Remote{{"corp", stranger}}, "", github.ReasonUnknownHost, "git.internal.example"},
		{"origin is a local path", []github.Remote{{"origin", "/srv/git/widgets.git"}}, "", github.ReasonNoGitHubRemote, ""},
		{"origin on GitHub with no owner/name", []github.Remote{{"origin", "https://github.com/acme"}}, "", github.ReasonNoGitHubRemote, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := github.PickRemote(tt.remotes, hostKinds)
			if tt.want == "" {
				if got.Found() {
					t.Fatalf("PickRemote = %s, want no Project (%s)", got.Repo, tt.reason)
				}
				if got.Reason != tt.reason || got.Host != tt.host {
					t.Fatalf("PickRemote reason = %q host %q, want %q host %q", got.Reason, got.Host, tt.reason, tt.host)
				}
				if got.Message() == "" {
					t.Fatal("empty-state message is empty")
				}
				return
			}
			if !got.Found() || got.Repo.String() != tt.want {
				t.Fatalf("PickRemote = %+v, want %s", got, tt.want)
			}
		})
	}
}

func TestResolutionMessages(t *testing.T) {
	tests := []struct {
		res  github.Resolution
		want string
	}{
		{github.Resolution{Reason: github.ReasonNoPath}, "no project path in this group"},
		{github.Resolution{Reason: github.ReasonNotGit}, "not a git repository"},
		{github.Resolution{Reason: github.ReasonNoGitHubRemote}, "no GitHub remote"},
		{github.Resolution{Reason: github.ReasonOtherForge, Host: "gitlab.com"}, "remote is on gitlab.com, not GitHub"},
		{github.Resolution{Reason: github.ReasonUnknownHost, Host: "git.corp"}, "gh has no login for git.corp"},
		{github.Resolution{Reason: github.ReasonNotFound, Repo: github.Repo{Host: "github.com", Owner: "acme", Name: "nope"}}, "repository acme/nope not found"},
	}
	for _, tt := range tests {
		if got := tt.res.Message(); got != tt.want {
			t.Errorf("Message() for %s = %q, want %q", tt.res.Reason, got, tt.want)
		}
	}
}
