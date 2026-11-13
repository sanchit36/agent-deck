package prompttemplate

import (
	"sort"
	"strings"
)

// Selection is the outcome of matching templates to one Issue or PR.
type Selection struct {
	// Chosen is the template to prefill, or "" to open the picker.
	Chosen string
	// Order is the picker list: label matches first, then the rest of the
	// templates that apply to this kind and Project. Within each group,
	// templates whose repos name the Project come first, then by name.
	Order []string
	// Matches is how many leading entries of Order matched by label.
	Matches int
}

// Select picks a template for an item of kind in project (owner/name) with
// labels. Labels match exactly, case-insensitive. One match is used; several
// open the picker; none fall back to the kind's default (a user default
// beats a built-in one, a repo-scoped default beats a global one, then the
// first by name), else the picker.
func Select(ts []Template, kind Kind, project string, labels []string) Selection {
	var matched, rest []Template
	for _, t := range ts {
		if t.Kind != kind || !appliesToProject(t, project) {
			continue
		}
		if labelsMatch(t.Labels, labels) {
			matched = append(matched, t)
		} else {
			rest = append(rest, t)
		}
	}
	rank := func(list []Template) {
		sort.SliceStable(list, func(i, j int) bool {
			si, sj := len(list[i].Repos) > 0, len(list[j].Repos) > 0
			if si != sj {
				return si
			}
			return list[i].Name < list[j].Name
		})
	}
	rank(matched)
	rank(rest)

	sel := Selection{Matches: len(matched)}
	for _, t := range append(append([]Template{}, matched...), rest...) {
		sel.Order = append(sel.Order, t.Name)
	}
	switch {
	case len(matched) == 1:
		sel.Chosen = matched[0].Name
	case len(matched) == 0:
		sel.Chosen = pickDefault(rest)
	}
	return sel
}

// pickDefault takes ranked templates and returns the winning default.
func pickDefault(ranked []Template) string {
	var builtin string
	for _, t := range ranked {
		if !t.Default {
			continue
		}
		if !t.Builtin {
			return t.Name
		}
		if builtin == "" {
			builtin = t.Name
		}
	}
	return builtin
}

func appliesToProject(t Template, project string) bool {
	if len(t.Repos) == 0 {
		return true
	}
	for _, r := range t.Repos {
		if strings.EqualFold(strings.TrimSpace(r), project) {
			return true
		}
	}
	return false
}

func labelsMatch(want, have []string) bool {
	for _, w := range want {
		for _, h := range have {
			if strings.EqualFold(strings.TrimSpace(w), strings.TrimSpace(h)) {
				return true
			}
		}
	}
	return false
}
