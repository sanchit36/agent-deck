package github_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/ghexec"
	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

var widgets = github.Repo{Host: "github.com", Owner: "acme", Name: "widgets"}

func TestRepoString(t *testing.T) {
	if got := (github.Repo{Owner: "acme", Name: "widgets"}).String(); got != "github.com/acme/widgets" {
		t.Fatalf("String() with no host = %q, want github.com/acme/widgets", got)
	}
	if got := (github.Repo{Host: "ghe.example.com", Owner: "acme", Name: "widgets"}).String(); got != "ghe.example.com/acme/widgets" {
		t.Fatalf("String() = %q", got)
	}
}

func TestClientRepoAlwaysPassesHostQualifiedRepo(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.OK),
		"issue", "list", "--json", "number,title", "-R", "github.com/acme/widgets")
	c := github.NewClient(fake)
	out, err := c.Repo(context.Background(), github.Repo{Owner: "acme", Name: "widgets"},
		github.Call{Args: []string{"issue", "list", "--json", "number,title"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[{\"number\":1,\"title\":\"First issue\"}]\n" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestClientPassesTimeoutAndStdin(t *testing.T) {
	fake := githubtest.NewFake().ServeFunc(githubtest.MustLoad(t, githubtest.OK), func([]string) bool { return true })
	c := github.NewClient(fake)
	if _, err := c.Repo(context.Background(), widgets, github.Call{Args: []string{"pr", "merge", "3"}, Timeout: ghexec.MergeTimeout, Stdin: []byte("in")}); err != nil {
		t.Fatal(err)
	}
	call := fake.Calls()[0]
	if call.Timeout != ghexec.MergeTimeout || string(call.Stdin) != "in" {
		t.Fatalf("call = %+v, want the merge timeout and stdin passed through", call)
	}
}

func TestClientAPINamesTheHost(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.OK),
		"api", "--hostname", "ghe.example.com", "graphql", "-f", "query=q")
	c := github.NewClient(fake)
	if _, err := c.API(context.Background(), "ghe.example.com", github.Call{Args: []string{"graphql", "-f", "query=q"}}); err != nil {
		t.Fatal(err)
	}
}

func TestClientClassifiesFailures(t *testing.T) {
	fake := githubtest.NewFake().ServeFunc(githubtest.MustLoad(t, githubtest.NotFound), func([]string) bool { return true })
	_, err := github.NewClient(fake).Repo(context.Background(), widgets, github.Call{Args: []string{"issue", "view", "1"}})
	if github.ClassOf(err) != github.ClassNotFound {
		t.Fatalf("err = %v, want class not_found", err)
	}
}

func TestClientAuthStatus(t *testing.T) {
	tests := []struct {
		fixture string
		host    string
		class   github.Class
	}{
		{githubtest.AuthStatusOK, "github.com", ""},
		{githubtest.AuthStatusNoHost, "ghe.example.invalid", github.ClassNotLoggedIn},
		{githubtest.NotLoggedIn, "github.com", github.ClassNotLoggedIn},
		{githubtest.AuthStatusOffline, "ghe.example.com", github.ClassFailed},
		{githubtest.GhMissing, "github.com", github.ClassGhMissing},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, tt.fixture), "auth", "status", "--hostname", tt.host)
			err := github.NewClient(fake).AuthStatus(context.Background(), tt.host)
			if github.ClassOf(err) != tt.class || (tt.class == "") != (err == nil) {
				t.Fatalf("AuthStatus = %v (class %q), want class %q", err, github.ClassOf(err), tt.class)
			}
		})
	}
}

// TestClientUnknownHostNeedsGhToHaveNoLogin: "error connecting to <host>" is
// a DNS failure, which an unknown GHE host and a GHE host behind a VPN that
// is down both produce. Only a host gh has no login for is unknown.
func TestClientUnknownHostNeedsGhToHaveNoLogin(t *testing.T) {
	ghe := github.Repo{Host: "ghe.example.invalid", Owner: "acme", Name: "widgets"}
	isList := func(args []string) bool { return slices.Contains(args, "list") }
	tests := []struct {
		auth  string
		class github.Class
	}{
		{githubtest.AuthStatusNoHost, github.ClassUnknownHost},
		{githubtest.AuthStatusOK, github.ClassNetwork},
		{githubtest.AuthStatusOffline, github.ClassNetwork},
	}
	for _, tt := range tests {
		t.Run(tt.auth, func(t *testing.T) {
			fake := githubtest.NewFake().
				ServeFunc(githubtest.MustLoad(t, githubtest.UnknownHost), isList).
				Serve(githubtest.MustLoad(t, tt.auth), "auth", "status", "--hostname", "ghe.example.invalid")
			_, err := github.NewClient(fake).Repo(context.Background(), ghe, github.Call{Args: []string{"issue", "list"}})
			if github.ClassOf(err) != tt.class {
				t.Fatalf("err = %v (class %q), want class %q", err, github.ClassOf(err), tt.class)
			}
		})
	}
}

func TestClientRateLimitedCarriesResetTime(t *testing.T) {
	fake := githubtest.NewFake().
		Serve(githubtest.MustLoad(t, githubtest.RateLimited), "issue", "list", "-R", "github.com/acme/widgets").
		Serve(githubtest.MustLoad(t, githubtest.RateLimitStatus), "api", "--hostname", "github.com", "rate_limit")
	_, err := github.NewClient(fake).Repo(context.Background(), widgets, github.Call{Args: []string{"issue", "list"}})
	var ge *github.Error
	if !errors.As(err, &ge) || ge.Class != github.ClassRateLimited {
		t.Fatalf("err = %v, want class rate_limited", err)
	}
	// graphql is the exhausted resource (remaining 0); its reset wins over core's.
	if want := time.Unix(1794569400, 0); !ge.Reset.Equal(want) {
		t.Fatalf("Reset = %v, want %v", ge.Reset, want)
	}
}

func TestClientRateLimitedWithoutStatusKeepsZeroReset(t *testing.T) {
	fake := githubtest.NewFake().
		Serve(githubtest.MustLoad(t, githubtest.RateLimited), "issue", "list", "-R", "github.com/acme/widgets").
		Serve(githubtest.MustLoad(t, githubtest.Network), "api", "--hostname", "github.com", "rate_limit")
	_, err := github.NewClient(fake).Repo(context.Background(), widgets, github.Call{Args: []string{"issue", "list"}})
	var ge *github.Error
	if !errors.As(err, &ge) || ge.Class != github.ClassRateLimited || !ge.Reset.IsZero() {
		t.Fatalf("err = %v, want class rate_limited with no reset time", err)
	}
}
