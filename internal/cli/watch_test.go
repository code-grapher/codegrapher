package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
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
		EventsReceived: 3, DirtyPaths: 2, CoalescedEvents: 1, IgnoredEvents: 2,
		QueuedFor: 25 * time.Millisecond, Debounce: 20 * time.Millisecond,
	})
	output.observe(watch.Observation{
		Kind: watch.ObservationOperationCompleted, At: at, Operation: "reconcile", OperationID: 4,
		EventsReceived: 3, DirtyPaths: 2, CoalescedEvents: 1, IgnoredEvents: 2,
		Duration: 14 * time.Millisecond, TotalDuration: 39 * time.Millisecond,
		Result: watch.SyncResult{FilesChecked: 8, FilesAdded: 1, FilesModified: 1, FilesRemoved: 0, NodesUpdated: 6},
	})

	got := stdout.String()
	for _, want := range []string{
		"2026-09-12T09:30:00Z event received op=WRITE path=src/app.go",
		"operation=4 started",
		"operation=4 completed kind=reconcile duration=14ms",
		"events=3 dirty_paths=2 coalesced=1",
		"ignored=2 queued=25ms debounce=20ms",
		"total=39ms no_op=false",
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

func TestWatchOutputDefaultDescribesFullRebuildTruthfully(t *testing.T) {
	var stdout, stderr bytes.Buffer
	output := newWatchOutput(&stdout, &stderr, false)
	output.observe(watch.Observation{
		Kind: watch.ObservationOperationCompleted, Duration: 7 * time.Millisecond,
		Result: watch.SyncResult{FilesChecked: 12, NodesUpdated: 31, FullReindex: true},
	})
	got := stdout.String()
	if !strings.Contains(got, "Rebuilt index from 12 files (31 nodes) in 7ms") {
		t.Fatalf("default full-rebuild output = %q", got)
	}
	if strings.Contains(got, "Updated 0 files") {
		t.Fatalf("default full-rebuild output is misleading: %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestReconcilePathsForWatchSurfacesRealIndexerFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, _, err := indexer.Init(dir, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	if err := os.Mkdir(filepath.Join(dir, "not-a-file"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := reconcilePathsForWatch(idx, []string{"not-a-file"})
	if err == nil || !strings.Contains(err.Error(), "index reconciliation failed") {
		t.Fatalf("reconcilePathsForWatch error = %v", err)
	}
	if result.FilesChecked != 1 {
		t.Fatalf("result = %+v", result)
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:failure-remains-dirty-and-visible
func TestWatcherRetriesAfterRealIndexerFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, _, err := indexer.Init(dir, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	badPath := filepath.Join(dir, "bad")
	if err := os.Mkdir(badPath, 0o755); err != nil {
		t.Fatal(err)
	}
	failures := make(chan watch.Observation, 1)
	w := watch.NewWithPaths(dir, func(paths []string) (watch.SyncResult, error) {
		return reconcilePathsForWatch(idx, paths)
	}, watch.Options{
		DebounceMs:    30,
		InertForTests: true,
		OnObservation: func(observation watch.Observation) {
			if observation.Kind == watch.ObservationOperationFailed {
				select {
				case failures <- observation:
				default:
				}
			}
		},
	})
	if err := w.StartWithError(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Stop)
	w.IngestEventForTests("bad")
	select {
	case <-failures:
	case <-time.After(2 * time.Second):
		t.Fatal("real indexer failure was not observed")
	}
	if len(w.PendingFiles()) != 1 {
		t.Fatalf("pending after failure = %+v", w.PendingFiles())
	}
	if err := os.Remove(badPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badPath, []byte("now a file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForCLI(t, 3*time.Second, func() bool { return len(w.PendingFiles()) == 0 })
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:worktree-index-is-local
func TestWatchCommandRejectsNearestIndexFromDifferentGitWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	outer := t.TempDir()
	runGitForWatchTest(t, outer, "init", "-q")
	if err := os.WriteFile(filepath.Join(outer, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, _, err := indexer.Init(outer, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(outer, "nested-worktree")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitForWatchTest(t, nested, "init", "-q")

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	cmd := newWatchCmd()
	err = cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "different git worktree") || !strings.Contains(err.Error(), "codegrapher init") {
		t.Fatalf("watch error = %v, want actionable worktree-local init error", err)
	}
}

func runGitForWatchTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:foreground-watch-reconciles-real-edit
// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:populated-directory-move-is-reconciled
// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:graceful-cancellation
func TestWatchCommandReconcilesRealFilesystemEditAndCancels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real watcher integration test in short mode")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/watchtest\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainPath, []byte("package main\n\nfunc Target() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "caller.go"), []byte("package main\n\nfunc Caller() int { return Target() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	obsoleteDir := filepath.Join(dir, "obsolete")
	if err := os.Mkdir(obsoleteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(obsoleteDir, "old.go"), []byte("package obsolete\n\nfunc Obsolete() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForWatchTest(t, dir, "init", "-q")
	runGitForWatchTest(t, dir, "add", "go.mod", "main.go", "caller.go", "obsolete/old.go")
	originalInfo, err := os.Stat(mainPath)
	if err != nil {
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
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("watch stdout:\n%s\nwatch stderr:\n%s", stdout.String(), stderr.String())
		}
	})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	waitForCLI(t, 5*time.Second, func() bool { return strings.Contains(stdout.String(), "Watching ") })
	// Keep both byte length and mtime unchanged. The watcher must pass its
	// exact dirty path to SyncFiles so content hashing still finds the edit.
	updatedMain := []byte("package main\n\nfunc Target() int { return 2 }\n")
	if err := os.WriteFile(mainPath, updatedMain, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(mainPath, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	waitForCLI(t, 8*time.Second, func() bool {
		return strings.Contains(stdout.String(), "modified=1")
	})
	renamedMainPath := filepath.Join(dir, "target.go")
	if err := os.Rename(mainPath, renamedMainPath); err != nil {
		t.Fatal(err)
	}
	waitForCLI(t, 8*time.Second, func() bool {
		got := stdout.String()
		return strings.Contains(got, "kind=full_reconcile") && strings.Contains(got, "full_reindex=true")
	})
	if err := os.WriteFile(filepath.Join(dir, "added.go"), []byte("package main\n\nfunc Added() int { return 7 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForCLI(t, 8*time.Second, func() bool {
		return strings.Contains(stdout.String(), "completed") && strings.Contains(stdout.String(), "added=1")
	})
	if err := os.Rename(obsoleteDir, filepath.Join(t.TempDir(), "obsolete")); err != nil {
		t.Fatal(err)
	}
	waitForCLI(t, 8*time.Second, func() bool {
		return strings.Count(stdout.String(), "kind=full_reconcile") >= 4
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
	var foundAdded, foundTarget, foundObsolete, preservedCall, updatedHash bool
	for _, store := range idx.Stores() {
		nodes, getErr := store.GetNodesByName("Added")
		if getErr != nil {
			t.Fatal(getErr)
		}
		foundAdded = foundAdded || len(nodes) > 0
		nodes, getErr = store.GetNodesByName("Target")
		if getErr != nil {
			t.Fatal(getErr)
		}
		foundTarget = foundTarget || len(nodes) > 0
		nodes, getErr = store.GetNodesByName("Obsolete")
		if getErr != nil {
			t.Fatal(getErr)
		}
		foundObsolete = foundObsolete || len(nodes) > 0
		callers, getErr := store.GetNodesByName("Caller")
		if getErr != nil {
			t.Fatal(getErr)
		}
		if len(callers) == 1 {
			edges, edgeErr := store.GetOutgoingEdges(callers[0].ID, []model.EdgeKind{model.EdgeCalls}, "")
			if edgeErr != nil {
				t.Fatal(edgeErr)
			}
			preservedCall = preservedCall || len(edges) == 1
		}
		files, fileErr := store.GetAllFiles()
		if fileErr != nil {
			t.Fatal(fileErr)
		}
		for _, file := range files {
			if file.Path == "target.go" {
				updatedHash = file.ContentHash == indexer.HashContent(updatedMain)
			}
		}
	}
	if !foundAdded || !foundTarget || foundObsolete || !preservedCall || !updatedHash {
		t.Fatalf("unexpected graph added=%t target=%t obsolete=%t preserved_call=%t updated_hash=%t; stdout=%s stderr=%s",
			foundAdded, foundTarget, foundObsolete, preservedCall, updatedHash, stdout.String(), stderr.String())
	}

	cleanDir := t.TempDir()
	for rel, content := range map[string][]byte{
		"go.mod":    []byte("module example.com/watchtest\n\ngo 1.22\n"),
		"caller.go": []byte("package main\n\nfunc Caller() int { return Target() }\n"),
		"target.go": updatedMain,
		"added.go":  []byte("package main\n\nfunc Added() int { return 7 }\n"),
	} {
		if err := os.WriteFile(filepath.Join(cleanDir, rel), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cleanIdx, _, err := indexer.Init(cleanDir, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cleanIdx.Close() }()
	if got, want := graphFingerprint(t, idx), graphFingerprint(t, cleanIdx); !reflect.DeepEqual(got, want) {
		t.Fatalf("watch graph differs from clean index\nwatch=%v\nclean=%v", got, want)
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:event-storm-falls-back-to-full-reconciliation
func TestWatchCommandConvergesAfterGitMerge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real watcher Git integration test in short mode")
	}
	dir := t.TempDir()
	base := []byte("package merged\n\nfunc Base() int { return 1 }\n")
	if err := os.WriteFile(filepath.Join(dir, "base.go"), base, 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForWatchTest(t, dir, "init", "-q")
	runGitForWatchTest(t, dir, "config", "user.email", "watch@example.test")
	runGitForWatchTest(t, dir, "config", "user.name", "Watch Test")
	runGitForWatchTest(t, dir, "branch", "-M", "main")
	runGitForWatchTest(t, dir, "add", "base.go")
	runGitForWatchTest(t, dir, "commit", "-qm", "base")
	runGitForWatchTest(t, dir, "checkout", "-qb", "feature")
	feature := []byte("package merged\n\nfunc Feature() int { return Base() }\n")
	if err := os.WriteFile(filepath.Join(dir, "feature.go"), feature, 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForWatchTest(t, dir, "add", "feature.go")
	runGitForWatchTest(t, dir, "commit", "-qm", "feature")
	runGitForWatchTest(t, dir, "checkout", "-q", "main")
	mainOnly := []byte("package merged\n\nfunc MainOnly() int { return 2 }\n")
	if err := os.WriteFile(filepath.Join(dir, "main_only.go"), mainOnly, 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForWatchTest(t, dir, "add", "main_only.go")
	runGitForWatchTest(t, dir, "commit", "-qm", "main")
	idx, _, err := indexer.Init(dir, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CODEGRAPH_WATCH_DEBOUNCE_MS", "50")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := newWatchCmd()
	cmd.SetArgs([]string{dir, "--verbose"})
	var stdout, stderr lockedBuffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- cmd.ExecuteContext(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
		}
	})
	waitForCLI(t, 5*time.Second, func() bool { return strings.Contains(stdout.String(), "Watching ") })
	runGitForWatchTest(t, dir, "merge", "--no-ff", "-qm", "merge feature", "feature")
	waitForCLI(t, 8*time.Second, func() bool {
		candidate, openErr := indexer.Open(dir, indexer.Options{})
		if openErr != nil {
			return false
		}
		defer func() { _ = candidate.Close() }()
		for _, graphStore := range candidate.Stores() {
			nodes, getErr := graphStore.GetNodesByName("Feature")
			if getErr == nil && len(nodes) > 0 {
				return true
			}
		}
		return false
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("watch command: %v; stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}

	idx, err = indexer.Open(dir, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	cleanDir := t.TempDir()
	for rel, content := range map[string][]byte{"base.go": base, "feature.go": feature, "main_only.go": mainOnly} {
		if err := os.WriteFile(filepath.Join(cleanDir, rel), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cleanIdx, _, err := indexer.Init(cleanDir, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cleanIdx.Close() }()
	if got, want := graphFingerprint(t, idx), graphFingerprint(t, cleanIdx); !reflect.DeepEqual(got, want) {
		t.Fatalf("post-merge watch graph differs from clean index\nwatch=%v\nclean=%v", got, want)
	}
}

func graphFingerprint(t *testing.T, idx *indexer.Indexer) []string {
	t.Helper()
	var fingerprint []string
	for _, graphStore := range idx.Stores() {
		nodes, err := graphStore.AllNodes()
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range nodes {
			fingerprint = append(fingerprint, fmt.Sprintf("node|%s|%s|%s|%s", node.ID, node.Kind, node.QualifiedName, node.FilePath))
		}
		edges, err := graphStore.AllEdges()
		if err != nil {
			t.Fatal(err)
		}
		for _, edge := range edges {
			fingerprint = append(fingerprint, fmt.Sprintf("edge|%s|%s|%s", edge.Source, edge.Kind, edge.Target))
		}
	}
	sort.Strings(fingerprint)
	return fingerprint
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
	t.Fatalf("condition not met within %v", timeout)
}
