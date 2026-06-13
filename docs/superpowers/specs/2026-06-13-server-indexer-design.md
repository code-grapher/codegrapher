# Server Indexer Design

**Date:** 2026-06-13  
**Status:** Approved

---

## Overview

Build a private Go HTTP server (`code-grapher/server`) that accepts repo indexing requests, clones HEAD-only, runs the codegrapher indexer (via library, not binary), manages disk usage with eviction, and runs on the `ai` VPS reachable via `vm`.

Requires two small additions to the `codegrapher` library first.

---

## Part 1 — Codegrapher Library Changes

Two new fields added to `indexer.Options`:

### `CodeGraphDir string`
When non-empty, stores the codegraph data at this absolute path instead of `{projectRoot}/.codegraph/`. Lets callers decouple the index from the source tree.

**Files modified:**
- `indexer/dir.go` — add `resolveCodeGraphDir(root, override)`, `IsInitializedAt(cgDir)`
- `indexer/indexer.go` — add `codeGraphDir string` field to `Indexer`; update `newIndexer`
- `indexer/init.go` — thread `cgDir` through `Init`, `Open`, decompression in `Open`

### `CompressGraph bool`
When true, after a successful `IndexAll`:
1. Run `VACUUM` on the SQLite db (compacts freed pages, 20–40% reduction)
2. Compress `codegraph.db` → `codegraph.db.zst` using zstd
3. Delete the uncompressed `codegraph.db`

On subsequent `Open` calls: if `codegraph.db` is absent but `codegraph.db.zst` exists, decompress in-place before opening.

**New dependency:** `github.com/klauspost/compress/zstd` (pure Go, CGO_ENABLED=0 compatible)

**Files modified:**
- `indexer/indexer.go` — add `compressCodeGraph(cgDir)` helper; call it at end of `IndexAll`
- `store/store.go` — add `Vacuum() error` method
- `go.mod` / `go.sum`

---

## Part 2 — Server

### Repository
`github.com/code-grapher/server` (private, Go module `github.com/code-grapher/server`)

### Storage Layout

```
$BASE_DIR/
  repos/
    github.com/org/repo/     ← shallow git clone (HEAD only)
  codegraphs/
    github.com/org/repo/     ← codegraph data (codegraph.db.zst after indexing)
  manifest.json              ← registry of all indexed repos
```

### Endpoint

```
POST /index/{git_host}/{org}/{repo}
     ?force=true              ← optional: re-clone instead of git fetch
```

- `200 OK` — success (idempotent, safe to retry)
- `400 Bad Request` — malformed repo ID
- `500 Internal Server Error` — clone or indexing failure (body has error message)

### Indexing Flow

1. Parse `{git_host}/{org}/{repo}` → clone URL `https://{git_host}/{org}/{repo}`
2. Acquire per-repo `sync.Mutex` (prevents concurrent double-indexing)
3. Clone or update:
   - First time: `git clone --depth=1 --single-branch {url} {repoPath}`
   - Subsequent: `git -C {repoPath} fetch --depth=1 origin HEAD && git -C {repoPath} reset --hard FETCH_HEAD`
   - `?force=true`: delete existing clone, re-clone
4. Index:
   - Not yet indexed: `indexer.Init(repoPath, Options{CodeGraphDir: cgPath, CompressGraph: true})`
   - Already indexed: `indexer.Open(repoPath, Options{CodeGraphDir: cgPath, CompressGraph: true})` + `IndexAll`
5. Measure sizes via `du -sb`, update `manifest.json`
6. Run eviction check
7. Spawn background goroutine: `git -C {repoPath} gc --aggressive --prune=now`
8. Return `200`

### Manifest

In-memory `map[string]*RepoEntry` guarded by `sync.RWMutex`, persisted to `$BASE_DIR/manifest.json` after each write.

```go
type RepoEntry struct {
    ClonedAt       time.Time `json:"cloned_at"`
    LastIndexedAt  time.Time `json:"last_indexed_at"`
    RepoSizeBytes  int64     `json:"repo_size_bytes"`
    GraphSizeBytes int64     `json:"graph_size_bytes"`
}
```

### Eviction

Triggered after each successful index. Config via env:

| Env var | Default | Meaning |
|---|---|---|
| `MAX_TOTAL_GB` | `20` | Evict oldest until total `RepoSizeBytes + GraphSizeBytes` fits |
| `MAX_REPOS` | `100` | Evict oldest if repo count exceeds this |

Eviction = delete `repos/{id}` + `codegraphs/{id}` + remove manifest entry.  
"Oldest" = smallest `last_indexed_at`.

### Config (env vars)

| Var | Default | Required |
|---|---|---|
| `BASE_DIR` | — | yes |
| `PORT` | `8080` | no |
| `MAX_TOTAL_GB` | `20` | no |
| `MAX_REPOS` | `100` | no |

### Deployment

- Build: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server ./cmd/server`
- Copy to `ai` via `vm` (SSH)
- **Caddy** as reverse proxy — automatic HTTPS via Let's Encrypt
- **systemd** unit for process management

### File Structure

```
server/
  cmd/server/main.go          ← entry point, wires everything
  internal/
    config/config.go          ← env var parsing, Config struct
    manifest/manifest.go      ← RepoEntry, in-memory map, JSON flush
    gitops/gitops.go          ← clone, fetch, reset, gc (shells out to git)
    indexing/indexing.go      ← wraps codegrapher indexer.Init / Open+IndexAll
    eviction/eviction.go      ← eviction logic against manifest
    handler/handler.go        ← HTTP handler: POST /index/...
  go.mod
  go.sum
  deploy/
    Caddyfile
    server.service            ← systemd unit template
```

---

## Out of Scope

- Authentication (none for now)
- `ingitdb` / export compression (separate task)
- Any GET endpoints (no file serving)
