// Command codegrapher is the drop-in Go replacement for the original
// colbymchenry/codegraph TypeScript CLI.
//
// Skipped verbs (out of scope):
//   - install / uninstall: agent-config editors, orthogonal to code intelligence
//   - upgrade: npm self-update logic, meaningless for a static Go binary
//   - serve daemon/proxy modes: only direct stdio MCP mode is implemented;
//     CODEGRAPH_DAEMON_INTERNAL is rejected with a clear error and the
//     daemon-default transport falls back to direct mode with a stderr
//     notice (KNOWN-BUGS.md gap C-1)
//
// CODEGRAPH_* env vars honored:
//   - CODEGRAPH_NO_WATCH / CODEGRAPH_FORCE_WATCH (watcher policy — consumed by watch package)
//   - CODEGRAPH_WATCH_DEBOUNCE_MS (watcher debounce — consumed by watch package)
//   - CODEGRAPH_DIR (override .codegraph dir name — consumed by indexer/dir.go)
package main

import (
	"fmt"
	"os"

	"github.com/specscore/codegrapher/internal/cli"
)

func main() {
	root := cli.NewRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
