# Server Indexer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create `github.com/code-grapher/server` — a Go HTTP server with a single `POST /index/{git_host}/{org}/{repo}` endpoint that clones repos, indexes them with codegrapher, manages disk usage, and deploys to the `ai` VPS via Caddy + systemd.

**Architecture:** Single static binary (`CGO_ENABLED=0`). Six focused internal packages (`config`, `manifest`, `gitops`, `indexing`, `eviction`, `handler`) wired by `cmd/server/main.go`. Persists state to `manifest.json`. Background goroutine for `git gc` after each index.

**Tech Stack:** Go stdlib (`net/http`, `os/exec`, `sync`, `encoding/json`), `github.com/code-grapher/codegrapher/indexer` (library — must complete `2026-06-13-codegrapher-custom-dir-compress` plan first), Caddy (reverse proxy + HTTPS), systemd, GitHub Actions

**Prerequisite:** The `2026-06-13-codegrapher-custom-dir-compress` plan must be complete and merged before Task 5.

---

## File Map

```
server/
  cmd/server/main.go
  internal/
    config/config.go
    manifest/manifest.go
    gitops/gitops.go
    indexing/indexing.go
    eviction/eviction.go
    handler/handler.go
  go.mod
  go.sum
  deploy/
    Caddyfile
    server.service
  .github/
    workflows/
      ci.yml
```

---

### Task 1: Create GitHub repo and initialize Go module

- [ ] **Create private GitHub repo in `code-grapher` org**

```bash
gh repo create code-grapher/server --private --description "Codegrapher indexer HTTP server"
```

Expected: repo created at `github.com/code-grapher/server`.

- [ ] **Clone the repo locally**

```bash
cd /Users/alexandertrakhimenok/projects/code-grapher
gh repo clone code-grapher/server server
cd server
```

- [ ] **Initialize Go module**

```bash
go mod init github.com/code-grapher/server
```

- [ ] **Create directory structure**

```bash
mkdir -p cmd/server internal/config internal/manifest internal/gitops internal/indexing internal/eviction internal/handler deploy
```

- [ ] **Create `.gitignore`**

```
server
*.env
.env
```

Save to `.gitignore`.

- [ ] **Initial commit**

```bash
git add go.mod .gitignore
git commit -m "chore: initialize Go module"
git push -u origin main
```

---

### Task 2: Config package

**Files:**
- Create: `internal/config/config.go`

- [ ] **Create `internal/config/config.go`**

```go
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all server configuration parsed from environment variables.
type Config struct {
	BaseDir    string // BASE_DIR: root for repos/ and codegraphs/
	Port       string // PORT: listen port (default "8080")
	MaxTotalGB int64  // MAX_TOTAL_GB: eviction threshold in GB (default 20)
	MaxRepos   int    // MAX_REPOS: max repo count before eviction (default 100)
}

// Load reads configuration from environment variables.
// Returns an error if BASE_DIR is not set.
func Load() (*Config, error) {
	baseDir := os.Getenv("BASE_DIR")
	if baseDir == "" {
		return nil, fmt.Errorf("BASE_DIR env var is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	maxGB := int64(20)
	if v := os.Getenv("MAX_TOTAL_GB"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("MAX_TOTAL_GB must be a positive integer, got %q", v)
		}
		maxGB = n
	}

	maxRepos := 100
	if v := os.Getenv("MAX_REPOS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("MAX_REPOS must be a positive integer, got %q", v)
		}
		maxRepos = n
	}

	return &Config{
		BaseDir:    baseDir,
		Port:       port,
		MaxTotalGB: maxGB,
		MaxRepos:   maxRepos,
	}, nil
}
```

- [ ] **Verify it compiles**

```bash
go build ./internal/config/
```

Expected: clean.

- [ ] **Commit**

```bash
git add internal/config/config.go
git commit -m "feat: config package"
```

---

### Task 3: Manifest package

**Files:**
- Create: `internal/manifest/manifest.go`

- [ ] **Create `internal/manifest/manifest.go`**

