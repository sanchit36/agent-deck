package github_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

// TestClassifyFixtures: one recorded fixture per error class, classified as
// the real gh output would be.
func TestClassifyFixtures(t *testing.T) {
	tests := []struct {
		fixture   string
		host      string
		class     github.Class
		message   string
		url       string
		transient bool
		wraps     error
	}{
		{fixture: githubtest.GhMissing, host: "github.com", class: github.ClassGhMissing, wraps: exec.ErrNotFound},
		{fixture: githubtest.NotLoggedIn, host: "github.com", class: github.ClassNotLoggedIn,
			message: "To get started with GitHub CLI, please run:  gh auth login"},
		{fixture: githubtest.BadToken, host: "github.com", class: github.ClassBadToken,
			message: "HTTP 401: Bad credentials (https://api.github.com/graphql)"},
		{fixture: githubtest.SAML, host: "github.com", class: github.ClassSAML,
			message: "GraphQL: Resource protected by organization SAML enforcement. You must grant your Personal Access token access to this organization. (repository)",
			url:     "https://github.com/orgs/acme/sso?authorization_request=A1B2C3"},
		{fixture: githubtest.UnknownHost, host: "ghe.example.invalid", class: github.ClassUnknownHost,
			message: "error connecting to ghe.example.invalid"},
		{fixture: githubtest.NotFound, host: "github.com", class: github.ClassNotFound,
			message: "GraphQL: Could not resolve to a Repository with the name 'acme/nope'. (repository)"},
		{fixture: githubtest.Network, host: "github.com", class: github.ClassNetwork,
			message: "error connecting to api.github.com", transient: true},
		{fixture: githubtest.Timeout, host: "github.com", class: github.ClassNetwork, transient: true, wraps: context.DeadlineExceeded},
		{fixture: githubtest.RateLimited, host: "github.com", class: github.ClassRateLimited,
			message: "GraphQL: API rate limit already exceeded for user ID 1234567. (rateLimit)", transient: true},
		{fixture: githubtest.ServerError, host: "github.com", class: github.ClassServer,
			message: "HTTP 502: 502 Bad Gateway (https://api.github.com/graphql)", transient: true},
		{fixture: githubtest.Failed, host: "github.com", class: github.ClassFailed,
			message: "unknown flag: --frobnicate"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			f := githubtest.MustLoad(t, tt.fixture)
			err := github.Classify(tt.host, f.Result, f.Err)
			var ge *github.Error
			if !errors.As(err, &ge) {
				t.Fatalf("Classify = %v, want a *github.Error", err)
			}
			if ge.Class != tt.class {
				t.Fatalf("Class = %q, want %q (err %v)", ge.Class, tt.class, err)
			}
			if github.ClassOf(err) != tt.class {
				t.Fatalf("ClassOf = %q, want %q", github.ClassOf(err), tt.class)
			}
			if ge.Host != tt.host {
				t.Errorf("Host = %q, want %q", ge.Host, tt.host)
			}
			if tt.message != "" && ge.Message != tt.message {
				t.Errorf("Message = %q, want %q", ge.Message, tt.message)
			}
			if ge.AuthorizeURL != tt.url {
				t.Errorf("AuthorizeURL = %q, want %q", ge.AuthorizeURL, tt.url)
			}
			if ge.Transient() != tt.transient {
				t.Errorf("Transient() = %v, want %v", ge.Transient(), tt.transient)
			}
			if tt.wraps != nil && !errors.Is(err, tt.wraps) {
				t.Errorf("error %v does not wrap %v", err, tt.wraps)
			}
		})
	}
}

func TestClassifySuccessIsNil(t *testing.T) {
	f := githubtest.MustLoad(t, githubtest.OK)
	if err := github.Classify("github.com", f.Result, f.Err); err != nil {
		t.Fatalf("Classify(ok) = %v, want nil", err)
	}
}

