package cli

import (
	"fmt"
	"os"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize CodeGraph in a project directory and build the initial index",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := resolveArg(args)

			if indexer.IsInitialized(projectPath) {
				printWarn(fmt.Sprintf("Already initialized in %s", projectPath))
				printInfo("Use \"codegraph index\" to re-index or \"codegraph sync\" to update")
				return nil
			}

			opts := indexer.Options{}
			if verbose {
				opts.OnProgress = verboseProgress()
			} else if isTTY() {
				sp := newSpinner()
				sp.start("Indexing…")
				opts.OnProgress = func(p indexer.IndexProgress) {
					sp.update(progressLabel(p))
				}
				defer sp.stop()
			}

			idx, result, err := indexer.Init(projectPath, opts)
			if err != nil {
				printError(fmt.Sprintf("Failed: %s", err))
				os.Exit(1)
			}
			defer func() { _ = idx.Close() }()

			printIndexResult(result, projectPath)
			if !result.Success {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show detailed progress")
	// Accept -i for backward compat (no-op per the original).
	cmd.Flags().BoolP("index", "i", false, "Deprecated: indexing now runs by default")
	_ = cmd.Flags().MarkHidden("index")
	return cmd
}

// printIndexResult prints indexing outcome to stdout.
func printIndexResult(result indexer.IndexResult, projectPath string) {
	hasErrors := result.FilesErrored > 0

	if !result.Success && !hasErrors && result.FilesIndexed == 0 {
		for _, e := range result.Errors {
			if e.Severity == "error" {
				printError(e.Message)
				return
			}
		}
		printError("Indexing failed — no further details available")
		return
	}

	if result.FilesIndexed > 0 {
		if hasErrors {
			printSuccess(fmt.Sprintf("Indexed %s files (%s could not be parsed)",
				formatNumber(result.FilesIndexed), formatNumber(result.FilesErrored)))
		} else {
			printSuccess(fmt.Sprintf("Indexed %s files", formatNumber(result.FilesIndexed)))
		}
		printInfo(fmt.Sprintf("%s nodes, %s edges in %s",
			formatNumber(result.NodesCreated), formatNumber(result.EdgesCreated),
			formatDuration(result.DurationMs)))
	} else if hasErrors {
		printError(fmt.Sprintf("Indexing failed — all %s files had errors", formatNumber(result.FilesErrored)))
	} else {
		printWarn("No files found to index")
	}

	if hasErrors {
		printErrorBreakdown(result.Errors)
		if projectPath != "" {
			writeErrorLog(projectPath, result.Errors)
			printInfo("See .codegraph/errors.log for details")
		}
		if result.FilesIndexed > 0 {
			printInfo("The index is fully usable — only the failed files are missing.")
		}
	}
}

func printErrorBreakdown(errors []model.ExtractionError) {
	codeCounts := map[string]int{}
	for _, e := range errors {
		if e.Severity == "error" {
			code := e.Code
			if code == "" {
				code = "unknown"
			}
			codeCounts[code]++
		}
	}
	labels := map[string]string{
		"parse_error":          "files failed to parse",
		"read_error":           "files could not be read",
		"size_exceeded":        "files exceeded size limit",
		"path_traversal":       "blocked paths",
		"unsupported_language": "unsupported language",
		"parser_error":         "parser initialization failures",
	}
	for code, count := range codeCounts {
		label, ok := labels[code]
		if !ok {
			label = code
		}
		fmt.Printf("  %s %s\n", formatNumber(count), label)
	}
}

func writeErrorLog(projectPath string, errors []model.ExtractionError) {
	cgDir := indexer.GetCodeGraphDir(projectPath)
	if _, err := os.Stat(cgDir); err != nil {
		return
	}
	logPath := cgDir + "/errors.log"
	f, err := os.Create(logPath)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "CodeGraph Error Log\n")
	for _, e := range errors {
		if e.Severity != "error" {
			continue
		}
		if e.FilePath != "" {
			_, _ = fmt.Fprintf(f, "%s: %s\n", e.FilePath, e.Message)
		} else {
			_, _ = fmt.Fprintf(f, "%s\n", e.Message)
		}
	}
}