```go
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RepoEntry records metadata about one indexed repository.
type RepoEntry struct {
	ClonedAt       time.Time `json:"cloned_at"`
	LastIndexedAt  time.Time `json:"last_indexed_at"`
	RepoSizeBytes  int64     `json:"repo_size_bytes"`
	GraphSizeBytes int64     `json:"graph_size_bytes"`
}

// Manifest is an in-memory registry of indexed repositories, backed by a JSON file.
type Manifest struct {
	mu      sync.RWMutex
	path    string
	entries map[string]*RepoEntry // key: "{git_host}/{org}/{repo}"
}

// Load reads the manifest from path (or starts empty if the file doesn't exist).
func Load(path string) (*Manifest, error) {
	m := &Manifest{path: path, entries: make(map[string]*RepoEntry)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	if err := json.Unmarshal(data, &m.entries); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return m, nil
}

// Get returns a copy of the entry for repoID, and whether it exists.
func (m *Manifest) Get(repoID string) (RepoEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[repoID]
	if !ok {
		return RepoEntry{}, false
	}
	return *e, true
}

// Upsert sets or updates the entry for repoID and flushes to disk.
func (m *Manifest) Upsert(repoID string, entry RepoEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[repoID] = &entry
	return m.flush()
}

// Delete removes repoID from the manifest and flushes to disk.
func (m *Manifest) Delete(repoID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, repoID)
	return m.flush()
}

// All returns a snapshot of all entries (copy, safe to iterate without lock).
func (m *Manifest) All() map[string]RepoEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]RepoEntry, len(m.entries))
	for k, v := range m.entries {
		out[k] = *v
	}
	return out
}

// TotalSizeBytes returns the sum of RepoSizeBytes + GraphSizeBytes for all entries.
func (m *Manifest) TotalSizeBytes() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var total int64
	for _, e := range m.entries {
		total += e.RepoSizeBytes + e.GraphSizeBytes
	}
	return total
}

// Count returns the number of entries.
func (m *Manifest) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

// flush writes entries to disk. Caller must hold mu.
func (m *Manifest) flush() error {
	data, err := json.MarshalIndent(m.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(m.path, data, 0o644)
}
```

- [ ] **Verify it compiles**

```bash
go build ./internal/manifest/
```

Expected: clean.

- [ ] **Commit**

```bash
git add internal/manifest/manifest.go
git commit -m "feat: manifest package"
```

---

### Task 4: Git operations package

**Files:**
- Create: `internal/gitops/gitops.go`

- [ ] **Create `internal/gitops/gitops.go`**

```go
package gitops

import (
	"fmt"
	"os"
	"os/exec"
)

// Clone performs a shallow HEAD-only clone of url into destDir.
// destDir must not exist yet (or be empty).
func Clone(url, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", destDir, err)
	}
	cmd := exec.Command("git", "clone", "--depth=1", "--single-branch", url, destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone: %w\n%s", err, out)
	}
	return nil
}

// Fetch updates an existing shallow clone to the latest HEAD.
func Fetch(repoDir string) error {
	fetch := exec.Command("git", "-C", repoDir, "fetch", "--depth=1", "origin", "HEAD")
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %w\n%s", err, out)
	}
	reset := exec.Command("git", "-C", repoDir, "reset", "--hard", "FETCH_HEAD")
	if out, err := reset.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset: %w\n%s", err, out)
	}
	return nil
}

// GCBackground runs git gc --aggressive --prune=now in the background.
// Errors are silently discarded (gc is best-effort).
func GCBackground(repoDir string) {
	go func() {
		cmd := exec.Command("git", "-C", repoDir, "gc", "--aggressive", "--prune=now")
		_ = cmd.Run()
	}()
}

// Remove deletes repoDir from the filesystem.
func Remove(repoDir string) error {
	return os.RemoveAll(repoDir)
}
```

- [ ] **Verify it compiles**

```bash
go build ./internal/gitops/
```

Expected: clean.

- [ ] **Commit**

```bash
git add internal/gitops/gitops.go
git commit -m "feat: gitops package (clone, fetch, gc)"
```

---

### Task 5: Indexing package (requires codegrapher library plan complete)

**Files:**
- Create: `internal/indexing/indexing.go`

- [ ] **Add codegrapher as a dependency**

```bash
go get github.com/code-grapher/codegrapher@latest
```

Expected: `go.mod` and `go.sum` updated.

- [ ] **Create `internal/indexing/indexing.go`**

