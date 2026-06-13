# Codegrapher: Custom CodeGraph Dir + zstd Compression

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `CodeGraphDir` and `CompressGraph` options to `indexer.Options` so callers can store the codegraph outside the project root and compress it with zstd after indexing.

**Architecture:** `CodeGraphDir` threads through a new `resolveCodeGraphDir` helper into `Init`, `Open`, and `newIndexer`. `CompressGraph` triggers VACUUM + zstd compression at the end of `IndexAll`; `Open` transparently decompresses if only `.zst` exists.

**Tech Stack:** Go, `github.com/klauspost/compress/zstd` (pure Go, CGO_ENABLED=0 safe), `modernc.org/sqlite`

---

## File Map

| Action | File | Change |
|---|---|---|
| Modify | `indexer/dir.go` | Add `resolveCodeGraphDir`, `IsInitializedAt` |
| Modify | `indexer/indexer.go` | Add `codeGraphDir` field; update `newIndexer`, `Open`; add compression helpers; call compress in `IndexAll` |
| Modify | `indexer/init.go` | Thread `cgDir` through `Init` |
| Modify | `store/store.go` | Add `Vacuum() error` |
| Modify | `go.mod` / `go.sum` | Add `github.com/klauspost/compress` |
| Modify | `indexer/dir_test.go` | Tests for `resolveCodeGraphDir`, `IsInitializedAt` |
| Modify | `indexer/init_test.go` | Tests for custom `CodeGraphDir` and `CompressGraph` |

---

### Task 1: Add `resolveCodeGraphDir` and `IsInitializedAt` to `indexer/dir.go`

**Files:**
- Modify: `indexer/dir.go`
- Modify: `indexer/dir_test.go`

- [ ] **Write failing tests in `indexer/dir_test.go`**

Add to the existing test file:

```go
func TestResolveCodeGraphDir(t *testing.T) {
    root := "/some/project"
    // no override → falls back to default
    got := resolveCodeGraphDir(root, "")
    want := filepath.Join(root, defaultCodeGraphDir)
    if got != want {
        t.Errorf("resolveCodeGraphDir(%q, %q) = %q, want %q", root, "", got, want)
    }

    // explicit override → used as-is
    override := "/custom/path"
    got = resolveCodeGraphDir(root, override)
    if got != override {
        t.Errorf("resolveCodeGraphDir(%q, %q) = %q, want %q", root, override, got, override)
    }
}

func TestIsInitializedAt(t *testing.T) {
    dir := t.TempDir()

    // empty dir → not initialized
    if IsInitializedAt(dir) {
        t.Fatal("expected not initialized on empty dir")
    }

    // .db present → initialized
    dbPath := filepath.Join(dir, "codegraph.db")
    if err := os.WriteFile(dbPath, []byte("x"), 0o644); err != nil {
        t.Fatal(err)
    }
    if !IsInitializedAt(dir) {
        t.Fatal("expected initialized when codegraph.db exists")
    }

    // remove .db, add .zst → also initialized
    os.Remove(dbPath)
    if err := os.WriteFile(filepath.Join(dir, "codegraph.db.zst"), []byte("x"), 0o644); err != nil {
        t.Fatal(err)
    }
    if !IsInitializedAt(dir) {
        t.Fatal("expected initialized when codegraph.db.zst exists")
    }
}
```

- [ ] **Run tests to verify they fail**

```bash
cd /Users/alexandertrakhimenok/projects/code-grapher/codegrapher
CGO_ENABLED=0 go test ./indexer/ -run "TestResolveCodeGraphDir|TestIsInitializedAt" -v
```

Expected: `FAIL — resolveCodeGraphDir undefined`, `IsInitializedAt undefined`

- [ ] **Add `resolveCodeGraphDir` and `IsInitializedAt` to `indexer/dir.go`**

Add after the existing `IsInitialized` function:

