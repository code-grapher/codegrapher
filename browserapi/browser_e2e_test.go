package browserapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/specscore/codegrapher/indexer"
)

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/live-daemon-api#ac:real-server-browser-api-journey
func TestRealBrowserGeneratedClientJourney(t *testing.T) {
	if os.Getenv("CODEGRAPH_BROWSER_E2E") != "1" {
		t.Skip("set CODEGRAPH_BROWSER_E2E=1 to run the real Chrome journey")
	}
	repositoryRoot := packageRepositoryRoot(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package sample\n\nfunc Alpha() { Beta() }\nfunc Beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, result, err := indexer.Init(project, indexer.Options{})
	if err != nil || !result.Success {
		t.Fatalf("init: %v %+v", err, result)
	}
	defer func() { _ = idx.Close() }()

	pageListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pageListener.Close() }()
	origin := "http://" + pageListener.Addr().String()
	api, err := New(idx, Config{Token: testToken, AllowedOrigins: []string{origin}})
	if err != nil {
		t.Fatal(err)
	}
	apiListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	apiDone := make(chan error, 1)
	go func() { apiDone <- api.Serve(ctx, apiListener) }()
	defer func() { cancel(); <-apiDone }()

	entry := filepath.Join(t.TempDir(), "journey.ts")
	clientImport := filepath.ToSlash(filepath.Join(repositoryRoot, "clients/typescript/src/index.ts"))
	source := fmt.Sprintf(`import { V1Client } from %q;
globalThis.runCodeGrapherJourney = async (endpoint, token) => {
  const credential = { getBearerToken: async () => token };
  const client = new V1Client(credential, { endpoint, allowInsecureConnection: true });
  const status = await client.getStatus();
  const listed = await client.listRepositories();
  const repo = listed.repositories[0];
  const tree = await client.getTree(repo.id, repo.revision);
  const file = await client.getFile(repo.id, repo.revision, 'main.go');
  const search = await client.searchSymbols(repo.id, repo.revision, 'Alpha', { limit: 5 });
  const symbol = await client.getSymbol(repo.id, repo.revision, search.results[0].symbol.id);
  const graph = await client.getSymbolGraph(repo.id, repo.revision, symbol.symbol.id, { direction: 'both', depth: 1, maxNodes: 10, maxEdges: 10 });
  let traversalRejected = false;
  try { await client.getFile(repo.id, repo.revision, '../secret'); } catch { traversalRejected = true; }
  return { apiVersion: status.apiVersion, repositoryCount: listed.repositories.length, treeEntries: tree.entries.length, content: file.content, symbol: symbol.symbol.name, graphNodes: graph.nodes.length, traversalRejected };
};`, clientImport)
	if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(filepath.Dir(entry), "journey.js")
	build := exec.Command("pnpm", "exec", "esbuild", entry, "--bundle", "--format=iife", "--platform=browser", "--define:process.env.NODE_ENV=\"test\"", "--outfile="+bundle)
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("bundle generated client journey: %v\n%s", err, output)
	}
	bundleBytes, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	pageServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/journey.js" {
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = w.Write(bundleBytes)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<script src="/journey.js"></script>`))
	})}
	pageDone := make(chan error, 1)
	go func() { pageDone <- pageServer.Serve(pageListener) }()
	defer func() {
		shutdown, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = pageServer.Shutdown(shutdown)
		<-pageDone
	}()

	apiEndpoint := "http://" + apiListener.Addr().String() + BasePath
	runner := exec.Command("node", filepath.Join(repositoryRoot, "tools/run-browser-api-e2e.mjs"), origin, apiEndpoint, testToken)
	runner.Dir = repositoryRoot
	var diagnostics bytes.Buffer
	runner.Stderr = &diagnostics
	output, err := runner.Output()
	if err != nil {
		t.Fatalf("real browser journey: %v\n%s", err, diagnostics.String())
	}
	var got struct {
		APIVersion        string `json:"apiVersion"`
		RepositoryCount   int    `json:"repositoryCount"`
		TreeEntries       int    `json:"treeEntries"`
		Content           string `json:"content"`
		Symbol            string `json:"symbol"`
		GraphNodes        int    `json:"graphNodes"`
		TraversalRejected bool   `json:"traversalRejected"`
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode browser result: %v\n%s", err, output)
	}
	if got.APIVersion != "v1" || got.RepositoryCount != 1 || got.TreeEntries == 0 || !strings.Contains(got.Content, "func Alpha") || got.Symbol != "Alpha" || got.GraphNodes == 0 || !got.TraversalRejected {
		t.Fatalf("browser journey = %+v", got)
	}
}

func packageRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	return filepath.Dir(filepath.Dir(file))
}
