package prompttemplate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func issueVars() Vars {
	return Vars{
		Kind:   KindIssue,
		Number: 12,
		Title:  "Crash on start",
		Body:   "It crashes.",
		URL:    "https://github.com/acme/app/issues/12",
		Repo:   "acme/app",
		Labels: []string{"bug", "p1"},
		Author: "octocat",
	}
}

func prVars() Vars {
	return Vars{
		Kind:           KindPR,
		Number:         34,
		Title:          "Fix crash",
		Body:           "Fixes #12.",
		URL:            "https://github.com/acme/app/pull/34",
		Repo:           "acme/app",
		Labels:         []string{"bug"},
		Author:         "hubot",
		Comments:       "lgtm",
		HeadBranch:     "fix/crash",
		BaseBranch:     "main",
		ReviewComments: "main.go:10: nil check",
	}
}

func TestRender(t *testing.T) {
	tests := []struct {
		name string
		text string
		vars Vars
		want string
	}{
		{"shared variables", "#{number} {title} ({repo}) by {author} [{labels}] {url}\n{body}", issueVars(),
			"#12 Crash on start (acme/app) by octocat [bug, p1] https://github.com/acme/app/issues/12\nIt crashes."},
		{"pr variables", "{head-branch} -> {base-branch}: {review-comments} / {comments}", prVars(),
			"fix/crash -> main: main.go:10: nil check / lgtm"},
		{"pr variable in an issue template renders empty", "[{head-branch}][{review-comments}]", issueVars(), "[][]"},
		{"unknown variable stays literal", "{nope} {Title} { title} {}", issueVars(), "{nope} {Title} { title} {}"},
		{"unclosed brace stays literal", "see {title", issueVars(), "see {title"},
		{"brace runs", "{{title}}", issueVars(), "{Crash on start}"},
		{"substituted values are never re-expanded",
			"{title}|{body}",
			Vars{Kind: KindIssue, Number: 7, Repo: "a/b", Title: "{body}", Body: "{number} {title}"},
			"{body}|{number} {title}"},
		{"no labels", "[{labels}]", Vars{Kind: KindIssue}, "[]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Render(tt.text, tt.vars); got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderTruncatesLargeValuesOnARuneBoundary(t *testing.T) {
	big := strings.Repeat("é", MaxValueBytes) // 2 bytes each: twice the cap
	odd := "a" + big                          // shifts every rune boundary by one byte
	tests := []struct {
		name     string
		text     string
		vars     func() Vars
		wantHint string
	}{
		{"issue body", "{body}", func() Vars { v := issueVars(); v.Body = big; return v },
			"[truncated: run gh issue view 12 -R acme/app --comments]"},
		{"issue body, odd boundary", "{body}", func() Vars { v := issueVars(); v.Body = odd; return v },
			"[truncated: run gh issue view 12 -R acme/app --comments]"},
		{"issue comments", "{comments}", func() Vars { v := issueVars(); v.Comments = big; return v },
			"[truncated: run gh issue view 12 -R acme/app --comments]"},
		{"pr review comments", "{review-comments}", func() Vars { v := prVars(); v.ReviewComments = big; return v },
			"[truncated: run gh pr view 34 -R acme/app --comments]"},
		{"enterprise host is named in the hint", "{body}", func() Vars { v := prVars(); v.Host = "ghe.example.com"; v.Body = big; return v },
			"[truncated: run gh pr view 34 -R ghe.example.com/acme/app --comments]"},
		{"github.com host stays implicit", "{body}", func() Vars { v := prVars(); v.Host = "github.com"; v.Body = big; return v },
			"[truncated: run gh pr view 34 -R acme/app --comments]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Render(tt.text, tt.vars())
			if !strings.HasSuffix(got, "\n"+tt.wantHint) {
				tail := got
				if len(tail) > 80 {
					tail = tail[len(tail)-80:]
				}
				t.Fatalf("missing hint %q at the end of %q", tt.wantHint, tail)
			}
			kept := strings.TrimSuffix(got, "\n"+tt.wantHint)
			if !utf8.ValidString(kept) {
				t.Errorf("truncated value is not valid UTF-8")
			}
			if len(kept) > MaxValueBytes || len(kept) < MaxValueBytes-utf8.UTFMax {
				t.Errorf("kept %d bytes, want just under %d", len(kept), MaxValueBytes)
			}
		})
	}
}

func TestRenderKeepsValuesAtTheCap(t *testing.T) {
	v := issueVars()
	v.Body = strings.Repeat("a", MaxValueBytes)
	if got := Render("{body}", v); got != v.Body {
		t.Errorf("a body of exactly %d bytes must not be truncated", MaxValueBytes)
	}
}

func TestRenderDoesNotCapShortFields(t *testing.T) {
	v := issueVars()
	v.Title = strings.Repeat("t", MaxValueBytes+10)
	if got := Render("{title}", v); got != v.Title {
		t.Errorf("only body, comments and review-comments are capped")
	}
}

func TestUses(t *testing.T) {
	tests := []struct {
		text, name string
		want       bool
	}{
		{"x {comments} y", "comments", true},
		{"{body}", "comments", false},
		{"{review-comments}", "comments", false},
		{"{review-comments}", "review-comments", true},
	}
	for _, tt := range tests {
		if got := Uses(tt.text, tt.name); got != tt.want {
			t.Errorf("Uses(%q, %q) = %v, want %v", tt.text, tt.name, got, tt.want)
		}
	}
}
