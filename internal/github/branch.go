package github

import (
	"strconv"
	"strings"
)

// SlugMax is the longest branch slug Slug returns.
const SlugMax = 40

// Slug turns an Issue title into a branch slug (spec #18 §13): the
// lowercased ASCII letters and digits of title, every run of anything else
// turned into one "-", cut to SlugMax characters on a word boundary (a
// single word longer than that is cut hard). It is "" when title has no
// ASCII letter or digit.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	s := b.String()
	if len(s) <= SlugMax {
		return s
	}
	if s[SlugMax] == '-' {
		return s[:SlugMax]
	}
	if i := strings.LastIndexByte(s[:SlugMax], '-'); i > 0 {
		return s[:i]
	}
	return s[:SlugMax]
}

// IssueBranch is the branch a launch from Issue number creates or reuses:
// {prefix}{number}-{slug}, or {prefix}{number} when the title gives no slug.
func IssueBranch(prefix string, number int, title string) string {
	branch := prefix + strconv.Itoa(number)
	if s := Slug(title); s != "" {
		branch += "-" + s
	}
	return branch
}
