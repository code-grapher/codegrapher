package coverage

import (
	"fmt"
	"io"
	"sort"

	"golang.org/x/tools/cover"
)

// profileFile is one file's parsed coverage: the go module-relative file name
// straight from the profile (e.g. "example.com/m/pkg/f.go"), the profile mode,
// and the per-line covered/uncovered sets.
type profileFile struct {
	Name      string       // profile file name (module path), verbatim
	Mode      string       // set | count | atomic
	Covered   map[int]bool // line -> true (covered by ≥1 block with count>0)
	Uncovered map[int]bool // line -> true (measured but no covering block hit)
}

// parseProfiles parses a Go coverprofile and returns one profileFile per file.
//
// A line is COVERED if any block covering it has Count>0, and UNCOVERED if it
// is measured (inside ≥1 block) but no covering block was hit. A line measured
// by multiple blocks resolves to covered when at least one of them ran — the
// covered set therefore wins over the uncovered set on overlap.
func parseProfiles(r io.Reader) ([]profileFile, string, error) {
	profiles, err := cover.ParseProfilesFromReader(r)
	if err != nil {
		return nil, "", fmt.Errorf("coverage: parse profile: %w", err)
	}
	out := make([]profileFile, 0, len(profiles))
	mode := ""
	for _, p := range profiles {
		if mode == "" {
			mode = p.Mode
		}
		pf := profileFile{
			Name:      p.FileName,
			Mode:      p.Mode,
			Covered:   map[int]bool{},
			Uncovered: map[int]bool{},
		}
		for _, b := range p.Blocks {
			hit := b.Count > 0
			for ln := b.StartLine; ln <= b.EndLine; ln++ {
				if ln <= 0 {
					continue
				}
				if hit {
					pf.Covered[ln] = true
				} else if !pf.Covered[ln] {
					pf.Uncovered[ln] = true
				}
			}
		}
		// A line covered by one block and missed by another is covered: drop it
		// from the uncovered set so the two sets are disjoint.
		for ln := range pf.Covered {
			delete(pf.Uncovered, ln)
		}
		out = append(out, pf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, mode, nil
}

// sortedLines returns the keys of a line set in ascending order.
func sortedLines(set map[int]bool) []int {
	lines := make([]int, 0, len(set))
	for ln := range set {
		lines = append(lines, ln)
	}
	sort.Ints(lines)
	return lines
}
