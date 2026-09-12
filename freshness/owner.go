// Package freshness owns one repository's index, native watcher, startup
// reconciliation, generation barrier, health, and joined shutdown. Foreground
// and daemon lifecycles share this owner so process management never forks the
// graph mutation path.
package freshness

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/watch"
)

// ReconcilePathsFunc reconciles exact dirty paths; nil requests a full rebuild.
type ReconcilePathsFunc func(*indexer.Indexer, []string) (watch.SyncResult, error)

// ReconcileStartupFunc reconciles authoritative repository state at startup.
type ReconcileStartupFunc func(*indexer.Indexer) (watch.SyncResult, error)

// Options configures a repository freshness owner.
type Options struct {
	Watch            watch.Options
	ReconcilePaths   ReconcilePathsFunc
	ReconcileStartup ReconcileStartupFunc
	OnStartupStarted func(time.Time)
	OnStartupDone    func(time.Time, time.Duration, watch.SyncResult, error)
}

// StartupResult describes the reconciliation completed before readiness.
type StartupResult struct {
	Result   watch.SyncResult
	Duration time.Duration
}

// Status is a point-in-time freshness health snapshot.
type Status struct {
	ProjectPath           string
	WatchReady            bool
	IndexCurrent          bool
	PendingDirtyPaths     int
	WholeTreeDirty        bool
	AcceptedGeneration    uint64
	CompletedGeneration   uint64
	LastSuccessfulUpdate  time.Time
	LastReconciliation    time.Time
	LastOperationDuration time.Duration
	LastError             string
}

// Owner holds one open index and its watcher.
type Owner struct {
	root    string
	indexer *indexer.Indexer
	watcher *watch.FileWatcher

	mu                    sync.Mutex
	watchReady            bool
	indexCurrent          bool
	lastSuccessfulUpdate  time.Time
	lastReconciliation    time.Time
	lastOperationDuration time.Duration
	lastError             string
	closeOnce             sync.Once
	closeErr              error
}

// Start establishes native coverage first, runs startup reconciliation, then
// drains the accepted event generation captured after that reconciliation.
// It returns only at a linearized current point or after joined cleanup.
func Start(ctx context.Context, root string, opts Options) (*Owner, StartupResult, error) {
	if !indexer.IsInitialized(root) {
		return nil, StartupResult{}, fmt.Errorf("CodeGraph not initialized in %s; run 'codegrapher init' there first", root)
	}
	idx, err := indexer.Open(root, indexer.Options{})
	if err != nil {
		return nil, StartupResult{}, fmt.Errorf("open index: %w", err)
	}
	owner := &Owner{root: root, indexer: idx}
	pathReconcile := opts.ReconcilePaths
	if pathReconcile == nil {
		pathReconcile = ReconcilePaths
	}
	startupReconcile := opts.ReconcileStartup
	if startupReconcile == nil {
		startupReconcile = ReconcileStartup
	}
	externalObservation := opts.Watch.OnObservation
	opts.Watch.OnObservation = func(observation watch.Observation) {
		owner.observe(observation)
		if externalObservation != nil {
			externalObservation(observation)
		}
	}
	pathFilter := indexer.NewPathFilter(root)
	if opts.Watch.IsIgnored == nil {
		opts.Watch.IsIgnored = pathFilter.IsIgnored
	}
	owner.watcher = watch.NewWithPaths(root, func(paths []string) (watch.SyncResult, error) {
		return pathReconcile(idx, paths)
	}, opts.Watch)
	if err := owner.watcher.StartWithError(); err != nil {
		_ = owner.Close()
		return nil, StartupResult{}, fmt.Errorf("establish watch coverage: %w", err)
	}
	owner.mu.Lock()
	owner.watchReady = true
	owner.mu.Unlock()

	startedAt := time.Now()
	if opts.OnStartupStarted != nil {
		opts.OnStartupStarted(startedAt)
	}
	result, reconcileErr := startupReconcile(idx)
	finishedAt := time.Now()
	duration := finishedAt.Sub(startedAt)
	startup := StartupResult{Result: result, Duration: duration}
	if opts.OnStartupDone != nil {
		opts.OnStartupDone(finishedAt, duration, result, reconcileErr)
	}
	owner.recordStartup(finishedAt, duration, reconcileErr)
	if reconcileErr != nil {
		_ = owner.Close()
		return nil, startup, fmt.Errorf("startup reconciliation: %w", reconcileErr)
	}
	if err := owner.watcher.Drain(ctx); err != nil {
		_ = owner.Close()
		return nil, startup, fmt.Errorf("drain startup event generation: %w", err)
	}
	owner.mu.Lock()
	owner.indexCurrent = true
	owner.mu.Unlock()
	return owner, startup, nil
}

