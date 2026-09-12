package cli

import (
	"fmt"
	"io"
	"os"
	"os/signal"
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
			projectPath := resolveArg(args)
			if !indexer.IsInitialized(projectPath) {
				return fmt.Errorf("CodeGraph not initialized in %s; run 'codegrapher init' there first", projectPath)
			}

			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				return fmt.Errorf("open index: %w", err)
			}
			defer func() { _ = idx.Close() }()

			output := newWatchOutput(cmd.OutOrStdout(), cmd.ErrOrStderr(), verbose)
			watcher := watch.New(projectPath, func() (watch.SyncResult, error) {
				return reconcileForWatch(idx)
			}, watch.Options{OnObservation: output.observe})
			if !watcher.Start() {
				reason := watch.WatchDisabledReason(projectPath, watch.WatchProbe{})
				if reason == "" {
					reason = "the native filesystem watcher could not be started"
				}
				return fmt.Errorf("cannot watch %s: %s", projectPath, reason)
			}
			defer watcher.Stop()

			// Establish the native watch set first, then reconcile. Events arriving
			// during startup remain pending for the normal debounced path, closing
			// the otherwise unavoidable scan-to-watch race.
			started := time.Now()
			output.startupStarted(started)
			startupResult, err := reconcileForWatch(idx)
			if err != nil {
				return fmt.Errorf("startup reconciliation: %w", err)
			}
			output.startupCompleted(time.Now(), time.Since(started), startupResult)
			output.watching(projectPath)

			ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stopSignals()
			<-ctx.Done()
			output.stopped(projectPath)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show received events, operation lifecycle, timings, and batch statistics")
	return cmd
}

func reconcileForWatch(idx *indexer.Indexer) (watch.SyncResult, error) {
	result := idx.Sync(indexer.Options{})
	if result.FilesChecked == 0 && result.DurationMs == 0 {
		return watch.SyncResult{}, watch.NewLockUnavailableError("")
	}
	return watch.SyncResult{
		FilesChanged:  result.FilesAdded + result.FilesModified + result.FilesRemoved,
		FilesChecked:  result.FilesChecked,
		FilesAdded:    result.FilesAdded,
		FilesModified: result.FilesModified,
		FilesRemoved:  result.FilesRemoved,
		NodesUpdated:  result.NodesUpdated,
		DurationMs:    result.DurationMs,
		FullReindex:   result.FullReindex,
	}, nil
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
			_, _ = fmt.Fprintf(o.stdout,
				"%s operation=%d started kind=%s events=%d dirty_paths=%d coalesced=%d\n",
				prefix, observation.OperationID, observation.Operation,
				observation.EventsReceived, observation.DirtyPaths, observation.CoalescedEvents)
		}
	case watch.ObservationOperationCompleted:
		if o.verbose {
			_, _ = fmt.Fprintf(o.stdout,
				"%s operation=%d completed duration=%s events=%d dirty_paths=%d coalesced=%d checked=%d added=%d modified=%d removed=%d nodes_updated=%d full_reindex=%t\n",
				prefix, observation.OperationID, observationDuration(observation.Duration),
				observation.EventsReceived, observation.DirtyPaths, observation.CoalescedEvents,
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

func (o *watchOutput) startupStarted(at time.Time) {
	if !o.verbose {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprintf(o.stdout, "%s startup reconciliation started\n", at.UTC().Format(time.RFC3339Nano))
}

func (o *watchOutput) startupCompleted(at time.Time, duration time.Duration, result watch.SyncResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.verbose {
		_, _ = fmt.Fprintf(o.stdout,
			"%s startup reconciliation completed duration=%s checked=%d added=%d modified=%d removed=%d nodes_updated=%d full_reindex=%t\n",
			at.UTC().Format(time.RFC3339Nano), observationDuration(duration), result.FilesChecked,
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
