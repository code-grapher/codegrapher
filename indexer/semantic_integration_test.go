package indexer

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/scope"
)

const semanticModel = `entity "Invoice" {
  key = ["id"]
  property "id" { type = "uuid" }
  property "total" { type = "decimal" }
}
`
const semanticMeaning = `format: meaning/draft-1
id: demo
name: Demo
description: Demo concepts.
models:
  chinook: chinook.modelspec.hcl
concepts:
  - id: invoice-total
    kind: attribute
    labels: {en: Invoice total}
    synonyms: {en: [revenue]}
    description: The total of an invoice.
    bindings:
      - model: modelspec:///chinook.Invoice
        property: total
        role: value
`

func semanticNodes(t *testing.T, idx *Indexer, kind model.NodeKind) []model.Node {
	t.Helper()
	var found []model.Node
	for _, st := range idx.Stores() {
		nodes, err := st.AllNodes()
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range nodes {
			if n.Kind == kind {
				found = append(found, n)
			}
		}
	}
	return found
}
func semanticEdges(t *testing.T, idx *Indexer, kind model.EdgeKind) []model.Edge {
	t.Helper()
	var found []model.Edge
	for _, st := range idx.Stores() {
		edges, err := st.AllEdges()
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			if e.Kind == kind {
				found = append(found, e)
			}
		}
	}
	return found
}
func writeSemantic(t *testing.T, root, path, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, path), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
func TestSemanticIncrementalRefreshAndMove(t *testing.T) {
	root := t.TempDir()
	writeSemantic(t, root, "chinook.modelspec.hcl", semanticModel)
	writeSemantic(t, root, "demo.meaning.yaml", semanticMeaning)
	idx, result, err := Init(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := idx.Close(); err != nil {
			t.Error(err)
		}
	}()
	if !result.Success {
		t.Fatalf("init: %+v", result.Errors)
	}
	concepts := semanticNodes(t, idx, model.KindMeaningConcept)
	if len(concepts) != 1 {
		t.Fatalf("meaning concepts = %d", len(concepts))
	}
	if len(semanticEdges(t, idx, model.EdgeBindsTo)) != 1 {
		t.Fatal("binding absent")
	}
	id := concepts[0].ID
	if err := os.Rename(filepath.Join(root, "demo.meaning.yaml"), filepath.Join(root, "renamed.meaning.yaml")); err != nil {
		t.Fatal(err)
	}
	sync := idx.SyncFiles([]string{"demo.meaning.yaml", "renamed.meaning.yaml"}, Options{})
	if len(sync.Errors) > 0 {
		t.Fatalf("sync: %+v", sync.Errors)
	}
	concepts = semanticNodes(t, idx, model.KindMeaningConcept)
	if len(concepts) != 1 || concepts[0].ID != id || concepts[0].FilePath != "renamed.meaning.yaml" {
		t.Fatalf("move changed identity: %+v", concepts)
	}
	if len(semanticEdges(t, idx, model.EdgeBindsTo)) != 1 {
		t.Fatal("binding lost after move")
	}
	beforeConcept := semanticNodes(t, idx, model.KindMeaningConcept)[0]
	beforeBindings := semanticEdges(t, idx, model.EdgeBindsTo)
	full := idx.Rebuild(Options{})
	if len(full.Errors) > 0 {
		t.Fatalf("rebuild: %+v", full.Errors)
	}
	afterConcept := semanticNodes(t, idx, model.KindMeaningConcept)[0]
	afterBindings := semanticEdges(t, idx, model.EdgeBindsTo)
	if beforeConcept.ID != afterConcept.ID || beforeConcept.FilePath != afterConcept.FilePath || len(beforeBindings) != len(afterBindings) || beforeBindings[0].Source != afterBindings[0].Source || beforeBindings[0].Target != afterBindings[0].Target {
		t.Fatal("incremental and full semantic projections differ")
	}
	writeSemantic(t, root, "chinook.modelspec.hcl", `entity "Invoice" { property "id" { type = "uuid" } }`)
	sync = idx.SyncFiles([]string{"chinook.modelspec.hcl"}, Options{})
	if len(sync.Errors) > 0 {
		t.Fatalf("target sync: %+v", sync.Errors)
	}
	if len(semanticEdges(t, idx, model.EdgeBindsTo)) != 0 {
		t.Fatal("stale incoming binding")
	}
	if err := os.Remove(filepath.Join(root, "renamed.meaning.yaml")); err != nil {
		t.Fatal(err)
	}
	sync = idx.SyncFiles([]string{"renamed.meaning.yaml"}, Options{})
	if len(sync.Errors) > 0 {
		t.Fatalf("delete sync: %+v", sync.Errors)
	}
	if len(semanticNodes(t, idx, model.KindMeaningConcept)) != 0 {
		t.Fatal("deleted concept remains")
	}
}

