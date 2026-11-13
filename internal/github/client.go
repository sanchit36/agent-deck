package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultHost is the host of a Repo that names none.
const DefaultHost = "github.com"

// Repo is a repository on a GitHub host (github.com or a GHE host).
type Repo struct{ Host, Owner, Name string }

// HostOrDefault is r.Host, or DefaultHost when it is empty.
func (r Repo) HostOrDefault() string {
	if r.Host == "" {
		return DefaultHost
	}
	return r.Host
}

// SameRepo reports whether a and b name one repository: GitHub ignores
// case in hosts, owners and names, and an empty host is DefaultHost.
func SameRepo(a, b Repo) bool {
	return strings.EqualFold(a.HostOrDefault(), b.HostOrDefault()) &&
		strings.EqualFold(a.Owner, b.Owner) && strings.EqualFold(a.Name, b.Name)
}

// String is HOST/OWNER/REPO, the form `gh -R` takes.
func (r Repo) String() string { return r.HostOrDefault() + "/" + r.Owner + "/" + r.Name }

// Client runs gh calls through a Runner and classifies their failures. It
// is safe for concurrent use when its Runner is.
type Client struct{ runner Runner }

// NewClient returns a Client over r; a nil r runs the real gh.
func NewClient(r Runner) *Client {
	if r == nil {
		r = ExecRunner{}
	}
	return &Client{runner: r}
}

// Repo runs `gh <call.Args> -R HOST/OWNER/REPO` and returns its stdout. A
// repository-scoped gh command never relies on the working directory's git
// remotes. Failures are *Error (see Classify).
func (c *Client) Repo(ctx context.Context, repo Repo, call Call) ([]byte, error) {
	if repo.Owner == "" || repo.Name == "" {
		return nil, fmt.Errorf("running gh for repository %q: owner and name are required", repo.String())
	}
	call.Args = append(append([]string{}, call.Args...), "-R", repo.String())
	return c.do(ctx, repo.HostOrDefault(), call)
}

// API runs `gh api --hostname HOST <call.Args>` and returns its stdout.
// Use it for REST and GraphQL calls (`gh api` takes no -R; the repository
// is in the path or the query).
func (c *Client) API(ctx context.Context, host string, call Call) ([]byte, error) {
	if host == "" {
		host = DefaultHost
	}
	call.Args = append([]string{"api", "--hostname", host}, call.Args...)
	return c.do(ctx, host, call)
}

// AuthStatus runs `gh auth status --hostname HOST`: nil when gh has a
// working login for host, ClassNotLoggedIn when it has none, else the
// classified failure.
func (c *Client) AuthStatus(ctx context.Context, host string) error {
	if host == "" {
		host = DefaultHost
	}
	res, err := c.runner.Run(ctx, Call{Args: []string{"auth", "status", "--hostname", host}})
	if err != nil || res.ExitCode == 0 {
		return Classify(host, res, err)
	}
	out := string(res.Stderr) + "\n" + string(res.Stdout)
	if strings.Contains(out, "not logged into") {
		return &Error{Class: ClassNotLoggedIn, Host: host, Message: firstLine(out), ExitCode: res.ExitCode}
	}
	// gh auth status reports a failed check on stdout.
	res.Stderr = []byte(out)
	return Classify(host, res, nil)
}

func (c *Client) do(ctx context.Context, host string, call Call) ([]byte, error) {
	res, runErr := c.runner.Run(ctx, call)
	err := Classify(host, res, runErr)
	var e *Error
	if !errors.As(err, &e) {
		return res.Stdout, err
	}
	switch e.Class {
	case ClassUnknownHost:
		// A DNS failure: unknown only if gh has no login for the host,
		// otherwise the host is just unreachable right now (VPN, offline).
		if ClassOf(c.AuthStatus(ctx, host)) != ClassNotLoggedIn {
			e.Class = ClassNetwork
		}
	case ClassRateLimited:
		e.Reset = c.rateLimitReset(ctx, host)
	}
	return res.Stdout, e
}

// rateLimitReset asks `gh api rate_limit` (which costs no quota) when the
// exhausted limit resets: the latest reset of a resource with nothing
// remaining. Zero when it cannot tell (a secondary limit, or the call
// failed).
func (c *Client) rateLimitReset(ctx context.Context, host string) time.Time {
	res, err := c.runner.Run(ctx, Call{Args: []string{"api", "--hostname", host, "rate_limit"}})
	if err != nil || res.ExitCode != 0 {
		return time.Time{}
	}
	var body struct {
		Resources map[string]struct {
			Remaining int   `json:"remaining"`
			Reset     int64 `json:"reset"`
		} `json:"resources"`
	}
	if json.Unmarshal(res.Stdout, &body) != nil {
		return time.Time{}
	}
	var reset int64
	for _, r := range body.Resources {
		if r.Remaining == 0 && r.Reset > reset {
			reset = r.Reset
		}
	}
	if reset == 0 {
		return time.Time{}
	}
	return time.Unix(reset, 0)
}
