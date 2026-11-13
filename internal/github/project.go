package github

import (
	"net/url"
	"slices"
	"strings"
)

// Remote is one git remote of a checkout: its name and fetch URL.
type Remote struct{ Name, URL string }

// HostKind says whether a remote's host is GitHub (github.com or a GHE host
// gh has a login for), another forge, or a host nobody vouches for.
type HostKind int

const (
	// HostUnknown: neither github.com, a well-known other forge, nor a host
	// `gh auth status` knows.
	HostUnknown HostKind = iota
	// HostGitHub: github.com, or a GHE host gh has a login for.
	HostGitHub
	// HostOtherForge: GitLab, Bitbucket and the like.
	HostOtherForge
)

// Reason is why a path or group has no Project: the forge tabs show it as a
// one-line empty state (spec §2).
type Reason string

const (
	// ReasonNoPath: the group has neither a default path nor sessions.
	ReasonNoPath Reason = "no_path"
	// ReasonNotGit: the path is not inside a git repository (or is gone).
	ReasonNotGit Reason = "not_git"
	// ReasonNoGitHubRemote: no remote points at a GitHub repository (an
	// `upstream` remote alone never counts).
	ReasonNoGitHubRemote Reason = "no_github_remote"
	// ReasonOtherForge: the remote is on another forge (Resolution.Host).
	ReasonOtherForge Reason = "other_forge"
	// ReasonUnknownHost: the remote's host is not one gh has a login for.
	ReasonUnknownHost Reason = "unknown_host"
	// ReasonNotFound: GitHub says the repository does not exist or is not
	// visible to the user.
	ReasonNotFound Reason = "not_found"
)

// Resolution is what one path (or one remote set) resolves to: a Repo, or
// the Reason there is none.
type Resolution struct {
	// Repo is the Project; for ReasonNotFound it is the repository GitHub
	// did not find.
	Repo Repo
	// Root is the main checkout of the repository (worktrees resolve to it);
	// empty when the path is not in a git repository.
	Root   string
	Reason Reason
	// Host is the offending host for ReasonOtherForge and ReasonUnknownHost.
	Host string
}

// Found reports whether the resolution names a Project.
func (r Resolution) Found() bool { return r.Reason == "" && r.Repo.Owner != "" && r.Repo.Name != "" }

// Message is the one-line empty state for a resolution with no Project.
func (r Resolution) Message() string {
	switch r.Reason {
	case ReasonNoPath:
		return "no project path in this group"
	case ReasonNotGit:
		return "not a git repository"
	case ReasonNoGitHubRemote:
		return "no GitHub remote"
	case ReasonOtherForge:
		return "remote is on " + r.Host + ", not GitHub"
	case ReasonUnknownHost:
		return "gh has no login for " + r.Host
	case ReasonNotFound:
		return "repository " + r.Repo.Owner + "/" + r.Repo.Name + " not found"
	}
	return ""
}

// otherForges are hosts that are certainly not GitHub, so resolving a remote
// on them never asks gh about the host.
var otherForges = map[string]bool{
	"gitlab.com":                   true,
	"bitbucket.org":                true,
	"codeberg.org":                 true,
	"gitea.com":                    true,
	"git.sr.ht":                    true,
	"sr.ht":                        true,
	"gitee.com":                    true,
	"dev.azure.com":                true,
	"ssh.dev.azure.com":            true,
	"vs-ssh.visualstudio.com":      true,
	"source.developers.google.com": true,
}

// IsOtherForge reports whether host is a well-known forge that is not GitHub.
func IsOtherForge(host string) bool { return otherForges[strings.ToLower(host)] }

// ParseRemoteURL returns the lowercased host of a git remote URL (https,
// http, ssh, git, git+ssh or scp-like `user@host:path`) and, when its path
// is exactly OWNER/NAME, the Repo on that host. A local path or file URL has
// no host. ssh.github.com and www.github.com are github.com.
func ParseRemoteURL(raw string) (string, Repo) {
	raw = strings.TrimSpace(raw)
	var host, path string
	switch {
	case raw == "":
		return "", Repo{}
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "file" {
			return "", Repo{}
		}
		host, path = u.Hostname(), u.Path
	default:
		// scp-like: [user@]host:path, with the colon before any slash.
		colon := strings.Index(raw, ":")
		if colon <= 0 || strings.Contains(raw[:colon], "/") {
			return "", Repo{}
		}
		host, path = raw[:colon], raw[colon+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	}
	host = strings.ToLower(host)
	switch host {
	case "":
		return "", Repo{}
	case "ssh.github.com", "www.github.com":
		host = DefaultHost
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return host, Repo{}
	}
	name := strings.TrimSuffix(parts[1], ".git")
	if name == "" {
		return host, Repo{}
	}
	return host, Repo{Host: host, Owner: parts[0], Name: name}
}

// PickRemote applies the Project rule to one checkout's remotes (spec §2):
// `origin` is the Project, even when it is a fork. With no origin, the only
// GitHub remote, else the first by name. `upstream` never becomes the
// Project. kind says which hosts are GitHub; it is called only for hosts
// the rule needs.
func PickRemote(remotes []Remote, kind func(host string) HostKind) Resolution {
	sorted := slices.Clone(remotes)
	slices.SortStableFunc(sorted, func(a, b Remote) int { return strings.Compare(a.Name, b.Name) })

	candidates := sorted
	if i := slices.IndexFunc(sorted, func(r Remote) bool { return r.Name == "origin" }); i >= 0 {
		candidates = sorted[i : i+1]
	}
	var fallback Resolution
	for _, r := range candidates {
		if r.Name == "upstream" {
			continue
		}
		host, repo := ParseRemoteURL(r.URL)
		if host == "" {
			continue
		}
		switch k := kind(host); {
		case k == HostGitHub && repo.Owner != "":
			return Resolution{Repo: repo}
		case k == HostOtherForge && fallback.Reason == "":
			fallback = Resolution{Reason: ReasonOtherForge, Host: host}
		case k == HostUnknown && fallback.Reason == "":
			fallback = Resolution{Reason: ReasonUnknownHost, Host: host}
		}
	}
	if fallback.Reason != "" {
		return fallback
	}
	return Resolution{Reason: ReasonNoGitHubRemote}
}
