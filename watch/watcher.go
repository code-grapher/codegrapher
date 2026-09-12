package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultDebounceMS is the default debounce delay before a sync is triggered
// after the last file-change event (2000 ms, matching the original).
const DefaultDebounceMS = 2000

// DefaultMaxDirWatches caps the number of simultaneously-watched directories
// on Linux (per-directory inotify path). Matches the original's 50 000.
const DefaultMaxDirWatches = 50_000

// SyncResult is the value returned by a successful sync callback.
type SyncResult struct {
	FilesChanged  int
	FilesChecked  int
	FilesAdded    int
	FilesModified int
	FilesRemoved  int
	NodesUpdated  int
	DurationMs    int64
	FullReindex   bool
}

// ObservationKind identifies one point in the watcher event or reconciliation
// lifecycle. Observations are diagnostics only and never drive graph updates.
type ObservationKind string

const (
	ObservationEventReceived      ObservationKind = "event_received"
	ObservationOperationStarted   ObservationKind = "operation_started"
	ObservationOperationCompleted ObservationKind = "operation_completed"
	ObservationOperationFailed    ObservationKind = "operation_failed"
	ObservationOperationRetry     ObservationKind = "operation_retry"
	ObservationWatcherError       ObservationKind = "watcher_error"
)

// Observation is a structured, optional diagnostic emitted by FileWatcher.
// It lets CLI and daemon callers render their own logs without coupling this
// package to a terminal or logging framework.
type Observation struct {
	Kind            ObservationKind
	At              time.Time
	Path            string
	Operation       string
	OperationID     uint64
	EventsReceived  uint64
	DirtyPaths      int
	CoalescedEvents uint64
	Duration        time.Duration
	Result          SyncResult
	Err             error
}

// SyncFunc is the callback the watcher invokes after each debounce window.
// It should return ErrLockUnavailable when the cross-process write lock is
// held; the watcher retries without clearing pendingFiles in that case.
type SyncFunc func() (SyncResult, error)

// SyncPathsFunc is the callback the watcher invokes with the exact dirty
// project-relative paths in the current debounce batch.
type SyncPathsFunc func(paths []string) (SyncResult, error)

// IsSourceFileFunc reports whether a project-relative POSIX path is a source
// file that should be indexed.
type IsSourceFileFunc func(relPath string) bool

// IsIgnoredFunc reports whether a project-relative POSIX path should be
// ignored entirely (not just non-source, but also not a directory to recurse
// into on Linux).
type IsIgnoredFunc func(relPath string) bool

// NowFunc returns the current wall-clock time. Injectable for tests.
type NowFunc func() time.Time

// ErrLockUnavailable signals that the sync callback could not acquire the
// cross-process write lock. The watcher keeps pendingFiles intact and
// reschedules rather than reporting this as an error. Matches the original's
// LockUnavailableError.
type LockUnavailableError struct{ msg string }

func (e *LockUnavailableError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return "codegraph file lock unavailable; another process is writing"
}

// NewLockUnavailableError wraps a message as a LockUnavailableError.
func NewLockUnavailableError(msg string) *LockUnavailableError {
	return &LockUnavailableError{msg: msg}
}

// IsLockUnavailableError reports whether err is (or wraps) a
// LockUnavailableError.
func IsLockUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*LockUnavailableError)
	return ok
}

// PendingFile is a source file the watcher observed since the last successful
// sync. Exposed via [FileWatcher.PendingFiles] so callers can flag stale
// results without blocking on a sync.
type PendingFile struct {
	// Path is the project-relative POSIX path (e.g. "src/foo.ts").
	Path string
	// FirstSeenMs is the wall-clock ms at the first event since the last sync.
	FirstSeenMs int64
	// LastSeenMs is the wall-clock ms at the most-recent event.
	LastSeenMs int64
	// Indexing is true when a sync is in flight that started after this
	// file's most-recent event — meaning the next successful sync will
	// absorb the edit.
	Indexing bool
}

