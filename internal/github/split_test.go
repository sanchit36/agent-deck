package github_test

import (
	"slices"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
)

// TestPairLines pins the split view's pairing (spec #18 §10): a context
// line sits on both sides; inside each change block (a run of removed and
// added lines) the i-th removed line pairs with the i-th added one, and
// the shorter side gets blank cells (-1).
func TestPairLines(t *testing.T) {
	ctx := func(s string) github.DiffLine { return github.DiffLine{Kind: github.LineContext, Text: s} }
	del := func(s string) github.DiffLine { return github.DiffLine{Kind: github.LineDel, Text: s} }
	add := func(s string) github.DiffLine { return github.DiffLine{Kind: github.LineAdd, Text: s} }
	type pair = github.LinePair
	for _, tc := range []struct {
		name  string
		lines []github.DiffLine
		want  []pair
	}{
		{"empty", nil, nil},
		{"context only", []github.DiffLine{ctx("a"), ctx("b")}, []pair{{Old: 0, New: 0}, {Old: 1, New: 1}}},
		{"added only", []github.DiffLine{add("a"), add("b")}, []pair{{Old: -1, New: 0}, {Old: -1, New: 1}}},
		{"removed only", []github.DiffLine{del("a")}, []pair{{Old: 0, New: -1}}},
		{"even block", []github.DiffLine{del("a"), del("b"), add("c"), add("d")},
			[]pair{{Old: 0, New: 2}, {Old: 1, New: 3}}},
		{"more removed", []github.DiffLine{del("a"), del("b"), del("c"), add("d")},
			[]pair{{Old: 0, New: 3}, {Old: 1, New: -1}, {Old: 2, New: -1}}},
		{"more added", []github.DiffLine{del("a"), add("b"), add("c")},
			[]pair{{Old: 0, New: 1}, {Old: -1, New: 2}}},
		{"context splits blocks", []github.DiffLine{ctx("x"), del("a"), add("b"), ctx("y"), add("c"), ctx("z")},
			[]pair{{Old: 0, New: 0}, {Old: 1, New: 2}, {Old: 3, New: 3}, {Old: -1, New: 4}, {Old: 5, New: 5}}},
		{"interleaved block keeps each side's order", []github.DiffLine{del("a"), add("b"), del("c"), add("d")},
			[]pair{{Old: 0, New: 1}, {Old: 2, New: 3}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := github.PairLines(tc.lines); !slices.Equal(got, tc.want) {
				t.Fatalf("PairLines = %v, want %v", got, tc.want)
			}
		})
	}
}
