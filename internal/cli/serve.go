package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/mcp"
	"github.com/specscore/codegrapher/store"
	"github.com/spf13/cobra"
)

// newServeCmd implements `codegraph serve`, mirroring the upstream CLI
// surface (src/bin/codegraph.ts): -p/--path, --mcp, --no-watch.
//
// Only direct (stdio) mode is implemented. Upstream defaults to a shared
// daemon + proxy transport selected via environment variables
// (CODEGRAPH_NO_DAEMON opts out; CODEGRAPH_DAEMON_INTERNAL marks the spawned
// daemon process) — that mode is not implemented here (KNOWN-BUGS gap C-1),
// so daemon-specific env is either rejected with a clear message
// (CODEGRAPH_DAEMON_INTERNAL — the caller explicitly asked this process to BE
// the daemon) or noted and ignored (daemon default — we serve direct instead).
func newServeCmd() *cobra.Command {
	var pathFlag string
	var mcpFlag bool
	var noWatch bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start CodeGraph as an MCP server for AI assistants",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Daemon-internal launch: this process was asked to BE the
			// shared daemon — unimplemented, fail with a clear message.
			if envTruthy(os.Getenv("CODEGRAPH_DAEMON_INTERNAL")) {
				return fmt.Errorf("daemon mode is not implemented in this build (KNOWN-BUGS gap C-1): " +
					"unset CODEGRAPH_DAEMON_INTERNAL and run `codegraph serve --mcp` for direct stdio mode")
			}

			// Mirror upstream: --no-watch routes through the same env-var
			// chokepoint the watcher honors.
			if noWatch {
				os.Setenv("CODEGRAPH_NO_WATCH", "1")
			}

			if !mcpFlag {
				printServeInfo()
				return nil
			}

			// Upstream defaults to daemon/proxy transport unless
			// CODEGRAPH_NO_DAEMON is set; this build only implements direct
			// stdio mode — say so once on stderr, then serve.
			if !envTruthy(os.Getenv("CODEGRAPH_NO_DAEMON")) {
				fmt.Fprintln(os.Stderr,
					"codegraph: daemon/proxy mode is not implemented; serving in direct stdio mode "+
						"(set CODEGRAPH_NO_DAEMON=1 to silence this notice)")
			}

			projectPath := resolveArg(nil, 0)
			if pathFlag != "" {
				projectPath = resolveArg([]string{pathFlag}, 0)
			}
			if !indexer.IsInitialized(projectPath) {
				return fmt.Errorf("CodeGraph not initialized in %s. Run 'codegraph init' in that project first", projectPath)
			}

			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				return fmt.Errorf("failed to start server: %w", err)
			}
			defer idx.Close()

			// TODO(integration): use idx.Stores() once the multi-store indexer
			// change lands; until then wrap the single store as a one-element
			// slice so this worktree builds (MultiBackend over one store is
			// behavior-identical to NewStoreBackend).
			backend := mcp.NewMultiBackend([]*store.Store{idx.Store()}, projectPath)
			server := mcp.NewServer(backend)
			return server.Serve(cmd.Context(), os.Stdin, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path (optional for MCP mode)")
	cmd.Flags().BoolVar(&mcpFlag, "mcp", false, "Run as MCP server (stdio transport)")
	cmd.Flags().BoolVar(&noWatch, "no-watch", false, "Disable the file watcher (no auto-sync; useful on slow filesystems like WSL2 /mnt drives)")
	return cmd
}

// envTruthy mirrors upstream's daemon env parsing: set, not "0", not "false".
func envTruthy(raw string) bool {
	if raw == "" {
		return false
	}
	return raw != "0" && strings.ToLower(raw) != "false"
}

// printServeInfo mirrors the upstream no---mcp info screen (stderr so stdout
// stays clean for piped/stdio usage).
func printServeInfo() {
	fmt.Fprint(os.Stderr, `
CodeGraph MCP Server

Use --mcp flag to start the MCP server

To use with Claude Code, add to your MCP configuration:

{
  "mcpServers": {
    "codegraph": {
      "command": "codegraph",
      "args": ["serve", "--mcp"]
    }
  }
}

Available tools:
  codegraph_explore   - Primary: source of the relevant symbols for any question
  codegraph_search    - Search for code symbols
  codegraph_callers   - Find callers of a symbol
  codegraph_callees   - Find what a symbol calls
  codegraph_impact    - Analyze impact of changes
  codegraph_node      - Get symbol details
  codegraph_files     - Get project file structure
  codegraph_status    - Get index status
`)
}