type pendingEntry struct {
	firstSeenMs  int64
	lastSeenMs   int64
	lastEventSeq uint64
}

// Options configures a [FileWatcher].
type Options struct {
	// DebounceMs is the debounce delay in ms. 0 uses DefaultDebounceMS.
	// Override via CODEGRAPH_WATCH_DEBOUNCE_MS env var (read at construction).
	DebounceMs int

	// OnSyncComplete is called after each successful sync.
	OnSyncComplete func(SyncResult)

	// OnSyncError is called when syncFn returns an error that is NOT
	// ErrLockUnavailable.
	OnSyncError func(error)

	// OnObservation receives structured event and reconciliation diagnostics.
	// It must return promptly. The callback is never invoked while the watcher
	// mutex is held.
	OnObservation func(Observation)

	// IsSourceFile decides whether a project-relative path should be tracked.
	// Defaults to a built-in set of Go/TS/JS extensions.
	IsSourceFile IsSourceFileFunc

	// IsIgnored decides whether a project-relative path should be dropped
	// entirely (before the IsSourceFile check). Defaults to nil (nothing extra
	// ignored beyond .codegraph/ and .git/).
	IsIgnored IsIgnoredFunc

	// Now overrides the clock. Defaults to time.Now.
	Now NowFunc

	// MaxDirWatches caps the Linux per-directory watch count. 0 = DefaultMaxDirWatches.
	MaxDirWatches int

	// InertForTests disables all OS-level watchers. Events are only fed
	// through [FileWatcher.IngestEventForTests].
	InertForTests bool
}

// FileWatcher watches a project root for source-file changes and calls a
// debounced sync callback.
type FileWatcher struct {
	root     string
	syncFn   SyncPathsFunc
	opts     Options
	debounce time.Duration

	mu                sync.Mutex
	pending           map[string]*pendingEntry
	timer             *time.Timer
	syncing           bool
	syncStarted       time.Time
	stopped           bool
	ready             bool
	readyCh           chan struct{}
	eventSeq          uint64
	completedEventSeq uint64
	operationSeq      uint64
	syncWG            sync.WaitGroup

	// fsnotify watcher (nil until Start is called).
	fsw *fsnotify.Watcher
	// Set of watched directories for Linux recursive-emulation tracking.
	watchedDirs map[string]struct{}

	inert bool // set when InertForTests is true
}

// New creates a FileWatcher that has not yet started. Call [FileWatcher.Start]
// to begin watching.
func New(root string, syncFn SyncFunc, opts Options) *FileWatcher {
	return NewWithPaths(root, func([]string) (SyncResult, error) {
		return syncFn()
	}, opts)
}

// NewWithPaths creates a FileWatcher whose reconciliation callback receives
// the exact dirty paths in each batch. Call [FileWatcher.Start] to begin
// watching.
func NewWithPaths(root string, syncFn SyncPathsFunc, opts Options) *FileWatcher {
	debounceMs := opts.DebounceMs
	if debounceMs == 0 {
		if raw := os.Getenv("CODEGRAPH_WATCH_DEBOUNCE_MS"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				debounceMs = n
			}
		}
	}
	if debounceMs == 0 {
		debounceMs = DefaultDebounceMS
	}
	if opts.IsSourceFile == nil {
		opts.IsSourceFile = defaultIsSourceFile
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	maxDirs := opts.MaxDirWatches
	if maxDirs == 0 {
		if raw := os.Getenv("CODEGRAPH_MAX_DIR_WATCHES"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				maxDirs = n
			}
		}
	}
	if maxDirs == 0 {
		maxDirs = DefaultMaxDirWatches
	}
	opts.MaxDirWatches = maxDirs

	return &FileWatcher{
		root:        root,
		syncFn:      syncFn,
		opts:        opts,
		debounce:    time.Duration(debounceMs) * time.Millisecond,
		pending:     make(map[string]*pendingEntry),
		readyCh:     make(chan struct{}),
		watchedDirs: make(map[string]struct{}),
	}
}

