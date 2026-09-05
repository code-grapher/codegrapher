package indexer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractOneSkipsDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	job := extractOne(root, "skills")
	if job.readErr != nil {
		t.Fatalf("directory became a read error: %v", job.readErr)
	}
	if len(job.result.Nodes) != 0 || len(job.result.Errors) != 0 {
		t.Fatalf("directory unexpectedly extracted: %+v", job.result)
	}
}
