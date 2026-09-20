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

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/sync-initialize-if-missing#ac:first-update-initializes-then-next-update-syncs
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

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/sync-initialize-if-missing#ac:default-sync-does-not-create-policy
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
	if !strings.Contains(err.Error(), "could not update file") {
		t.Fatalf("error = %q, want first fatal diagnostic", err)
	}
}

func TestSyncAllowsExtractionWarnings(t *testing.T) {
	projectPath := initializedSyncFixture(t)
	command := newSyncCmdWithRunner(func(*indexer.Indexer, indexer.Options) indexer.SyncResult {
		return indexer.SyncResult{
			FilesModified: 1,
			Errors: []model.ExtractionError{{
				FilePath: "spec/decisions/0001-example.md",
				Message:  "unrecognized or missing format",
				Severity: "warning",
			}},
		}
	})
	command.SetArgs([]string{"--quiet", projectPath})

	if err := command.Execute(); err != nil {
		t.Fatalf("sync rejected a non-fatal extraction warning: %v", err)
	}
}

func TestSyncReportsFatalCountAndFirstFatalAmongWarnings(t *testing.T) {
	projectPath := initializedSyncFixture(t)
	command := newSyncCmdWithRunner(func(*indexer.Indexer, indexer.Options) indexer.SyncResult {
		return indexer.SyncResult{Errors: []model.ExtractionError{
			{FilePath: "before.md", Message: "warning before", Severity: "warning"},
			{FilePath: "first.go", Message: "first fatal", Severity: "error"},
			{FilePath: "between.md", Message: "warning between", Severity: "WARNING"},
			{FilePath: "second.go", Message: "second fatal", Severity: "error"},
		}}
	})
	command.SetArgs([]string{"--quiet", projectPath})

	err := command.Execute()
	if err == nil {
		t.Fatal("sync unexpectedly succeeded with mixed fatal diagnostics")
	}
	if got, want := err.Error(), "sync failed for 2 file(s): first fatal"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestSyncPersistsWarningDiagnosticsOnSuccess(t *testing.T) {
	projectPath := initializedSyncFixture(t)
	relPath := filepath.Join("spec", "features", "README.md")
	fullPath := filepath.Join(projectPath, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte("---\nformat: https://specscore.md/broken-specification\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := newSyncCmd()
	command.SetArgs([]string{"--quiet", projectPath})
	if err := command.Execute(); err != nil {
		t.Fatalf("sync rejected warning-only diagnostics: %v", err)
	}

	idx, err := indexer.Open(projectPath, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	var found bool
	for _, s := range idx.Stores() {
		rec, err := s.GetFileByPath(filepath.ToSlash(relPath))
		if err != nil {
			t.Fatal(err)
		}
		if rec == nil {
			continue
		}
		found = true
		if len(rec.Errors) == 0 || !strings.EqualFold(rec.Errors[0].Severity, "warning") {
			t.Fatalf("persisted diagnostics = %+v, want warning", rec.Errors)
		}
	}
	if !found {
		t.Fatalf("no persisted file record for %s", relPath)
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
