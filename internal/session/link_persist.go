package session

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/github"
)

// A Link ties a session to a GitHub Issue or Pull Request (GLOSSARY.md,
// spec #18 §11). A session holds at most one Issue link and one PR link;
// both live in the tool_data extras zone of the session row, so there is no
// schema change, a legacy binary preserves them on save, and they are
// deleted with the row.

// LinkKind says what a Link points at.
type LinkKind string

const (
	// LinkIssue links a session to an Issue.
	LinkIssue LinkKind = "issue"
	// LinkPR links a session to a Pull Request.
	LinkPR LinkKind = "pr"
)

// Link is the repo (host/owner/name) and number of a linked item. Its kind
// is the slot it sits in (Instance.IssueLink or Instance.PRLink).
type Link struct {
	Host   string `json:"host"`
	Owner  string `json:"owner"`
	Name   string `json:"name"`
	Number int    `json:"number"`
}

const (
	toolDataIssueLinkKey = "issue_link"
	toolDataPRLinkKey    = "pr_link"
)

// Repo is the link's repo as HOST/OWNER/NAME, the form gh's -R takes.
func (l Link) Repo() string {
	return l.Host + "/" + l.Owner + "/" + l.Name
}

// Label is the static row badge text: "#12" for an Issue, "PR#34" for a PR.
func (l Link) Label(kind LinkKind) string {
	n := strconv.Itoa(l.Number)
	if kind == LinkPR {
		return "PR#" + n
	}
	return "#" + n
}

// ParseLinkKind accepts "issue" or "pr" (any case).
func ParseLinkKind(s string) (LinkKind, error) {
	switch LinkKind(strings.ToLower(strings.TrimSpace(s))) {
	case LinkIssue:
		return LinkIssue, nil
	case LinkPR:
		return LinkPR, nil
	}
	return "", fmt.Errorf("invalid link kind %q: want issue or pr", s)
}

// ParseLinkRepo reads a repo as OWNER/NAME (host github.com), HOST/OWNER/NAME,
// or a git remote URL (https://, ssh://, [user@]host:owner/name). Every form
// goes through github.ParseRemoteURL, so a link names the same repo the
// forge tabs resolve (ssh.github.com and www.github.com are github.com; a
// local path or file URL is no repo). The returned Link has no number. Hosts
// are lowercased; owner and name keep their case.
func ParseLinkRepo(s string) (Link, error) {
	raw := strings.TrimSpace(s)
	remote := raw
	if !strings.Contains(raw, ":") {
		// [HOST/]OWNER/NAME, the form gh's -R takes.
		parts := strings.Split(strings.Trim(raw, "/"), "/")
		switch {
		case strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, "?#@ \t") || len(parts) < 2 || len(parts) > 3:
			return Link{}, fmt.Errorf("invalid repo %q: want OWNER/NAME or HOST/OWNER/NAME", s)
		case len(parts) == 2:
			parts = append([]string{github.DefaultHost}, parts...)
		}
		remote = "https://" + strings.Join(parts, "/")
	}
	_, repo := github.ParseRemoteURL(remote)
	if repo.Host == "" || repo.Owner == "" || repo.Name == "" {
		return Link{}, fmt.Errorf("invalid repo %q: want OWNER/NAME, HOST/OWNER/NAME or a GitHub remote URL", s)
	}
	return Link{Host: repo.Host, Owner: repo.Owner, Name: repo.Name}, nil
}

// LinkOf returns the session's link of kind, nil when there is none.
func (i *Instance) LinkOf(kind LinkKind) *Link {
	if kind == LinkPR {
		return i.PRLink
	}
	return i.IssueLink
}

// SetLink records l as the session's link of kind, replacing any earlier one.
func (i *Instance) SetLink(kind LinkKind, l Link) {
	if kind == LinkPR {
		i.PRLink, i.prLinkCleared = &l, false
		return
	}
	i.IssueLink, i.issueLinkCleared = &l, false
}

// ClearLink removes the session's link of kind and reports whether there was
// one. The next save writes an explicit null so the extras merge cannot carry
// the stored link forward.
func (i *Instance) ClearLink(kind LinkKind) bool {
	if kind == LinkPR {
		had := i.PRLink != nil
		i.PRLink, i.prLinkCleared = nil, i.prLinkCleared || had
		return had
	}
	had := i.IssueLink != nil
	i.IssueLink, i.issueLinkCleared = nil, i.issueLinkCleared || had
	return had
}

// WriteLinksToToolData records both link slots in the blob. A nil link
// removes its key (rows that were never linked keep their bytes) unless its
// cleared flag is set, which writes an explicit null instead (same protocol
// as WriteFavoriteToToolData).
func WriteLinksToToolData(td json.RawMessage, issue, pr *Link, issueCleared, prCleared bool) json.RawMessage {
	if issue == nil && pr == nil && !issueCleared && !prCleared {
		return td // fast path: nothing to add or remove for most rows
	}
	m := map[string]json.RawMessage{}
	if len(td) > 0 {
		_ = json.Unmarshal(td, &m)
	}
	putLink(m, toolDataIssueLinkKey, issue, issueCleared)
	putLink(m, toolDataPRLinkKey, pr, prCleared)
	out, _ := json.Marshal(m)
	return out
}

func putLink(m map[string]json.RawMessage, key string, l *Link, cleared bool) {
	switch {
	case l != nil:
		b, _ := json.Marshal(l)
		m[key] = b
	case cleared:
		m[key] = json.RawMessage("null")
	default:
		delete(m, key)
	}
}

// ReadLinksFromToolData returns the stored Issue and PR links; nil for a
// legacy row, a cleared slot or a malformed value.
func ReadLinksFromToolData(td json.RawMessage) (issue, pr *Link) {
	if len(td) == 0 {
		return nil, nil
	}
	var blob struct {
		Issue *Link `json:"issue_link"`
		PR    *Link `json:"pr_link"`
	}
	if err := json.Unmarshal(td, &blob); err != nil {
		return nil, nil
	}
	return validLink(blob.Issue), validLink(blob.PR)
}

func validLink(l *Link) *Link {
	if l == nil || l.Number <= 0 || l.Owner == "" || l.Name == "" || l.Host == "" {
		return nil
	}
	return l
}