```go
// resolveCodeGraphDir returns the codegraph directory for a project.
// If override is non-empty and absolute, it is used directly.
// Otherwise the default (projectRoot + CodeGraphDirName()) is returned.
func resolveCodeGraphDir(projectRoot, override string) string {
	if override != "" {
		return override
	}
	return filepath.Join(projectRoot, CodeGraphDirName())
}

// IsInitializedAt reports whether a codegraph directory is initialized.
// It accepts the codegraph directory directly (not the project root).
// Considers both codegraph.db and codegraph.db.zst as valid.
func IsInitializedAt(codeGraphDir string) bool {
	fi, err := os.Stat(codeGraphDir)
	if err != nil || !fi.IsDir() {
		return false
	}
	if _, err := os.Stat(filepath.Join(codeGraphDir, "codegraph.db")); err == nil {
		return true
	}
	_, err = os.Stat(filepath.Join(codeGraphDir, "codegraph.db.zst"))
	return err == nil
}
```

- [ ] **Run tests to verify they pass**

```bash
CGO_ENABLED=0 go test ./indexer/ -run "TestResolveCodeGraphDir|TestIsInitializedAt" -v
```

Expected: `PASS`

- [ ] **Commit**

```bash
git add indexer/dir.go indexer/dir_test.go
git commit -m "indexer: add resolveCodeGraphDir and IsInitializedAt helpers"
```

---

### Task 2: Add `CodeGraphDir` and `CompressGraph` to `indexer.Options`; add `codeGraphDir` to `Indexer`

**Files:**
- Modify: `indexer/indexer.go`

- [ ] **Add fields to `Options` and `codeGraphDir` to `Indexer` struct**

In `indexer/indexer.go`, add to the `Options` struct (after the existing fields):

```go
// CodeGraphDir, when non-empty, overrides the default {projectRoot}/.codegraph
// storage location. Must be an absolute path.
CodeGraphDir string

// CompressGraph, when true, runs VACUUM and zstd-compresses codegraph.db
// after a successful IndexAll. The uncompressed .db is removed. Open
// transparently decompresses if only .zst is present.
CompressGraph bool
```

Update `newIndexer` signature and `Indexer` struct:

```go
// Indexer is an open codegraph project.
type Indexer struct {
	root         string
	codeGraphDir string // resolved codegraph directory (may differ from root/.codegraph)
	store        *store.Store
	lock         *lock.FileLock
	mu           sync.Mutex
}

func newIndexer(root, codeGraphDir string, s *store.Store) *Indexer {
	return &Indexer{
		root:         root,
		codeGraphDir: codeGraphDir,
		store:        s,
		lock:         lock.New(filepath.Join(codeGraphDir, "codegraph.lock")),
	}
}
```

- [ ] **Update `Open` to use `CodeGraphDir` and decompress `.zst` if needed**

Replace the existing `Open` function body:

```go
func Open(projectRoot string, opts Options) (*Indexer, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	cgDir := resolveCodeGraphDir(root, opts.CodeGraphDir)
	dbPath := filepath.Join(cgDir, "codegraph.db")
	zstPath := dbPath + ".zst"

	// Transparently decompress if only the compressed version exists.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		if _, zstErr := os.Stat(zstPath); zstErr == nil {
			if err := decompressZst(zstPath, dbPath); err != nil {
				return nil, fmt.Errorf("decompress codegraph: %w", err)
			}
			os.Remove(zstPath)
		}
	}

	if !IsInitializedAt(cgDir) {
		return nil, fmt.Errorf("CodeGraph not initialized in %s. Run Init first", cgDir)
	}

	storeOpts := []store.Option{}
	if opts.Clock != nil {
		storeOpts = append(storeOpts, store.WithNowFunc(opts.Clock))
	}
	s, err := store.Open(dbPath, storeOpts...)
	if err != nil {
		return nil, err
	}
	return newIndexer(root, cgDir, s), nil
}
```

- [ ] **Verify build passes**

```bash
CGO_ENABLED=0 go build ./...
```

