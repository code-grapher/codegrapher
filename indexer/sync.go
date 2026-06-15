package indexer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/model"
)

// Sync reconciles the index with the current filesystem state. Change
// detection is filesystem-based, never git: a (size, mtime) stat pre-filter
// skips unchanged files, then a content-hash compare confirms real changes.
// Changed files are deleted and re-extracted, references are re-resolved, and
// maintenance runs when anything changed. When the cross-process file lock is
// held elsewhere, the zero-value SyncResult is returned (not an error), so
// callers like the file watcher can detect the lock case by FilesChecked==0
// && DurationMs==0. Mirrors ExtractionOrchestrator.sync + CodeGraph.sync.
func (idx *Indexer) Sync(opts Options) SyncResult {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if err := idx.lock.Acquire(); err != nil {
		return SyncResult{}
	}
	defer idx.lock.Release()

	now := opts.clock()
	start := now()
	result := SyncResult{}

	// Version gate: an index built by a different scanner/extraction version
	// (or one predating version stamping) can't be safely updated in place —
	// incrementally merging new-format data onto stale-format data yields a
	// silently inconsistent graph. Rebuild from scratch instead. indexAllLocked
	// re-stamps the current version metadata when it finishes.
	if idx.indexVersionStale() {
		for _, s := range idx.Stores() {
			_ = s.Clear()
		}
		ir := idx.indexAllLocked(opts)
		result.FullReindex = true
		result.FilesChecked = ir.FilesIndexed
		result.NodesUpdated = ir.NodesCreated
		result.DurationMs = now() - start
		return result
	}

	opts.progress(IndexProgress{Phase: PhaseScanning})

	currentFiles := ScanDirectory(idx.root)
	result.FilesChecked = len(currentFiles)
	currentSet := make(map[string]bool, len(currentFiles))
	for _, f := range currentFiles {
		currentSet[f] = true
	}

	tracked, err := idx.allTrackedFiles()
	if err != nil {
		result.DurationMs = now() - start
		return result
	}
	trackedMap := make(map[string]model.FileRecord, len(tracked))
	for _, f := range tracked {
		trackedMap[f.Path] = f
	}

	// Removals: tracked in the DB but no longer a present source file. Check
	// the filesystem directly — `git ls-files` still lists a file deleted
	// from disk but not yet staged.
	for _, rec := range tracked {
		exists := true
		if _, err := os.Stat(filepath.Join(idx.root, filepath.FromSlash(rec.Path))); err != nil {
			exists = false
		}
		if !currentSet[rec.Path] || !exists {
			if idx.deleteFileEverywhere(rec.Path) {
				result.FilesRemoved++
			}
		}
	}

	// Adds / modifications.
	var filesToIndex []string
	for _, filePath := range currentFiles {
		fullPath := filepath.Join(idx.root, filepath.FromSlash(filePath))
		rec, isTracked := trackedMap[filePath]

		// Cheap pre-filter: an already-indexed file whose size AND mtime both
		// match the DB is unchanged — skip without reading or hashing.
		if isTracked {
			fi, err := os.Stat(fullPath)
			if err != nil {
				continue // unstattable — skip, like the original
			}
			if fi.Size() == rec.Size && statMtimeMs(fi) == rec.ModifiedAt {
				continue
			}
		}

		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue // unreadable — skip
		}
		hash := HashContent(content)

		if !isTracked {
			filesToIndex = append(filesToIndex, filePath)
			result.ChangedFilePaths = append(result.ChangedFilePaths, filePath)
			result.FilesAdded++
		} else if rec.ContentHash != hash {
			filesToIndex = append(filesToIndex, filePath)
			result.ChangedFilePaths = append(result.ChangedFilePaths, filePath)
			result.FilesModified++
		}
	}

	idx.syncChangedFiles(filesToIndex, opts, &result)

	if result.FilesAdded > 0 || result.FilesModified > 0 || result.FilesRemoved > 0 {
		idx.runMaintenanceAll()
	}

	result.DurationMs = now() - start
	return result
}

