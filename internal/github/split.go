package github

// LinePair is one row of a hunk's side-by-side (split) view: Old and New
// index the hunk's Lines shown on the old and new side, -1 for a blank
// cell. A context line is on both sides (Old == New).
type LinePair struct {
	Old, New int
}

// PairLines pairs a hunk's lines for the split view (spec §10): a context
// line sits on both sides, and inside each change block (a run of removed
// and added lines between context lines) the i-th removed line pairs with
// the i-th added one, the shorter side padded with blank cells. Each side
// keeps its own order. Pure; nil for no lines.
func PairLines(lines []DiffLine) []LinePair {
	var out []LinePair
	var dels, adds []int
	flush := func() {
		for i := 0; i < max(len(dels), len(adds)); i++ {
			p := LinePair{Old: -1, New: -1}
			if i < len(dels) {
				p.Old = dels[i]
			}
			if i < len(adds) {
				p.New = adds[i]
			}
			out = append(out, p)
		}
		dels, adds = dels[:0], adds[:0]
	}
	for i, l := range lines {
		switch l.Kind {
		case LineDel:
			dels = append(dels, i)
		case LineAdd:
			adds = append(adds, i)
		default:
			flush()
			out = append(out, LinePair{Old: i, New: i})
		}
	}
	flush()
	return out
}
