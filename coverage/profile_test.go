package coverage

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseProfiles(t *testing.T) {
	tests := []struct {
		name          string
		profile       string
		wantMode      string
		file          string
		wantCovered   []int
		wantUncovered []int
	}{
		{
			name: "set mode hit and miss",
			profile: "mode: set\n" +
				"ex.com/m/a.go:1.1,3.10 2 1\n" + // lines 1-3 hit
				"ex.com/m/a.go:5.1,6.10 1 0\n", // lines 5-6 miss
			wantMode:      "set",
			file:          "ex.com/m/a.go",
			wantCovered:   []int{1, 2, 3},
			wantUncovered: []int{5, 6},
		},
		{
			name: "count mode positive count is covered",
			profile: "mode: count\n" +
				"ex.com/m/a.go:1.1,1.10 1 7\n" +
				"ex.com/m/a.go:2.1,2.10 1 0\n",
			wantMode:      "count",
			file:          "ex.com/m/a.go",
			wantCovered:   []int{1},
			wantUncovered: []int{2},
		},
		{
			name: "atomic mode",
			profile: "mode: atomic\n" +
				"ex.com/m/a.go:10.1,12.5 3 4\n",
			wantMode:      "atomic",
			file:          "ex.com/m/a.go",
			wantCovered:   []int{10, 11, 12},
			wantUncovered: nil,
		},
		{
			name: "multi-block line: covered wins over miss",
			profile: "mode: count\n" +
				"ex.com/m/a.go:5.1,5.8 1 0\n" + // line 5 miss
				"ex.com/m/a.go:5.9,5.20 1 3\n", // line 5 also hit
			wantMode:      "count",
			file:          "ex.com/m/a.go",
			wantCovered:   []int{5},
			wantUncovered: nil,
		},
		{
			name: "zero-count only is uncovered",
			profile: "mode: set\n" +
				"ex.com/m/a.go:1.1,2.2 1 0\n",
			wantMode:      "set",
			file:          "ex.com/m/a.go",
			wantCovered:   nil,
			wantUncovered: []int{1, 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, mode, err := parseProfiles(strings.NewReader(tt.profile))
			if err != nil {
				t.Fatalf("parseProfiles: %v", err)
			}
			if mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", mode, tt.wantMode)
			}
			if len(files) != 1 || files[0].Name != tt.file {
				t.Fatalf("files = %+v, want one file %q", files, tt.file)
			}
			gotCov := sortedLines(files[0].Covered)
			gotUnc := sortedLines(files[0].Uncovered)
			if !eqInts(gotCov, tt.wantCovered) {
				t.Errorf("covered = %v, want %v", gotCov, tt.wantCovered)
			}
			if !eqInts(gotUnc, tt.wantUncovered) {
				t.Errorf("uncovered = %v, want %v", gotUnc, tt.wantUncovered)
			}
		})
	}
}

func TestParseProfiles_MultipleFilesSorted(t *testing.T) {
	profile := "mode: set\n" +
		"ex.com/m/z.go:1.1,1.5 1 1\n" +
		"ex.com/m/a.go:1.1,1.5 1 1\n"
	files, _, err := parseProfiles(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name != "ex.com/m/a.go" || files[1].Name != "ex.com/m/z.go" {
		t.Errorf("files not sorted by name: %+v", files)
	}
}

func eqInts(a, b []int) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