```go
package indexing

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/code-grapher/codegrapher/indexer"
)

// Index runs the codegrapher indexer on repoDir, storing the codegraph at cgDir.
// If cgDir already contains a codegraph (or compressed .zst), it re-indexes.
// If not yet indexed, it initializes and indexes.
// Returns an error if the index operation fails.
func Index(repoDir, cgDir string) error {
	opts := indexer.Options{
		CodeGraphDir:  cgDir,
		CompressGraph: true,
	}

	if isIndexed(cgDir) {
		idx, err := indexer.Open(repoDir, opts)
		if err != nil {
			return fmt.Errorf("open indexer: %w", err)
		}
		defer idx.Close()
		result := idx.IndexAll(opts)
		if !result.Success {
			return fmt.Errorf("re-index failed: %d errors", len(result.Errors))
		}
		return nil
	}

	idx, result, err := indexer.Init(repoDir, opts)
	if err != nil {
		return fmt.Errorf("init indexer: %w", err)
	}
	defer idx.Close()
	if !result.Success {
		return fmt.Errorf("index failed: %d errors", len(result.Errors))
	}
	return nil
}

// isIndexed returns true if cgDir contains codegraph.db or codegraph.db.zst.
func isIndexed(cgDir string) bool {
	if _, err := os.Stat(filepath.Join(cgDir, "codegraph.db")); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join(cgDir, "codegraph.db.zst"))
	return err == nil
}
```

- [ ] **Verify it compiles**

```bash
go build ./internal/indexing/
```

Expected: clean.

- [ ] **Commit**

```bash
git add internal/indexing/indexing.go go.mod go.sum
git commit -m "feat: indexing package wrapping codegrapher library"
```

---

### Task 6: Eviction package

**Files:**
- Create: `internal/eviction/eviction.go`

- [ ] **Create `internal/eviction/eviction.go`**

```go
package eviction

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/code-grapher/server/internal/manifest"
)

// Config holds eviction thresholds.
type Config struct {
	MaxTotalBytes int64 // evict oldest until total size fits
	MaxRepos      int   // evict oldest if count exceeds this
}

// candidate is a repo eligible for eviction.
type candidate struct {
	id            string
	lastIndexedAt time.Time
}

// Evict removes the oldest repos from baseDir until both thresholds are satisfied.
// It deletes repos/{id} and codegraphs/{id} from baseDir, and removes the manifest entry.
func Evict(m *manifest.Manifest, baseDir string, cfg Config) error {
	for {
		total := m.TotalSizeBytes()
		count := m.Count()

		if total <= cfg.MaxTotalBytes && count <= cfg.MaxRepos {
			break
		}

		// Find the oldest entry.
		oldest := oldestEntry(m)
		if oldest == "" {
			break // nothing left to evict
		}

		// Delete from filesystem.
		repoPath := filepath.Join(baseDir, "repos", oldest)
		graphPath := filepath.Join(baseDir, "codegraphs", oldest)
		os.RemoveAll(repoPath)
		os.RemoveAll(graphPath)

		// Remove from manifest.
		if err := m.Delete(oldest); err != nil {
			return err
		}
	}
	return nil
}

// oldestEntry returns the repoID with the smallest LastIndexedAt, or "" if empty.
func oldestEntry(m *manifest.Manifest) string {
	all := m.All()
	if len(all) == 0 {
		return ""
	}
	candidates := make([]candidate, 0, len(all))
	for id, e := range all {
		candidates = append(candidates, candidate{id: id, lastIndexedAt: e.LastIndexedAt})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].lastIndexedAt.Before(candidates[j].lastIndexedAt)
	})
	return candidates[0].id
}
```

- [ ] **Verify it compiles**

```bash
go build ./internal/eviction/
```

Expected: clean.

- [ ] **Commit**

```bash
git add internal/eviction/eviction.go
git commit -m "feat: eviction package"
```

---

### Task 7: Size measurement helper

**Files:**
- Create: `internal/manifest/size.go`

- [ ] **Create `internal/manifest/size.go`**

```go
package manifest

import (
	"os"
	"path/filepath"
)

// DirSizeBytes returns the total size in bytes of all files under dir.
// Returns 0 if dir does not exist.
func DirSizeBytes(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err != nil || fi == nil {
			return nil
		}
		if !fi.IsDir() {
			total += fi.Size()
		}
		return nil
	})
	return total
}
```

- [ ] **Verify it compiles**

```bash
go build ./internal/manifest/
```

Expected: clean.

- [ ] **Commit**

```bash
git add internal/manifest/size.go
git commit -m "feat: DirSizeBytes helper"
```

---

### Task 8: HTTP handler

**Files:**
- Create: `internal/handler/handler.go`

- [ ] **Create `internal/handler/handler.go`**

