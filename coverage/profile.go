package coverage

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

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
	Blocks    []Block
}

// parseProfiles parses a Go coverprofile and returns one profileFile per file.
//
// A line is COVERED if any block covering it has Count>0, and UNCOVERED if it
// is measured (inside ≥1 block) but no covering block was hit. A line measured
// by multiple blocks resolves to covered when at least one of them ran — the
// covered set therefore wins over the uncovered set on overlap.
func parseProfiles(r io.Reader) ([]profileFile, string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("coverage: read profile: %w", err)
	}
	header := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	mode := strings.TrimSpace(strings.TrimPrefix(header, "mode:"))
	if !strings.HasPrefix(header, "mode:") || (mode != "set" && mode != "count" && mode != "atomic") {
		return nil, "", fmt.Errorf("coverage: invalid Go profile mode header %q", header)
	}
	profiles, err := cover.ParseProfilesFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("coverage: parse profile: %w", err)
	}
	out := make([]profileFile, 0, len(profiles))
	for _, p := range profiles {
		if p.Mode != mode {
			return nil, "", fmt.Errorf("coverage: mixed profile modes %q and %q", mode, p.Mode)
		}
		pf := profileFile{
			Name:      p.FileName,
			Mode:      p.Mode,
			Covered:   map[int]bool{},
			Uncovered: map[int]bool{},
		}
		for _, b := range p.Blocks {
			hit := b.Count > 0
			pf.Blocks = append(pf.Blocks, Block{StartLine: b.StartLine, StartCol: b.StartCol, EndLine: b.EndLine, EndCol: b.EndCol, NumStmt: b.NumStmt, Hit: hit})
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

// mergeProfiles combines focused runs only when their instrumentation layout
// agrees. It never adds counts: a hit in any run makes the exact block hit.
func mergeProfiles(inputs ...io.Reader) ([]profileFile, string, error) {
	if len(inputs) == 0 {
		return nil, "", fmt.Errorf("coverage: no profiles supplied")
	}
	byName := map[string]profileFile{}
	var mode string
	for _, input := range inputs {
		files, gotMode, err := parseProfiles(input)
		if err != nil {
			return nil, "", err
		}
		if gotMode == "" {
			return nil, "", fmt.Errorf("coverage: empty profile")
		}
		if mode != "" && mode != gotMode {
			return nil, "", fmt.Errorf("coverage: incompatible profile modes %q and %q", mode, gotMode)
		}
		mode = gotMode
		for _, pf := range files {
			old, ok := byName[pf.Name]
			if !ok {
				byName[pf.Name] = pf
				continue
			}
			if err := mergeBlocks(old.Blocks, pf.Blocks); err != nil {
				return nil, "", fmt.Errorf("coverage: %s: %w", pf.Name, err)
			}
			old.Covered, old.Uncovered = lineSets(old.Blocks)
			byName[pf.Name] = old
		}
	}
	out := make([]profileFile, 0, len(byName))
	for _, pf := range byName {
		out = append(out, pf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, mode, nil
}

func mergeBlocks(dst, src []Block) error {
	if len(dst) != len(src) {
		return fmt.Errorf("block layout differs (%d versus %d blocks); regenerate profiles from one unchanged checkout", len(dst), len(src))
	}
	for i := range dst {
		a, b := dst[i], src[i]
		if a.StartLine != b.StartLine || a.StartCol != b.StartCol || a.EndLine != b.EndLine || a.EndCol != b.EndCol || a.NumStmt != b.NumStmt {
			return fmt.Errorf("block layout differs at block %d; regenerate profiles from one unchanged checkout", i+1)
		}
		dst[i].Hit = a.Hit || b.Hit
	}
	return nil
}

func lineSets(blocks []Block) (covered, uncovered map[int]bool) {
	covered, uncovered = map[int]bool{}, map[int]bool{}
	for _, b := range blocks {
		for ln := b.StartLine; ln <= b.EndLine; ln++ {
			if b.Hit {
				covered[ln] = true
			} else {
				uncovered[ln] = true
			}
		}
	}
	for ln := range covered {
		delete(uncovered, ln)
	}
	return
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