// ReconcilePaths uses the canonical indexer path-aware/full-rebuild boundary.
func ReconcilePaths(idx *indexer.Indexer, paths []string) (watch.SyncResult, error) {
	if paths == nil {
		return MapSyncResult(idx.Rebuild(indexer.Options{}))
	}
	return MapSyncResult(idx.SyncFiles(paths, indexer.Options{}))
}

// ReconcileStartup performs authoritative incremental startup reconciliation.
func ReconcileStartup(idx *indexer.Indexer) (watch.SyncResult, error) {
	return MapSyncResult(idx.Sync(indexer.Options{}))
}

// MapSyncResult maps indexer outcomes into watcher lifecycle semantics.
func MapSyncResult(result indexer.SyncResult) (watch.SyncResult, error) {
	mapped := watch.SyncResult{
		FilesChanged:  result.FilesAdded + result.FilesModified + result.FilesRemoved,
		FilesChecked:  result.FilesChecked,
		FilesAdded:    result.FilesAdded,
		FilesModified: result.FilesModified,
		FilesRemoved:  result.FilesRemoved,
		NodesUpdated:  result.NodesUpdated,
		DurationMs:    result.DurationMs,
		FullReindex:   result.FullReindex,
	}
	if len(result.Errors) > 0 {
		messages := make([]string, 0, len(result.Errors))
		for _, extractionErr := range result.Errors {
			message := extractionErr.Message
			if extractionErr.FilePath != "" {
				message = extractionErr.FilePath + ": " + message
			}
			messages = append(messages, message)
		}
		return mapped, fmt.Errorf("index reconciliation failed: %s", strings.Join(messages, "; "))
	}
	if result.LockUnavailable {
		return mapped, watch.NewLockUnavailableError("")
	}
	return mapped, nil
}

func (o *Owner) recordStartup(at time.Time, duration time.Duration, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastReconciliation = at
	o.lastOperationDuration = duration
	if err != nil {
		o.indexCurrent = false
		o.lastError = err.Error()
		return
	}
	o.lastSuccessfulUpdate = at
	o.lastError = ""
}

func (o *Owner) observe(observation watch.Observation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch observation.Kind {
	case watch.ObservationOperationStarted:
		o.indexCurrent = false
	case watch.ObservationOperationCompleted:
		o.indexCurrent = true
		o.lastReconciliation = observation.At
		o.lastSuccessfulUpdate = observation.At
		o.lastOperationDuration = observation.Duration
		o.lastError = ""
	case watch.ObservationOperationRetry, watch.ObservationOperationFailed:
		o.indexCurrent = false
		o.lastReconciliation = observation.At
		o.lastOperationDuration = observation.Duration
		if observation.Err != nil {
			o.lastError = observation.Err.Error()
		}
	case watch.ObservationWatcherError:
		o.watchReady = false
		o.indexCurrent = false
		if observation.Err != nil {
			o.lastError = observation.Err.Error()
		}
	}
}

// Status returns live health, deriving currency from watcher generations so an
// older successful operation cannot hide later queued dirtiness.
func (o *Owner) Status() Status {
	dirty := o.watcher.DirtyState()
	o.mu.Lock()
	defer o.mu.Unlock()
	current := o.indexCurrent && dirty.CompletedGeneration == dirty.AcceptedGeneration &&
		!dirty.WholeTree && len(dirty.Paths) == 0
	return Status{
		ProjectPath:           o.root,
		WatchReady:            o.watchReady && o.watcher.IsActive(),
		IndexCurrent:          current,
		PendingDirtyPaths:     len(dirty.Paths),
		WholeTreeDirty:        dirty.WholeTree,
		AcceptedGeneration:    dirty.AcceptedGeneration,
		CompletedGeneration:   dirty.CompletedGeneration,
		LastSuccessfulUpdate:  o.lastSuccessfulUpdate,
		LastReconciliation:    o.lastReconciliation,
		LastOperationDuration: o.lastOperationDuration,
		LastError:             o.lastError,
	}
}

// Wait blocks until cancellation or a native watcher failure.
func (o *Owner) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case err := <-o.watcher.FatalErrors():
		return fmt.Errorf("watch failed: %w", err)
	}
}

// Close joins watcher work before closing the index. It is idempotent.
func (o *Owner) Close() error {
	o.closeOnce.Do(func() {
		if o.watcher != nil {
			o.watcher.StopAndWait()
		}
		o.mu.Lock()
		o.watchReady = false
		o.indexCurrent = false
		o.mu.Unlock()
		if o.indexer != nil {
			o.closeErr = o.indexer.Close()
		}
	})
	return o.closeErr
}
