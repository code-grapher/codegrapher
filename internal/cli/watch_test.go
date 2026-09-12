package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/watch"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestWatchOutputVerboseIncludesEventLifecycleAndStats(t *testing.T) {
	var stdout, stderr bytes.Buffer
	output := newWatchOutput(&stdout, &stderr, true)
	at := time.Date(2026, 9, 12, 9, 30, 0, 0, time.UTC)
	output.observe(watch.Observation{Kind: watch.ObservationEventReceived, At: at, Path: "src/app.go", Operation: "WRITE"})
	output.observe(watch.Observation{
		Kind: watch.ObservationOperationStarted, At: at, OperationID: 4,
		EventsReceived: 3, DirtyPaths: 2, CoalescedEvents: 1,
	})
	output.observe(watch.Observation{
		Kind: watch.ObservationOperationCompleted, At: at, OperationID: 4,
		EventsReceived: 3, DirtyPaths: 2, CoalescedEvents: 1, Duration: 14 * time.Millisecond,
		Result: watch.SyncResult{FilesChecked: 8, FilesAdded: 1, FilesModified: 1, FilesRemoved: 0, NodesUpdated: 6},
	})

	got := stdout.String()
	for _, want := range []string{
		"2026-09-12T09:30:00Z event received op=WRITE path=src/app.go",
		"operation=4 started",
		"operation=4 completed duration=14ms",
		"events=3 dirty_paths=2 coalesced=1",
		"checked=8 added=1 modified=1 removed=0 nodes_updated=6 full_reindex=false",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("verbose output missing %q:\n%s", want, got)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestWatchOutputDefaultSuppressesRawEventsAndNoOpCompletion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	output := newWatchOutput(&stdout, &stderr, false)
	output.observe(watch.Observation{Kind: watch.ObservationEventReceived, Path: "src/app.go", Operation: "WRITE"})
	output.observe(watch.Observation{Kind: watch.ObservationOperationStarted, OperationID: 1})
	output.observe(watch.Observation{Kind: watch.ObservationOperationCompleted, OperationID: 1, Duration: time.Millisecond})
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("default no-op output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	output.observe(watch.Observation{
		Kind: watch.ObservationOperationCompleted, OperationID: 2, Duration: 3 * time.Millisecond,
		Result: watch.SyncResult{FilesChanged: 1, FilesModified: 1, NodesUpdated: 2},
	})
	if got := stdout.String(); !strings.Contains(got, "Updated 1 file") || strings.Contains(got, "event received") {
		t.Fatalf("default material output = %q", got)
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:foreground-watch-reconciles-real-edit
// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:graceful-cancellation
func TestWatchCommandReconcilesRealFilesystemEditAndCancels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real watcher integration test in short mode")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/watchtest\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, _, err := indexer.Init(dir, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CODEGRAPH_WATCH_DEBOUNCE_MS", "50")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := newWatchCmd()
	cmd.SetArgs([]string{dir, "--verbose"})
	var stdout, stderr lockedBuffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	waitForCLI(t, 5*time.Second, func() bool { return strings.Contains(stdout.String(), "Watching ") })
	if err := os.WriteFile(filepath.Join(dir, "added.go"), []byte("package main\n\nfunc Added() int { return 7 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForCLI(t, 8*time.Second, func() bool {
		return strings.Contains(stdout.String(), "completed") && strings.Contains(stdout.String(), "added=1")
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch command: %v; stderr=%s", err, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch command did not stop after cancellation")
	}

	idx, err = indexer.Open(dir, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	var found bool
	for _, store := range idx.Stores() {
		nodes, getErr := store.GetNodesByName("Added")
		if getErr != nil {
			t.Fatal(getErr)
		}
		found = found || len(nodes) > 0
	}
	if !found {
		t.Fatalf("Added symbol not indexed; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func waitForCLI(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("condition not met within %v", timeout))
}
