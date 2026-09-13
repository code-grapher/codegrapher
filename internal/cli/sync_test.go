package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
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

func TestSyncReturnsErrorWhenWriterLockIsUnavailable(t *testing.T) {
	projectPath := initializedSyncFixture(t)
	command := newSyncCmdWithRunner(func(*indexer.Indexer, indexer.Options) indexer.SyncResult {
		return indexer.SyncResult{LockUnavailable: true}
	})
	command.SetArgs([]string{"--quiet", projectPath})

	err := command.Execute()
	if err == nil {
		t.Fatal("sync unexpectedly succeeded without acquiring the writer lock")
	}
	if !strings.Contains(err.Error(), "writer lock") {
		t.Fatalf("error = %q, want writer-lock explanation", err)
	}
}

func TestSyncReturnsErrorForPartialFailures(t *testing.T) {
	projectPath := initializedSyncFixture(t)
	command := newSyncCmdWithRunner(func(*indexer.Indexer, indexer.Options) indexer.SyncResult {
		return indexer.SyncResult{
			FilesModified: 1,
			Errors: []model.ExtractionError{{
				FilePath: "broken.go",
				Message:  "could not update file",
				Severity: "error",
			}},
		}
	})
	command.SetArgs([]string{"--quiet", projectPath})

	err := command.Execute()
	if err == nil {
		t.Fatal("sync unexpectedly succeeded with incremental update failures")
	}
	if !strings.Contains(err.Error(), "1 file") {
		t.Fatalf("error = %q, want failed-file count", err)
	}
}

func initializedSyncFixture(t *testing.T) string {
	t.Helper()
	projectPath := t.TempDir()
	if err := copyDir(filepath.Join("..", "..", "testdata", "fixtures", "go-small"), projectPath); err != nil {
		t.Fatal(err)
	}
	idx, result, err := indexer.Init(projectPath, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("fixture initialization failed: %+v", result.Errors)
	}
	return projectPath
}