// TestClassifyOtherWordings covers gh's other phrasings of the same classes
// (`gh api` prints "gh: <message> (HTTP n)") and the exit-code-first rule.
func TestClassifyOtherWordings(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		exit   int
		stderr string
		class  github.Class
		status int
	}{
		{"exit 4 beats the stderr", "github.com", 4, "HTTP 401: Bad credentials\n", github.ClassNotLoggedIn, 401},
		{"gh api 401", "github.com", 1, "gh: Bad credentials (HTTP 401)\n", github.ClassBadToken, 401},
		{"gh api 404", "github.com", 1, "gh: Not Found (HTTP 404)\n", github.ClassNotFound, 404},
		{"PR not found", "github.com", 1, "GraphQL: Could not resolve to a PullRequest with the number of 999999. (repository.pullRequest)\n", github.ClassNotFound, 0},
		{"gh api 500", "github.com", 1, "gh: Server Error (HTTP 500)\n", github.ClassServer, 500},
		{"503", "github.com", 1, "HTTP 503: Service Unavailable (https://api.github.com/graphql)\n", github.ClassServer, 503},
		{"REST rate limit", "github.com", 1, "gh: API rate limit exceeded for user ID 1. (HTTP 403)\n", github.ClassRateLimited, 403},
		{"secondary rate limit", "github.com", 1, "HTTP 403: You have exceeded a secondary rate limit. Please wait a few minutes before you try again.\n", github.ClassRateLimited, 403},
		{"429", "github.com", 1, "HTTP 429: Too Many Requests\n", github.ClassRateLimited, 429},
		{"plain 403 is not a rate limit", "github.com", 1, "HTTP 403: Must have admin rights to Repository.\n", github.ClassFailed, 403},
		{"dial error", "github.com", 1, "Post \"https://api.github.com/graphql\": dial tcp: lookup api.github.com: i/o timeout\n", github.ClassNetwork, 0},
		{"connection refused", "ghe.example.com", 1, "Post \"https://ghe.example.com/api/graphql\": dial tcp 10.0.0.1:443: connect: connection refused\n", github.ClassNetwork, 0},
		{"error connecting to a GHE API host", "ghe.example.com", 1, "error connecting to ghe.example.com\n", github.ClassUnknownHost, 0},
		{"error connecting to github.com is the network", "github.com", 1, "error connecting to github.com\n", github.ClassNetwork, 0},
		{"leading blank line", "github.com", 1, "\n  GraphQL: Could not resolve to a Repository with the name 'a/b'. (repository)\n", github.ClassNotFound, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := github.Classify(tt.host, github.Result{Stderr: []byte(tt.stderr), ExitCode: tt.exit}, nil)
			var ge *github.Error
			if !errors.As(err, &ge) {
				t.Fatalf("Classify = %v, want a *github.Error", err)
			}
			if ge.Class != tt.class {
				t.Fatalf("Class = %q, want %q", ge.Class, tt.class)
			}
			if ge.Status != tt.status {
				t.Errorf("Status = %d, want %d", ge.Status, tt.status)
			}
			if ge.ExitCode != tt.exit {
				t.Errorf("ExitCode = %d, want %d", ge.ExitCode, tt.exit)
			}
		})
	}
}

func TestClassifyCancelIsNotAClass(t *testing.T) {
	err := github.Classify("github.com", github.Result{}, fmt.Errorf("running gh: %w", context.Canceled))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Classify(cancelled) = %v, want it to wrap context.Canceled", err)
	}
	if c := github.ClassOf(err); c != "" {
		t.Fatalf("ClassOf(cancelled) = %q, want none: the caller gave up, gh did not fail", c)
	}
}

func TestErrorMessageNamesHostAndCause(t *testing.T) {
	f := githubtest.MustLoad(t, githubtest.SAML)
	err := github.Classify("github.com", f.Result, nil)
	want := "gh on github.com: saml authorization required: GraphQL: Resource protected by organization SAML enforcement. You must grant your Personal Access token access to this organization. (repository)"
	if err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
}