// SyncFiles incrementally re-indexes a known set of changed files (e.g. from
// a git hook or watcher event): hash-compares each candidate against the
// index, deletes + re-extracts real changes, removes entries whose file is
// gone, and re-resolves references. Paths are project-relative (POSIX or
// native separators).
func (idx *Indexer) SyncFiles(changed []string, opts Options) SyncResult {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if err := idx.lock.Acquire(); err != nil {
		return SyncResult{}
	}
	defer idx.lock.Release()

	now := opts.clock()
	start := now()
	result := SyncResult{FilesChecked: len(changed)}

	var filesToIndex []string
	for _, raw := range changed {
		filePath := filepath.ToSlash(strings.TrimPrefix(raw, "./"))
		fullPath := filepath.Join(idx.root, filepath.FromSlash(filePath))
		rec, err := idx.fileRecord(filePath)
		if err != nil {
			continue
		}

		if _, statErr := os.Stat(fullPath); statErr != nil {
			// Gone from disk — drop it from the index if tracked.
			if rec != nil {
				if idx.deleteFileEverywhere(filePath) {
					result.FilesRemoved++
				}
			}
			continue
		}
		if !IsSourceFile(filePath) {
			continue
		}

		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		hash := HashContent(content)

		if rec == nil {
			filesToIndex = append(filesToIndex, filePath)
			result.ChangedFilePaths = append(result.ChangedFilePaths, filePath)
			result.FilesAdded++
		} else if rec.ContentHash != hash {
			filesToIndex = append(filesToIndex, filePath)
			result.ChangedFilePaths = append(result.ChangedFilePaths, filePath)
			result.FilesModified++
		}
	}

	idx.syncChangedFiles(filesToIndex, opts, &result)

	if result.FilesAdded > 0 || result.FilesModified > 0 || result.FilesRemoved > 0 {
		idx.runMaintenanceAll()
	}

	result.DurationMs = now() - start
	return result
}

// syncChangedFiles extracts + stores the changed files and re-resolves the
// references they recorded. Resolution is naturally scoped: the unresolved
// table only ever holds refs from files (re-)extracted since the last
// resolution pass, matching the original's changed-file scoping.
func (idx *Indexer) syncChangedFiles(filesToIndex []string, opts Options, result *SyncResult) {
	if len(filesToIndex) == 0 {
		return
	}
	sort.Strings(filesToIndex)

	var ir IndexResult
	idx.extractAndStore(filesToIndex, opts, &ir)

	// nodesUpdated is the sum of nodes now stored for the changed files
	// (the original's `nodesUpdated += result.nodes.length`).
	nodesUpdated := 0
	for _, f := range filesToIndex {
		nodes, err := idx.nodesByFile(f)
		if err == nil {
			nodesUpdated += len(nodes)
		}
	}
	result.NodesUpdated = nodesUpdated

	if ir.FilesIndexed > 0 {
		var dummy IndexResult
		idx.resolveAll(opts, &dummy)
	}
}

// GetChangedFiles classifies filesystem changes since the last index without
// applying them. Uses `git status --porcelain` as a fast path when available,
// falling back to a full scan + hash compare. Mirrors
// ExtractionOrchestrator.getChangedFiles.
func (idx *Indexer) GetChangedFiles() ChangedFiles {
	if changes, ok := gitChangedFiles(idx.root); ok {
		out := ChangedFiles{}
		for _, filePath := range changes.deleted {
			if rec, err := idx.fileRecord(filePath); err == nil && rec != nil {
				out.Removed = append(out.Removed, filePath)
			}
		}
		// Untracked (`??`) files stay untracked in git even after indexing,
		// so they are hash-compared like modified files instead of always
		// counting as added (issue #206 upstream).
		for _, filePath := range append(append([]string{}, changes.modified...), changes.added...) {
			content, err := os.ReadFile(filepath.Join(idx.root, filepath.FromSlash(filePath)))
			if err != nil {
				continue
			}
			hash := HashContent(content)
			rec, err := idx.fileRecord(filePath)
			if err != nil {
				continue
			}
			if rec == nil {
				out.Added = append(out.Added, filePath)
			} else if rec.ContentHash != hash {
				out.Modified = append(out.Modified, filePath)
			}
		}
		return out
	}

	// Fallback: full scan (non-git project or git failure).
	currentFiles := ScanDirectory(idx.root)
	currentSet := make(map[string]bool, len(currentFiles))
	for _, f := range currentFiles {
		currentSet[f] = true
	}
	tracked, err := idx.allTrackedFiles()
	if err != nil {
		return ChangedFiles{}
	}
	out := ChangedFiles{}
	for _, rec := range tracked {
		if !currentSet[rec.Path] {
			out.Removed = append(out.Removed, rec.Path)
		}
	}
	trackedMap := make(map[string]model.FileRecord, len(tracked))
	for _, f := range tracked {
		trackedMap[f.Path] = f
	}
	for _, filePath := range currentFiles {
		content, err := os.ReadFile(filepath.Join(idx.root, filepath.FromSlash(filePath)))
		if err != nil {
			continue
		}
		hash := HashContent(content)
		rec, isTracked := trackedMap[filePath]
		if !isTracked {
			out.Added = append(out.Added, filePath)
		} else if rec.ContentHash != hash {
			out.Modified = append(out.Modified, filePath)
		}
	}
	return out
}

