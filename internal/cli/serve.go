package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/specscore/codegrapher/freshness"
	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/mcp"
	"github.com/spf13/cobra"
)

// newServeCmd builds the composable foreground server. Capability flags narrow
// selection; no capability flags means every capability in this build.
func newServeCmd() *cobra.Command {
	var pathFlag string
	var mcpFlag bool
	var watchFlag bool
	var apiFlag bool
	var noWatch bool
	var verbose bool

	cmd := &cobra.Command{
		Use:   "serve [path]",
		Short: "Serve CodeGrapher capabilities in the foreground",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if envTruthy(os.Getenv("CODEGRAPH_DAEMON_INTERNAL")) {
				return errors.New("CODEGRAPH_DAEMON_INTERNAL is obsolete; use 'codegrapher daemon start' for background service")
			}
			explicitCapability := cmd.Flags().Changed("mcp") || cmd.Flags().Changed("watch") || cmd.Flags().Changed("api")
			mcpEnabled := mcpFlag
			watchEnabled := watchFlag
			apiEnabled := apiFlag
			if !explicitCapability {
				mcpEnabled = true
				watchEnabled = !noWatch
				apiEnabled = false // Added to the default set when the public API lands.
			} else if noWatch {
				watchEnabled = false
			}
			if apiEnabled {
				return errors.New("browser API capability is not yet available; omit --api until the next implementation phase")
			}
			if !mcpEnabled && !watchEnabled {
				return errors.New("no serve capability selected")
			}

			projectArgs := args
			if pathFlag != "" {
				projectArgs = []string{pathFlag}
			}
			projectPath := resolveArg(projectArgs)
			if mismatch := indexer.DetectWorktreeIndexMismatch(watchStartPath(projectArgs), projectPath); mismatch != nil {
				return fmt.Errorf("cannot serve a different git worktree's index:\n%s", indexer.WorktreeMismatchWarning(*mismatch))
			}
			if !indexer.IsInitialized(projectPath) {
				return fmt.Errorf("CodeGraph not initialized in %s; run 'codegrapher init' there first", projectPath)
			}

			ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stopSignals()
			var owner *freshness.Owner
			var output *watchOutput
			if watchEnabled {
				watchStdout := cmd.OutOrStdout()
				if mcpEnabled {
					watchStdout = cmd.ErrOrStderr()
				}
				output = newWatchOutput(watchStdout, cmd.ErrOrStderr(), verbose)
				var err error
				owner, err = startForegroundWatch(ctx, projectPath, output)
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				defer func() {
					_ = owner.Close()
					output.stopped(projectPath)
				}()
			}

			if !mcpEnabled {
				return owner.Wait(ctx)
			}
			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				return fmt.Errorf("open MCP index: %w", err)
			}
			defer func() { _ = idx.Close() }()
			backend := mcp.NewMultiBackend(idx.Stores(), projectPath)
			server := mcp.NewServer(backend)
			if owner == nil {
				return server.Serve(ctx, os.Stdin, cmd.OutOrStdout())
			}
			return runCombinedServe(ctx, owner, server, os.Stdin, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().BoolVar(&mcpFlag, "mcp", false, "Serve MCP over stdio")
	cmd.Flags().BoolVar(&watchFlag, "watch", false, "Keep the repository index current")
	cmd.Flags().BoolVar(&apiFlag, "api", false, "Serve the authenticated browser HTTP API")
	cmd.Flags().BoolVar(&noWatch, "no-watch", false, "Disable watching when using the default capability set")
	_ = cmd.Flags().MarkDeprecated("no-watch", "use explicit capability flags to select only the services you need")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show watcher events, operations, timings, and batch statistics")
	return cmd
}

type mcpServer interface {
	Serve(context.Context, io.Reader, io.Writer) error
}

func runCombinedServe(ctx context.Context, owner *freshness.Owner, server mcpServer, input io.Reader, output io.Writer) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	mcpDone := make(chan error, 1)
	watchDone := make(chan error, 1)
	go func() { mcpDone <- server.Serve(runCtx, input, output) }()
	go func() { watchDone <- owner.Wait(runCtx) }()
	var err error
	select {
	case err = <-mcpDone:
	case err = <-watchDone:
	case <-ctx.Done():
	}
	cancel()
	_ = owner.Close()
	return err
}

// envTruthy retains compatibility with the earlier serve environment parsing.
func envTruthy(raw string) bool {
	if raw == "" {
		return false
	}
	return raw != "0" && strings.ToLower(raw) != "false"
}
