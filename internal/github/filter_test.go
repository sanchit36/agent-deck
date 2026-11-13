package github_test

import (
	"slices"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
)

// TestFilterClassification pins where each query runs (spec #18 §6): the
// default list (the repository connection), a server search for anything
// with a qualifier, plain words narrowing the fetched rows, and plain
// words sent to the server once the user asks for it.
func TestFilterClassification(t *testing.T) {
	for _, c := range []struct {
		name   string
		query  string
		server bool
		fetch  string
		narrow []string
		search bool
	}{
		{"empty is the default list", "", false, "is:open", nil, false},
		{"is:open is the default list", "is:open", false, "is:open", nil, false},
		{"spaces around is:open", "  is:open  ", false, "is:open", nil, false},
		{"plain words narrow the default list", "crash login", false, "is:open", []string{"crash", "login"}, false},
		{"is:open plus words narrows", "is:open crash", false, "is:open", []string{"crash"}, false},
		{"words before is:open", "crash is:open", false, "is:open", []string{"crash"}, false},
		{"a qualifier searches", "label:bug", false, "label:bug", nil, true},
		{"qualifiers searched, words narrow", "is:open label:bug crash", false, "is:open label:bug", []string{"crash"}, true},
		{"closed searches", "is:closed", false, "is:closed", nil, true},
		{"negated qualifier", "-label:bug", false, "-label:bug", nil, true},
		{"quoted qualifier value", `label:"good first issue"`, false, `label:"good first issue"`, nil, true},
		{"quoted phrase is a word", `"exact phrase" x`, false, "is:open", []string{"exact phrase", "x"}, false},
		{"@me qualifier", "assignee:@me", false, "assignee:@me", nil, true},
		{"number is a word", "#123", false, "is:open", []string{"#123"}, false},
		{"time-like token is a word", "12:30", false, "is:open", []string{"12:30"}, false},
		{"empty value is a word", "foo:", false, "is:open", []string{"foo:"}, false},
		{"url is a word", "https://x.test/a", false, "is:open", []string{"https://x.test/a"}, false},
		{"words to the server", "crash login", true, "is:open crash login", nil, true},
		{"words to the server keep qualifiers", "is:closed crash", true, "is:closed crash", nil, true},
		{"server with no words is the default list", "is:open", true, "is:open", nil, false},
		{"phrase to the server keeps quotes", `is:open "two words"`, true, `is:open "two words"`, nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := github.ParseFilter(c.query)
			fetch, narrow := f.Fetch(c.server)
			if fetch != c.fetch {
				t.Fatalf("fetch = %q, want %q", fetch, c.fetch)
			}
			if !slices.Equal(narrow, c.narrow) {
				t.Fatalf("narrow = %q, want %q", narrow, c.narrow)
			}
			if got := github.UsesSearch(fetch); got != c.search {
				t.Fatalf("UsesSearch(%q) = %v, want %v", fetch, got, c.search)
			}
		})
	}
}

// TestFilterQualified: a query has a qualifier when any token is key:value.
func TestFilterQualified(t *testing.T) {
	for q, want := range map[string]bool{
		"":                 false,
		"crash":            false,
		"is:open":          true,
		"crash author:@me": true,
		`"a:b"`:            false,
	} {
		if got := github.ParseFilter(q).Qualified(); got != want {
			t.Errorf("ParseFilter(%q).Qualified() = %v, want %v", q, got, want)
		}
	}
}
