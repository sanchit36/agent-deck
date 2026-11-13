// Package prompttemplate is the Prompt Template engine (spec #18 §12): the
// [prompt_templates.<name>] config shape, the built-in templates, {var}
// rendering for an Issue or Pull Request, label matching and the config-load
// checks. It is pure: no gh calls, no UI, no logging.
package prompttemplate

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Kind is the kind of item a template applies to.
type Kind string

const (
	KindIssue Kind = "issue"
	KindPR    Kind = "pr"
)

// MaxValueBytes caps each of {body}, {comments} and {review-comments}.
const MaxValueBytes = 16 * 1024

// Variable names.
const (
	VarNumber         = "number"
	VarTitle          = "title"
	VarBody           = "body"
	VarURL            = "url"
	VarRepo           = "repo"
	VarLabels         = "labels"
	VarAuthor         = "author"
	VarComments       = "comments"
	VarHeadBranch     = "head-branch"
	VarBaseBranch     = "base-branch"
	VarReviewComments = "review-comments"
)

// SharedVariables apply to Issues and Pull Requests.
var SharedVariables = []string{VarNumber, VarTitle, VarBody, VarURL, VarRepo, VarLabels, VarAuthor, VarComments}

// PRVariables apply to Pull Requests only; in an issue template they render empty.
var PRVariables = []string{VarHeadBranch, VarBaseBranch, VarReviewComments}

// Vars are the values a template is filled with. Comments and ReviewComments
// are pre-formatted text (ReviewComments carries file:line per thread); the
// caller fetches them only when Uses reports the template needs them.
type Vars struct {
	Kind   Kind
	Number int
	Title  string
	Body   string
	URL    string
	// Repo is owner/name, the value of {repo}.
	Repo string
	// Host is the GitHub host; it is named in the truncation hint when it is
	// not github.com, so the hint works on GitHub Enterprise.
	Host           string
	Labels         []string
	Author         string
	Comments       string
	HeadBranch     string
	BaseBranch     string
	ReviewComments string
}

func isPRVariable(name string) bool {
	for _, n := range PRVariables {
		if n == name {
			return true
		}
	}
	return false
}

func isKnownVariable(name string) bool {
	for _, n := range SharedVariables {
		if n == name {
			return true
		}
	}
	return isPRVariable(name)
}

// value returns the substitution for a known variable.
func (v Vars) value(name string) string {
	if v.Kind != KindPR && isPRVariable(name) {
		return ""
	}
	switch name {
	case VarNumber:
		return strconv.Itoa(v.Number)
	case VarTitle:
		return v.Title
	case VarBody:
		return v.capped(v.Body)
	case VarURL:
		return v.URL
	case VarRepo:
		return v.Repo
	case VarLabels:
		return strings.Join(v.Labels, ", ")
	case VarAuthor:
		return v.Author
	case VarComments:
		return v.capped(v.Comments)
	case VarHeadBranch:
		return v.HeadBranch
	case VarBaseBranch:
		return v.BaseBranch
	case VarReviewComments:
		return v.capped(v.ReviewComments)
	}
	return ""
}

// capped cuts s to MaxValueBytes on a rune boundary and appends a hint that
// tells the agent how to read the rest.
func (v Vars) capped(s string) string {
	if len(s) <= MaxValueBytes {
		return s
	}
	cut := MaxValueBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n" + v.truncationHint()
}

func (v Vars) truncationHint() string {
	verb := "issue"
	if v.Kind == KindPR {
		verb = "pr"
	}
	repo := v.Repo
	if v.Host != "" && !strings.EqualFold(v.Host, "github.com") {
		repo = v.Host + "/" + v.Repo
	}
	return "[truncated: run gh " + verb + " view " + strconv.Itoa(v.Number) + " -R " + repo + " --comments]"
}

// scanVariable reports the {name} token starting at text[i] (which is '{'),
// returning the name and the index just past '}'. ok is false when the
// braces do not enclose a well-formed name ([a-z0-9-]+).
func scanVariable(text string, i int) (name string, end int, ok bool) {
	j := i + 1
	for j < len(text) {
		c := text[j]
		if c == '}' {
			if j == i+1 {
				return "", 0, false
			}
			return text[i+1 : j], j + 1, true
		}
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return "", 0, false
		}
		j++
	}
	return "", 0, false
}

// Render fills text with v in one pass over text: substituted values are
// never scanned again, so a {title} inside an issue body stays as written.
// Unknown variables stay literal; PR-only variables render empty for an Issue.
func Render(text string, v Vars) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		if text[i] == '{' {
			if name, end, ok := scanVariable(text, i); ok && isKnownVariable(name) {
				b.WriteString(v.value(name))
				i = end
				continue
			}
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

// Variables lists the well-formed {name} tokens in text, known or not, in
// order of first appearance.
func Variables(text string) []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		if name, _, ok := scanVariable(text, i); ok && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Uses reports whether text references the variable name, so the caller
// fetches comments or review comments only when a template needs them.
func Uses(text, name string) bool {
	for _, n := range Variables(text) {
		if n == name {
			return true
		}
	}
	return false
}
