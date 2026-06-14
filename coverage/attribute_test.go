package coverage

import (
	"sort"
	"testing"

	"github.com/specscore/codegrapher/model"
)

func fn(id string, start, end int) model.Node {
	return model.Node{ID: id, Kind: model.KindFunction, StartLine: start, EndLine: end}
}

func lineSet(lines ...int) map[int]bool {
	m := map[int]bool{}
	for _, l := range lines {
		m[l] = true
	}
	return m
}

func countsByID(cs []nodeLineCount) map[string]nodeLineCount {
	m := map[string]nodeLineCount{}
	for _, c := range cs {
		m[c.NodeID] = c
	}
	return m
}

// TestAttribute_NestedClosureExcludesChild verifies the parent function does not
// count lines that fall inside a nested function's span (non-overlapping
// innermost attribution).
func TestAttribute_NestedClosureExcludesChild(t *testing.T) {
	// parent spans 10..30; nested child spans 15..20.
	nodes := []model.Node{
		fn("parent", 10, 30),
		fn("child", 15, 20),
	}
	covered := lineSet(11, 12, 16, 17) // 11,12 -> parent; 16,17 -> child
	uncovered := lineSet(13, 18)       // 13 -> parent; 18 -> child

	got := countsByID(attributeLines(nodes, covered, uncovered))

	parent := got["parent"]
	if parent.Covered != 2 || parent.Uncovered != 1 {
		t.Errorf("parent = %+v, want covered 2 uncovered 1 (child lines excluded)", parent)
	}
	child := got["child"]
	if child.Covered != 2 || child.Uncovered != 1 {
		t.Errorf("child = %+v, want covered 2 uncovered 1", child)
	}
}

// TestAttribute_FileLevelLinesUnattributed verifies lines in no function node
// are not attributed to any node.
func TestAttribute_FileLevelLinesUnattributed(t *testing.T) {
	nodes := []model.Node{fn("f", 5, 8)}
	covered := lineSet(1, 6) // line 1 is file-level (var decl), 6 inside f
	got := countsByID(attributeLines(nodes, covered, nil))
	if len(got) != 1 || got["f"].Covered != 1 {
		t.Errorf("expected only f with 1 covered, got %+v", got)
	}
}

func TestAttribute_NoFunctionNodes(t *testing.T) {
	nodes := []model.Node{{ID: "t", Kind: model.KindStruct, StartLine: 1, EndLine: 10}}
	if got := attributeLines(nodes, lineSet(2, 3), nil); got != nil {
		t.Errorf("non-function nodes should yield no attribution, got %+v", got)
	}
}

func TestAttribute_MethodKindAttributed(t *testing.T) {
	nodes := []model.Node{{ID: "m", Kind: model.KindMethod, StartLine: 1, EndLine: 5}}
	got := countsByID(attributeLines(nodes, lineSet(2), lineSet(3)))
	if got["m"].Covered != 1 || got["m"].Uncovered != 1 {
		t.Errorf("method attribution wrong: %+v", got["m"])
	}
}

func TestEncodeRanges_RoundTrip(t *testing.T) {
	covered := lineSet(1, 2, 3, 10)
	uncovered := lineSet(4, 5, 11)
	ranges := encodeRanges(covered, uncovered)

	// Expect: [1-3 hit][4-5 miss][10 hit][11 miss]
	want := []Range{
		{1, 3, KindHit}, {4, 5, KindMiss}, {10, 10, KindHit}, {11, 11, KindMiss},
	}
	if len(ranges) != len(want) {
		t.Fatalf("ranges = %+v, want %+v", ranges, want)
	}
	for i := range want {
		if ranges[i] != want[i] {
			t.Errorf("range[%d] = %+v, want %+v", i, ranges[i], want[i])
		}
	}

	// Round-trip is lossless.
	gotCov, gotUnc := decodeRanges(ranges)
	if !eqSet(gotCov, covered) || !eqSet(gotUnc, uncovered) {
		t.Errorf("round-trip lost data: cov=%v unc=%v", sortKeys(gotCov), sortKeys(gotUnc))
	}
}

func TestEncodeRanges_Empty(t *testing.T) {
	if r := encodeRanges(nil, nil); len(r) != 0 {
		t.Errorf("empty input should yield no ranges, got %+v", r)
	}
}

func eqSet(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sortKeys(m map[int]bool) []int {
	ks := make([]int, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	return ks
}
