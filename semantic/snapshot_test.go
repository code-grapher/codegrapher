package semantic

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	meaning "github.com/meaninggraph/cli/pkg/meaning"
	modelspec "github.com/modelspec-org/cli/pkg/modelspec"
)

func TestSemanticSnapshotHonorsReadLimitBeforeCaching(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "oversized.modelspec.hcl")
	if err := os.WriteFile(path, []byte("entity \"Long\" {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := newSourceSnapshot(root)
	if _, err := snapshot.readMax(path, 4); err == nil {
		t.Fatal("oversized read accepted")
	} else {
		var tooLarge snapshotTooLarge
		if !errors.As(err, &tooLarge) {
			t.Fatalf("oversized read error = %v", err)
		}
	}
	if len(snapshot.files) != 0 {
		t.Fatal("oversized source was cached")
	}
	if _, err := snapshot.read(path); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentSemanticSourceHashRejectsFIFOWithoutBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFO test requires Unix")
	}
	root := t.TempDir()
	path := filepath.Join(root, "changed-to-fifo.meaning.yaml")
	if err := exec.Command("mkfifo", path).Run(); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := CurrentSourceHash(root, path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted as semantic source")
		}
	case <-time.After(time.Second):
		t.Fatal("semantic source hash blocked on FIFO")
	}
}

func TestSemanticParsersAndRangesUseOneSourceSnapshot(t *testing.T) {
	root := t.TempDir()
	modelPath := filepath.Join(root, "geo.modelspec.hcl")
	meaningPath := filepath.Join(root, "geo.meaning.yaml")
	modelA := "entity \"Original\" {\n  property \"id\" { type = \"string\" }\n}\n"
	meaningA := "format: meaning/draft-1\nid: geo\nname: Geo\nconcepts:\n  - id: original\n    kind: entity\n    labels: {en: Original}\n"
	for path, content := range map[string]string{modelPath: modelA, meaningPath: meaningA} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := newSourceSnapshot(root)
	modelFS := snapshotModelFS{snapshot: snapshot}
	meaningFS := snapshotMeaningFS{snapshot: snapshot}
	modelFile, err := modelFS.Open(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(modelFile); err != nil {
		t.Fatal(err)
	}
	_ = modelFile.Close()
	if _, err := meaningFS.ReadFile(meaningPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("entity \"Changed\" {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meaningPath, []byte(strings.Replace(meaningA, "original", "changed", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	models, _, err := modelspec.Load(modelFS, []modelspec.Source{{Path: modelPath, Abs: modelPath}}, nil)
	if err != nil || len(models) != 1 || len(models[0].Concepts) != 1 || models[0].Concepts[0].Name != "Original" {
		t.Fatalf("ModelSpec snapshot = %+v, %v", models, err)
	}
	graph, err := meaning.LoadFiles(meaningFS, []string{meaningPath})
	if err != nil || len(graph.Files) != 1 || len(graph.Files[0].Concepts) != 1 || graph.Files[0].Concepts[0].ID != "original" {
		t.Fatalf("MeaningGraph snapshot = %+v, %v", graph, err)
	}
	b := builder{snapshot: snapshot}
	if got := b.blockEnd(modelPath, 1); got != 3 {
		t.Fatalf("ModelSpec range from changed file = %d", got)
	}
	if got := b.yamlBlockEnd(meaningPath, 5); got != 7 {
		t.Fatalf("MeaningGraph range from changed file = %d", got)
	}
	if snapshot.hashes()["geo.modelspec.hcl"] == "" || snapshot.hashes()["geo.meaning.yaml"] == "" {
		t.Fatal("source snapshot hashes missing")
	}
}
