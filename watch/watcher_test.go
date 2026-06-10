package watch_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/specscore/codegrapher/watch"
)

// waitFor polls condition until it returns true or timeout elapses.
func waitFor(t *testing.T, condition func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out after %v", timeout)
}

// newInertWatcher creates a watcher in inert-for-tests mode for testDir.
// syncFn is called after each debounce window.
func newInertWatcher(t *testing.T, testDir string, syncFn watch.SyncFunc, opts watch.Options) *watch.FileWatcher {
	t.Helper()
	opts.InertForTests = true
	if opts.DebounceMs == 0 {
		opts.DebounceMs = 100 // fast debounce for tests
	}
	return watch.New(testDir, syncFn, opts)
}

func TestStartStop(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	syncFn := func() (watch.SyncResult, error) {
		calls.Add(1)
		return watch.SyncResult{}, nil
	}
	w := newInertWatcher(t, dir, syncFn, watch.Options{})

	if w.IsActive() {
		t.Fatal("should not be active before Start")
	}
	if !w.Start() {
		t.Fatal("Start returned false")
	}
	if !w.IsActive() {
		t.Fatal("should be active after Start")
	}
	w.Stop()
	if w.IsActive() {
		t.Fatal("should not be active after Stop")
	}
}

func TestDoubleStart(t *testing.T) {
	dir := t.TempDir()
	syncFn := func() (watch.SyncResult, error) { return watch.SyncResult{}, nil }
	w := newInertWatcher(t, dir, syncFn, watch.Options{})

	if !w.Start() {
		t.Fatal("first Start failed")
	}
	if !w.Start() {
		t.Fatal("second Start should return true (idempotent)")
	}
	w.Stop()
}

func TestDoubleStop(t *testing.T) {
	dir := t.TempDir()
	syncFn := func() (watch.SyncResult, error) { return watch.SyncResult{}, nil }
	w := newInertWatcher(t, dir, syncFn, watch.Options{})
	w.Start()
	w.Stop()
	w.Stop() // must not panic
}

// TestWaitUntilReady confirms that WaitUntilReady returns immediately after Start.
func TestWaitUntilReady(t *testing.T) {
	dir := t.TempDir()
	syncFn := func() (watch.SyncResult, error) { return watch.SyncResult{}, nil }
	w := newInertWatcher(t, dir, syncFn, watch.Options{})
	w.Start()
	if err := w.WaitUntilReady(time.Second); err != nil {
		t.Fatal(err)
	}
	w.Stop()
}

// TestDebounceCoalesces verifies that rapid events produce a single sync call.
func TestDebounceCoalesces(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	syncFn := func() (watch.SyncResult, error) {
		calls.Add(1)
		return watch.SyncResult{FilesChanged: 1}, nil
	}
	w := newInertWatcher(t, dir, syncFn, watch.Options{DebounceMs: 200})
	w.Start()
	_ = w.WaitUntilReady(time.Second)

	// Five rapid-fire events within 200 ms debounce window.
	for i := 0; i < 5; i++ {
		watch.EmitEventForTests(dir, "src/file.ts")
		time.Sleep(20 * time.Millisecond)
	}

	waitFor(t, func() bool { return calls.Load() > 0 }, 2*time.Second)

	// The debounce must collapse them into one call.
	if n := calls.Load(); n != 1 {
		t.Errorf("expected 1 sync call, got %d", n)
	}
	w.Stop()
}

// TestFilterNonSourceFile verifies that non-source files do not trigger a sync.
func TestFilterNonSourceFile(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	syncFn := func() (watch.SyncResult, error) {
		calls.Add(1)
		return watch.SyncResult{}, nil
	}
	w := newInertWatcher(t, dir, syncFn, watch.Options{DebounceMs: 150})
	w.Start()
	_ = w.WaitUntilReady(time.Second)

	watch.EmitEventForTests(dir, "README.md")

	time.Sleep(300 * time.Millisecond)
	if calls.Load() != 0 {
		t.Errorf("sync should not be called for non-source file, got %d calls", calls.Load())
	}
	w.Stop()
}

