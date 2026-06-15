package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/specscore/codegrapher/internal/extract"
	"github.com/specscore/codegrapher/internal/paritytest"
	"github.com/specscore/codegrapher/mcp"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/scope"
	"github.com/specscore/codegrapher/store"
)

const repoRoot = ".."

// mcpRequest pairs a JSON-RPC request with the golden file holding the
// captured response. The requests are byte-for-byte the ones
// tools/parity/capture-mcp-golden.sh sent to the original TS server.
type mcpRequest struct {
	golden  string
	request string
}

func requestsFor(fixture string) []mcpRequest {
	switch fixture {
	case "go-small":
		return []mcpRequest{
			{"initialize.json", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`},
			{"", `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`},
			{"tools-list.json", `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`},
			{"status.json", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"codegraph_status","arguments":{}}}`},
			{"files.json", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"codegraph_files","arguments":{}}}`},
			{"search-1.json", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"codegraph_search","arguments":{"query":"store","limit":20}}}`},
			{"callers-1.json", `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"codegraph_callers","arguments":{"symbol":"Get"}}}`},
			{"callees-1.json", `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"codegraph_callees","arguments":{"symbol":"Get"}}}`},
			{"impact-1.json", `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"codegraph_impact","arguments":{"symbol":"Get"}}}`},
			{"node-1.json", `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"codegraph_node","arguments":{"symbol":"Get","includeCode":true}}}`},
			{"explore-1.json", `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"how does the store work"}}}`},
			{"explore-2.json", `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"Get Set Lookup"}}}`},
			{"explore-3.json", `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"normalize"}}}`},
		}
	case "ts-small":
		return []mcpRequest{
			{"initialize.json", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`},
			{"", `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`},
			{"tools-list.json", `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`},
			{"status.json", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"codegraph_status","arguments":{}}}`},
			{"files.json", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"codegraph_files","arguments":{}}}`},
			{"search-1.json", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"codegraph_search","arguments":{"query":"store","limit":20}}}`},
			{"callers-1.json", `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"codegraph_callers","arguments":{"symbol":"get"}}}`},
			{"callees-1.json", `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"codegraph_callees","arguments":{"symbol":"get"}}}`},
			{"impact-1.json", `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"codegraph_impact","arguments":{"symbol":"get"}}}`},
			{"node-1.json", `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"codegraph_node","arguments":{"symbol":"get","includeCode":true}}}`},
			{"explore-1.json", `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"how does the cache work"}}}`},
			{"explore-2.json", `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"get set lookup"}}}`},
			{"explore-3.json", `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"normalize"}}}`},
		}
	}
	return nil
}

// buildFixtureStores builds one store per (folded-language, version) scope for a
// fixture, mirroring how the indexer partitions a project (go.mod folds into the
// Go scope, package.json into the node scope) so the MCP MultiBackend fans out
// exactly as the binary does. Includes the file records the status/files tools
// need. Returns stores in stable scope-key order.
func buildFixtureStores(t *testing.T, fixtureDir string) []*store.Store {
	t.Helper()

	foldScope := func(lang model.Language) model.Language {
		switch lang {
		case model.LangGoMod:
			return model.LangGo
		case model.LangPackageJSON:
			return model.LangNode
		default:
			return lang
		}
	}

	byScope := map[string]*store.Store{}
	var order []string

	err := filepath.Walk(fixtureDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, err := filepath.Rel(fixtureDir, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		// Mirror the production indexer: unknown-language files get a single
		// bare file-level node (no parse, no symbols) rather than being
		// skipped (whole-repo-file-nodes change).
		lang := extract.DetectLanguage(path)
		var content []byte
		if lang != model.LangUnknown {
			content, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}

		key := scope.Scope{
			Language: foldScope(lang),
			Version:  scope.DetectVersion(fixtureDir, path, lang),
		}.Key()
		s := byScope[key]
		if s == nil {
			s, err = store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
			if err != nil {
				t.Fatalf("store.Initialize: %v", err)
			}
			t.Cleanup(func() { _ = s.Close() })
			byScope[key] = s
			order = append(order, key)
		}

		result, err := extract.ExtractFile(relPath, content, lang)
		if err != nil {
			return err
		}
		if err := s.InsertNodes(result.Nodes); err != nil {
			return err
		}
		if err := s.InsertEdges(result.Edges); err != nil {
			return err
		}
		if err := s.InsertUnresolvedRefs(result.UnresolvedReferences); err != nil {
			return err
		}
		return s.UpsertFile(model.FileRecord{
			Path:        relPath,
			ContentHash: fmt.Sprintf("%d", len(content)),
			Language:    lang,
			Size:        info.Size(),
			ModifiedAt:  info.ModTime().UnixMilli(),
			IndexedAt:   time.Now().UnixMilli(),
			NodeCount:   len(result.Nodes),
		})
	})
	if err != nil {
		t.Fatalf("walk fixture: %v", err)
	}

	sort.Strings(order)
	stores := make([]*store.Store, 0, len(order))
	for _, key := range order {
		s := byScope[key]
		if _, err := resolve.Resolve(s, fixtureDir); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		stores = append(stores, s)
	}
	return stores
}

