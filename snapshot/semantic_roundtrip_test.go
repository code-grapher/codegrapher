package snapshot_test

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/snapshot"
	"github.com/specscore/codegrapher/store"
)

func TestSemanticMetadataRoundTrip(t *testing.T) {
	root := t.TempDir()
	srcPath := filepath.Join(root, "src.db")
	dstPath := filepath.Join(root, "dst.db")
	s, err := store.Initialize(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	a := model.Node{ID: "meaning_concept:a", Kind: model.KindMeaningConcept, Name: "a", FilePath: "a.meaning.yaml", Language: model.LangMeaningGraph, StartLine: 5, EndLine: 9, Metadata: map[string]any{"labels": map[string]any{"en": "Revenue"}, "synonyms": map[string]any{"en": []string{"income"}}}}
	b := model.Node{ID: "model_member:b", Kind: model.KindModelMember, Name: "b", FilePath: "a.modelspec.hcl", Language: model.LangModelSpec, StartLine: 2, EndLine: 3}
	if err := s.InsertNodes([]model.Node{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertEdge(model.Edge{Source: a.ID, Target: b.ID, Kind: model.EdgeBindsTo, Metadata: map[string]any{"role": "value"}}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	dir := filepath.Join(root, "snapshot")
	if err := snapshot.Export(srcPath, dir, ""); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Import(dstPath, dir); err != nil {
		t.Fatal(err)
	}
	d, err := store.Open(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.GetNodeByID(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Metadata["labels"] == nil || got.Metadata["synonyms"] == nil {
		t.Fatalf("metadata lost: %+v", got)
	}
	edges, err := d.AllEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].Metadata["role"] != "value" {
		t.Fatalf("binding metadata lost: %+v", edges)
	}
}