// --- multi-scope fan-out helpers ---------------------------------------------
//
// A file belongs to exactly one scope, so these read across every scope store
// and concatenate; lookups return the first scope that has the path.

// allTrackedFiles returns the union of tracked files across all scope stores.
func (idx *Indexer) allTrackedFiles() ([]model.FileRecord, error) {
	var all []model.FileRecord
	for _, s := range idx.Stores() {
		files, err := s.GetAllFiles()
		if err != nil {
			return nil, err
		}
		all = append(all, files...)
	}
	return all, nil
}

// fileRecord returns the record for path from whichever scope store has it, or
// (nil, nil) when no scope tracks it.
func (idx *Indexer) fileRecord(path string) (*model.FileRecord, error) {
	for _, s := range idx.Stores() {
		rec, err := s.GetFileByPath(path)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			return rec, nil
		}
	}
	return nil, nil
}

// deleteFileEverywhere removes path from every scope store that tracks it,
// reporting whether any deletion succeeded.
func (idx *Indexer) deleteFileEverywhere(path string) bool {
	deleted := false
	for _, s := range idx.Stores() {
		if err := s.DeleteFile(path); err == nil {
			deleted = true
		}
	}
	return deleted
}

// nodesByFile returns the nodes recorded for path across all scope stores.
func (idx *Indexer) nodesByFile(path string) ([]model.Node, error) {
	var all []model.Node
	for _, s := range idx.Stores() {
		nodes, err := s.GetNodesByFile(path)
		if err != nil {
			return nil, err
		}
		all = append(all, nodes...)
	}
	return all, nil
}

// runMaintenanceAll runs store maintenance on every scope store.
func (idx *Indexer) runMaintenanceAll() {
	for _, s := range idx.Stores() {
		s.RunMaintenance()
	}
}

// gitChanges classifies `git status --porcelain` output.
type gitChanges struct {
	modified []string
	added    []string
	deleted  []string
}

// gitChangedFiles parses `git status --porcelain --no-renames`. Returns
// ok=false when git is unavailable so callers fall back to a full scan.
func gitChangedFiles(rootDir string) (gitChanges, bool) {
	out, err := gitOutput(rootDir, "status", "--porcelain", "--no-renames")
	if err != nil {
		return gitChanges{}, false
	}
	changes := gitChanges{}
	for line := range strings.SplitSeq(out, "\n") {
		if len(line) < 4 {
			continue // minimum: "XY file"
		}
		statusCode := line[:2]
		filePath := filepath.ToSlash(strings.TrimSpace(line[3:]))
		if !IsSourceFile(filePath) {
			continue
		}
		switch {
		case statusCode == "??":
			changes.added = append(changes.added, filePath)
		case strings.Contains(statusCode, "D"):
			changes.deleted = append(changes.deleted, filePath)
		default:
			changes.modified = append(changes.modified, filePath)
		}
	}
	return changes, true
}
