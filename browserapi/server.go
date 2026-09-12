package browserapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/specscore/codegrapher/freshness"
	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/query"
	"github.com/strongo/buildinfo"
)

var DefaultAllowedOrigins = []string{"https://codegrapher.com", "https://codegrapher.dev"}

type Config struct {
	Token          string
	AllowedOrigins []string
	Limits         Limits
	ServerVersion  string
	Freshness      func() freshness.Status
}

type Server struct {
	root, repositoryID, token, version string
	idx                                *indexer.Indexer
	limits                             Limits
	origins                            map[string]struct{}
	freshness                          func() freshness.Status
	handler                            http.Handler
}

func New(idx *indexer.Indexer, config Config) (*Server, error) {
	if idx == nil {
		return nil, errors.New("browser API requires an index")
	}
	if strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("browser API requires a credential")
	}
	id, err := loadOrCreateRepositoryID(idx.ProjectRoot())
	if err != nil {
		return nil, err
	}
	config.Limits = normalizedLimits(config.Limits)
	if config.ServerVersion == "" {
		config.ServerVersion = buildinfo.Get("codegrapher").Version
	}
	if config.Freshness == nil {
		config.Freshness = func() freshness.Status { return freshness.Status{IndexCurrent: true} }
	}
	origins := map[string]struct{}{}
	for _, origin := range append(append([]string{}, DefaultAllowedOrigins...), config.AllowedOrigins...) {
		origin = strings.TrimSuffix(strings.TrimSpace(origin), "/")
		if origin != "" && origin != "*" {
			origins[origin] = struct{}{}
		}
	}
	server := &Server{root: idx.ProjectRoot(), repositoryID: id, token: config.Token, version: config.ServerVersion, idx: idx, limits: config.Limits, origins: origins, freshness: config.Freshness}
	server.handler = server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler { return s.handler }
func (s *Server) RepositoryID() string  { return s.repositoryID }
func (s *Server) Revision() (string, error) {
	value, err := snapshotIndex(s.idx)
	return value.revision, err
}

