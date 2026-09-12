package browserapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/freshness"
	"github.com/specscore/codegrapher/indexer"
)

const testToken = "browser-token"

func TestBrowserAPIAuthenticatedJourneyAndPathSafety(t *testing.T) {
	root, api := newTestAPI(t, func() freshness.Status { return freshness.Status{IndexCurrent: true} })

	unauthorized := request(t, api, http.MethodGet, BasePath+"/status", "", "")
	assertError(t, unauthorized, http.StatusUnauthorized, "unauthorized")
	forbidden := request(t, api, http.MethodGet, BasePath+"/status", testToken, "https://evil.example")
	assertError(t, forbidden, http.StatusForbidden, "origin_forbidden")
	preflight := request(t, api, http.MethodOptions, BasePath+"/status", "", "https://codegrapher.dev")
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != "https://codegrapher.dev" {
		t.Fatalf("preflight = %d headers=%v", preflight.Code, preflight.Header())
	}

	status := request(t, api, http.MethodGet, BasePath+"/status", testToken, "https://codegrapher.dev")
	if status.Code != http.StatusOK || strings.Contains(status.Body.String(), root) {
		t.Fatalf("status = %d %s", status.Code, status.Body.String())
	}
	var repositories struct {
		Repositories []Repository `json:"repositories"`
	}
	decodeResponse(t, request(t, api, http.MethodGet, BasePath+"/repositories", testToken, ""), &repositories)
	if len(repositories.Repositories) != 1 || !validRepositoryID(repositories.Repositories[0].ID) || repositories.Repositories[0].Revision == "" {
		t.Fatalf("repositories = %+v", repositories)
	}
	repository := repositories.Repositories[0]
	base := BasePath + "/repositories/" + repository.ID + "/revisions/" + repository.Revision

	var tree TreeResponse
	decodeResponse(t, request(t, api, http.MethodGet, base+"/tree", testToken, ""), &tree)
	if len(tree.Entries) == 0 || tree.Entries[0].Path == "" {
		t.Fatalf("tree = %+v", tree)
	}
	var file FileResponse
	decodeResponse(t, request(t, api, http.MethodGet, base+"/files?path=main.go", testToken, ""), &file)
	if !strings.Contains(file.Content, "func Alpha") || file.Path != "main.go" {
		t.Fatalf("file = %+v", file)
	}

	for _, unsafe := range []string{"../secret", "/etc/passwd", "dir\\file", "C:%5Csecret", "./main.go"} {
		response := request(t, api, http.MethodGet, base+"/files?path="+unsafe, testToken, "")
		assertError(t, response, http.StatusBadRequest, "invalid_path")
		if strings.Contains(response.Body.String(), root) {
			t.Fatalf("unsafe response leaked root: %s", response.Body.String())
		}
	}

	var search SearchResponse
	decodeResponse(t, request(t, api, http.MethodGet, base+"/search?query=Alpha&limit=5", testToken, ""), &search)
	if len(search.Results) == 0 || search.Results[0].Symbol.Name != "Alpha" {
		t.Fatalf("search = %+v", search)
	}
	symbolID := search.Results[0].Symbol.ID
	var symbol SymbolResponse
	decodeResponse(t, request(t, api, http.MethodGet, base+"/symbols/"+symbolID, testToken, ""), &symbol)
	if symbol.Symbol.ID != symbolID {
		t.Fatalf("symbol = %+v", symbol)
	}
	var graph GraphResponse
	decodeResponse(t, request(t, api, http.MethodGet, base+"/symbols/"+symbolID+"/graph?direction=both&depth=1&maxNodes=10&maxEdges=10", testToken, ""), &graph)
	if len(graph.Nodes) == 0 || graph.MaxNodes != 10 || graph.MaxEdges != 10 {
		t.Fatalf("graph = %+v", graph)
	}

	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package sample\n\nfunc Changed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := request(t, api, http.MethodGet, base+"/files?path=main.go", testToken, "")
	assertError(t, stale, http.StatusConflict, "index_stale")
}

