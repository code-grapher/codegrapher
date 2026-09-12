package cli

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/watch"
	"github.com/spf13/cobra"
)

func newWatchCmd() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "watch [path]",
		Short: "Keep the graph current as files change",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stopSignals()

			startPath := watchStartPath(args)
			projectPath := resolveArg(args)
			if mismatch := indexer.DetectWorktreeIndexMismatch(startPath, projectPath); mismatch != nil {
				return fmt.Errorf("cannot watch a different git worktree's index:\n%s", indexer.WorktreeMismatchWarning(*mismatch))
			}
			if !indexer.IsInitialized(projectPath) {
				return fmt.Errorf("CodeGraph not initialized in %s; run 'codegrapher init' there first", projectPath)
			}

			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				return fmt.Errorf("open index: %w", err)
			}
			defer func() { _ = idx.Close() }()

			output := newWatchOutput(cmd.OutOrStdout(), cmd.ErrOrStderr(), verbose)
			pathFilter := indexer.NewPathFilter(projectPath)
			watcher := watch.NewWithPaths(projectPath, func(paths []string) (watch.SyncResult, error) {
				return reconcilePathsForWatch(idx, paths)
			}, watch.Options{
				IsIgnored:     pathFilter.IsIgnored,
				OnObservation: output.observe,
			})
			if err := watcher.StartWithError(); err != nil {
				return fmt.Errorf("cannot watch %s: %w", projectPath, err)
			}
			defer watcher.StopAndWait()

			// Establish the native watch set first, then reconcile. Events arriving
			// during startup remain pending for the normal debounced path, closing
			// the otherwise unavoidable scan-to-watch race.
			started := time.Now()
			const startupOperationID = 0
			output.startupStarted(started, startupOperationID)
			startupResult, err := reconcileStartupForWatch(idx)
			if err != nil {
				return fmt.Errorf("startup reconciliation: %w", err)
			}
			output.startupCompleted(time.Now(), startupOperationID, time.Since(started), startupResult)
			if ctx.Err() != nil {
				watcher.StopAndWait()
				return nil
			}
			output.watching(projectPath)

			select {
			case <-ctx.Done():
				watcher.StopAndWait()
				output.stopped(projectPath)
				return nil
			case fatalErr := <-watcher.FatalErrors():
				watcher.StopAndWait()
				return fmt.Errorf("watch failed: %w", fatalErr)
			}
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show received events, operation lifecycle, timings, and batch statistics")
	return cmd
}

func watchStartPath(args []string) string {
	raw := ""
	if len(args) > 0 {
		raw = args[0]
	} else {
		raw, _ = os.Getwd()
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return raw
	}
	return abs
}

func reconcilePathsForWatch(idx *indexer.Indexer, paths []string) (watch.SyncResult, error) {
	if paths == nil {
		return mapReconcileResult(idx.Rebuild(indexer.Options{}))
	}
	return mapReconcileResult(idx.SyncFiles(paths, indexer.Options{}))
}

func reconcileStartupForWatch(idx *indexer.Indexer) (watch.SyncResult, error) {
	return mapReconcileResult(idx.Sync(indexer.Options{}))
}

