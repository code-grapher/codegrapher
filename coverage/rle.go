package coverage

import "sort"

// LineStates is a navigation aid derived from exact blocks. A line touched
// by both hit and missed blocks is partial; absent lines are unmeasured.
func LineStates(blocks []Block) map[int]string {
	type hits struct{ hit, miss bool }
	seen := map[int]hits{}
	for _, b := range blocks {
		for ln := b.StartLine; ln <= b.EndLine; ln++ {
			if ln == b.EndLine && b.EndCol <= 1 && ln != b.StartLine {
				continue
			}
			h := seen[ln]
			if b.Hit {
				h.hit = true
			} else {
				h.miss = true
			}
			seen[ln] = h
		}
	}
	out := map[int]string{}
	for ln, h := range seen {
		switch {
		case h.hit && h.miss:
			out[ln] = KindPartial
		case h.hit:
			out[ln] = KindHit
		default:
			out[ln] = KindMiss
		}
	}
	return out
}

func rangesFromStates(states map[int]string) []Range {
	lines := make([]int, 0, len(states))
	for ln := range states {
		lines = append(lines, ln)
	}
	sort.Ints(lines)
	var out []Range
	for _, ln := range lines {
		kind := states[ln]
		if n := len(out); n > 0 && out[n-1].Kind == kind && out[n-1].End == ln-1 {
			out[n-1].End = ln
		} else {
			out = append(out, Range{Start: ln, End: ln, Kind: kind})
		}
	}
	return out
}

// encodeRanges run-length-encodes covered and uncovered line sets into a single
// ascending []Range. Adjacent lines sharing a state collapse into one Range;
// "hit" and "miss" runs are emitted in line order. Lines absent from both sets
// (blank lines, comments, declarations the compiler did not instrument) produce
// no Range and break a run. The two input sets are assumed disjoint
// (parseProfiles guarantees this); on overlap, covered wins.
func encodeRanges(covered, uncovered map[int]bool) []Range {
	kindByLine := make(map[int]string, len(covered)+len(uncovered))
	for ln := range uncovered {
		kindByLine[ln] = KindMiss
	}
	for ln := range covered {
		kindByLine[ln] = KindHit // covered wins on overlap
	}
	lines := make([]int, 0, len(kindByLine))
	for ln := range kindByLine {
		lines = append(lines, ln)
	}
	sort.Ints(lines)

	var ranges []Range
	for _, ln := range lines {
		kind := kindByLine[ln]
		if n := len(ranges); n > 0 && ranges[n-1].Kind == kind && ranges[n-1].End == ln-1 {
			ranges[n-1].End = ln
			continue
		}
		ranges = append(ranges, Range{Start: ln, End: ln, Kind: kind})
	}
	return ranges
}

// decodeRanges expands a []Range back into covered/uncovered line sets — the
// inverse of encodeRanges, used in round-trip tests and by any consumer that
// needs per-line state from a stored recordset.
func decodeRanges(ranges []Range) (covered, uncovered map[int]bool) {
	covered, uncovered = map[int]bool{}, map[int]bool{}
	for _, r := range ranges {
		for ln := r.Start; ln <= r.End; ln++ {
			switch r.Kind {
			case KindHit:
				covered[ln] = true
			case KindMiss:
				uncovered[ln] = true
			}
		}
	}
	return covered, uncovered
}
