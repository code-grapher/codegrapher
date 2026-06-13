package indexer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/specscore/codegrapher/lock"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// PackageVersion stamps the index with the engine that built it
// (indexed_with_version metadata, mirroring CodeGraphPackageVersion).
const PackageVersion = "0.1.0"

// ExtractionVersion mirrors EXTRACTION_VERSION in
// src/extraction/extraction-version.ts at the time of the port.
const ExtractionVersion = 14

// Phase identifies a stage of an indexing operation.
type Phase string

// Progress phases, mirroring IndexProgress.phase in src/extraction/index.ts.
const (
	PhaseScanning  Phase = "scanning"
	PhaseParsing   Phase = "parsing"
	PhaseStoring   Phase = "storing"
	PhaseResolving Phase = "resolving"
)

// IndexProgress is reported to the OnProgress callback during indexing.
type IndexProgress struct {
	Phase       Phase
	Current     int
	Total       int
	CurrentFile string
}

// IndexResult is the outcome of a full or partial indexing operation.
type IndexResult struct {
	Success      bool
	FilesIndexed int
	FilesSkipped int
	FilesErrored int
	NodesCreated int
	EdgesCreated int
	Errors       []model.ExtractionError
	DurationMs   int64
}

// SyncResult is the outcome of an incremental sync.
type SyncResult struct {
	FilesChecked     int
	FilesAdded       int
	FilesModified    int
	FilesRemoved     int
	NodesUpdated     int
	DurationMs       int64
	ChangedFilePaths []string
}

// ChangedFiles classifies pending filesystem changes against the index.
type ChangedFiles struct {
	Added    []string
	Modified []string
	Removed  []string
}

// Options configures indexing operations. The zero value is usable.
type Options struct {
	// Workers bounds the extraction goroutine pool (0 = DefaultWorkers).
	Workers int

	// OnProgress, when non-nil, receives progress updates.
	OnProgress func(IndexProgress)

	// Clock returns the current time in Unix milliseconds. Injectable for
	// deterministic tests (0/nil = time.Now).
	Clock func() int64
}

// DefaultWorkers is the default extraction pool size.
const DefaultWorkers = 8

func (o Options) workers() int {
	if o.Workers > 0 {
		return o.Workers
	}
	return DefaultWorkers
}

func (o Options) clock() func() int64 {
	if o.Clock != nil {
		return o.Clock
	}
	return func() int64 { return time.Now().UnixMilli() }
}

func (o Options) progress(p IndexProgress) {
	if o.OnProgress != nil {
		o.OnProgress(p)
	}
}

// Indexer is an open codegraph project: the seam embedding consumers use to
// build and maintain the index. Construct with Init or Open.
type Indexer struct {
	root string
	reg  *Registry
	lock *lock.FileLock

	// mu serializes in-process indexing operations (the indexMutex in
	// src/index.ts); the file lock serializes across processes.
	mu sync.Mutex
}

// storeOptsFrom builds store options from indexer Options.
func storeOptsFrom(opts Options) []store.Option {
	var storeOpts []store.Option
	if opts.Clock != nil {
		storeOpts = append(storeOpts, store.WithNowFunc(opts.Clock))
	}
	return storeOpts
}

// Open opens an existing CodeGraph project.
func Open(projectRoot string, opts Options) (*Indexer, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	if !IsInitialized(root) {
		return nil, fmt.Errorf("CodeGraph not initialized in %s. Run Init first", root)
	}
	reg, err := OpenRegistry(root, storeOptsFrom(opts)...)
	if err != nil {
		return nil, err
	}
	return newIndexer(root, reg), nil
}

func newIndexer(root string, reg *Registry) *Indexer {
	return &Indexer{
		root: root,
		reg:  reg,
		lock: lock.New(filepath.Join(GetCodeGraphDir(root), "codegraph.lock")),
	}
}

// ProjectRoot returns the project root directory.
func (idx *Indexer) ProjectRoot() string { return idx.root }

// Registry exposes the per-scope store registry.
func (idx *Indexer) Registry() *Registry { return idx.reg }

// Stores returns every open scope store, ordered deterministically by scope
// key. Query consumers fan out across these and merge.
func (idx *Indexer) Stores() []*store.Store {
	scopes := idx.reg.Scopes()
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Key() < scopes[j].Key() })
	out := make([]*store.Store, 0, len(scopes))
	stores := idx.reg.Stores()
	for _, sc := range scopes {
		out = append(out, stores[sc])
	}
	return out
}

// StoresFiltered returns the scope stores whose scope key is in scopeKeys,
// ordered deterministically by key. An empty scopeKeys returns all stores
// (identical to Stores). Unknown keys are silently ignored.
func (idx *Indexer) StoresFiltered(scopeKeys []string) []*store.Store {
	if len(scopeKeys) == 0 {
		return idx.Stores()
	}
	want := make(map[string]struct{}, len(scopeKeys))
	for _, k := range scopeKeys {
		want[k] = struct{}{}
	}
	scopes := idx.reg.Scopes()
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Key() < scopes[j].Key() })
	stores := idx.reg.Stores()
	out := make([]*store.Store, 0, len(scopes))
	for _, sc := range scopes {
		if _, ok := want[sc.Key()]; ok {
			out = append(out, stores[sc])
		}
	}
	return out
}

// Store returns the primary (lexicographically-first) scope store. It is a
// convenience for single-scope projects and tests; multi-scope consumers must
// use Stores.
func (idx *Indexer) Store() *store.Store {
	all := idx.Stores()
	if len(all) == 0 {
		return nil
	}
	return all[0]
}

// ClearAll clears every scope store.
func (idx *Indexer) ClearAll() error {
	for _, s := range idx.Stores() {
		if err := s.Clear(); err != nil {
			return err
		}
	}
	return nil
}

// Close releases the file lock (if held) and closes every scope store.
func (idx *Indexer) Close() error {
	idx.lock.Release()
	return idx.reg.Close()
}

// Uninit closes the index and removes the project's .codegraph directory.
func (idx *Indexer) Uninit() error {
	if err := idx.Close(); err != nil {
		return err
	}
	return RemoveDirectory(idx.root)
}

// Uninit removes the .codegraph directory of a project that isn't open.
func Uninit(projectRoot string) error {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return err
	}
	return RemoveDirectory(root)
}

// statMtimeMs returns the file's mtime in whole milliseconds, matching the
// Math.floor(stats.mtimeMs) comparison in the original.
func statMtimeMs(fi os.FileInfo) int64 {
	return fi.ModTime().UnixMilli()
}