func normalizedLimits(limits Limits) Limits {
	defaults := DefaultLimits()
	if limits.MaxTreeEntries <= 0 {
		limits.MaxTreeEntries = defaults.MaxTreeEntries
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = defaults.MaxFileBytes
	}
	if limits.MaxSearchResults <= 0 {
		limits.MaxSearchResults = defaults.MaxSearchResults
	}
	if limits.MaxGraphDepth <= 0 {
		limits.MaxGraphDepth = defaults.MaxGraphDepth
	}
	if limits.MaxGraphNodes <= 0 {
		limits.MaxGraphNodes = defaults.MaxGraphNodes
	}
	if limits.MaxGraphEdges <= 0 {
		limits.MaxGraphEdges = defaults.MaxGraphEdges
	}
	return limits
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	server := &http.Server{Handler: s.handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		case <-done:
		}
	}()
	err := server.Serve(listener)
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+BasePath+"/status", s.getStatus)
	mux.HandleFunc("GET "+BasePath+"/repositories", s.listRepositories)
	mux.HandleFunc("GET "+BasePath+"/repositories/{repositoryId}", s.getRepository)
	mux.HandleFunc("GET "+BasePath+"/repositories/{repositoryId}/revisions/{revision}/tree", s.getTree)
	mux.HandleFunc("GET "+BasePath+"/repositories/{repositoryId}/revisions/{revision}/files", s.getFile)
	mux.HandleFunc("GET "+BasePath+"/repositories/{repositoryId}/revisions/{revision}/symbols/{symbolId}", s.getSymbol)
	mux.HandleFunc("GET "+BasePath+"/repositories/{repositoryId}/revisions/{revision}/search", s.search)
	mux.HandleFunc("GET "+BasePath+"/repositories/{repositoryId}/revisions/{revision}/symbols/{symbolId}/graph", s.graph)
	mux.HandleFunc("OPTIONS "+BasePath+"/{rest...}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return s.security(mux)
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSuffix(r.Header.Get("Origin"), "/")
		if origin != "" {
			if _, allowed := s.origins[origin]; !allowed {
				s.writeError(w, http.StatusForbidden, "origin_forbidden", "Browser origin is not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, x-ms-useragent, x-ms-client-request-id, traceparent")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method != http.MethodOptions {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if len(provided) != len(s.token) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) != 1 {
				s.writeError(w, http.StatusUnauthorized, "unauthorized", "A valid browser credential is required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) getStatus(w http.ResponseWriter, _ *http.Request) {
	snapshot, err := snapshotIndex(s.idx)
	if err != nil {
		s.internalError(w)
		return
	}
	s.writeJSON(w, http.StatusOK, StatusResponse{APIVersion: "v1", ServerVersion: s.version, Capabilities: []string{"repositories", "tree", "files", "symbols", "search", "graph"}, RepositoryCount: 1, Freshness: publicFreshness(s.freshness(), snapshot.indexedAt), Limits: s.limits})
}

func (s *Server) listRepositories(w http.ResponseWriter, _ *http.Request) {
	repository, err := s.repository()
	if err != nil {
		s.internalError(w)
		return
	}
	s.writeJSON(w, http.StatusOK, struct {
		Repositories []Repository `json:"repositories"`
	}{[]Repository{repository}})
}

func (s *Server) getRepository(w http.ResponseWriter, r *http.Request) {
	if !s.validRepository(w, r) {
		return
	}
	repository, err := s.repository()
	if err != nil {
		s.internalError(w)
		return
	}
	s.writeJSON(w, http.StatusOK, repository)
}

func (s *Server) repository() (Repository, error) {
	snapshot, err := snapshotIndex(s.idx)
	if err != nil {
		return Repository{}, err
	}
	remote, branch, head := gitMetadata(s.root)
	label := filepath.Base(s.root)
	if label == "." || label == string(filepath.Separator) {
		label = "repository"
	}
	return Repository{ID: s.repositoryID, Label: label, Remote: remote, Branch: branch, HeadCommit: head, Revision: snapshot.revision, Freshness: publicFreshness(s.freshness(), snapshot.indexedAt), FileCount: snapshot.fileCount, SymbolCount: snapshot.symbolCount, EdgeCount: snapshot.edgeCount}, nil
}

func (s *Server) getTree(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.validRevision(w, r)
	if !ok {
		return
	}
	directory, err := normalizeRelativePath(r.URL.Query().Get("path"), true)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_path", "Path must be a normalized repository-relative path")
		return
	}
	prefix := ""
	if directory != "" {
		prefix = directory + "/"
	}
	entries := map[string]TreeEntry{}
	for filePath, record := range snapshot.files {
		if !strings.HasPrefix(filePath, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(filePath, prefix)
		if remainder == "" {
			continue
		}
		segment, rest, _ := strings.Cut(remainder, "/")
		entryPath := segment
		if directory != "" {
			entryPath = directory + "/" + segment
		}
		if rest != "" {
			entries[segment] = TreeEntry{Name: segment, Path: entryPath, Kind: "directory"}
			continue
		}
		entries[segment] = TreeEntry{Name: segment, Path: entryPath, Kind: "file", Size: record.Size, Language: string(record.Language), ContentHash: record.ContentHash}
	}
	if directory != "" && len(entries) == 0 {
		s.writeError(w, http.StatusNotFound, "tree_not_found", "Indexed directory was not found")
		return
	}
	ordered := make([]TreeEntry, 0, len(entries))
	for _, entry := range entries {
		ordered = append(ordered, entry)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Kind != ordered[j].Kind {
			return ordered[i].Kind == "directory"
		}
		return ordered[i].Name < ordered[j].Name
	})
	truncated := len(ordered) > s.limits.MaxTreeEntries
	if truncated {
		ordered = ordered[:s.limits.MaxTreeEntries]
	}
	s.writeJSON(w, http.StatusOK, TreeResponse{RepositoryID: s.repositoryID, Revision: snapshot.revision, Path: directory, Entries: ordered, Limit: s.limits.MaxTreeEntries, Truncated: truncated, Freshness: publicFreshness(s.freshness(), snapshot.indexedAt)})
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.validRevision(w, r)
	if !ok {
		return
	}
	relative, err := normalizeRelativePath(r.URL.Query().Get("path"), false)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_path", "Path must be a normalized repository-relative path")
		return
	}
	record, exists := snapshot.files[relative]
	if !exists {
		s.writeError(w, http.StatusNotFound, "file_not_found", "Indexed file was not found")
		return
	}
	if record.Size > int64(s.limits.MaxFileBytes) {
		s.writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", "Indexed file exceeds the response limit")
		return
	}
	resolved, err := s.safeFile(relative)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_path", "Path does not resolve to a regular file in the repository")
		return
	}
	file, err := os.Open(resolved)
	if err != nil {
		s.writeError(w, http.StatusConflict, "index_stale", "Indexed file is no longer readable")
		return
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, int64(s.limits.MaxFileBytes)+1))
	if err != nil {
		s.internalError(w)
		return
	}
	if len(content) > s.limits.MaxFileBytes {
		s.writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", "File exceeds the response limit")
		return
	}
	if indexer.HashContent(content) != record.ContentHash {
		s.writeError(w, http.StatusConflict, "index_stale", "File changed after the advertised index revision")
		return
	}
	lineCount := 0
	if len(content) > 0 {
		lineCount = strings.Count(string(content), "\n")
		if content[len(content)-1] != '\n' {
			lineCount++
		}
	}
	s.writeJSON(w, http.StatusOK, FileResponse{RepositoryID: s.repositoryID, Revision: snapshot.revision, Path: relative, Language: string(record.Language), Size: int64(len(content)), LineCount: lineCount, ContentHash: record.ContentHash, Content: string(content), Freshness: publicFreshness(s.freshness(), snapshot.indexedAt)})
}

