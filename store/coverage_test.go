package store

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
)

func TestCoverage_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	rows := []CoverageRow{
		{FilePath: "b.go", ContentHash: "h2", Mode: "set",
			Ranges:       `[{"start":1,"end":3,"kind":"hit"}]`,
			LinesCovered: 3, LinesUncovered: 1, PctCovered: 75, RunAt: 100},
		{FilePath: "a.go", ContentHash: "h1", Mode: "count",
			Ranges: `[]`, LinesCovered: 0, LinesUncovered: 0, PctCovered: 0, RunAt: 100},
	}
	if err := s.PutCoverage(rows); err != nil {
		t.Fatalf("PutCoverage: %v", err)
	}
	all, err := s.GetAllCoverage()
	if err != nil {
		t.Fatalf("GetAllCoverage: %v", err)
	}
	if len(all) != 2 || all[0].FilePath != "a.go" || all[1].FilePath != "b.go" {
		t.Fatalf("GetAllCoverage order/count wrong: %+v", all)
	}
	got, err := s.GetCoverageByFile("b.go")
	if err != nil || got == nil {
		t.Fatalf("GetCoverageByFile: %v / %v", got, err)
	}
	if got.ContentHash != "h2" || got.Mode != "set" || got.LinesCovered != 3 || got.PctCovered != 75 {
		t.Errorf("coverage round-trip mismatch: %+v", got)
	}
	if absent, _ := s.GetCoverageByFile("missing.go"); absent != nil {
		t.Errorf("GetCoverageByFile(missing) = %+v, want nil", absent)
	}

	// Replace semantics: re-put with new hash overwrites.
	rows[1].ContentHash = "h1b"
	if err := s.PutCoverage(rows[1:]); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.GetCoverageByFile("a.go")
	if got2.ContentHash != "h1b" {
		t.Errorf("PutCoverage did not replace: %+v", got2)
	}
}

func TestNodeCoverage_RoundTripAndCascade(t *testing.T) {
	s := newTestStore(t)
	if err := s.InsertNodes([]model.Node{
		testNode("function:a", "a", "a.go", 1),
		testNode("function:b", "b", "a.go", 10),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutNodeCoverage([]NodeCoverageRow{
		{NodeID: "function:a", ContentHash: "h", LinesCovered: 2, LinesUncovered: 1, PctCovered: 66.6, RunAt: 5},
		{NodeID: "function:b", ContentHash: "h", LinesCovered: 0, LinesUncovered: 4, PctCovered: 0, RunAt: 5},
	}); err != nil {
		t.Fatalf("PutNodeCoverage: %v", err)
	}
	all, err := s.GetAllNodeCoverage()
	if err != nil || len(all) != 2 {
		t.Fatalf("GetAllNodeCoverage = %d (%v)", len(all), err)
	}
	if all[0].NodeID != "function:a" || all[0].LinesCovered != 2 {
		t.Errorf("node coverage mismatch: %+v", all[0])
	}

	// node_coverage cascades when its node is deleted.
	if err := s.DeleteNodesByFile("a.go"); err != nil {
		t.Fatal(err)
	}
	if remaining, _ := s.GetAllNodeCoverage(); len(remaining) != 0 {
		t.Errorf("node_coverage should cascade on node delete, got %d", len(remaining))
	}
}

// TestMigrations_FromV5 simulates opening a v5 database (no coverage tables) and
// verifies migration v6 creates them cleanly and they are usable.
func TestMigrations_FromV5(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DatabaseFilename)

	s, err := Initialize(path, WithNowFunc(fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	// Downgrade to v5: drop the v6 coverage tables and reset the version.
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_node_coverage_hash`,
		`DROP TABLE IF EXISTS node_coverage`,
		`DROP TABLE IF EXISTS coverage`,
		`ALTER TABLE nodes DROP COLUMN metadata`,
		`DELETE FROM schema_versions WHERE version > 1`,
		`INSERT OR IGNORE INTO schema_versions (version, applied_at, description) VALUES (5, 0, 'v5')`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("downgrade %q: %v", stmt, err)
		}
	}
	s.Close()

	reopened, err := Open(path, WithNowFunc(fixedNow))
	if err != nil {
		t.Fatalf("Open with v6 migration: %v", err)
	}
	defer reopened.Close()
	v, _ := reopened.SchemaVersion()
	if v != CurrentSchemaVersion {
		t.Errorf("migrated version = %d, want %d", v, CurrentSchemaVersion)
	}
	// Coverage tables usable after migration.
	if err := reopened.PutCoverage([]CoverageRow{
		{FilePath: "x.go", ContentHash: "h", Mode: "set", Ranges: "[]", RunAt: 1},
	}); err != nil {
		t.Errorf("coverage table after migration: %v", err)
	}
}
