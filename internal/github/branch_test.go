package github_test

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
)

// TestIssueBranch: {branch_prefix}{number}-{slug}. The slug is the
// lowercased ASCII title with every run of other characters turned into one
// "-", cut to 40 characters on a word boundary (spec #18 §13).
func TestIssueBranch(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		number int
		title  string
		want   string
	}{
		{"plain", "feature/", 123, "Fix the login bug", "feature/123-fix-the-login-bug"},
		{"no prefix", "", 7, "Add docs", "7-add-docs"},
		{"punctuation runs", "feature/", 9, "  crash: on `n` -- when (empty)!  ", "feature/9-crash-on-n-when-empty"},
		{"non-ascii is a separator", "feature/", 4, "Café über naïve", "feature/4-caf-ber-na-ve"},
		{"digits kept", "fix/", 12, "Bump Go to 1.26.10", "fix/12-bump-go-to-1-26-10"},
		{
			"cut on a word boundary",
			"feature/", 35,
			"Launch a session from an Issue or PR with templates and worktrees",
			// 40 chars would end inside "with" ("...-pr-wit"); the cut backs up to "pr".
			"feature/35-launch-a-session-from-an-issue-or-pr",
		},
		{
			"one long word is cut hard",
			"", 1,
			"Pneumonoultramicroscopicsilicovolcanoconiosis",
			"1-pneumonoultramicroscopicsilicovolcanocon",
		},
		{"exactly forty", "", 2, "abcdefghij abcdefghij abcdefghij abcdefg", "2-abcdefghij-abcdefghij-abcdefghij-abcdefg"},
		{"no usable characters", "feature/", 5, "🚀 ✨", "feature/5"},
		{"empty title", "feature/", 6, "", "feature/6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := github.IssueBranch(tt.prefix, tt.number, tt.title); got != tt.want {
				t.Fatalf("IssueBranch(%q, %d, %q) = %q, want %q", tt.prefix, tt.number, tt.title, got, tt.want)
			}
		})
	}
}

// TestSlugLength: no slug is longer than 40 characters or ends in "-".
func TestSlugLength(t *testing.T) {
	for _, title := range []string{
		"a b c d e f g h i j k l m n o p q r s t u v w x y z a b c d e f",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"word-word-word-word-word-word-word-word-word-word",
	} {
		s := github.Slug(title)
		if len(s) > 40 || s == "" || s[len(s)-1] == '-' {
			t.Fatalf("Slug(%q) = %q (%d chars)", title, s, len(s))
		}
	}
}