func mapReconcileResult(result indexer.SyncResult) (watch.SyncResult, error) {
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

type watchOutput struct {
	stdout  io.Writer
	stderr  io.Writer
	verbose bool
	mu      sync.Mutex
}

func newWatchOutput(stdout, stderr io.Writer, verbose bool) *watchOutput {
	return &watchOutput{stdout: stdout, stderr: stderr, verbose: verbose}
}

func (o *watchOutput) observe(observation watch.Observation) {
	o.mu.Lock()
	defer o.mu.Unlock()

	prefix := observation.At.UTC().Format(time.RFC3339Nano)
	switch observation.Kind {
	case watch.ObservationEventReceived:
		if o.verbose {
			_, _ = fmt.Fprintf(o.stdout, "%s event received op=%s path=%s\n", prefix, observation.Operation, observation.Path)
		}
	case watch.ObservationOperationStarted:
		if o.verbose {
			reason := observation.FullReconcileReason
			if reason == "" {
				reason = "none"
			}
			_, _ = fmt.Fprintf(o.stdout,
				"%s operation=%d started kind=%s reason=%s events=%d dirty_paths=%d coalesced=%d ignored=%d queued=%s debounce=%s\n",
				prefix, observation.OperationID, observation.Operation,
				reason,
				observation.EventsReceived, observation.DirtyPaths, observation.CoalescedEvents,
				observation.IgnoredEvents, observationDuration(observation.QueuedFor), observationDuration(observation.Debounce))
		}
	case watch.ObservationOperationCompleted:
		if o.verbose {
			reason := observation.FullReconcileReason
			if reason == "" {
				reason = "none"
			}
			_, _ = fmt.Fprintf(o.stdout,
				"%s operation=%d completed kind=%s duration=%s total=%s no_op=%t reason=%s events=%d dirty_paths=%d coalesced=%d ignored=%d checked=%d added=%d modified=%d removed=%d nodes_updated=%d full_reindex=%t\n",
				prefix, observation.OperationID, observation.Operation, observationDuration(observation.Duration),
				observationDuration(observation.TotalDuration), observation.NoOp, reason,
				observation.EventsReceived, observation.DirtyPaths, observation.CoalescedEvents, observation.IgnoredEvents,
				observation.Result.FilesChecked, observation.Result.FilesAdded,
				observation.Result.FilesModified, observation.Result.FilesRemoved,
				observation.Result.NodesUpdated, observation.Result.FullReindex)
		} else if observation.Result.FilesChanged > 0 || observation.Result.FullReindex {
			o.writeMaterialUpdate(observation.Result, observation.Duration)
		}
	case watch.ObservationOperationRetry:
		if o.verbose {
			_, _ = fmt.Fprintf(o.stderr,
				"%s operation=%d retry duration=%s reason=lock_unavailable events=%d dirty_paths=%d\n",
				prefix, observation.OperationID, observationDuration(observation.Duration),
				observation.EventsReceived, observation.DirtyPaths)
		}
	case watch.ObservationOperationFailed:
		_, _ = fmt.Fprintf(o.stderr, "%s operation=%d failed duration=%s error=%v\n",
			prefix, observation.OperationID, observationDuration(observation.Duration), observation.Err)
	case watch.ObservationWatcherError:
		_, _ = fmt.Fprintf(o.stderr, "%s watcher error=%v\n", prefix, observation.Err)
	}
}

func (o *watchOutput) startupStarted(at time.Time, operationID uint64) {
	if !o.verbose {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprintf(o.stdout, "%s operation=%d started kind=startup_reconcile\n",
		at.UTC().Format(time.RFC3339Nano), operationID)
}

func (o *watchOutput) startupCompleted(at time.Time, operationID uint64, duration time.Duration, result watch.SyncResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.verbose {
		_, _ = fmt.Fprintf(o.stdout,
			"%s operation=%d completed kind=startup_reconcile duration=%s total=%s no_op=%t checked=%d added=%d modified=%d removed=%d nodes_updated=%d full_reindex=%t\n",
			at.UTC().Format(time.RFC3339Nano), operationID, observationDuration(duration), observationDuration(duration),
			result.FilesChanged == 0 && !result.FullReindex, result.FilesChecked,
			result.FilesAdded, result.FilesModified, result.FilesRemoved, result.NodesUpdated, result.FullReindex)
		return
	}
	_, _ = fmt.Fprintf(o.stdout, "Reconciled %d files in %s\n", result.FilesChecked, observationDuration(duration))
}

func (o *watchOutput) watching(projectPath string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprintf(o.stdout, "Watching %s (press Ctrl-C to stop)\n", projectPath)
}

func (o *watchOutput) stopped(projectPath string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprintf(o.stdout, "Stopped watching %s\n", projectPath)
}

func (o *watchOutput) writeMaterialUpdate(result watch.SyncResult, duration time.Duration) {
	if result.FullReindex {
		_, _ = fmt.Fprintf(o.stdout, "Rebuilt index from %d files (%d nodes) in %s\n",
			result.FilesChecked, result.NodesUpdated, observationDuration(duration))
		return
	}
	word := "files"
	if result.FilesChanged == 1 {
		word = "file"
	}
	_, _ = fmt.Fprintf(o.stdout,
		"Updated %d %s (added=%d modified=%d removed=%d nodes=%d) in %s\n",
		result.FilesChanged, word, result.FilesAdded, result.FilesModified,
		result.FilesRemoved, result.NodesUpdated, observationDuration(duration))
}

func observationDuration(duration time.Duration) string {
	return formatDuration(duration.Milliseconds())
}