// TestFilterCodeGraphDir verifies that .codegraph directory events are ignored.
func TestFilterCodeGraphDir(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	syncFn := func() (watch.SyncResult, error) {
		calls.Add(1)
		return watch.SyncResult{}, nil
	}
	w := newInertWatcher(t, dir, syncFn, watch.Options{DebounceMs: 150})
	w.Start()
	_ = w.WaitUntilReady(time.Second)

	watch.EmitEventForTests(dir, ".codegraph/codegraph.db")

	time.Sleep(300 * time.Millisecond)
	if calls.Load() != 0 {
		t.Errorf("sync should not trigger for .codegraph paths, got %d", calls.Load())
	}
	w.Stop()
}

// TestFilterGitDir verifies that .git directory events are ignored.
func TestFilterGitDir(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	syncFn := func() (watch.SyncResult, error) {
		calls.Add(1)
		return watch.SyncResult{}, nil
	}
	w := newInertWatcher(t, dir, syncFn, watch.Options{DebounceMs: 150})
	w.Start()
	_ = w.WaitUntilReady(time.Second)

	watch.EmitEventForTests(dir, ".git/COMMIT_EDITMSG")

	time.Sleep(300 * time.Millisecond)
	if calls.Load() != 0 {
		t.Errorf("sync should not trigger for .git paths, got %d", calls.Load())
	}
	w.Stop()
}

// TestPendingFilesBeforeSync verifies that getPendingFiles is populated before
// the debounce fires (#403 equivalent).
func TestPendingFilesBeforeSync(t *testing.T) {
	dir := t.TempDir()
	syncFn := func() (watch.SyncResult, error) { return watch.SyncResult{}, nil }
	// Long debounce so we can assert before sync fires.
	w := newInertWatcher(t, dir, syncFn, watch.Options{DebounceMs: 5000})
	w.Start()
	_ = w.WaitUntilReady(time.Second)

	if pf := w.PendingFiles(); len(pf) != 0 {
		t.Fatalf("pending files should be empty before any event, got %v", pf)
	}

	watch.EmitEventForTests(dir, "src/pending.go")

	pf := w.PendingFiles()
	found := false
	for _, p := range pf {
		if p.Path == "src/pending.go" {
			found = true
			if p.FirstSeenMs <= 0 {
				t.Error("FirstSeenMs should be > 0")
			}
			if p.LastSeenMs < p.FirstSeenMs {
				t.Error("LastSeenMs should be >= FirstSeenMs")
			}
			if p.Indexing {
				t.Error("Indexing should be false before debounce fires")
			}
		}
	}
	if !found {
		t.Errorf("expected src/pending.go in pending files, got %v", pf)
	}
	w.Stop()
}

// TestPendingFilesCleared verifies that after a successful sync the entry is
// removed from pendingFiles.
func TestPendingFilesCleared(t *testing.T) {
	dir := t.TempDir()
	syncFn := func() (watch.SyncResult, error) { return watch.SyncResult{FilesChanged: 1}, nil }
	w := newInertWatcher(t, dir, syncFn, watch.Options{DebounceMs: 100})
	w.Start()
	_ = w.WaitUntilReady(time.Second)

	watch.EmitEventForTests(dir, "src/fresh.go")

	if len(w.PendingFiles()) == 0 {
		t.Fatal("pending files should contain the emitted path")
	}

	waitFor(t, func() bool {
		for _, p := range w.PendingFiles() {
			if p.Path == "src/fresh.go" {
				return false
			}
		}
		return true
	}, 2*time.Second)

	if len(w.PendingFiles()) != 0 {
		t.Errorf("pending files should be empty after sync, got %v", w.PendingFiles())
	}
	w.Stop()
}

// TestPendingFilesRetainedOnSyncError verifies that a sync error leaves
// pendingFiles untouched.
func TestPendingFilesRetainedOnSyncError(t *testing.T) {
	dir := t.TempDir()

	var attempt atomic.Int32
	var errorCallbacks atomic.Int32
	syncFn := func() (watch.SyncResult, error) {
		n := attempt.Add(1)
		if n == 1 {
			return watch.SyncResult{}, errors.New("boom")
		}
		return watch.SyncResult{FilesChanged: 1}, nil
	}
	onErr := func(err error) { errorCallbacks.Add(1) }

	w := newInertWatcher(t, dir, syncFn, watch.Options{
		DebounceMs:  100,
		OnSyncError: onErr,
	})
	w.Start()
	_ = w.WaitUntilReady(time.Second)

	watch.EmitEventForTests(dir, "src/will-fail.go")

	// Wait for first sync to fail.
	waitFor(t, func() bool { return errorCallbacks.Load() > 0 }, 2*time.Second)

	// File must still be pending after the failed sync.
	found := false
	for _, p := range w.PendingFiles() {
		if p.Path == "src/will-fail.go" {
			found = true
		}
	}
	if !found {
		t.Error("pending file should remain after sync error")
	}

	// Retry succeeds automatically; pending entry clears.
	waitFor(t, func() bool {
		for _, p := range w.PendingFiles() {
			if p.Path == "src/will-fail.go" {
				return false
			}
		}
		return true
	}, 3*time.Second)
	w.Stop()
}