// dbSizeRe normalizes the one machine-specific value embedded in tool text:
// the SQLite file size differs between the original's node:sqlite build and
// modernc.org/sqlite (page allocation), exactly why the parity harness
// normalizes dbSizeBytes in the CLI status payload.
var dbSizeRe = regexp.MustCompile(`\*\*Database size:\*\* [0-9.]+ MB`)

func normalizeMCP(raw []byte) []byte {
	return dbSizeRe.ReplaceAll(raw, []byte(`**Database size:** <NORM> MB`))
}

// TestMCPParityGoldens serves MCP over an in-memory pipe for each fixture and
// asserts every captured golden response matches at full value.
func TestMCPParityGoldens(t *testing.T) {
	for _, fixture := range []string{"go-small", "ts-small"} {
		fixture := fixture
		t.Run(fixture, func(t *testing.T) {
			fixtureDir, err := filepath.Abs(filepath.Join(repoRoot, "testdata", "fixtures", fixture))
			if err != nil {
				t.Fatalf("abs fixture dir: %v", err)
			}
			goldenDir := filepath.Join(repoRoot, "testdata", "golden", fixture, "mcp")

			stores := buildFixtureStores(t, fixtureDir)
			backend := mcp.NewMultiBackend(stores, fixtureDir)
			server := mcp.NewServer(backend)

			inR, inW := io.Pipe()
			outR, outW := io.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			serveErr := make(chan error, 1)
			go func() {
				serveErr <- server.Serve(ctx, inR, outW)
				_ = outW.Close()
			}()

			responses := make(map[int64]json.RawMessage)
			scanner := bufio.NewScanner(outR)
			scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
			readResponse := func(wantID int64) (json.RawMessage, bool) {
				for scanner.Scan() {
					line := append([]byte(nil), scanner.Bytes()...)
					var env struct {
						ID *int64 `json:"id"`
					}
					if json.Unmarshal(line, &env) != nil || env.ID == nil {
						continue
					}
					responses[*env.ID] = line
					if *env.ID == wantID {
						return line, true
					}
				}
				return nil, false
			}

			for _, req := range requestsFor(fixture) {
				if _, err := io.WriteString(inW, req.request+"\n"); err != nil {
					t.Fatalf("write request: %v", err)
				}
				if req.golden == "" {
					continue // notification
				}
				var env struct {
					ID int64 `json:"id"`
				}
				if err := json.Unmarshal([]byte(req.request), &env); err != nil {
					t.Fatalf("parse request id: %v", err)
				}
				got, ok := responses[env.ID]
				if !ok {
					got, ok = readResponse(env.ID)
				}
				if !ok {
					t.Fatalf("%s: no response for id %d", req.golden, env.ID)
				}

				goldenPath := filepath.Join(goldenDir, req.golden)
				want, err := os.ReadFile(goldenPath)
				if err != nil {
					t.Fatalf("read golden %s: %v", req.golden, err)
				}

				cw, err := paritytest.Canonicalize(normalizeMCP(want), false)
				if err != nil {
					t.Fatalf("canonicalize golden %s: %v", req.golden, err)
				}
				cg, err := paritytest.Canonicalize(normalizeMCP(got), false)
				if err != nil {
					t.Fatalf("canonicalize got %s: %v", req.golden, err)
				}
				if string(cw) != string(cg) {
					t.Errorf("%s/%s: parity mismatch\n--- golden (canonical)\n%s\n--- got (canonical)\n%s",
						fixture, req.golden, summarizeDiff(cw, cg), cg)
				}
			}

			_ = inW.Close()
			if err := <-serveErr; err != nil {
				t.Fatalf("serve: %v", err)
			}
		})
	}
}

// summarizeDiff returns the golden side plus a pointer to the first byte
// divergence to make large text diffs readable.
func summarizeDiff(want, got []byte) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	lo := i - 120
	if lo < 0 {
		lo = 0
	}
	hiW := i + 200
	if hiW > len(want) {
		hiW = len(want)
	}
	hiG := i + 200
	if hiG > len(got) {
		hiG = len(got)
	}
	return fmt.Sprintf("%s\n--- first divergence at byte %d ---\nwant: …%s…\ngot:  …%s…",
		want, i, want[lo:hiW], got[lo:hiG])
}