// Start begins watching. Returns true if watching started, false if disabled
// (e.g., CODEGRAPH_NO_WATCH or WSL2 /mnt drive).
func (fw *FileWatcher) Start() bool {
	return fw.StartWithError() == nil
}

// StartWithError begins watching and returns the reason native watching could
// not be established. Start is retained as the compatibility bool API.
func (fw *FileWatcher) StartWithError() error {
	fw.mu.Lock()

	if fw.ready || fw.inert {
		fw.mu.Unlock()
		return nil // already started
	}
	fw.stopped = false

	// Check watch-disabled policy.
	if reason := WatchDisabledReason(fw.root, WatchProbe{}); reason != "" {
		fw.mu.Unlock()
		return fmt.Errorf("%s", reason)
	}

	if fw.opts.InertForTests {
		fw.inert = true
	} else {
		var err error
		fw.fsw, err = fsnotify.NewWatcher()
		if err != nil {
			fw.mu.Unlock()
			return err
		}
		if err := fw.addWatches(); err != nil {
			_ = fw.fsw.Close()
			fw.fsw = nil
			fw.watchedDirs = make(map[string]struct{})
			fw.mu.Unlock()
			fw.observe(Observation{Kind: ObservationWatcherError, Err: err})
			return err
		}
		go fw.readEvents(fw.fsw)
	}

	fw.pending = make(map[string]*pendingEntry)
	fw.ready = true
	close(fw.readyCh)

	// Register in the test registry (no-op if not a test).
	registerForTests(fw.root, fw)
	fw.mu.Unlock()
	return nil
}

// addWatches sets up fsnotify watches by walking the tree and adding a watch
// for every non-ignored directory. fsnotify does not expose a public recursive
// API (the path/... convention is test-only in v1.10), so we use the same
// per-directory strategy on all platforms. The cap (MaxDirWatches) bounds
// inotify usage on Linux and kqueue descriptor usage on macOS.
func (fw *FileWatcher) addWatches() error {
	return fw.watchTreeLocked(fw.root, false)
}

// watchTreeLocked recursively walks dir and adds an fsnotify watch for each
// non-ignored directory. Must be called with fw.mu held (or before goroutines
// start). If markExisting is true, source files already in the directory are
// added to pendingFiles.
func (fw *FileWatcher) watchTreeLocked(dir string, markExisting bool) error {
	if _, ok := fw.watchedDirs[dir]; !ok {
		if len(fw.watchedDirs) >= fw.opts.MaxDirWatches {
			return fmt.Errorf("directory-watch cap reached (%d); increase CODEGRAPH_MAX_DIR_WATCHES", fw.opts.MaxDirWatches)
		}
		if err := fw.fsw.Add(dir); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("watch directory %s: %w", dir, err)
		}
		fw.watchedDirs[dir] = struct{}{}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read watched directory %s: %w", dir, err)
	}
	for _, e := range entries {
		child := filepath.Join(dir, e.Name())
		if e.IsDir() {
			rel := toRelPOSIX(fw.root, child)
			if fw.isAlwaysIgnored(rel) {
				continue
			}
			if fw.opts.IsIgnored != nil && fw.opts.IsIgnored(rel+"/") {
				continue
			}
			if err := fw.watchTreeLocked(child, markExisting); err != nil {
				return err
			}
		} else if markExisting && e.Type().IsRegular() {
			rel := toRelPOSIX(fw.root, child)
			if !fw.isAlwaysIgnored(rel) &&
				(fw.opts.IsIgnored == nil || !fw.opts.IsIgnored(rel)) &&
				fw.opts.IsSourceFile(rel) {
				fw.recordPendingLocked(rel)
			}
		}
	}
	return nil
}

