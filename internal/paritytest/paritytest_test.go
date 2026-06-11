package paritytest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func goldenPath(t *testing.T, parts ...string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(append([]string{root, "testdata", "golden"}, parts...)...)
}

func TestDiff_GoldenAgainstItself(t *testing.T) {
	// Every golden file must be equivalent to itself after canonicalization —
	// proves normalization is total and stable on real original-CLI output.
	files := []struct {
		path      string
		unordered bool
	}{
		{goldenPath(t, "go-small", "status.json"), false},
		{goldenPath(t, "go-small", "query.json"), false},
		{goldenPath(t, "go-small", "files.json"), true},
		{goldenPath(t, "go-small", "callers-Get.json"), false},
		{goldenPath(t, "ts-small", "callees-Cache::lookup.json"), false},
		{goldenPath(t, "ts-small", "impact-describe.json"), false},
	}
	for _, f := range files {
		raw, err := readFile(f.path)
		if err != nil {
			t.Fatalf("read %s: %v", f.path, err)
		}
		diff, err := Diff(f.path, raw, f.unordered)
		if err != nil {
			t.Errorf("%s: %v", f.path, err)
		}
		if diff != "" {
			t.Errorf("%s: golden differs from itself:\n%s", f.path, diff)
		}
	}
}

func TestDiff_NormalizesMachineFields(t *testing.T) {
	golden := []byte(`{"projectPath":"/home/a/x","nodeCount":3,"dbSizeBytes":111}`)
	got := []byte(`{"projectPath":"/ci/other","nodeCount":3,"dbSizeBytes":999}`)
	cw, _ := Canonicalize(golden, false)
	cg, _ := Canonicalize(got, false)
	if string(cw) != string(cg) {
		t.Errorf("machine fields should normalize equal:\n%s\n%s", cw, cg)
	}
}

func TestDiff_DetectsRealDifferences(t *testing.T) {
	a, _ := Canonicalize([]byte(`{"symbol":"x","callers":[{"name":"a"}]}`), false)
	b, _ := Canonicalize([]byte(`{"symbol":"x","callers":[{"name":"B"}]}`), false)
	if string(a) == string(b) {
		t.Error("distinct caller sets must not normalize equal")
	}
}

func TestCanonicalize_SortsUnorderedArrays(t *testing.T) {
	a, _ := Canonicalize([]byte(`{"callers":[{"name":"b"},{"name":"a"}]}`), false)
	b, _ := Canonicalize([]byte(`{"callers":[{"name":"a"},{"name":"b"}]}`), false)
	if string(a) != string(b) {
		t.Errorf("callers order should be insignificant:\n%s\n%s", a, b)
	}
}

func TestCanonicalize_PreservesQueryOrder(t *testing.T) {
	a, _ := Canonicalize([]byte(`[{"score":2},{"score":1}]`), false)
	b, _ := Canonicalize([]byte(`[{"score":1},{"score":2}]`), false)
	if string(a) == string(b) {
		t.Error("top-level query order must be preserved (descending score is meaningful)")
	}
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func TestSortArray_ContentNotPositionOrder(t *testing.T) {
	// Regression: keys must follow items during sorting. With a detached key
	// slice, sort.SliceStable compares stale positions and the result depends
	// on input order — two permutations of the same set normalized unequal.
	perm1 := []byte(`{"affected":[{"name":"lookup"},{"name":"report"},{"name":"Cache"},{"name":"main"}]}`)
	perm2 := []byte(`{"affected":[{"name":"Cache"},{"name":"lookup"},{"name":"main"},{"name":"report"}]}`)
	a, err := Canonicalize(perm1, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Canonicalize(perm2, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("permutations of the same affected set must normalize equal:\n%s\n%s", a, b)
	}
	want := `{"affected":[{"name":"Cache"},{"name":"lookup"},{"name":"main"},{"name":"report"}]}`
	if string(a) != want {
		t.Errorf("expected canonical key order:\ngot  %s\nwant %s", a, want)
	}
}
