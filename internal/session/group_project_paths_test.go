package session

import (
	"slices"
	"testing"
)

func TestGroupProjectPaths(t *testing.T) {
	instances := []*Instance{
		{ID: "a", GroupPath: "work", ProjectPath: "/src/widgets", AdditionalPaths: []string{"/src/extra"}},
		{ID: "b", GroupPath: "work/child", ProjectPath: "/src/child"},
		{ID: "c", GroupPath: "work", ProjectPath: "/wt/widgets-feature", WorktreePath: "/wt/widgets-feature", WorktreeRepoRoot: "/src/widgets"},
		{ID: "d", GroupPath: "work", ProjectPath: "/src/widgets"},
		{ID: "e", GroupPath: "other", ProjectPath: "/src/other"},
		{ID: "f", GroupPath: "work", ProjectPath: ""},
		nil,
		{ID: "g", GroupPath: "work", ProjectPath: "/src/gadgets"},
	}
	got := GroupProjectPaths("work", instances)
	// ProjectPath only (never AdditionalPaths), direct members only (no
	// child group), worktrees by their main repo, each path once, in order.
	want := []string{"/src/widgets", "/src/gadgets"}
	if !slices.Equal(got, want) {
		t.Fatalf("GroupProjectPaths = %v, want %v", got, want)
	}
	if got := GroupProjectPaths("empty", instances); len(got) != 0 {
		t.Fatalf("GroupProjectPaths(empty) = %v, want none", got)
	}
}
