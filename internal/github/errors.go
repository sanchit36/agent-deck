package github

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Class says why a gh call failed, so the UI can pick its message and
// whether to retry (spec §1 and §5).
type Class string

const (
	// ClassGhMissing: gh is not on PATH.
	ClassGhMissing Class = "gh_missing"
	// ClassNotLoggedIn: gh has no login (exit 4, or `gh auth status` says so).
	ClassNotLoggedIn Class = "not_logged_in"
	// ClassBadToken: gh has a token that GitHub rejects (HTTP 401).
	ClassBadToken Class = "bad_token"
	// ClassSAML: an organization's SAML SSO has not authorized the token;
	// Error.AuthorizeURL is the page that does.
	ClassSAML Class = "saml"
	// ClassUnknownHost: a GHE host gh cannot reach and has no login for.
	ClassUnknownHost Class = "unknown_host"
	// ClassNotFound: the repository, issue or PR does not exist or is not
	// visible to the user.
	ClassNotFound Class = "not_found"
	// ClassNetwork: the network is down or the call timed out.
	ClassNetwork Class = "network"
	// ClassRateLimited: GitHub's rate limit; Error.Reset is when it lifts,
	// when known.
	ClassRateLimited Class = "rate_limited"
	// ClassServer: GitHub answered with a 5xx.
	ClassServer Class = "server_error"
	// ClassFailed: any other failure; Error.Message is gh's first line.
	ClassFailed Class = "failed"
)

var classText = map[Class]string{
	ClassGhMissing:   "gh is not installed",
	ClassNotLoggedIn: "not logged in",
	ClassBadToken:    "bad credentials",
	ClassSAML:        "saml authorization required",
	ClassUnknownHost: "unknown host",
	ClassNotFound:    "not found",
	ClassNetwork:     "network unreachable",
	ClassRateLimited: "rate-limited",
	ClassServer:      "server error",
	ClassFailed:      "failed",
}

// Error is a classified gh failure.
type Error struct {
	Class Class
	// Host is the GitHub host the call went to.
	Host string
	// Message is gh's first non-blank stderr line, as gh printed it.
	Message string
	// AuthorizeURL is the SAML SSO page to open (ClassSAML only).
	AuthorizeURL string
	// Reset is when the rate limit lifts (ClassRateLimited only; zero when
	// unknown, e.g. a secondary rate limit).
	Reset time.Time
	// Status is the HTTP status gh reported, 0 when it named none.
	Status int
	// ExitCode is gh's exit code, 0 when gh did not run to completion.
	ExitCode int
	// Err is the Runner error (exec.ErrNotFound, context.DeadlineExceeded),
	// nil when gh exited non-zero.
	Err error
}

func (e *Error) Error() string {
	msg := "gh on " + e.Host + ": " + classText[e.Class]
	switch {
	case e.Message != "":
		msg += ": " + e.Message
	case e.Err != nil:
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// Transient reports whether the same call may succeed later without the
// user doing anything: network, rate limit and 5xx. Auth, SAML and gh
// missing are not retried automatically (spec §5).
func (e *Error) Transient() bool {
	switch e.Class {
	case ClassNetwork, ClassRateLimited, ClassServer:
		return true
	}
	return false
}

// ClassOf returns the Class of err, or "" when err is not a *Error.
func ClassOf(err error) Class {
	var e *Error
	if errors.As(err, &e) {
		return e.Class
	}
	return ""
}

var (
	samlURLRe       = regexp.MustCompile(`Authorize in your web browser:\s+(\S+)`)
	httpStatusRe    = regexp.MustCompile(`\bHTTP ([1-5]\d\d)\b`)
	connectingToRe  = regexp.MustCompile(`^error connecting to (\S+)`)
	networkMarkers  = []string{"dial tcp", "i/o timeout", "connection refused", "connection reset", "no such host", "network is unreachable", "tls handshake timeout", "context deadline exceeded", "unexpected eof"}
	rateLimitMarker = regexp.MustCompile(`(?i)rate limit`)
)

// Classify turns the outcome of one gh call to host into nil (success), the
// runner error itself when ctx was cancelled (the caller gave up; gh did
// not fail), or an *Error. The exit code decides first (gh missing,
// timeout, exit 4), then gh's stderr: the SAML line anywhere, else the
// first non-blank line.
//
// "error connecting to <host>" is a DNS failure: for a GHE host it is
// classed ClassUnknownHost here, and Client turns it into ClassNetwork
// when gh does have a login for that host.
func Classify(host string, res Result, runErr error) error {
	e := &Error{Host: host, ExitCode: res.ExitCode, Err: runErr}
	switch {
	case runErr != nil && errors.Is(runErr, exec.ErrNotFound):
		e.Class, e.ExitCode = ClassGhMissing, 0
		return e
	case runErr != nil && errors.Is(runErr, context.DeadlineExceeded):
		e.Class, e.ExitCode = ClassNetwork, 0
		return e
	case runErr != nil && errors.Is(runErr, context.Canceled):
		return runErr
	case runErr != nil:
		e.Class, e.ExitCode = ClassFailed, 0
		return e
	case res.ExitCode == 0:
		return nil
	}

	stderr := string(res.Stderr)
	e.Message = firstLine(stderr)
	if m := httpStatusRe.FindStringSubmatch(e.Message); m != nil {
		e.Status, _ = strconv.Atoi(m[1])
	}
	lower := strings.ToLower(e.Message)
	switch {
	case res.ExitCode == 4:
		e.Class = ClassNotLoggedIn
	case samlURLRe.MatchString(stderr):
		e.Class = ClassSAML
		e.AuthorizeURL = samlURLRe.FindStringSubmatch(stderr)[1]
	case e.Status == 401:
		e.Class = ClassBadToken
	case connectingToRe.MatchString(e.Message):
		e.Class = ClassUnknownHost
		if isDotCom(connectingToRe.FindStringSubmatch(e.Message)[1]) || isDotCom(host) {
			e.Class = ClassNetwork
		}
	case strings.Contains(e.Message, "Could not resolve to a") || e.Status == 404:
		e.Class = ClassNotFound
	case e.Status == 429 || rateLimitMarker.MatchString(e.Message) && strings.Contains(lower, "exceeded"):
		e.Class = ClassRateLimited
	case e.Status >= 500:
		e.Class = ClassServer
	case containsAny(lower, networkMarkers):
		e.Class = ClassNetwork
	default:
		e.Class = ClassFailed
	}
	return e
}

func isDotCom(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || host == "api.github.com"
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