func (s *Server) getSymbol(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.validRevision(w, r)
	if !ok {
		return
	}
	node, err := s.findNode(r.PathValue("symbolId"))
	if err != nil {
		s.internalError(w)
		return
	}
	if node == nil {
		s.writeError(w, http.StatusNotFound, "symbol_not_found", "Indexed symbol was not found")
		return
	}
	s.writeJSON(w, http.StatusOK, SymbolResponse{RepositoryID: s.repositoryID, Revision: snapshot.revision, Symbol: publicSymbol(*node), Freshness: publicFreshness(s.freshness(), snapshot.indexedAt)})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.validRevision(w, r)
	if !ok {
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("query"))
	if raw == "" || len(raw) > 512 {
		s.writeError(w, http.StatusUnprocessableEntity, "invalid_query", "Search query must contain 1 to 512 characters")
		return
	}
	limit, valid := boundedInt(r.URL.Query().Get("limit"), 20, s.limits.MaxSearchResults)
	if !valid {
		s.writeError(w, http.StatusUnprocessableEntity, "invalid_limit", "Search limit must be a positive integer")
		return
	}
	all := make([]SearchResult, 0)
	for _, store := range s.idx.Stores() {
		results, err := query.SearchNodes(store, raw, query.SearchOptions{Limit: limit + 1})
		if err != nil {
			s.internalError(w)
			return
		}
		for _, result := range results {
			all = append(all, SearchResult{Score: result.Score, Symbol: publicSymbol(result.Node)})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].Symbol.ID < all[j].Symbol.ID
	})
	all = dedupeSearch(all)
	truncated := len(all) > limit
	if truncated {
		all = all[:limit]
	}
	s.writeJSON(w, http.StatusOK, SearchResponse{RepositoryID: s.repositoryID, Revision: snapshot.revision, Query: raw, Results: all, Limit: limit, Truncated: truncated, Freshness: publicFreshness(s.freshness(), snapshot.indexedAt)})
}

func (s *Server) validRepository(w http.ResponseWriter, r *http.Request) bool {
	if r.PathValue("repositoryId") != s.repositoryID {
		s.writeError(w, http.StatusNotFound, "repository_not_found", "Registered repository was not found")
		return false
	}
	return true
}

func (s *Server) validRevision(w http.ResponseWriter, r *http.Request) (snapshot, bool) {
	if !s.validRepository(w, r) {
		return snapshot{}, false
	}
	value, err := snapshotIndex(s.idx)
	if err != nil {
		s.internalError(w)
		return snapshot{}, false
	}
	if r.PathValue("revision") != value.revision {
		s.writeError(w, http.StatusConflict, "revision_changed", "Repository index revision has changed")
		return snapshot{}, false
	}
	return value, true
}

func (s *Server) findNode(id string) (*model.Node, error) {
	for _, store := range s.idx.Stores() {
		node, err := store.GetNodeByID(id)
		if err != nil {
			return nil, err
		}
		if node != nil {
			return node, nil
		}
	}
	return nil, nil
}

func publicSymbol(node model.Node) Symbol {
	filePath := node.FilePath
	if _, err := normalizeRelativePath(filePath, false); err != nil {
		filePath = ""
	}
	return Symbol{ID: node.ID, Kind: string(node.Kind), Name: node.Name, QualifiedName: node.QualifiedName, FilePath: filePath, Language: string(node.Language), Range: SourceRange{StartLine: node.StartLine, EndLine: node.EndLine, StartColumn: node.StartColumn, EndColumn: node.EndColumn}, Signature: node.Signature, Docstring: node.Docstring, Visibility: node.Visibility, Exported: node.IsExported}
}

func dedupeSearch(values []SearchResult) []SearchResult {
	seen := map[string]bool{}
	out := values[:0]
	for _, value := range values {
		if !seen[value.Symbol.ID] {
			seen[value.Symbol.ID] = true
			out = append(out, value)
		}
	}
	return out
}

func boundedInt(raw string, defaultValue, maximum int) (int, bool) {
	if raw == "" {
		return defaultValue, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, false
	}
	if value > maximum {
		value = maximum
	}
	return value, true
}

func normalizeRelativePath(raw string, allowEmpty bool) (string, error) {
	if strings.ContainsRune(raw, 0) || strings.Contains(raw, "\\") || strings.HasPrefix(raw, "/") || (len(raw) >= 2 && raw[1] == ':') {
		return "", errors.New("unsafe path")
	}
	cleaned := path.Clean(raw)
	if raw == "" && allowEmpty {
		return "", nil
	}
	if raw == "" || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != raw {
		return "", errors.New("unsafe path")
	}
	return cleaned, nil
}

func (s *Server) safeFile(relative string) (string, error) {
	current := s.root
	for _, segment := range strings.Split(relative, "/") {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("unsafe file")
		}
	}
	info, err := os.Stat(current)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("not regular")
	}
	resolvedRoot, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("escaped root")
	}
	return resolved, nil
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) writeError(w http.ResponseWriter, status int, code, message string) {
	s.writeJSON(w, status, ErrorResponse{Code: code, Message: message, RequestID: requestID()})
}
func (s *Server) internalError(w http.ResponseWriter) {
	s.writeError(w, http.StatusInternalServerError, "internal_error", "The repository request could not be completed")
}
func requestID() string {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err == nil {
		return hex.EncodeToString(bytes)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}
