package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

// ExtractFile on an unknown-language file emits exactly one bare file-level
// node — no symbol nodes, edges, or unresolved references — and never parses.
func TestUnknownLanguageEmitsBareFileNode(t *testing.T) {
	res, err := ExtractFile("notes.txt", []byte("not code at all\n"), model.LangUnknown)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(res.Nodes) != 1 {
		t.Fatalf("nodes = %d, want exactly 1", len(res.Nodes))
	}
	n := res.Nodes[0]
	if n.Kind != model.KindFile {
		t.Errorf("kind = %q, want %q", n.Kind, model.KindFile)
	}
	if n.Language != model.LangUnknown {
		t.Errorf("language = %q, want %q", n.Language, model.LangUnknown)
	}
	if n.Name != "notes.txt" {
		t.Errorf("name = %q, want notes.txt", n.Name)
	}
	if len(res.Edges) != 0 || len(res.UnresolvedReferences) != 0 || len(res.Errors) != 0 {
		t.Errorf("expected no edges/refs/errors, got %d/%d/%d",
			len(res.Edges), len(res.UnresolvedReferences), len(res.Errors))
	}
}

// A nil content slice (as the indexer passes for unknown files, to avoid
// loading a binary blob into the parser) still yields a single file node.
func TestUnknownLanguageNilContent(t *testing.T) {
	res, err := ExtractFile("image.png", nil, model.LangUnknown)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("want one file node, got %+v", res.Nodes)
	}
}