func TestRepositoryIdentitySurvivesMoveAndFreshnessErrorIsScrubbed(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "before")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package sample\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, result, err := indexer.Init(root, indexer.Options{})
	if err != nil || !result.Success {
		t.Fatalf("init: %v %+v", err, result)
	}
	api, err := New(idx, Config{Token: testToken, Freshness: func() freshness.Status {
		return freshness.Status{WatchReady: true, LastError: root + ": permission denied"}
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := api.RepositoryID()
	status := request(t, api, http.MethodGet, BasePath+"/status", testToken, "")
	if strings.Contains(status.Body.String(), root) || !strings.Contains(status.Body.String(), "index synchronization failed") {
		t.Fatalf("unscrubbed status: %s", status.Body.String())
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	after := filepath.Join(parent, "after")
	if err := os.Rename(root, after); err != nil {
		t.Fatal(err)
	}
	reopened, err := indexer.Open(after, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	moved, err := New(reopened, Config{Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	if moved.RepositoryID() != id {
		t.Fatalf("repository ID changed after move: %q -> %q", id, moved.RepositoryID())
	}
}

func TestAPIErrorAndBoundedBranches(t *testing.T) {
	root, api := newTestAPI(t, func() freshness.Status { return freshness.Status{WatchReady: true} })
	revision, err := api.Revision()
	if err != nil {
		t.Fatal(err)
	}
	repositoryID := api.RepositoryID()
	base := BasePath + "/repositories/" + repositoryID + "/revisions/" + revision

	var repository Repository
	decodeResponse(t, request(t, api, http.MethodGet, BasePath+"/repositories/"+repositoryID, testToken, ""), &repository)
	if repository.ID != repositoryID {
		t.Fatalf("repository = %+v", repository)
	}
	assertError(t, request(t, api, http.MethodGet, BasePath+"/repositories/missing", testToken, ""), http.StatusNotFound, "repository_not_found")
	assertError(t, request(t, api, http.MethodGet, BasePath+"/repositories/"+repositoryID+"/revisions/old/tree", testToken, ""), http.StatusConflict, "revision_changed")
	assertError(t, request(t, api, http.MethodGet, base+"/tree?path=missing", testToken, ""), http.StatusNotFound, "tree_not_found")
	assertError(t, request(t, api, http.MethodGet, base+"/files?path=missing.go", testToken, ""), http.StatusNotFound, "file_not_found")
	assertError(t, request(t, api, http.MethodGet, base+"/symbols/missing", testToken, ""), http.StatusNotFound, "symbol_not_found")
	assertError(t, request(t, api, http.MethodGet, base+"/search?query=", testToken, ""), http.StatusUnprocessableEntity, "invalid_query")
	assertError(t, request(t, api, http.MethodGet, base+"/search?query=Alpha&limit=-1", testToken, ""), http.StatusUnprocessableEntity, "invalid_limit")

	var search SearchResponse
	decodeResponse(t, request(t, api, http.MethodGet, base+"/search?query=Alpha&limit=9999", testToken, ""), &search)
	if search.Limit != api.limits.MaxSearchResults {
		t.Fatalf("search limit = %d", search.Limit)
	}
	symbolID := search.Results[0].Symbol.ID
	assertError(t, request(t, api, http.MethodGet, base+"/symbols/"+symbolID+"/graph?direction=sideways", testToken, ""), http.StatusUnprocessableEntity, "invalid_direction")
	assertError(t, request(t, api, http.MethodGet, base+"/symbols/"+symbolID+"/graph?depth=0", testToken, ""), http.StatusUnprocessableEntity, "invalid_depth")
	assertError(t, request(t, api, http.MethodGet, base+"/symbols/missing/graph", testToken, ""), http.StatusNotFound, "symbol_not_found")
	var boundedGraph GraphResponse
	decodeResponse(t, request(t, api, http.MethodGet, base+"/symbols/"+symbolID+"/graph?maxNodes=1", testToken, ""), &boundedGraph)
	if len(boundedGraph.Nodes) != 1 || len(boundedGraph.Edges) != 0 || !boundedGraph.Truncated {
		t.Fatalf("bounded graph broke referential limits: %+v", boundedGraph)
	}

	escape := filepath.Join(filepath.Dir(root), "escape.txt")
	if err := os.WriteFile(escape, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escape, filepath.Join(root, "escape-link")); err == nil {
		if _, err := api.safeFile("escape-link"); err == nil {
			t.Fatal("escaping symlink was accepted")
		}
	}
}

func TestPublicAPIHelpers(t *testing.T) {
	credential, err := GenerateCredential()
	if err != nil || len(credential) != 64 {
		t.Fatalf("credential = %q err=%v", credential, err)
	}
	if validRepositoryID("repo_bad") || validRepositoryID("wrong_0123456789abcdef0123456789abcdef") {
		t.Fatal("invalid repository identity accepted")
	}
	remotes := map[string]string{
		"git@github.com:code-grapher/codegrapher.git":         "github.com/code-grapher/codegrapher",
		"alice@example.com:owner/repo.git":                    "example.com/owner/repo",
		"https://token@example.com/owner/repo.git?secret=yes": "https://example.com/owner/repo",
		"file:///private/repo":                                "",
		"/private/repo":                                       "",
	}
	for input, want := range remotes {
		if got := sanitizeRemote(input); got != want {
			t.Errorf("sanitizeRemote(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := New(nil, Config{Token: testToken}); err == nil {
		t.Fatal("nil index accepted")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, result, err := indexer.Init(root, indexer.Options{})
	if err != nil || !result.Success {
		t.Fatalf("init: %v %+v", err, result)
	}
	defer func() { _ = idx.Close() }()
	if _, err := New(idx, Config{}); err == nil {
		t.Fatal("empty credential accepted")
	}
	api, err := New(idx, Config{Token: testToken, Limits: Limits{MaxTreeEntries: 7}})
	if err != nil {
		t.Fatal(err)
	}
	if api.limits.MaxTreeEntries != 7 || api.limits.MaxFileBytes != DefaultLimits().MaxFileBytes {
		t.Fatalf("partial limits were not normalized: %+v", api.limits)
	}
	if err := api.Serve(context.Background(), failingListener{}); err == nil || !strings.Contains(err.Error(), "accept failed") {
		t.Fatalf("serve error = %v", err)
	}
	internal := httptest.NewRecorder()
	api.internalError(internal)
	assertError(t, internal, http.StatusInternalServerError, "internal_error")
}

type failingListener struct{}

func (failingListener) Accept() (net.Conn, error) { return nil, errors.New("accept failed") }
func (failingListener) Close() error              { return nil }
func (failingListener) Addr() net.Addr            { return &net.TCPAddr{} }

func newTestAPI(t *testing.T, state func() freshness.Status) (string, *Server) {
	t.Helper()
	root := t.TempDir()
	content := "package sample\n\nfunc Alpha() { Beta() }\nfunc Beta() {}\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, result, err := indexer.Init(root, indexer.Options{})
	if err != nil || !result.Success {
		t.Fatalf("init: %v %+v", err, result)
	}
	t.Cleanup(func() { _ = idx.Close() })
	api, err := New(idx, Config{Token: testToken, AllowedOrigins: []string{"http://127.0.0.1:4173"}, Freshness: state})
	if err != nil {
		t.Fatal(err)
	}
	return root, api
}

func request(t *testing.T, api *Server, method, target, token, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, req)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatal(err)
	}
}
func assertError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	var body ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != code || body.RequestID == "" {
		t.Fatalf("error = %+v, want %s", body, code)
	}
}
