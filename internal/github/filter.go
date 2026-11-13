package github

import (
	"strings"
	"unicode"
)

// DefaultFilter is the filter of the default list: a Project's open items,
// fetched from the repository connection (IssueList, PRList), never from
// search (spec #18 §6).
const DefaultFilter = "is:open"

// Filter is a list query in GitHub search syntax, split into its
// qualifiers (key:value, -key:value) and its plain words. A quoted phrase
// is one word; a quoted qualifier value stays one qualifier.
type Filter struct {
	Qualifiers []string
	// Words are the plain words without their quotes.
	Words []string
	// words are the plain words as typed, quotes kept, for the server.
	words []string
}

// ParseFilter splits query into qualifiers and plain words.
func ParseFilter(query string) Filter {
	var f Filter
	for _, tok := range filterTokens(query) {
		if isQualifier(tok) {
			f.Qualifiers = append(f.Qualifiers, tok)
			continue
		}
		f.words = append(f.words, tok)
		f.Words = append(f.Words, unquote(tok))
	}
	return f
}

// Qualified reports whether the query has a qualifier, so it runs as a
// server search on enter.
func (f Filter) Qualified() bool { return len(f.Qualifiers) > 0 }

// Fetch is the filter the list is fetched with and the words that narrow
// its rows locally. The qualifiers (DefaultFilter when there are none) are
// fetched; the plain words narrow the fetched rows, unless server is set,
// when they go to the server with the qualifiers.
func (f Filter) Fetch(server bool) (fetch string, narrow []string) {
	quals := f.Qualifiers
	if len(quals) == 0 {
		quals = []string{DefaultFilter}
	}
	if server && len(f.words) > 0 {
		return strings.Join(append(append([]string{}, quals...), f.words...), " "), nil
	}
	return strings.Join(quals, " "), f.Words
}

// UsesSearch reports whether a list fetched with filter goes through
// GitHub search: every filter but DefaultFilter does.
func UsesSearch(filter string) bool {
	return strings.TrimSpace(filter) != "" && strings.TrimSpace(filter) != DefaultFilter
}

// filterTokens splits query on spaces outside double quotes.
func filterTokens(query string) []string {
	var toks []string
	var b strings.Builder
	quoted := false
	for _, r := range query {
		switch {
		case r == '"':
			quoted = !quoted
			b.WriteRune(r)
		case unicode.IsSpace(r) && !quoted:
			if b.Len() > 0 {
				toks = append(toks, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		toks = append(toks, b.String())
	}
	return toks
}

// isQualifier reports whether tok is key:value (optionally -key:value)
// with a key of letters, digits, - and _ starting with a letter, and a
// non-empty value that is not the rest of a URL.
func isQualifier(tok string) bool {
	tok = strings.TrimPrefix(tok, "-")
	key, value, ok := strings.Cut(tok, ":")
	// A URL (scheme://...) is a word, not a qualifier.
	if !ok || key == "" || value == "" || strings.HasPrefix(value, "//") {
		return false
	}
	for i, r := range key {
		switch {
		case unicode.IsLetter(r) && r < unicode.MaxASCII:
		case i > 0 && (unicode.IsDigit(r) || r == '-' || r == '_'):
		default:
			return false
		}
	}
	return true
}

func unquote(tok string) string {
	return strings.ReplaceAll(tok, `"`, "")
}
