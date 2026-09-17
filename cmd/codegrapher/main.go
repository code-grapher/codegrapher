// Command codegrapher is the drop-in Go replacement for the original
// colbymchenry/codegraph TypeScript CLI.
//
// Skipped verbs (out of scope):
//   - uninstall: an agent-config editor, orthogonal to code intelligence
//   - MCP daemon/proxy transport: the local repository freshness daemon is
//     separate from the existing direct stdio MCP transport.
//
// install and upgrade are NOT skipped: they come from the shared
// github.com/strongo/cli-helpers/cliinstall library (see internal/cli/
// install.go, internal/cli/upgrade.go) and cover fleet CLI discovery and
// self-update, not the original codegraph TypeScript CLI's own npm-based
// install/upgrade verbs, which have no equivalent for a static Go binary.
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
