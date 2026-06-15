package coverage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// newIngestStore builds a temp store with a go.mod root and returns the store
// plus the root path.
func newIngestStore(t *testing.T, module string) (*store.Store, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module "+module+"\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := store.Initialize(filepath.Join(root, ".codegraph", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, root
}

func TestIngest_FileAndNodeCounts(t *testing.T) {
	s, root := newIngestStore(t, "example.com/m")

	// One indexed file with two functions.
	if err := s.UpsertFile(model.FileRecord{
		Path: "pkg/f.go", ContentHash: "hash-f", Language: model.LangGo,
	}); err != nil {
		t.Fatal(err)
	}
	fooID := model.GenerateNodeID("pkg/f.go", model.KindFunction, "Foo", 1)
	barID := model.GenerateNodeID("pkg/f.go", model.KindFunction, "Bar", 10)
	if err := s.InsertNodes([]model.Node{
		{ID: fooID, Kind: model.KindFunction, Name: "Foo", QualifiedName: "Foo",
			FilePath: "pkg/f.go", Language: model.LangGo, StartLine: 1, EndLine: 5},
		{ID: barID, Kind: model.KindFunction, Name: "Bar", QualifiedName: "Bar",
			FilePath: "pkg/f.go", Language: model.LangGo, StartLine: 10, EndLine: 15},
	}); err != nil {
		t.Fatal(err)
	}

	profile := "mode: set\n" +
		"example.com/m/pkg/f.go:2.1,4.2 2 1\n" + // lines 2-4 hit -> Foo
		"example.com/m/pkg/f.go:11.1,12.2 1 0\n" // lines 11-12 miss -> Bar

	sum, err := NewIngestor().Ingest(context.Background(), s,
		strings.NewReader(profile), Options{Root: root, Now: func() int64 { return 777 }})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if sum.FilesMatched != 1 || sum.FilesSkipped != 0 {
		t.Errorf("summary match/skip = %d/%d, want 1/0", sum.FilesMatched, sum.FilesSkipped)
	}
	if sum.LinesCovered != 3 || sum.LinesUncovered != 2 {
		t.Errorf("summary lines = %d/%d, want 3/2", sum.LinesCovered, sum.LinesUncovered)
	}
	if sum.PctCovered != 60 {
		t.Errorf("summary pct = %v, want 60", sum.PctCovered)
	}

	cov, err := s.GetCoverageByFile("pkg/f.go")
	if err != nil || cov == nil {
		t.Fatalf("GetCoverageByFile: %v / %v", cov, err)
	}
	if cov.ContentHash != "hash-f" || cov.Mode != "set" || cov.RunAt != 777 {
		t.Errorf("file coverage stamp wrong: %+v", cov)
	}
	if cov.LinesCovered != 3 || cov.LinesUncovered != 2 || cov.PctCovered != 60 {
		t.Errorf("file coverage counts wrong: %+v", cov)
	}

	nodeCov, err := s.GetAllNodeCoverage()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]store.NodeCoverageRow{}
	for _, r := range nodeCov {
		got[r.NodeID] = r
	}
	if foo := got[fooID]; foo.LinesCovered != 3 || foo.LinesUncovered != 0 {
		t.Errorf("Foo node coverage = %+v, want 3/0", foo)
	}
	if bar := got[barID]; bar.LinesCovered != 0 || bar.LinesUncovered != 2 {
		t.Errorf("Bar node coverage = %+v, want 0/2", bar)
	}
}

func TestIngest_UnmatchedFileSkipped(t *testing.T) {
	s, root := newIngestStore(t, "example.com/m")
	// No files indexed.
	profile := "mode: set\nexample.com/m/ghost.go:1.1,1.5 1 1\n"
	sum, err := NewIngestor().Ingest(context.Background(), s,
		strings.NewReader(profile), Options{Root: root})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if sum.FilesMatched != 0 || sum.FilesSkipped != 1 {
		t.Errorf("summary = %+v, want 0 matched 1 skipped", sum)
	}
}

func TestResolveRepoPath(t *testing.T) {
	cases := []struct{ name, module, want string }{
		{"example.com/m/pkg/f.go", "example.com/m", "pkg/f.go"},
		{"example.com/m/f.go", "example.com/m", "f.go"},
		{"pkg/f.go", "", "pkg/f.go"},
		{"other.com/x/f.go", "example.com/m", "other.com/x/f.go"},
	}
	for _, c := range cases {
		if got := resolveRepoPath(c.name, c.module); got != c.want {
			t.Errorf("resolveRepoPath(%q,%q) = %q, want %q", c.name, c.module, got, c.want)
		}
	}
}
