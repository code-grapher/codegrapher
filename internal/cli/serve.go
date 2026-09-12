package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/specscore/codegrapher/browserapi"
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
	var apiListen string
	var corsOrigins []string

	cmd := &cobra.Command{
		Use:   "serve [path]",
		Short: "Serve CodeGrapher capabilities in the foreground",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if envTruthy(os.Getenv("CODEGRAPH_DAEMON_INTERNAL")) {
				return errors.New("CODEGRAPH_DAEMON_INTERNAL is obsolete; use 'codegrapher daemon start' for background service")
			}
			explicitCapability := cmd.Flags().Changed("mcp") || cmd.Flags().Changed("watch") || cmd.Flags().Changed("api")
			selected := selectServeCapabilities(explicitCapability, mcpFlag, watchFlag, apiFlag, noWatch)
			mcpEnabled, watchEnabled, apiEnabled := selected.mcp, selected.watch, selected.api
			if !mcpEnabled && !watchEnabled && !apiEnabled {
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

			idx := (*indexer.Indexer)(nil)
			if owner != nil {
				idx = owner.Indexer()
			} else {
				var err error
				idx, err = indexer.Open(projectPath, indexer.Options{})
				if err != nil {
					return fmt.Errorf("open served index: %w", err)
				}
				defer func() { _ = idx.Close() }()
			}

			participants := make([]func(context.Context) error, 0, 3)
			if owner != nil {
				participants = append(participants, owner.Wait)
			}
			if mcpEnabled {
				backend := mcp.NewMultiBackend(idx.Stores(), projectPath)
				server := mcp.NewServer(backend)
				participants = append(participants, func(runCtx context.Context) error { return server.Serve(runCtx, os.Stdin, cmd.OutOrStdout()) })
			}
			if apiEnabled {
				token := strings.TrimSpace(os.Getenv("CODEGRAPH_BROWSER_TOKEN"))
				if token == "" {
					var err error
					token, err = browserapi.GenerateCredential()
					if err != nil {
						return err
					}
				}
				listener, err := net.Listen("tcp", apiListen)
				if err != nil {
					return fmt.Errorf("bind browser API: %w", err)
				}
				allowedOrigins := append([]string{}, corsOrigins...)
				for origin := range strings.SplitSeq(os.Getenv("CODEGRAPH_BROWSER_CORS_ORIGINS"), ",") {
					if origin = strings.TrimSpace(origin); origin != "" {
						allowedOrigins = append(allowedOrigins, origin)
					}
				}
				api, err := browserapi.New(idx, browserapi.Config{Token: token, AllowedOrigins: allowedOrigins, Freshness: func() freshness.Status {
					if owner != nil {
						return owner.Status()
					}
					return freshness.Status{IndexCurrent: true}
				}})
				if err != nil {
					_ = listener.Close()
					return err
				}
				revision, err := api.Revision()
				if err != nil {
					_ = listener.Close()
					return fmt.Errorf("read browser API revision: %w", err)
				}
				authority := listener.Addr().String()
				browserLink := "https://codegrapher.dev/browse/" + url.QueryEscape(authority) + "/repos/" + url.PathEscape(api.RepositoryID()) + "/revisions/" + url.PathEscape(revision) + "/tree#secret=" + url.QueryEscape(token)
				humanOut := cmd.OutOrStdout()
				if mcpEnabled {
					humanOut = cmd.ErrOrStderr()
				}
				_, _ = fmt.Fprintf(humanOut, "Browser API: http://%s%s\nBrowser link: %s\n", authority, browserapi.BasePath, browserLink)
				participants = append(participants, func(runCtx context.Context) error { return api.Serve(runCtx, listener) })
			}
			return runServeGroup(ctx, participants)
		},
	}

	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().BoolVar(&mcpFlag, "mcp", false, "Serve MCP over stdio")
	cmd.Flags().BoolVar(&watchFlag, "watch", false, "Keep the repository index current")
	cmd.Flags().BoolVar(&apiFlag, "api", false, "Serve the authenticated browser HTTP API")
	cmd.Flags().StringVar(&apiListen, "api-listen", "127.0.0.1:7331", "Browser API listen address")
	cmd.Flags().StringSliceVar(&corsOrigins, "cors-origin", nil, "Additional exact browser origin allowed by CORS")
	cmd.Flags().BoolVar(&noWatch, "no-watch", false, "Disable watching when using the default capability set")
	_ = cmd.Flags().MarkDeprecated("no-watch", "use explicit capability flags to select only the services you need")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show watcher events, operations, timings, and batch statistics")
	return cmd
}

type serveCapabilities struct{ mcp, watch, api bool }

func selectServeCapabilities(explicit, mcp, watch, api, noWatch bool) serveCapabilities {
	if !explicit {
		return serveCapabilities{mcp: true, watch: !noWatch, api: true}
	}
	if noWatch {
		watch = false
	}
	return serveCapabilities{mcp: mcp, watch: watch, api: api}
}

type mcpServer interface {
	Serve(context.Context, io.Reader, io.Writer) error
}

type freshnessSession interface {
	Wait(context.Context) error
	Close() error
}

func runCombinedServe(ctx context.Context, owner freshnessSession, server mcpServer, input io.Reader, output io.Writer) error {
	err := runServeGroup(ctx, []func(context.Context) error{owner.Wait, func(runCtx context.Context) error { return server.Serve(runCtx, input, output) }})
	_ = owner.Close()
	return err
}

func runServeGroup(ctx context.Context, participants []func(context.Context) error) error {
	if len(participants) == 0 {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, len(participants))
	for _, participant := range participants {
		go func(run func(context.Context) error) { done <- run(runCtx) }(participant)
	}
	var first error
	select {
	case first = <-done:
	case <-ctx.Done():
	}
	cancel()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for remaining := len(participants) - 1; remaining > 0; remaining-- {
		select {
		case <-done:
		case <-timer.C:
			if first != nil {
				return fmt.Errorf("%w; serve participant did not stop", first)
			}
			return errors.New("serve participant did not stop")
		}
	}
	return first
}

// envTruthy retains compatibility with the earlier serve environment parsing.
func envTruthy(raw string) bool {
	if raw == "" {
		return false
	}
	return raw != "0" && strings.ToLower(raw) != "false"
}
