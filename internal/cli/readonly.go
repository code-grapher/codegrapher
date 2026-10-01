package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/store"
	"github.com/spf13/cobra"
)

// ExitNeedsUpgrade is the process exit code for an index that cannot be read
// until a read-write `codegrapher sync` (or --refresh) upgrades it. It is
// distinct from the generic failure code 1 so scripts and pollers can tell
// "run sync" apart from a real error.
const ExitNeedsUpgrade = 3

// ExitCodeFor maps a command error to the process exit code.
func ExitCodeFor(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	if errors.Is(err, store.ErrNeedsUpgrade) {
		return ExitNeedsUpgrade
	}
	return 1
}

// addRefreshFlag registers the shared --refresh flag of the read commands:
// run a read-write sync (which also performs any pending index upgrade) before
// answering. Without it, read commands never write to the index.
func addRefreshFlag(cmd *cobra.Command, refresh *bool) {
	cmd.Flags().BoolVar(refresh, "refresh", false,
		"Sync the index first (read-write; also upgrades an old index), then answer. Without it the index is opened read-only and never modified")
}

// refreshIndex is the read-write step behind --refresh: it brings the index up
// to date (migrating the schema and converting a legacy WAL index on open) and
// stamps the freshness revision. A seam for tests.
var refreshIndex = func(projectPath string) (indexer.SyncResult, error) {
	idx, err := indexer.Open(projectPath, indexer.Options{})
	if err != nil {
		return indexer.SyncResult{}, fmt.Errorf("refresh: open index: %w", err)
	}
	defer func() { _ = idx.Close() }()
	res, err := idx.RefreshForRead(indexer.Options{})
	if err != nil {
		return res, fmt.Errorf("refresh: %w", err)
	}
	return res, nil
}

// openIndexForRead opens projectPath's index for a read command. By default
// the open is read-only: nothing is migrated, converted or written, and a
// stale or legacy-WAL index fails with a store.NeedsUpgradeError. With
// refresh, a read-write sync runs first (the upgrade path), then the index is
// reopened read-only to answer.
func openIndexForRead(projectPath string, refresh bool) (*indexer.Indexer, error) {
	idx, _, err := openIndexForReadResult(projectPath, refresh)
	return idx, err
}

// openIndexForReadResult is openIndexForRead that also returns the refresh
// result (zero when refresh is false).
func openIndexForReadResult(projectPath string, refresh bool) (*indexer.Indexer, indexer.SyncResult, error) {
	var res indexer.SyncResult
	if refresh {
		var err error
		if res, err = refreshIndex(projectPath); err != nil {
			return nil, res, err
		}
	}
	idx, err := indexer.OpenReadOnly(projectPath, indexer.Options{})
	if err != nil {
		return nil, res, err
	}
	return idx, res, nil
}

// failOpen reports a failed read-only open and exits: with ExitNeedsUpgrade
// when the index needs `codegrapher sync`, else 1.
func failOpen(err error) {
	printError(fmt.Sprintf("Failed to open index: %s", err))
	exitProcess(ExitCodeFor(err))
}

// exitProcess is os.Exit, replaceable in tests.
var exitProcess = os.Exit

// ExitError ends a command with a specific exit code after the command has
// already reported the reason itself (no extra "error:" line is printed).
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ReportAndExitCode prints err (unless the command already reported it) and
// returns the process exit code for it.
func ReportAndExitCode(err error, stderr io.Writer) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	_, _ = fmt.Fprintln(stderr, err)
	return ExitCodeFor(err)
}

// printWarnings prints each warning on its own line.
func printWarnings(warnings []string) {
	for _, w := range warnings {
		printWarn(w)
	}
}
