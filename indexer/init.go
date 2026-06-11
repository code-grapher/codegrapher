package indexer

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/specscore/codegrapher/internal/extract"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

// lockUnavailableMessage mirrors the error message the original returns when
// the cross-process file lock is held.
const lockUnavailableMessage = "Could not acquire file lock - another process may be indexing"

// Init initializes a new CodeGraph project: creates the .codegraph directory
// and database, then runs a full index (scan → concurrent extraction →
// batched store writes → resolution → maintenance) and stamps the project
// metadata. Mirrors CodeGraph.init(root, {index: true}).
func Init(projectRoot string, opts Options) (*Indexer, IndexResult, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, IndexResult{}, err
	}
	if IsInitialized(root) {
		return nil, IndexResult{}, fmt.Errorf("CodeGraph already initialized in %s", root)
	}
	if err := CreateDirectory(root); err != nil {
		return nil, IndexResult{}, err
	}
	storeOpts := []store.Option{}
	if opts.Clock != nil {
		storeOpts = append(storeOpts, store.WithNowFunc(opts.Clock))
	}
	s, err := store.Initialize(DatabasePath(root), storeOpts...)
	if err != nil {
		return nil, IndexResult{}, err
	}
	idx := newIndexer(root, s)
	result := idx.IndexAll(opts)
	return idx, result, nil
}

// IndexAll indexes every source file in the project. It holds the in-process
// mutex and the cross-process file lock for the duration; when the file lock
// is held elsewhere it returns a failed result (not an error), like the
// original.
func (idx *Indexer) IndexAll(opts Options) IndexResult {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if err := idx.lock.Acquire(); err != nil {
		return IndexResult{
			Success: false,
			Errors: []model.ExtractionError{
				{Message: lockUnavailableMessage, Severity: "error"},
			},
		}
	}
	defer idx.lock.Release()

	now := opts.clock()
	start := now()
	result := IndexResult{}

	before, err := idx.store.GetStats()
	if err != nil {
		result.Errors = append(result.Errors, model.ExtractionError{
			Message: err.Error(), Severity: "error",
		})
		result.DurationMs = now() - start
		return result
	}

	// Phase 1: scan.
	opts.progress(IndexProgress{Phase: PhaseScanning})
	files := ScanDirectory(idx.root)

	// Phase 2: concurrent extraction, serialized store writes.
	idx.extractAndStore(files, opts, &result)

	result.Success = result.FilesIndexed > 0 || !hasSevereError(result.Errors)

	// Phase 3: resolution.
	if result.Success && result.FilesIndexed > 0 {
		idx.resolveAll(opts, &result)
	}

	// Phase 4: maintenance + metadata stamp (advisory — never fails a run).
	if result.Success && result.FilesIndexed > 0 {
		idx.store.RunMaintenance()

		if after, err := idx.store.GetStats(); err == nil {
			result.NodesCreated = after.NodeCount - before.NodeCount
			result.EdgesCreated = after.EdgeCount - before.EdgeCount
		}

		_ = idx.store.SetMetadata("indexed_with_version", PackageVersion)
		_ = idx.store.SetMetadata("indexed_with_extraction_version", strconv.Itoa(ExtractionVersion))
	}

	result.DurationMs = now() - start
	return result
}

// resolveAll runs the full resolution pass with progress reporting.
func (idx *Indexer) resolveAll(opts Options, result *IndexResult) {
	total, err := idx.store.GetUnresolvedReferencesCount()
	if err != nil {
		result.Errors = append(result.Errors, model.ExtractionError{
			Message: err.Error(), Severity: "error",
		})
		return
	}
	opts.progress(IndexProgress{Phase: PhaseResolving, Current: 0, Total: total})
	if _, err := resolve.Resolve(idx.store, idx.root); err != nil {
		result.Errors = append(result.Errors, model.ExtractionError{
			Message: err.Error(), Severity: "error",
		})
		return
	}
	opts.progress(IndexProgress{Phase: PhaseResolving, Current: total, Total: total})
}