```go
package handler

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/code-grapher/server/internal/eviction"
	"github.com/code-grapher/server/internal/gitops"
	"github.com/code-grapher/server/internal/indexing"
	"github.com/code-grapher/server/internal/manifest"
)

// Handler handles POST /index/{git_host}/{org}/{repo}.
type Handler struct {
	baseDir string
	mf      *manifest.Manifest
	evCfg   eviction.Config

	mu    sync.Mutex            // guards repoLocks
	locks map[string]*sync.Mutex // per-repo lock
}

// New creates a Handler.
func New(baseDir string, mf *manifest.Manifest, evCfg eviction.Config) *Handler {
	return &Handler{
		baseDir: baseDir,
		mf:      mf,
		evCfg:   evCfg,
		locks:   make(map[string]*sync.Mutex),
	}
}

// ServeHTTP handles all requests. Routes:
//
//	POST /index/{git_host}/{org}/{repo}[?force=true]
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse: /index/{git_host}/{org}/{repo}
	path := strings.TrimPrefix(r.URL.Path, "/index/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		http.Error(w, "invalid repo id: want {git_host}/{org}/{repo}", http.StatusBadRequest)
		return
	}
	repoID := strings.Join(parts, "/") // e.g. "github.com/org/repo"
	force := r.URL.Query().Get("force") == "true"

	if err := h.index(repoID, force); err != nil {
		log.Printf("ERROR indexing %s: %v", repoID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) index(repoID string, force bool) error {
	repoPath := filepath.Join(h.baseDir, "repos", repoID)
	cgPath := filepath.Join(h.baseDir, "codegraphs", repoID)

	// Per-repo lock prevents concurrent indexing of the same repo.
	lock := h.repoLock(repoID)
	lock.Lock()
	defer lock.Unlock()

	cloneURL := "https://" + repoID

	// Ensure parent dirs exist.
	if err := os.MkdirAll(filepath.Dir(repoPath), 0o755); err != nil {
		return fmt.Errorf("mkdir repos: %w", err)
	}
	if err := os.MkdirAll(cgPath, 0o755); err != nil {
		return fmt.Errorf("mkdir codegraphs: %w", err)
	}

	// Clone or update.
	cloned := false
	if _, err := os.Stat(repoPath); os.IsNotExist(err) || force {
		if force {
			_ = gitops.Remove(repoPath)
		}
		if err := gitops.Clone(cloneURL, repoPath); err != nil {
			return fmt.Errorf("clone %s: %w", cloneURL, err)
		}
		cloned = true
	} else {
		if err := gitops.Fetch(repoPath); err != nil {
			return fmt.Errorf("fetch %s: %w", repoID, err)
		}
	}

	// Index.
	if err := indexing.Index(repoPath, cgPath); err != nil {
		return fmt.Errorf("index %s: %w", repoID, err)
	}

	// Measure sizes.
	repoSize := manifest.DirSizeBytes(repoPath)
	graphSize := manifest.DirSizeBytes(cgPath)

	// Update manifest.
	entry, exists := h.mf.Get(repoID)
	if !exists || cloned {
		entry.ClonedAt = time.Now()
	}
	entry.LastIndexedAt = time.Now()
	entry.RepoSizeBytes = repoSize
	entry.GraphSizeBytes = graphSize
	if err := h.mf.Upsert(repoID, entry); err != nil {
		log.Printf("WARN manifest upsert %s: %v", repoID, err)
	}

	// Evict if over threshold.
	if err := eviction.Evict(h.mf, h.baseDir, h.evCfg); err != nil {
		log.Printf("WARN eviction: %v", err)
	}

	// Background git gc.
	gitops.GCBackground(repoPath)

	return nil
}

func (h *Handler) repoLock(repoID string) *sync.Mutex {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.locks[repoID] == nil {
		h.locks[repoID] = &sync.Mutex{}
	}
	return h.locks[repoID]
}
```

- [ ] **Verify it compiles**

```bash
go build ./internal/handler/
```

Expected: clean.

- [ ] **Commit**

```bash
git add internal/handler/handler.go
git commit -m "feat: HTTP handler for POST /index"
```

---

### Task 9: Main entry point

**Files:**
- Create: `cmd/server/main.go`

- [ ] **Create `cmd/server/main.go`**

```go
package main

import (
	"log"
	"net/http"
	"path/filepath"

	"github.com/code-grapher/server/internal/config"
	"github.com/code-grapher/server/internal/eviction"
	"github.com/code-grapher/server/internal/handler"
	"github.com/code-grapher/server/internal/manifest"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	mf, err := manifest.Load(filepath.Join(cfg.BaseDir, "manifest.json"))
	if err != nil {
		log.Fatalf("manifest: %v", err)
	}

	evCfg := eviction.Config{
		MaxTotalBytes: cfg.MaxTotalGB * 1024 * 1024 * 1024,
		MaxRepos:      cfg.MaxRepos,
	}

	h := handler.New(cfg.BaseDir, mf, evCfg)

	mux := http.NewServeMux()
	mux.Handle("/index/", h)

	addr := ":" + cfg.Port
	log.Printf("codegrapher server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
```

