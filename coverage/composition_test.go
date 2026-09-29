package coverage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/model"
)

func TestIngestRejectsOldProfileAndStaleIndex(t *testing.T) {
	s, root := newIngestStore(t, "example.com/m")
	source := []byte("package m\nfunc F() {}\n")
	path := filepath.Join(root, "f.go")
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(source)
	if err := s.UpsertFile(model.FileRecord{Path: "f.go", ContentHash: hex.EncodeToString(hash[:]), Language: model.LangGo}); err != nil {
		t.Fatal(err)
	}
	profile := "mode: set\nexample.com/m/f.go:2.1,2.12 1 1\n"
	if _, err := NewIngestor().Ingest(context.Background(), s, strings.NewReader(profile), Options{Root: root, ProfileModifiedAt: 1}); err == nil {
		t.Fatal("accepted profile older than source")
	}
	if err := os.WriteFile(path, []byte("package m\nfunc F() { panic(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewIngestor().Ingest(context.Background(), s, strings.NewReader(profile), Options{Root: root}); err == nil {
		t.Fatal("accepted index for other source")
	}
}

func TestMergeProfilesExactBlocksAndPartialLine(t *testing.T) {
	a := "mode: set\nexample.com/m/f.go:2.1,2.8 2 0\nexample.com/m/f.go:2.9,2.20 3 0\n"
	b := "mode: set\nexample.com/m/f.go:2.1,2.8 2 1\nexample.com/m/f.go:2.9,2.20 3 0\n"
	files, mode, err := mergeProfiles(strings.NewReader(a), strings.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if mode != "set" || len(files) != 1 {
		t.Fatalf("mode=%s files=%d", mode, len(files))
	}
	if cov, unc := statementTotals(files[0].Blocks); cov != 2 || unc != 3 {
		t.Fatalf("statements %d/%d", cov, unc)
	}
	if got := LineStates(files[0].Blocks)[2]; got != KindPartial {
		t.Fatalf("line 2 = %q", got)
	}
}

func TestMergeProfilesRejectsModeAndLayout(t *testing.T) {
	a := "mode: set\nexample.com/m/f.go:2.1,2.8 2 1\n"
	for _, b := range []string{
		"mode: count\nexample.com/m/f.go:2.1,2.8 2 1\n",
		"mode: set\nexample.com/m/f.go:2.1,2.9 2 1\n",
		"mode: set\nexample.com/m/f.go:2.1,2.8 3 1\n",
	} {
		if _, _, err := mergeProfiles(strings.NewReader(a), strings.NewReader(b)); err == nil {
			t.Fatalf("accepted incompatible profile %q", b)
		}
	}
}

func TestMergeProfilesAllowsHeaderOnlyContribution(t *testing.T) {
	profile := "mode: set\nexample.com/m/f.go:2.1,2.8 1 1\n"
	files, mode, err := mergeProfiles(strings.NewReader("mode: set\n"), strings.NewReader(profile))
	if err != nil || mode != "set" || len(files) != 1 {
		t.Fatalf("empty contribution: mode=%q files=%+v err=%v", mode, files, err)
	}
	if _, _, err := mergeProfiles(strings.NewReader("mode: count\n"), strings.NewReader(profile)); err == nil {
		t.Fatal("accepted incompatible empty profile mode")
	}
}

func TestIngestMergeRefAndStaleSource(t *testing.T) {
	s, root := newIngestStore(t, "example.com/m")
	if err := s.UpsertFile(model.FileRecord{Path: "f.go", ContentHash: "old", Language: model.LangGo}); err != nil {
		t.Fatal(err)
	}
	a := "mode: set\nexample.com/m/f.go:2.1,2.8 2 0\n"
	b := "mode: set\nexample.com/m/f.go:2.1,2.8 2 1\n"
	ing := NewIngestor()
	if _, err := ing.Ingest(context.Background(), s, strings.NewReader(a), Options{Root: root, Ref: "A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ing.Ingest(context.Background(), s, strings.NewReader(b), Options{Root: root, Ref: "B", Merge: true}); err == nil {
		t.Fatal("accepted different ref")
	}
	if _, err := ing.Ingest(context.Background(), s, strings.NewReader(b), Options{Root: root, Ref: "A", Merge: true}); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetCoverageByFile("f.go")
	if err != nil {
		t.Fatal(err)
	}
	if row.StatementsCovered != 2 || row.StatementsUncovered != 0 || row.Ref != "A" {
		t.Fatalf("merged row %+v", row)
	}
	if err := s.UpsertFile(model.FileRecord{Path: "f.go", ContentHash: "new", Language: model.LangGo}); err != nil {
		t.Fatal(err)
	}
	if _, err := ing.Ingest(context.Background(), s, strings.NewReader(b), Options{Root: root, Ref: "A", Merge: true}); err == nil {
		t.Fatal("accepted stale stored coverage")
	}
}

func TestIngestManyComposesBeforeWriting(t *testing.T) {
	s, root := newIngestStore(t, "example.com/m")
	if err := s.UpsertFile(model.FileRecord{Path: "f.go", ContentHash: "h", Language: model.LangGo}); err != nil {
		t.Fatal(err)
	}
	a := "mode: set\nexample.com/m/f.go:2.1,2.8 2 0\n"
	b := "mode: set\nexample.com/m/f.go:2.1,2.8 2 1\n"
	sum, err := NewIngestor().IngestMany(context.Background(), s, []io.Reader{strings.NewReader(a), strings.NewReader(b)}, Options{Root: root, Ref: "batch"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.StatementsCovered != 2 || sum.StatementsUncovered != 0 {
		t.Fatalf("summary %+v", sum)
	}
	r, err := s.GetCoverageByFile("f.go")
	if err != nil || r == nil || r.StatementsCovered != 2 || r.Ref != "batch" {
		t.Fatalf("row %+v: %v", r, err)
	}
}

func TestIngestPersistsBothSameLineFunctionOwners(t *testing.T) {
	s, root := newIngestStore(t, "example.com/m")
	if err := s.UpsertFile(model.FileRecord{Path: "f.go", ContentHash: "h", Language: model.LangGo}); err != nil {
		t.Fatal(err)
	}
	nodes := []model.Node{
		{ID: "a", Kind: model.KindFunction, Name: "A", QualifiedName: "A", FilePath: "f.go", Language: model.LangGo, StartLine: 2, EndLine: 2, StartColumn: 0, EndColumn: 20},
		{ID: "b", Kind: model.KindFunction, Name: "B", QualifiedName: "B", FilePath: "f.go", Language: model.LangGo, StartLine: 2, EndLine: 2, StartColumn: 22, EndColumn: 42},
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	profile := "mode: set\nexample.com/m/f.go:2.5,2.15 1 1\nexample.com/m/f.go:2.28,2.38 1 0\n"
	if _, err := NewIngestor().Ingest(context.Background(), s, strings.NewReader(profile), Options{Root: root}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.GetAllNodeCoverage()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("missing same-line row: %+v", rows)
	}
	if rows[0].StatementsCovered != 1 || rows[1].StatementsUncovered != 1 {
		t.Fatalf("wrong same-line statement owners: %+v", rows)
	}
}
