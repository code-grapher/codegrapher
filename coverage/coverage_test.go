package coverage

import (
	"bytes"
	"strings"
	"testing"
)

func TestPct(t *testing.T) {
	cases := []struct {
		cov, unc int
		want     float64
	}{
		{0, 0, 0},
		{3, 1, 75},
		{1, 0, 100},
		{0, 5, 0},
	}
	for _, c := range cases {
		if got := Pct(c.cov, c.unc); got != c.want {
			t.Errorf("Pct(%d,%d)=%v want %v", c.cov, c.unc, got, c.want)
		}
	}
}

func TestFileCoverageRoundTrip(t *testing.T) {
	in := []FileCoverage{
		{
			FilePath:    "b/file.go",
			ContentHash: "hash-b",
			Mode:        "set",
			Ranges:      []Range{{Start: 1, End: 3, Kind: KindHit}, {Start: 4, End: 4, Kind: KindMiss}},
			// pct is derived; counts drive it
			LinesCovered:   3,
			LinesUncovered: 1,
			RunAt:          123,
		},
		{FilePath: "a/file.go", ContentHash: "hash-a", Mode: "set", LinesCovered: 0, LinesUncovered: 0, RunAt: 123},
	}
	var buf bytes.Buffer
	if err := EncodeFileCoverage(&buf, in); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := Validate(RecordsetCoverage, buf.Bytes()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	out, err := DecodeFileCoverage(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d records, want 2", len(out))
	}
	// sorted by path: a/file.go first
	if out[0].FilePath != "a/file.go" || out[1].FilePath != "b/file.go" {
		t.Fatalf("not sorted by path: %v", []string{out[0].FilePath, out[1].FilePath})
	}
	b := out[1]
	if b.LinesCovered != 3 || b.LinesUncovered != 1 || b.PctCovered != 75 {
		t.Errorf("b counts: cov=%d unc=%d pct=%v", b.LinesCovered, b.LinesUncovered, b.PctCovered)
	}
	if len(b.Ranges) != 2 || b.Ranges[0].Kind != KindHit || b.Ranges[1].Kind != KindMiss {
		t.Errorf("ranges round-trip wrong: %+v", b.Ranges)
	}
	if b.ContentHash != "hash-b" || b.Mode != "set" || b.RunAt != 123 {
		t.Errorf("scalar fields wrong: %+v", b)
	}
}

func TestNodeCoverageRoundTrip(t *testing.T) {
	in := []NodeCoverage{
		{NodeID: "n2", ContentHash: "h", LinesCovered: 2, LinesUncovered: 2, RunAt: 9},
		{NodeID: "n1", ContentHash: "h", LinesCovered: 5, LinesUncovered: 0, RunAt: 9},
	}
	var buf bytes.Buffer
	if err := EncodeNodeCoverage(&buf, in); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := Validate(RecordsetNodeCoverage, buf.Bytes()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	out, err := DecodeNodeCoverage(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 || out[0].NodeID != "n1" || out[1].NodeID != "n2" {
		t.Fatalf("not sorted by id: %+v", out)
	}
	if out[0].PctCovered != 100 || out[1].PctCovered != 50 {
		t.Errorf("pct wrong: %v %v", out[0].PctCovered, out[1].PctCovered)
	}
}

func TestValidateRejectsUnknownRecordset(t *testing.T) {
	if err := Validate("bogus", []byte("x")); err == nil {
		t.Fatal("expected error for unknown recordset")
	}
}

func TestValidateRejectsBadRangeKind(t *testing.T) {
	bad := []FileCoverage{{FilePath: "f", ContentHash: "h", Mode: "set",
		Ranges: []Range{{Start: 1, End: 2, Kind: "weird"}}, LinesCovered: 2}}
	var buf bytes.Buffer
	if err := EncodeFileCoverage(&buf, bad); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := Validate(RecordsetCoverage, buf.Bytes()); err == nil || !strings.Contains(err.Error(), "range kind") {
		t.Fatalf("expected bad range kind error, got %v", err)
	}
}