func TestVersion14IndexRebuildsSemanticProjection(t *testing.T) {
	root := t.TempDir()
	writeSemantic(t, root, "chinook.modelspec.hcl", semanticModel)
	writeSemantic(t, root, "demo.meaning.yaml", semanticMeaning)
	idx, result, err := Init(root, Options{})
	if err != nil || !result.Success {
		t.Fatalf("init: %v %+v", err, result)
	}
	defer func() {
		if err := idx.Close(); err != nil {
			t.Error(err)
		}
	}()
	for _, st := range idx.Stores() {
		if err := st.SetMetadata("indexed_with_extraction_version", "14"); err != nil {
			t.Fatal(err)
		}
		nodes, err := st.AllNodes()
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range nodes {
			if node.Kind != model.KindMeaningGraph {
				continue
			}
			if err := st.ReplaceSemantic(nil, nil); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if got := len(semanticNodes(t, idx, model.KindMeaningConcept)); got != 0 {
		t.Fatalf("pre-upgrade concepts = %d", got)
	}
	res := idx.Sync(Options{})
	if !res.FullReindex || len(res.Errors) != 0 {
		t.Fatalf("version-14 migration = %+v", res)
	}
	if got := len(semanticNodes(t, idx, model.KindMeaningConcept)); got != 1 {
		t.Fatalf("post-upgrade concepts = %d", got)
	}
}

func TestSemanticOnlyFailedRebuildRetainsStaleVersionForRetry(t *testing.T) {
	root := t.TempDir()
	idx, result, err := Init(root, Options{})
	if err != nil || !result.Success {
		t.Fatalf("init: %v %+v", err, result)
	}
	defer func() {
		if err := idx.Close(); err != nil {
			t.Error(err)
		}
	}()
	semanticStore, err := idx.reg.Store(scope.Scope{Language: model.Language("semantic"), Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := semanticStore.SetMetadata("indexed_with_extraction_version", "14"); err != nil {
		t.Fatal(err)
	}
	if err := semanticStore.Transaction(func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER fail_semantic_stamp BEFORE UPDATE ON project_metadata
			WHEN NEW.key = 'indexed_with_version' BEGIN SELECT RAISE(FAIL, 'blocked'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	failed := idx.IndexAll(Options{})
	if failed.Success || len(failed.Errors) == 0 {
		t.Fatalf("failed semantic rebuild = %+v", failed)
	}
	if version, err := semanticStore.GetMetadata("indexed_with_extraction_version"); err != nil || version != "14" {
		t.Fatalf("failed rebuild stamped version %q, %v", version, err)
	}
	if err := semanticStore.Transaction(func(tx *sql.Tx) error { _, err := tx.Exec(`DROP TRIGGER fail_semantic_stamp`); return err }); err != nil {
		t.Fatal(err)
	}
	retry := idx.Sync(Options{})
	if !retry.FullReindex || len(retry.Errors) > 0 {
		t.Fatalf("retry = %+v", retry)
	}
	if version, err := semanticStore.GetMetadata("indexed_with_extraction_version"); err != nil || version != "15" {
		t.Fatalf("retry version %q, %v", version, err)
	}
}
