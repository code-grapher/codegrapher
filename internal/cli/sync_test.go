package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/indexer"
)

func TestSyncInitInitializesThenUsesIncrementalReconciliation(t *testing.T) {
	projectPath := t.TempDir()
	if err := copyDir(filepath.Join("..", "..", "testdata", "fixtures", "go-small"), projectPath); err != nil {
		t.Fatal(err)
	}
	if indexer.IsInitialized(projectPath) {
		t.Fatal("fixture unexpectedly initialized")
	}

	command := newSyncCmd()
	command.SetArgs([]string{"--init", "--quiet", projectPath})
	if err := command.Execute(); err != nil {
		t.Fatalf("first sync --init: %v", err)
	}
	if !indexer.IsInitialized(projectPath) {
		t.Fatal("sync --init did not initialize the repository")
	}

	changedPath := filepath.Join(projectPath, "internal", "store", "store.go")
	file, err := os.OpenFile(changedPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\nfunc AddedAfterInit() {}\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	command = newSyncCmd()
	command.SetArgs([]string{"--init", "--quiet", projectPath})
	if err := command.Execute(); err != nil {
		t.Fatalf("second sync --init: %v", err)
	}
	idx, err := indexer.Open(projectPath, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	changes := idx.GetChangedFiles()
	if len(changes.Added)+len(changes.Modified)+len(changes.Removed) != 0 {
		t.Fatalf("pending changes after incremental sync = %+v", changes)
	}
}

func TestSyncWithoutInitStillRefusesUninitializedRepository(t *testing.T) {
	projectPath := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestSyncWithoutInitHelper$")
	command.Env = append(os.Environ(), "CODEGRAPHER_SYNC_WITHOUT_INIT_HELPER="+projectPath)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("sync without --init unexpectedly succeeded")
	}
	if !strings.Contains(string(output), "CodeGraph not initialized") {
		t.Fatalf("output = %q", output)
	}
	if indexer.IsInitialized(projectPath) {
		t.Fatal("sync without --init created a CodeGraph")
	}
}

func TestSyncWithoutInitHelper(t *testing.T) {
	projectPath := os.Getenv("CODEGRAPHER_SYNC_WITHOUT_INIT_HELPER")
	if projectPath == "" {
		return
	}
	command := newSyncCmd()
	command.SetArgs([]string{projectPath})
	_ = command.Execute()
}