Expected: compile error about `decompressZst` undefined (we'll add it in Task 4) and `newIndexer` call-sites in `init.go` not yet updated. That's expected — note the errors and continue to Task 3.

---

### Task 3: Thread `codeGraphDir` through `Init` in `indexer/init.go`

**Files:**
- Modify: `indexer/init.go`

- [ ] **Update `Init` to use `CodeGraphDir` option**

Replace the existing `Init` function body (keep the signature `Init(projectRoot string, opts Options)`):

```go
func Init(projectRoot string, opts Options) (*Indexer, IndexResult, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, IndexResult{}, err
	}
	cgDir := resolveCodeGraphDir(root, opts.CodeGraphDir)

	if IsInitializedAt(cgDir) {
		return nil, IndexResult{}, fmt.Errorf("CodeGraph already initialized in %s", cgDir)
	}

	// Create the codegraph directory.
	if err := os.MkdirAll(cgDir, 0o755); err != nil {
		return nil, IndexResult{}, err
	}
	// Write .gitignore only if the cgDir is inside the project root
	// (i.e. no custom override) to avoid littering external directories.
	if opts.CodeGraphDir == "" {
		giPath := filepath.Join(cgDir, ".gitignore")
		if _, err := os.Stat(giPath); os.IsNotExist(err) {
			_ = os.WriteFile(giPath, []byte(dataDirGitignore), 0o644)
		}
	}

	storeOpts := []store.Option{}
	if opts.Clock != nil {
		storeOpts = append(storeOpts, store.WithNowFunc(opts.Clock))
	}
	s, err := store.Initialize(filepath.Join(cgDir, "codegraph.db"), storeOpts...)
	if err != nil {
		return nil, IndexResult{}, err
	}
	idx := newIndexer(root, cgDir, s)
	result := idx.IndexAll(opts)
	return idx, result, nil
}
```

- [ ] **Verify build passes (excluding `decompressZst`)**

```bash
CGO_ENABLED=0 go build ./... 2>&1 | grep -v decompressZst
```

Expected: only `decompressZst` undefined remains.

- [ ] **Run existing tests to check no regressions**

```bash
CGO_ENABLED=0 go test ./indexer/ -count=1 -v 2>&1 | tail -20
```

Expected: failures only on `decompressZst` linkage; all pre-existing tests still pass once that's added.

---

### Task 4: Add `Vacuum` to `store.Store` and zstd compression to `indexer.IndexAll`

**Files:**
- Modify: `store/store.go`
- Modify: `indexer/indexer.go`
- Modify: `go.mod`

- [ ] **Add zstd dependency**

```bash
cd /Users/alexandertrakhimenok/projects/code-grapher/codegrapher
CGO_ENABLED=0 go get github.com/klauspost/compress/zstd
```

Expected: `go.mod` and `go.sum` updated.

- [ ] **Add `Vacuum` to `store/store.go`**

Add after the `Close` method:

```go
// Vacuum runs SQLite VACUUM, compacting the database by removing free pages.
// Call before compression to maximize space savings.
func (s *Store) Vacuum() error {
	_, err := s.db.Exec("VACUUM")
	return err
}
```

- [ ] **Add compression helpers to `indexer/indexer.go`**

Add at the bottom of `indexer/indexer.go` (new imports needed: `"io"`, `"github.com/klauspost/compress/zstd"`):

```go
// compressCodeGraph runs VACUUM on the store then compresses codegraph.db
// to codegraph.db.zst using zstd. On success it removes codegraph.db.
// Called at the end of a successful IndexAll when opts.CompressGraph is true.
func (idx *Indexer) compressCodeGraph() error {
	if err := idx.store.Vacuum(); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}

	dbPath := filepath.Join(idx.codeGraphDir, "codegraph.db")
	zstPath := dbPath + ".zst"

	// Close and reopen to ensure all WAL data is flushed before we read the file.
	// (The store remains usable; we re-open below only to get a clean file handle.)
	in, err := os.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open db for compression: %w", err)
	}
	defer in.Close()

	out, err := os.Create(zstPath)
	if err != nil {
		return fmt.Errorf("create zst: %w", err)
	}
	defer out.Close()

	enc, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		out.Close()
		os.Remove(zstPath)
		return fmt.Errorf("zstd encoder: %w", err)
	}
	if _, err := io.Copy(enc, in); err != nil {
		enc.Close()
		out.Close()
		os.Remove(zstPath)
		return fmt.Errorf("compress: %w", err)
	}
	if err := enc.Close(); err != nil {
		out.Close()
		os.Remove(zstPath)
		return fmt.Errorf("flush zstd: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(zstPath)
		return fmt.Errorf("close zst: %w", err)
	}
	in.Close()

	return os.Remove(dbPath)
}

// decompressZst decompresses src (a .zst file) to dst.
func decompressZst(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	dec, err := zstd.NewReader(in)
	if err != nil {
		return err
	}
	defer dec.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, dec)
	return err
}
```

- [ ] **Call `compressCodeGraph` at end of `IndexAll` when `CompressGraph` is set**

In `IndexAll`, add after `result.DurationMs = now() - start` at the bottom, still inside the function:

```go
	// Optional post-index compression (Phase 5).
	if opts.CompressGraph && result.Success {
		if err := idx.compressCodeGraph(); err != nil {
			// Non-fatal: compression failure doesn't invalidate the index.
			result.Errors = append(result.Errors, model.ExtractionError{
				Message:  fmt.Sprintf("compress codegraph: %s", err),
				Severity: "warning",
			})
		}
	}

	return result
```

- [ ] **Verify build passes**

```bash
CGO_ENABLED=0 go build ./...
```

Expected: clean build.

- [ ] **Commit**

```bash
git add indexer/dir.go indexer/dir_test.go indexer/indexer.go indexer/init.go store/store.go go.mod go.sum
git commit -m "indexer: add CodeGraphDir and CompressGraph options"
```

---

### Task 5: Tests for `CodeGraphDir` and `CompressGraph`

**Files:**
- Modify: `indexer/init_test.go`

- [ ] **Write failing tests**

Add to `indexer/init_test.go`:

```go
func TestInitCustomCodeGraphDir(t *testing.T) {
	repoDir := t.TempDir()
	cgDir := t.TempDir() // separate from repoDir

	// Create a minimal Go file so the indexer has something to index.
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, result, err := indexer.Init(repoDir, indexer.Options{CodeGraphDir: cgDir})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer idx.Close()

	if !result.Success {
		t.Fatalf("Init failed: %+v", result)
	}

	// codegraph.db must be in cgDir, not in repoDir/.codegraph
	if _, err := os.Stat(filepath.Join(cgDir, "codegraph.db")); err != nil {
		t.Errorf("codegraph.db not found in cgDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".codegraph")); err == nil {
		t.Error(".codegraph should not exist inside repoDir when CodeGraphDir is set")
	}
}

func TestCompressGraph(t *testing.T) {
	repoDir := t.TempDir()
	cgDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, result, err := indexer.Init(repoDir, indexer.Options{
		CodeGraphDir:  cgDir,
		CompressGraph: true,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer idx.Close()

	if !result.Success {
		t.Fatalf("Init failed: %+v", result)
	}

	// After compression, .zst must exist, .db must be gone.
	if _, err := os.Stat(filepath.Join(cgDir, "codegraph.db.zst")); err != nil {
		t.Errorf("codegraph.db.zst not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cgDir, "codegraph.db")); err == nil {
		t.Error("codegraph.db should have been removed after compression")
	}
}

func TestOpenDecompressesZst(t *testing.T) {
	repoDir := t.TempDir()
	cgDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Init with compression.
	idx, _, err := indexer.Init(repoDir, indexer.Options{CodeGraphDir: cgDir, CompressGraph: true})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	idx.Close()

	// Open should decompress transparently.
	idx2, err := indexer.Open(repoDir, indexer.Options{CodeGraphDir: cgDir})
	if err != nil {
		t.Fatalf("Open after compression: %v", err)
	}
	defer idx2.Close()

	// Re-index should succeed.
	result := idx2.IndexAll(indexer.Options{CodeGraphDir: cgDir})
	if !result.Success {
		t.Fatalf("IndexAll after reopen: %+v", result)
	}
}
```

- [ ] **Run tests to verify they fail**

```bash
CGO_ENABLED=0 go test ./indexer/ -run "TestInitCustomCodeGraphDir|TestCompressGraph|TestOpenDecompressesZst" -v
```

Expected: `FAIL` (functions not yet exported / wired correctly — tests reveal any wiring gaps).

- [ ] **Run all tests and fix any regressions**

```bash
CGO_ENABLED=0 go test -count=1 ./...
```

Expected: all pre-existing tests pass; new tests pass. Fix any failures before committing.

- [ ] **Commit**

```bash
git add indexer/init_test.go
git commit -m "indexer: tests for CodeGraphDir and CompressGraph options"
```

---

### Task 6: Final gate

- [ ] **Run full gate**

```bash
gofmt -l ./... 2>&1 | grep -v '^$' && echo "fmt ok"
go vet ./...
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test -count=1 ./...
```

Expected: all green, no fmt diffs, no vet errors, all tests pass (including the 46-golden binary parity test).

- [ ] **Commit gate result**

```bash
git commit --allow-empty -m "chore: gate green for CodeGraphDir+CompressGraph"
```