// readEvents processes fsnotify events in a goroutine. fsw is passed directly
// to avoid reading fw.fsw (which Stop may nil under the mutex) from a
// non-mutex-holding goroutine.
func (fw *FileWatcher) readEvents(fsw *fsnotify.Watcher) {
	for {
		select {
		case event, ok := <-fsw.Events:
			if !ok {
				return
			}
			fw.handleFSNotifyEvent(event)
		case err, ok := <-fsw.Errors:
			if !ok {
				return
			}
			fw.observe(Observation{Kind: ObservationWatcherError, Err: err})
		}
	}
}

// handleFSNotifyEvent routes an fsnotify event.
func (fw *FileWatcher) handleFSNotifyEvent(event fsnotify.Event) {
	name := filepath.ToSlash(event.Name)

	// For recursive watches, name is absolute; compute relative.
	rel := toRelPOSIX(fw.root, name)
	if rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return
	}
	fw.observe(Observation{
		Kind:      ObservationEventReceived,
		Path:      rel,
		Operation: event.Op.String(),
	})

	// A newly-created directory needs its own watch (per-directory strategy on
	// all platforms, since fsnotify v1.10 has no public recursive API).
	if event.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(name); err == nil && info.IsDir() {
			var watchErr error
			var shouldSchedule bool
			fw.mu.Lock()
			if !fw.isAlwaysIgnored(rel) &&
				(fw.opts.IsIgnored == nil || !fw.opts.IsIgnored(rel+"/")) {
				before := fw.eventSeq
				watchErr = fw.watchTreeLocked(name, true)
				shouldSchedule = fw.eventSeq > before
				if shouldSchedule {
					fw.scheduleSyncLocked()
				}
			}
			fw.mu.Unlock()
			if watchErr != nil {
				fw.observe(Observation{Kind: ObservationWatcherError, Path: rel, Err: watchErr})
			}
			return
		}
	}

	fw.handleChange(rel)
	if filepath.Base(rel) == ".gitignore" {
		var watchErr error
		fw.mu.Lock()
		before := fw.eventSeq
		watchErr = fw.watchTreeLocked(fw.root, true)
		if fw.eventSeq > before {
			fw.scheduleSyncLocked()
		}
		fw.mu.Unlock()
		if watchErr != nil {
			fw.observe(Observation{Kind: ObservationWatcherError, Path: rel, Err: watchErr})
		}
	}
}

// handleChange is the shared path for both real and synthetic events.
func (fw *FileWatcher) handleChange(rel string) {
	if rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return
	}
	if fw.isAlwaysIgnored(rel) {
		return
	}
	if fw.opts.IsIgnored != nil && fw.opts.IsIgnored(rel) {
		return
	}
	if !fw.opts.IsSourceFile(rel) {
		return
	}

	fw.mu.Lock()
	defer fw.mu.Unlock()

	if fw.stopped {
		return
	}
	if fw.ready {
		fw.recordPendingLocked(rel)
	}
	fw.scheduleSyncLocked()
}

func (fw *FileWatcher) recordPendingLocked(rel string) {
	now := fw.opts.Now().UnixMilli()
	fw.eventSeq++
	if e, ok := fw.pending[rel]; ok {
		e.lastSeenMs = now
		e.lastEventSeq = fw.eventSeq
	} else {
		fw.pending[rel] = &pendingEntry{
			firstSeenMs:  now,
			lastSeenMs:   now,
			lastEventSeq: fw.eventSeq,
		}
	}
}

func (fw *FileWatcher) scheduleSyncLocked() {
	if fw.timer != nil {
		fw.timer.Reset(fw.debounce)
		return
	}
	fw.timer = time.AfterFunc(fw.debounce, fw.flush)
}

