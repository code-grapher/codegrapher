package watch

import (
	"fmt"
	"os"
	"path/filepath"
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
	FilesChanged int
	DurationMs   int
}

// SyncFunc is the callback the watcher invokes after each debounce window.
// It should return ErrLockUnavailable when the cross-process write lock is
// held; the watcher retries without clearing pendingFiles in that case.
type SyncFunc func() (SyncResult, error)

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
	firstSeenMs int64
	lastSeenMs  int64
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
	root    string
	syncFn  SyncFunc
	opts    Options
	debounce time.Duration

	mu          sync.Mutex
	pending     map[string]*pendingEntry
	timer       *time.Timer
	syncing     bool
	syncStarted time.Time
	stopped     bool
	ready       bool
	readyCh     chan struct{}
	dirCapWarn  bool

	// fsnotify watcher (nil until Start is called).
	fsw *fsnotify.Watcher
	// Set of watched directories for Linux recursive-emulation tracking.
	watchedDirs map[string]struct{}

	inert bool // set when InertForTests is true
}

// New creates a FileWatcher that has not yet started. Call [FileWatcher.Start]
// to begin watching.
func New(root string, syncFn SyncFunc, opts Options) *FileWatcher {
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
	fw.mu.Lock()
	defer fw.mu.Unlock()

	if fw.ready || fw.inert {
		return true // already started
	}
	fw.stopped = false

	// Check watch-disabled policy.
	if reason := WatchDisabledReason(fw.root, WatchProbe{}); reason != "" {
		return false
	}

	if fw.opts.InertForTests {
		fw.inert = true
	} else {
		var err error
		fw.fsw, err = fsnotify.NewWatcher()
		if err != nil {
			return false
		}
		if err := fw.addWatches(); err != nil {
			_ = fw.fsw.Close()
			fw.fsw = nil
			return false
		}
		go fw.readEvents(fw.fsw)
	}

	fw.pending = make(map[string]*pendingEntry)
	fw.ready = true
	close(fw.readyCh)

	// Register in the test registry (no-op if not a test).
	registerForTests(fw.root, fw)
	return true
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
	if _, ok := fw.watchedDirs[dir]; ok {
		return nil
	}
	if len(fw.watchedDirs) >= fw.opts.MaxDirWatches {
		if !fw.dirCapWarn {
			fw.dirCapWarn = true
			// best-effort warning; no log package dependency
			_, _ = fmt.Fprintf(os.Stderr,
				"[codegrapher/watch] directory-watch cap reached (%d); "+
					"remaining subtrees rely on manual sync\n",
				fw.opts.MaxDirWatches)
		}
		return nil
	}

	if err := fw.fsw.Add(dir); err != nil {
		// ENOENT / permission denied — skip quietly.
		return nil
	}
	fw.watchedDirs[dir] = struct{}{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // can't read — skip
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
			_ = fw.watchTreeLocked(child, markExisting)
		} else if markExisting && e.Type().IsRegular() {
			rel := toRelPOSIX(fw.root, child)
			fw.recordPendingLocked(rel)
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
			_ = err // log if needed
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

	// A newly-created directory needs its own watch (per-directory strategy on
	// all platforms, since fsnotify v1.10 has no public recursive API).
	if event.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(name); err == nil && info.IsDir() {
			fw.mu.Lock()
			if !fw.isAlwaysIgnored(rel) &&
				(fw.opts.IsIgnored == nil || !fw.opts.IsIgnored(rel+"/")) {
				_ = fw.watchTreeLocked(name, true)
			}
			fw.mu.Unlock()
			return
		}
	}

	fw.handleChange(rel)
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
	if e, ok := fw.pending[rel]; ok {
		e.lastSeenMs = now
	} else {
		fw.pending[rel] = &pendingEntry{firstSeenMs: now, lastSeenMs: now}
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
	fw.mu.Unlock()

	result, err := fw.syncFn()

	fw.mu.Lock()
	fw.syncing = false
	if err == nil {
		// Remove entries whose most recent event predates this sync start.
		for path, e := range fw.pending {
			if time.UnixMilli(e.lastSeenMs).Compare(fw.syncStarted) <= 0 {
				delete(fw.pending, path)
			}
		}
		if fw.opts.OnSyncComplete != nil {
			fw.opts.OnSyncComplete(result)
		}
	} else if IsLockUnavailableError(err) {
		// Lock-busy: keep pendingFiles intact, reschedule quietly.
	} else {
		if fw.opts.OnSyncError != nil {
			fw.opts.OnSyncError(err)
		}
	}
	// Re-schedule if there are still pending files.
	if len(fw.pending) > 0 && !fw.stopped {
		fw.scheduleSyncLocked()
	}
	fw.mu.Unlock()
}

// Stop shuts down the watcher and clears state.
func (fw *FileWatcher) Stop() {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	fw.stopped = true
	if fw.timer != nil {
		fw.timer.Stop()
		fw.timer = nil
	}
	if fw.fsw != nil {
		_ = fw.fsw.Close()
		fw.fsw = nil
	}
	fw.watchedDirs = make(map[string]struct{})
	fw.dirCapWarn = false
	fw.inert = false
	fw.pending = make(map[string]*pendingEntry)
	// Reset ready state so the watcher can be re-started.
	fw.ready = false
	fw.readyCh = make(chan struct{})
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
	fw.handleChange(toRelPOSIX("", relPath))
}

// isAlwaysIgnored reports whether rel (a project-relative POSIX path) is a
// directory that should never be watched, regardless of .gitignore.
func (fw *FileWatcher) isAlwaysIgnored(rel string) bool {
	top := rel
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		top = rel[:i]
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

// defaultIsSourceFile is the built-in is-source-file predicate. Matches the
// Go and TypeScript/JavaScript extensions that the port indexes.
func defaultIsSourceFile(rel string) bool {
	if i := strings.LastIndexByte(rel, '.'); i >= 0 {
		switch strings.ToLower(rel[i:]) {
		case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
			return true
		}
	}
	return false
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
