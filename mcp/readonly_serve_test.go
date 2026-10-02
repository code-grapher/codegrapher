package mcp_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/specscore/codegrapher/mcp"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// TestServeReadOnlyHandleHoldsNoLockWhileIdle is the regression test for
// `codegrapher serve`: it keeps ONE read-only handle open for its lifetime, and
// in rollback-journal mode an idle connection holds no lock, so a concurrent
// `sync` (a second, read-write connection) is never blocked by it.
func TestServeReadOnlyHandleHoldsNoLockWhileIdle(t *testing.T) {
	path := filepath.Join(t.TempDir(), store.DatabaseFilename)
	writer, err := store.Initialize(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if err := writer.InsertNodes([]model.Node{fnNode("n1", "Alpha", "a.go", model.LangGo)}); err != nil {
		t.Fatal(err)
	}

	ro, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	backend := mcp.NewMultiBackend([]*store.Store{ro}, filepath.Dir(path))

	// Query through the backend (the way serve answers a tool call).
	results, err := backend.SearchNodes("Alpha", nil, 10)
	if err != nil || len(results) == 0 {
		t.Fatalf("SearchNodes = %v, %v", results, err)
	}
	if _, err := backend.GetStats(); err != nil {
		t.Fatal(err)
	}

	// Now the handle is idle. A write from a second connection must succeed
	// immediately: a held lock would make it wait out busy_timeout (5s) and fail.
	start := time.Now()
	if err := writer.InsertNodes([]model.Node{fnNode("n2", "Beta", "b.go", model.LangGo)}); err != nil {
		t.Fatalf("write while serve handle idle: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("write took %v; the idle read-only handle is holding a lock", elapsed)
	}

	// And the served handle sees the new row on its next query.
	results, err = backend.SearchNodes("Beta", nil, 10)
	if err != nil || len(results) == 0 {
		t.Fatalf("SearchNodes after write = %v, %v", results, err)
	}
}

// statsBackend serves canned stats; handleStatus calls nothing else.
type statsBackend struct {
	mcp.GraphBackend
	stats mcp.GraphStats
}

func (b statsBackend) GetStats() (mcp.GraphStats, error) { return b.stats, nil }

func TestStatusToolReportsJournalMode(t *testing.T) {
	cases := map[string]string{
		"delete": "**Journal mode:** delete",
		"wal":    "legacy index; run `codegrapher sync`",
		"":       "**Journal mode:** unknown",
		"memory": "**Journal mode:** memory",
	}
	for mode, want := range cases {
		t.Run("mode="+mode, func(t *testing.T) {
			server := mcp.NewServer(statsBackend{stats: mcp.GraphStats{JournalMode: mode}})
			var out bytes.Buffer
			req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"codegraph_status","arguments":{}}}` + "\n"
			if err := server.Serve(context.Background(), strings.NewReader(req), &out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), want) {
				t.Errorf("status output lacks %q: %s", want, out.String())
			}
		})
	}
}