// extractJob carries one file through the read → parse → store pipeline.
type extractJob struct {
	path     string
	content  []byte
	size     int64
	mtimeMs  int64
	result   model.ExtractionResult
	readErr  error
	tooLarge bool
}

// extractAndStore reads and parses files concurrently (bounded pool of
// opts.Workers goroutines per batch) and writes results to the store
// serially, in deterministic file order. Replaces the original's
// worker-thread machinery; Go needs no WASM-memory recycling or retry passes.
func (idx *Indexer) extractAndStore(files []string, opts Options, result *IndexResult) {
	workers := opts.workers()
	now := opts.clock()
	total := len(files)
	processed := 0

	opts.progress(IndexProgress{Phase: PhaseParsing, Current: 0, Total: total})

	for batchStart := 0; batchStart < total; batchStart += workers {
		batchEnd := min(batchStart+workers, total)
		batch := files[batchStart:batchEnd]
		jobs := make([]extractJob, len(batch))

		var wg sync.WaitGroup
		for i, relPath := range batch {
			wg.Add(1)
			go func(i int, relPath string) {
				defer wg.Done()
				jobs[i] = extractOne(idx.root, relPath)
			}(i, relPath)
		}
		wg.Wait()

		// Store on this goroutine — writes stay serialized and deterministic.
		for i := range jobs {
			job := &jobs[i]
			opts.progress(IndexProgress{
				Phase: PhaseParsing, Current: processed, Total: total, CurrentFile: job.path,
			})
			processed++

			if job.readErr != nil {
				result.FilesErrored++
				result.Errors = append(result.Errors, model.ExtractionError{
					Message:  fmt.Sprintf("Failed to read file: %v", job.readErr),
					FilePath: job.path,
					Severity: "error",
					Code:     "read_error",
				})
				continue
			}
			if job.tooLarge {
				result.FilesSkipped++
				result.Errors = append(result.Errors, model.ExtractionError{
					Message:  fmt.Sprintf("File exceeds max size (%d > %d)", job.size, MaxFileSize),
					FilePath: job.path,
					Severity: "warning",
					Code:     "size_exceeded",
				})
				continue
			}

			if len(job.result.Nodes) > 0 || len(job.result.Errors) == 0 {
				lang := extract.DetectLanguage(job.path)
				if err := storeExtractionResult(
					idx.store, job.path, job.content, lang,
					job.size, job.mtimeMs, job.result, now,
				); err != nil {
					result.FilesErrored++
					result.Errors = append(result.Errors, model.ExtractionError{
						Message:  err.Error(),
						FilePath: job.path,
						Severity: "error",
						Code:     "store_error",
					})
					continue
				}
			}

			for _, e := range job.result.Errors {
				if e.FilePath == "" {
					e.FilePath = job.path
				}
				result.Errors = append(result.Errors, e)
			}

			if len(job.result.Nodes) > 0 {
				result.FilesIndexed++
			} else if hasSevereError(job.result.Errors) {
				result.FilesErrored++
			} else {
				result.FilesSkipped++
			}
		}
	}

	opts.progress(IndexProgress{Phase: PhaseParsing, Current: total, Total: total})
}

// extractOne reads and parses a single file (no store access — safe to run
// concurrently).
func extractOne(rootDir, relPath string) extractJob {
	job := extractJob{path: relPath}
	fullPath := filepath.Join(rootDir, relPath)

	fi, err := os.Stat(fullPath)
	if err != nil {
		job.readErr = err
		return job
	}
	job.size = fi.Size()
	job.mtimeMs = statMtimeMs(fi)
	if job.size > MaxFileSize {
		job.tooLarge = true
		return job
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		job.readErr = err
		return job
	}
	job.content = content

	lang := extract.DetectLanguage(relPath)
	res, err := extract.ExtractFile(relPath, content, lang)
	if err != nil {
		res.Errors = append(res.Errors, model.ExtractionError{
			Message:  err.Error(),
			FilePath: relPath,
			Severity: "error",
			Code:     "parse_error",
		})
	}
	job.result = res
	return job
}

func hasSevereError(errs []model.ExtractionError) bool {
	for _, e := range errs {
		if e.Severity == "error" {
			return true
		}
	}
	return false
}