- [ ] **Build the binary**

```bash
CGO_ENABLED=0 go build -o server ./cmd/server
```

Expected: `./server` binary created.

- [ ] **Smoke-test locally**

```bash
BASE_DIR=/tmp/cg-test ./server &
SERVER_PID=$!
sleep 1

# Should return 400 for bad path
curl -s -o /dev/null -w "%{http_code}" -X POST http://localhost:8080/index/bad
# Expected: 400

# Clean up
kill $SERVER_PID
rm -rf /tmp/cg-test
```

- [ ] **Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: main entry point"
```

---

### Task 10: Deploy to VPS

**Files:**
- Create: `deploy/Caddyfile`
- Create: `deploy/server.service`

- [ ] **Create `deploy/Caddyfile`**

Replace `codegrapher.YOUR_DOMAIN` with the actual domain or IP:

```
codegrapher.YOUR_DOMAIN {
    reverse_proxy localhost:8080
}
```

- [ ] **Create `deploy/server.service`**

```ini
[Unit]
Description=Codegrapher Indexer Server
After=network.target

[Service]
Type=simple
User=codegrapher
EnvironmentFile=/etc/codegrapher/env
ExecStart=/usr/local/bin/codegrapher-server
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

- [ ] **Build Linux binary**

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server-linux ./cmd/server
```

- [ ] **Deploy to the `ai` machine**

```bash
# Connect via vm (opens SSH session to 'ai')
# Run the following on the remote machine via vm:

vm << 'EOF'
# Create user and dirs
sudo useradd -r -s /bin/false codegrapher 2>/dev/null || true
sudo mkdir -p /etc/codegrapher /var/lib/codegrapher

# Create env file
sudo tee /etc/codegrapher/env > /dev/null << 'ENVEOF'
BASE_DIR=/var/lib/codegrapher
PORT=8080
MAX_TOTAL_GB=20
MAX_REPOS=100
ENVEOF

sudo chown codegrapher:codegrapher /var/lib/codegrapher

# Install Caddy if not present
which caddy || (curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg && curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list && sudo apt update && sudo apt install -y caddy)

echo "Setup complete"
EOF
```

- [ ] **Copy binary and start service**

```bash
# Copy binary
scp server-linux ai:/tmp/codegrapher-server
vm << 'EOF'
sudo mv /tmp/codegrapher-server /usr/local/bin/codegrapher-server
sudo chmod +x /usr/local/bin/codegrapher-server
EOF

# Copy systemd unit
scp deploy/server.service ai:/tmp/server.service
vm << 'EOF'
sudo mv /tmp/server.service /etc/systemd/system/codegrapher-server.service
sudo systemctl daemon-reload
sudo systemctl enable codegrapher-server
sudo systemctl start codegrapher-server
sudo systemctl status codegrapher-server
EOF
```

- [ ] **Update Caddyfile with domain and reload Caddy**

```bash
# Edit deploy/Caddyfile with actual domain first, then:
scp deploy/Caddyfile ai:/tmp/Caddyfile
vm << 'EOF'
sudo mv /tmp/Caddyfile /etc/caddy/Caddyfile
sudo systemctl reload caddy
EOF
```

- [ ] **Verify the endpoint is reachable**

```bash
# Replace with actual domain
curl -s -o /dev/null -w "%{http_code}" -X POST https://codegrapher.YOUR_DOMAIN/index/bad/path
# Expected: 400

curl -s -w "\n%{http_code}" -X POST https://codegrapher.YOUR_DOMAIN/index/github.com/code-grapher/server
# Expected: 200 (may take 30–60s for clone + index)
```

- [ ] **Commit deploy files**

```bash
git add deploy/Caddyfile deploy/server.service
git commit -m "deploy: Caddyfile and systemd unit"
git push
```

---

### Task 11: GitHub Actions CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Create `.github/workflows/ci.yml`**

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Vet
        run: go vet ./...

      - name: Build
        run: CGO_ENABLED=0 go build ./...

      - name: Test
        run: CGO_ENABLED=0 go test -count=1 -race ./...
```

- [ ] **Verify the workflow file is valid YAML**

```bash
python3 -c "import yaml, sys; yaml.safe_load(open('.github/workflows/ci.yml'))" && echo "valid"
```

Expected: `valid`

- [ ] **Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add GitHub Actions workflow (vet, build, test)"
git push
```

Expected: CI runs automatically on GitHub within ~30s of push.