// flush runs after the debounce window closes.
func (fw *FileWatcher) flush() {
	fw.mu.Lock()
	if fw.syncing || fw.stopped {
		fw.mu.Unlock()
		return
	}
	fw.syncing = true
	fw.timer = nil
	fw.syncStarted = fw.opts.Now()
	fw.operationSeq++
	operationID := fw.operationSeq
	batchEndSeq := fw.eventSeq
	eventsReceived := batchEndSeq - fw.completedEventSeq
	dirtyPaths := 0
	paths := make([]string, 0, len(fw.pending))
	for path, entry := range fw.pending {
		if entry.lastEventSeq <= batchEndSeq {
			dirtyPaths++
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	coalescedEvents := uint64(0)
	if eventsReceived > uint64(dirtyPaths) {
		coalescedEvents = eventsReceived - uint64(dirtyPaths)
	}
	startedAt := fw.syncStarted
	fw.syncWG.Add(1)
	fw.mu.Unlock()

	fw.observe(Observation{
		Kind:            ObservationOperationStarted,
		Operation:       "reconcile",
		OperationID:     operationID,
		EventsReceived:  eventsReceived,
		DirtyPaths:      dirtyPaths,
		CoalescedEvents: coalescedEvents,
		At:              startedAt,
	})

	result, err := fw.syncFn(paths)
	finishedAt := fw.opts.Now()
	duration := finishedAt.Sub(startedAt)
	if duration < 0 {
		duration = 0
	}

	fw.mu.Lock()
	fw.syncing = false
	var onComplete func(SyncResult)
	var onError func(error)
	observationKind := ObservationOperationCompleted
	if err == nil {
		// Remove entries whose most recent event predates this sync start.
		for path, e := range fw.pending {
			if e.lastEventSeq <= batchEndSeq {
				delete(fw.pending, path)
			}
		}
		fw.completedEventSeq = batchEndSeq
		onComplete = fw.opts.OnSyncComplete
	} else if IsLockUnavailableError(err) {
		// Lock-busy: keep pendingFiles intact, reschedule quietly.
		observationKind = ObservationOperationRetry
	} else {
		observationKind = ObservationOperationFailed
		onError = fw.opts.OnSyncError
	}
	// Re-schedule if there are still pending files.
	if len(fw.pending) > 0 && !fw.stopped {
		fw.scheduleSyncLocked()
	}
	fw.mu.Unlock()

	fw.observe(Observation{
		Kind:            observationKind,
		Operation:       "reconcile",
		OperationID:     operationID,
		EventsReceived:  eventsReceived,
		DirtyPaths:      dirtyPaths,
		CoalescedEvents: coalescedEvents,
		Duration:        duration,
		Result:          result,
		Err:             err,
		At:              finishedAt,
	})
	if onComplete != nil {
		onComplete(result)
	}
	if onError != nil {
		onError(err)
	}
	fw.syncWG.Done()
}

// Stop shuts down the watcher and clears state.
func (fw *FileWatcher) Stop() {
	fw.mu.Lock()
	fw.stopped = true
	if fw.timer != nil {
		fw.timer.Stop()
		fw.timer = nil
	}
	if fw.fsw != nil {
		_ = fw.fsw.Close()
		fw.fsw = nil
	}
	fw.mu.Unlock()

	// Do not let callers close the index while an in-flight reconciliation is
	// still writing to it.
	fw.syncWG.Wait()

	fw.mu.Lock()
	fw.watchedDirs = make(map[string]struct{})
	fw.inert = false
	fw.pending = make(map[string]*pendingEntry)
	fw.eventSeq = 0
	fw.completedEventSeq = 0
	fw.operationSeq = 0
	// Reset ready state so the watcher can be re-started.
	fw.ready = false
	fw.readyCh = make(chan struct{})
	fw.mu.Unlock()
	unregisterForTests(fw.root)
}

// IsActive reports whether the watcher is currently running.
func (fw *FileWatcher) IsActive() bool {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return !fw.stopped && (fw.fsw != nil || fw.inert)
}

// WaitUntilReady blocks until the watch set is established, or until the
// context deadline is reached.
func (fw *FileWatcher) WaitUntilReady(timeout time.Duration) error {
	select {
	case <-fw.readyCh:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("FileWatcher.WaitUntilReady timed out after %v", timeout)
	}
}

// PendingFiles returns a snapshot of files seen since the last successful sync.
func (fw *FileWatcher) PendingFiles() []PendingFile {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	result := make([]PendingFile, 0, len(fw.pending))
	for path, e := range fw.pending {
		indexing := fw.syncing &&
			!fw.syncStarted.IsZero() &&
			fw.syncStarted.UnixMilli() >= e.lastSeenMs
		result = append(result, PendingFile{
			Path:        path,
			FirstSeenMs: e.firstSeenMs,
			LastSeenMs:  e.lastSeenMs,
			Indexing:    indexing,
		})
	}
	return result
}

// IngestEventForTests feeds a synthetic project-relative path through the
// full filter → pendingFiles → debounce pipeline. Only for use in tests.
func (fw *FileWatcher) IngestEventForTests(relPath string) {
	rel := toRelPOSIX("", relPath)
	fw.observe(Observation{
		Kind:      ObservationEventReceived,
		Path:      rel,
		Operation: "SYNTHETIC",
	})
	fw.handleChange(rel)
}

func (fw *FileWatcher) observe(observation Observation) {
	if fw.opts.OnObservation == nil {
		return
	}
	if observation.At.IsZero() {
		observation.At = fw.opts.Now()
	}
	fw.opts.OnObservation(observation)
}

// isAlwaysIgnored reports whether rel (a project-relative POSIX path) is a
// directory that should never be watched, regardless of .gitignore.
func (fw *FileWatcher) isAlwaysIgnored(rel string) bool {
	top := rel
	if before, _, ok := strings.Cut(rel, "/"); ok {
		top = before
	}
	return isCodeGraphDataDir(top) || top == ".git" || rel == ".git" || strings.HasPrefix(rel, ".git/")
}

// isCodeGraphDataDir reports whether name is the codegraph data directory or a
// sibling variant (e.g., .codegraph-win). Ported from directory.ts.
func isCodeGraphDataDir(name string) bool {
	dir := codeGraphDirName()
	return name == ".codegraph" || name == dir || strings.HasPrefix(name, ".codegraph-")
}

// codeGraphDirName returns the configured data directory name (CODEGRAPH_DIR
// env var, defaulting to ".codegraph").
func codeGraphDirName() string {
	if d := os.Getenv("CODEGRAPH_DIR"); d != "" {
		return d
	}
	return ".codegraph"
}

// toRelPOSIX computes a POSIX-slash relative path from root to abs.
// If root is "" it just normalizes slashes.
func toRelPOSIX(root, abs string) string {
	if root == "" {
		return filepath.ToSlash(abs)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// defaultIsSourceFile is the built-in admission predicate for the watcher.
// Admission is decoupled from language detection: every path that survives the
// watcher's always-ignored and .gitignore checks (applied before this predicate
// in handleChange) is admitted, so unknown-language files become bare
// file-level nodes rather than being dropped.
func defaultIsSourceFile(rel string) bool {
	return true
}

// Test registry: maps project root → live watcher for IngestEventForTests.
var (
	testRegistryMu sync.Mutex
	testRegistry   = map[string]*FileWatcher{}
)

// registerForTests registers fw under root in the test registry.
func registerForTests(root string, fw *FileWatcher) {
	testRegistryMu.Lock()
	testRegistry[root] = fw
	testRegistryMu.Unlock()
}

func unregisterForTests(root string) {
	testRegistryMu.Lock()
	delete(testRegistry, root)
	testRegistryMu.Unlock()
}

// EmitEventForTests feeds a synthetic event to the live watcher registered for
// root. Returns false if no watcher is registered. For use in tests only.
func EmitEventForTests(root, relPath string) bool {
	testRegistryMu.Lock()
	fw, ok := testRegistry[root]
	testRegistryMu.Unlock()
	if !ok {
		return false
	}
	fw.IngestEventForTests(relPath)
	return true
}
