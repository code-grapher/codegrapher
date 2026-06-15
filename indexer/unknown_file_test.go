package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
)

// allNodesAcross gathers every node from every scope store.
func allNodesAcross(t *testing.T, idx *Indexer) []model.Node {
	t.Helper()
	var all []model.Node
	for _, s := range idx.Stores() {
		ns, err := s.AllNodes()
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, ns...)
	}
	return all
}

// nodesForFile returns the nodes whose FilePath equals path.
func nodesForFile(nodes []model.Node, path string) []model.Node {
	var out []model.Node
	for _, n := range nodes {
		if n.FilePath == path {
			out = append(out, n)
		}
	}
	return out
}

// AC unknown-extension-gets-node: a non-gitignored file with an unrecognized
// extension yields exactly one file-level node with Language=unknown and zero
// symbol nodes.
func TestUnknownExtensionGetsBareFileNode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes.txt"), "just some prose, not code\n")

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = idx.Close() }()

	nodes := nodesForFile(allNodesAcross(t, idx), "notes.txt")
	if len(nodes) != 1 {
		t.Fatalf("notes.txt nodes = %d, want exactly 1", len(nodes))
	}
	n := nodes[0]
	if n.Kind != model.KindFile {
		t.Errorf("node kind = %q, want %q", n.Kind, model.KindFile)
	}
	if n.Language != model.LangUnknown {
		t.Errorf("node language = %q, want %q", n.Language, model.LangUnknown)
	}

	// The file record exists, carries Language=unknown and a content hash.
	rec := fileRecord(t, idx, "notes.txt")
	if rec == nil {
		t.Fatal("no file record for notes.txt")
	}
	if rec.Language != model.LangUnknown {
		t.Errorf("file record language = %q, want %q", rec.Language, model.LangUnknown)
	}
	if rec.ContentHash == "" {
		t.Error("file record content hash is empty")
	}
}

// AC binary-file-gets-node: a binary file of arbitrary size yields a single
// file-level node with Language=unknown and no parse/symbol extraction. We use
// a binary blob larger than MaxFileSize to prove the size cap does not apply.
func TestBinaryFileGetsBareFileNode(t *testing.T) {
	dir := t.TempDir()
	// A small PNG header followed by enough bytes to exceed MaxFileSize.
	blob := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxFileSize+1024)...)
	if err := os.WriteFile(filepath.Join(dir, "image.png"), blob, 0o644); err != nil {
		t.Fatal(err)
	}

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = idx.Close() }()

	nodes := nodesForFile(allNodesAcross(t, idx), "image.png")
	if len(nodes) != 1 {
		t.Fatalf("image.png nodes = %d, want exactly 1", len(nodes))
	}
	if nodes[0].Kind != model.KindFile || nodes[0].Language != model.LangUnknown {
		t.Errorf("node = {kind:%q lang:%q}, want {file unknown}", nodes[0].Kind, nodes[0].Language)
	}

	rec := fileRecord(t, idx, "image.png")
	if rec == nil {
		t.Fatal("no file record for image.png")
	}
	if rec.Language != model.LangUnknown {
		t.Errorf("file record language = %q, want %q", rec.Language, model.LangUnknown)
	}
	// A binary file is admitted regardless of size: no size_exceeded skip.
}

// AC recognized-source-unchanged: a recognized .go source file still gets full
// symbol extraction alongside an unknown-language file in the same repo.
func TestRecognizedSourceStillExtracted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc helper() {}\n")
	writeFile(t, filepath.Join(dir, "notes.txt"), "prose\n")

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = idx.Close() }()

	ns, err := idx.Store().GetNodesByName("helper")
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 {
		t.Errorf("helper nodes = %d, want 1", len(ns))
	}

	// main.go also has more than a bare file node (the symbol node is present).
	goNodes := nodesForFile(allNodesAcross(t, idx), "main.go")
	if len(goNodes) < 2 {
		t.Errorf("main.go nodes = %d, want > 1 (file node + symbols)", len(goNodes))
	}
}

// fileRecord finds the file record for path across all scope stores.
func fileRecord(t *testing.T, idx *Indexer, path string) *model.FileRecord {
	t.Helper()
	for _, s := range idx.Stores() {
		rec, err := s.GetFileByPath(path)
		if err != nil {
			t.Fatal(err)
		}
		if rec != nil {
			return rec
		}
	}
	return nil
}