// TestLockUnavailableReschedules verifies the LockUnavailableError path:
// no onSyncError called, pendingFiles preserved, retry succeeds.
func TestLockUnavailableReschedules(t *testing.T) {
	dir := t.TempDir()

	var attempt atomic.Int32
	var completions atomic.Int32
	var errorCallbacks atomic.Int32

	syncFn := func() (watch.SyncResult, error) {
		n := attempt.Add(1)
		if n == 1 {
			return watch.SyncResult{}, watch.NewLockUnavailableError("")
		}
		return watch.SyncResult{FilesChanged: 1}, nil
	}
	onComplete := func(_ watch.SyncResult) { completions.Add(1) }
	onErr := func(_ error) { errorCallbacks.Add(1) }

	w := newInertWatcher(t, dir, syncFn, watch.Options{
		DebounceMs:     100,
		OnSyncComplete: onComplete,
		OnSyncError:    onErr,
	})
	w.Start()
	_ = w.WaitUntilReady(time.Second)

	watch.EmitEventForTests(dir, "src/locked.go")

	// Wait for first attempt (lock busy).
	waitFor(t, func() bool { return attempt.Load() >= 1 }, 2*time.Second)

	// Pending file must still be present after lock failure.
	found := false
	for _, p := range w.PendingFiles() {
		if p.Path == "src/locked.go" {
			found = true
		}
	}
	if !found {
		t.Error("pending file should be retained after LockUnavailableError")
	}
	if errorCallbacks.Load() != 0 {
		t.Errorf("onSyncError must NOT be called for LockUnavailableError, got %d", errorCallbacks.Load())
	}
	if completions.Load() != 0 {
		t.Errorf("onSyncComplete must NOT be called after lock failure, got %d", completions.Load())
	}

	// Retry: second attempt succeeds and clears pending.
	waitFor(t, func() bool { return completions.Load() >= 1 }, 3*time.Second)
	waitFor(t, func() bool {
		for _, p := range w.PendingFiles() {
			if p.Path == "src/locked.go" {
				return false
			}
		}
		return true
	}, 2*time.Second)

	if errorCallbacks.Load() != 0 {
		t.Errorf("onSyncError should not be called at all, got %d", errorCallbacks.Load())
	}
	w.Stop()
}

// TestOnSyncComplete verifies the callback is invoked with the correct result.
func TestOnSyncComplete(t *testing.T) {
	dir := t.TempDir()
	syncFn := func() (watch.SyncResult, error) {
		return watch.SyncResult{FilesChanged: 2, DurationMs: 50}, nil
	}
	var got watch.SyncResult
	var gotCalled atomic.Bool
	onComplete := func(r watch.SyncResult) {
		got = r
		gotCalled.Store(true)
	}
	w := newInertWatcher(t, dir, syncFn, watch.Options{
		DebounceMs:     100,
		OnSyncComplete: onComplete,
	})
	w.Start()
	_ = w.WaitUntilReady(time.Second)

	watch.EmitEventForTests(dir, "src/test.go")

	waitFor(t, func() bool { return gotCalled.Load() }, 2*time.Second)
	if got.FilesChanged != 2 || got.DurationMs != 50 {
		t.Errorf("onSyncComplete got %+v, want {2 50}", got)
	}
	w.Stop()
}

// TestOnSyncError verifies the error callback is invoked on sync failure.
func TestOnSyncError(t *testing.T) {
	dir := t.TempDir()
	syncFn := func() (watch.SyncResult, error) {
		return watch.SyncResult{}, errors.New("sync failed")
	}
	var gotErr error
	var errCalled atomic.Bool
	onErr := func(e error) {
		gotErr = e
		errCalled.Store(true)
	}
	w := newInertWatcher(t, dir, syncFn, watch.Options{
		DebounceMs:  100,
		OnSyncError: onErr,
	})
	w.Start()
	_ = w.WaitUntilReady(time.Second)

	watch.EmitEventForTests(dir, "src/test.go")

	waitFor(t, func() bool { return errCalled.Load() }, 2*time.Second)
	if gotErr == nil || gotErr.Error() != "sync failed" {
		t.Errorf("expected 'sync failed', got %v", gotErr)
	}
	w.Stop()
}

