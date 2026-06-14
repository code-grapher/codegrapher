package coverage

import "sort"

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