// TestWatchDisabledReason_NoWatch verifies CODEGRAPH_NO_WATCH=1 disables watching.
func TestWatchDisabledReason_NoWatch(t *testing.T) {
	probe := watch.WatchProbe{Env: map[string]string{"CODEGRAPH_NO_WATCH": "1"}}
	reason := watch.WatchDisabledReason("/some/project", probe)
	if reason == "" {
		t.Error("expected a disabled reason for CODEGRAPH_NO_WATCH=1")
	}
}

// TestWatchDisabledReason_ForceWatch verifies CODEGRAPH_FORCE_WATCH=1 overrides WSL detection.
func TestWatchDisabledReason_ForceWatch(t *testing.T) {
	trueVal := true
	probe := watch.WatchProbe{
		Env:   map[string]string{"CODEGRAPH_FORCE_WATCH": "1"},
		IsWSL: &trueVal,
	}
	reason := watch.WatchDisabledReason("/mnt/c/project", probe)
	if reason != "" {
		t.Errorf("CODEGRAPH_FORCE_WATCH=1 should enable watching, got reason %q", reason)
	}
}

// TestWatchDisabledReason_WSL verifies WSL2 /mnt drive detection.
func TestWatchDisabledReason_WSL(t *testing.T) {
	trueVal := true
	probe := watch.WatchProbe{IsWSL: &trueVal}
	if reason := watch.WatchDisabledReason("/mnt/c/project", probe); reason == "" {
		t.Error("expected disabled for WSL /mnt/c path")
	}
	if reason := watch.WatchDisabledReason("/mnt/wsl/stuff", probe); reason != "" {
		t.Errorf("WSL /mnt/wsl should NOT be disabled, got %q", reason)
	}
	if reason := watch.WatchDisabledReason("/home/user/project", probe); reason != "" {
		t.Errorf("WSL non-/mnt path should NOT be disabled, got %q", reason)
	}
}

// TestCustomIsSourceFile verifies the injectable IsSourceFile predicate.
func TestCustomIsSourceFile(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	syncFn := func() (watch.SyncResult, error) {
		calls.Add(1)
		return watch.SyncResult{}, nil
	}
	// Only .rb files are "source" for this test.
	isRuby := func(rel string) bool {
		return len(rel) > 3 && rel[len(rel)-3:] == ".rb"
	}
	w := newInertWatcher(t, dir, syncFn, watch.Options{
		DebounceMs:   150,
		IsSourceFile: isRuby,
	})
	w.Start()
	_ = w.WaitUntilReady(time.Second)

	// This event would match the default predicate but not .rb-only.
	watch.EmitEventForTests(dir, "src/app.ts")
	time.Sleep(300 * time.Millisecond)
	if calls.Load() != 0 {
		t.Errorf("app.ts should be ignored by .rb-only predicate, got %d calls", calls.Load())
	}

	// This event matches .rb.
	watch.EmitEventForTests(dir, "src/app.rb")
	waitFor(t, func() bool { return calls.Load() > 0 }, 2*time.Second)
	w.Stop()
}

// TestRealWatcher is an end-to-end test that uses a real fsnotify watcher and
// a real file write. Skipped in short mode.
func TestRealWatcher(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-watcher test in short mode")
	}

	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var syncCalls atomic.Int32
	syncFn := func() (watch.SyncResult, error) {
		syncCalls.Add(1)
		return watch.SyncResult{FilesChanged: 1}, nil
	}

	w := watch.New(dir, syncFn, watch.Options{
		DebounceMs: 300,
	})
	if !w.Start() {
		t.Skip("watcher could not start (unsupported environment)")
	}
	if err := w.WaitUntilReady(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	// Give the watcher time to settle before writing.
	time.Sleep(100 * time.Millisecond)

	// Write a real source file.
	if err := os.WriteFile(filepath.Join(srcDir, "added.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool { return syncCalls.Load() > 0 }, 8*time.Second)
	if syncCalls.Load() == 0 {
		t.Error("expected at least one sync call after real file write")
	}
	w.Stop()
}
