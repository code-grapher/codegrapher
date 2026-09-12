package indexer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
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
		return idx.fullRebuildLocked(opts, start, now)
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
		result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), Severity: "error", Code: "files_read_error"})
		result.DurationMs = now() - start
		return result
	}
	trackedMap := make(map[string]model.FileRecord, len(tracked))
	for _, f := range tracked {
		trackedMap[f.Path] = f
	}
	dirtyPaths := make(map[string]struct{})
	if changes, ok := gitChangedFiles(idx.root); ok {
		for _, path := range append(append(changes.modified, changes.added...), changes.deleted...) {
			dirtyPaths[path] = struct{}{}
		}
	}

	// Removals: tracked in the DB but no longer a present source file. Check
	// the filesystem directly — `git ls-files` still lists a file deleted
	// from disk but not yet staged.
	for _, rec := range tracked {
		exists := true
		if _, err := os.Stat(filepath.Join(idx.root, filepath.FromSlash(rec.Path))); err != nil {
			if !os.IsNotExist(err) {
				result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), FilePath: rec.Path, Severity: "error", Code: "stat_error"})
				continue
			}
			exists = false
		}
		if !currentSet[rec.Path] || !exists {
			deleted, err := idx.deleteFileEverywhere(rec.Path)
			if err != nil {
				result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), FilePath: rec.Path, Severity: "error", Code: "delete_error"})
				continue
			}
			if deleted {
				result.FilesRemoved++
			}
		}
	}

	// Adds / modifications.
	var filesToIndex []string
	for _, filePath := range currentFiles {
		fullPath := filepath.Join(idx.root, filepath.FromSlash(filePath))
		rec, isTracked := trackedMap[filePath]
		if isTracked {
			fi, err := os.Stat(fullPath)
			if err != nil {
				result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), FilePath: filePath, Severity: "error", Code: "stat_error"})
				continue
			}
			if fi.Size() == rec.Size && statMtimeMs(fi) == rec.ModifiedAt {
				// Git lists dirty candidates cheaply; always hash those so a
				// same-size change within one millisecond is never missed.
				if _, dirty := dirtyPaths[filePath]; !dirty {
					continue
				}
			}
		}

		content, err := os.ReadFile(fullPath)
		if err != nil {
			result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), FilePath: filePath, Severity: "error", Code: "read_error"})
			continue
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
	if requiresScopeRebuild(result.ChangedFilePaths) {
		return idx.fullRebuildLocked(opts, start, now)
	}

	idx.syncChangedFiles(filesToIndex, opts, &result)
	// Keep the cross-scope trace projection aligned with the exact working tree
	// revision even when only directives or spec files changed.
	if err := idx.indexTrace(); err != nil {
		result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), Severity: "error", Code: "trace_error"})
	}

	if result.FilesAdded > 0 || result.FilesModified > 0 || result.FilesRemoved > 0 {
		idx.runMaintenanceAll()
	}
	if len(result.Errors) == 0 {
		if err := idx.markCurrentGitHead(); err != nil {
			result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), Severity: "error", Code: "git_head_metadata_error"})
		}
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
	if requiresScopeRebuild(changed) {
		return idx.Rebuild(opts)
	}
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
			result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), FilePath: filePath, Severity: "error", Code: "file_record_error"})
			continue
		}

		if _, statErr := os.Stat(fullPath); statErr != nil {
			// Gone from disk — drop it from the index if tracked.
			if !os.IsNotExist(statErr) {
				result.Errors = append(result.Errors, model.ExtractionError{Message: statErr.Error(), FilePath: filePath, Severity: "error", Code: "stat_error"})
				continue
			}
			if rec != nil {
				deleted, err := idx.deleteFileEverywhere(filePath)
				if err != nil {
					result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), FilePath: filePath, Severity: "error", Code: "delete_error"})
				} else if deleted {
					result.FilesRemoved++
				}
			}
			continue
		}
		content, err := os.ReadFile(fullPath)
		if err != nil {
			result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), FilePath: filePath, Severity: "error", Code: "read_error"})
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
	if err := idx.indexTrace(); err != nil {
		result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), Severity: "error", Code: "trace_error"})
	}

	if result.FilesAdded > 0 || result.FilesModified > 0 || result.FilesRemoved > 0 {
		idx.runMaintenanceAll()
	}

	result.DurationMs = now() - start
	return result
}

// Rebuild performs a strict from-scratch reconstruction. It is used when a
// manifest can move many files between versioned scopes.
func (idx *Indexer) Rebuild(opts Options) SyncResult {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if err := idx.lock.Acquire(); err != nil {
		return SyncResult{}
	}
	defer idx.lock.Release()
	now := opts.clock()
	start := now()
	return idx.fullRebuildLocked(opts, start, now)
}

func (idx *Indexer) fullRebuildLocked(opts Options, start int64, now func() int64) SyncResult {
	result := SyncResult{FullReindex: true}
	for _, s := range idx.Stores() {
		if err := s.Clear(); err != nil {
			result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), Severity: "error", Code: "clear_error"})
			result.DurationMs = now() - start
			return result
		}
	}
	ir := idx.indexAllLocked(opts)
	result.FilesChecked = ir.FilesIndexed
	result.NodesUpdated = ir.NodesCreated
	result.Errors = append(result.Errors, ir.Errors...)
	result.DurationMs = now() - start
	return result
}

func requiresScopeRebuild(paths []string) bool {
	for _, path := range paths {
		switch filepath.Base(filepath.ToSlash(path)) {
		case "package.json", "go.mod", "pom.xml", "build.gradle", "build.gradle.kts":
			return true
		}
	}
	return false
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
	backups, err := idx.captureIncomingEdges(filesToIndex)
	if err != nil {
		result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), Severity: "error", Code: "edge_backup_error"})
		return
	}

	var ir IndexResult
	idx.extractAndStore(filesToIndex, opts, &ir)
	if err := idx.restoreIncomingEdges(backups); err != nil {
		result.Errors = append(result.Errors, model.ExtractionError{Message: err.Error(), Severity: "error", Code: "edge_restore_error"})
		return
	}
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
		result.Errors = append(result.Errors, dummy.Errors...)
	}
	// Report failed files only after resolving every successfully stored file.
	// Otherwise a later retry of an unrelated failed file leaves valid callers'
	// unresolved references stranded indefinitely.
	result.Errors = append(result.Errors, ir.Errors...)
}

// incomingEdgeBackup retains an edge from an unchanged source while its target
// file is re-extracted. Deleting the old target node otherwise cascades the
// edge, and the source's unresolved reference was cleared by the prior resolve
// pass. The target's stable symbol identity lets us restore only edges whose
// definition still exists after the update; renames deliberately remain gone.
type incomingEdgeBackup struct {
	store  *store.Store
	target model.Node
	edge   model.Edge
}

func (idx *Indexer) captureIncomingEdges(changedFiles []string) ([]incomingEdgeBackup, error) {
	changed := make(map[string]struct{}, len(changedFiles))
	for _, file := range changedFiles {
		changed[file] = struct{}{}
	}
	var out []incomingEdgeBackup
	for _, s := range idx.Stores() {
		edges, err := s.GetIncomingEdgesForTargetFiles(changedFiles)
		if err != nil {
			return nil, fmt.Errorf("read incoming edges: %w", err)
		}
		ids := make([]string, 0, len(edges)*2)
		for _, edge := range edges {
			ids = append(ids, edge.Source, edge.Target)
		}
		nodes, err := s.GetNodesByIDs(ids)
		if err != nil {
			return nil, fmt.Errorf("read edge endpoints: %w", err)
		}
		for _, edge := range edges {
			source, sourceOK := nodes[edge.Source]
			target, targetOK := nodes[edge.Target]
			if !sourceOK || !targetOK {
				continue
			}
			if _, isChanging := changed[source.FilePath]; isChanging {
				continue
			}
			out = append(out, incomingEdgeBackup{store: s, target: target, edge: edge})
		}
	}
	return out, nil
}

func (idx *Indexer) restoreIncomingEdges(backups []incomingEdgeBackup) error {
	byStoreFile := make(map[*store.Store]map[string][]model.Node)
	for _, backup := range backups {
		files := byStoreFile[backup.store]
		if files == nil {
			files = make(map[string][]model.Node)
			byStoreFile[backup.store] = files
		}
		if _, loaded := files[backup.target.FilePath]; !loaded {
			nodes, err := backup.store.GetNodesByFile(backup.target.FilePath)
			if err != nil {
				return fmt.Errorf("read reindexed nodes for %s: %w", backup.target.FilePath, err)
			}
			files[backup.target.FilePath] = nodes
		}
	}
	restore := make(map[*store.Store][]model.Edge)
	for _, backup := range backups {
		// Node IDs can remain stable for a body-only edit even though deleting
		// the file cascaded its incoming edges. The edge, not the node ID, is
		// the authoritative indication that restoration is needed.
		survived, err := backup.store.EdgeExists(backup.edge)
		if err != nil {
			return fmt.Errorf("check preserved edge for %s: %w", backup.target.ID, err)
		}
		if survived {
			continue
		}
		for _, candidate := range byStoreFile[backup.store][backup.target.FilePath] {
			if candidate.Kind != backup.target.Kind || candidate.Name != backup.target.Name || candidate.QualifiedName != backup.target.QualifiedName || candidate.Signature != backup.target.Signature || candidate.ReturnType != backup.target.ReturnType || strings.Join(candidate.TypeParameters, "\x00") != strings.Join(backup.target.TypeParameters, "\x00") {
				continue
			}
			edge := backup.edge
			edge.Target = candidate.ID
			restore[backup.store] = append(restore[backup.store], edge)
			break
		}
	}
	for s, edges := range restore {
		if err := s.InsertEdges(edges); err != nil {
			return fmt.Errorf("restore incoming edges: %w", err)
		}
	}
	return nil
}

const indexedGitHeadKey = "indexed_git_head"

// GetChangedFiles prefers a persisted git revision plus git's own change
// lists. That avoids a whole-tree walk for each symbol read while still
// catching clean commits made after indexing. Non-git projects, and old
// indexes without a revision stamp, retain the filesystem/hash fallback.
func (idx *Indexer) GetChangedFiles() ChangedFiles {
	if tracked, err := idx.allTrackedFiles(); err == nil && idx.hasEmbeddedRepository(tracked) {
		return idx.getChangedFilesByScan(true)
	}
	if changed, ok := idx.gitChangedFilesSinceIndex(); ok {
		return changed
	}
	return idx.getChangedFilesByScan(false)
}

// RefreshForRead is the strict freshness boundary for symbol consumers. It
// honors the extraction-version gate, refreshes only the Git/metadata
// candidates, and stamps the observed revision only when HEAD did not move
// during the operation.
func (idx *Indexer) RefreshForRead(opts Options) (SyncResult, error) {
	if idx.indexVersionStale() {
		result := idx.Sync(opts)
		if result.FilesChecked == 0 && result.DurationMs == 0 {
			return result, fmt.Errorf("index is locked; cannot safely rebuild symbol data")
		}
		if len(result.Errors) > 0 || !result.FullReindex {
			return result, fmt.Errorf("full index rebuild failed")
		}
		return result, nil
	}
	observedHead, gitRepo := gitHead(idx.root)
	changes := idx.GetChangedFiles()
	paths := append(append([]string{}, changes.Added...), changes.Modified...)
	paths = append(paths, changes.Removed...)
	if len(paths) == 0 {
		if gitRepo {
			if err := idx.markGitHeadIfCurrent(observedHead); err != nil {
				return SyncResult{}, err
			}
		}
		return SyncResult{}, nil
	}
	result := idx.SyncFiles(paths, opts)
	if result.FilesChecked == 0 && result.DurationMs == 0 {
		return result, fmt.Errorf("index is locked; cannot safely refresh symbol data")
	}
	if len(result.Errors) > 0 {
		return result, fmt.Errorf("incremental refresh failed: %s", result.Errors[0].Message)
	}
	if gitRepo {
		if err := idx.markGitHeadIfCurrent(observedHead); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (idx *Indexer) getChangedFilesByScan(forceHash bool) ChangedFiles {
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
		// Always stat persisted paths: indexed untracked files may disappear
		// without appearing in git status or ScanDirectory's git candidate set.
		if !currentSet[rec.Path] {
			out.Removed = append(out.Removed, rec.Path)
		}
	}
	trackedMap := make(map[string]model.FileRecord, len(tracked))
	for _, f := range tracked {
		trackedMap[f.Path] = f
	}
	dirty := map[string]struct{}{}
	if changes, ok := gitChangedFiles(idx.root); ok {
		for _, p := range append(append(changes.modified, changes.added...), changes.deleted...) {
			dirty[p] = struct{}{}
		}
	}
	for _, filePath := range currentFiles {
		rec, isTracked := trackedMap[filePath]
		if !isTracked {
			out.Added = append(out.Added, filePath)
			continue
		}
		fi, err := os.Stat(filepath.Join(idx.root, filepath.FromSlash(filePath)))
		if err != nil {
			continue
		}
		_, gitDirty := dirty[filePath]
		if !forceHash && !gitDirty && fi.Size() == rec.Size && statMtimeMs(fi) == rec.ModifiedAt {
			continue
		}
		content, err := os.ReadFile(filepath.Join(idx.root, filepath.FromSlash(filePath)))
		if err != nil {
			continue
		}
		if rec.ContentHash != HashContent(content) {
			out.Modified = append(out.Modified, filePath)
		}
	}
	sort.Strings(out.Added)
	sort.Strings(out.Modified)
	sort.Strings(out.Removed)
	return out
}

func (idx *Indexer) gitChangedFilesSinceIndex() (ChangedFiles, bool) {
	indexedHead, err := idx.Store().GetMetadata(indexedGitHeadKey)
	if err != nil || indexedHead == "" {
		return ChangedFiles{}, false
	}
	currentHead, ok := gitHead(idx.root)
	if !ok {
		return ChangedFiles{}, false
	}
	trackedNow, ok := gitTrackedFiles(idx.root)
	if !ok {
		return ChangedFiles{}, false
	}
	tracked, err := idx.allTrackedFiles()
	if err != nil {
		return ChangedFiles{}, false
	}
	if idx.hasEmbeddedRepository(tracked) {
		// Root git cannot describe clean commits made inside an embedded repo.
		// Its scan already recurses into those repositories, so use the exact
		// scan/hash fallback only for this exceptional topology.
		return ChangedFiles{}, false
	}
	candidates := newChangedFilesSet()
	if indexedHead != currentHead {
		committed, ok := gitDiffFiles(idx.root, indexedHead, currentHead)
		if !ok {
			return ChangedFiles{}, false
		}
		candidates.merge(committed)
	}
	working, ok := gitChangedFiles(idx.root)
	if !ok {
		return ChangedFiles{}, false
	}
	candidates.merge(working)
	records := make(map[string]model.FileRecord, len(tracked))
	for _, rec := range tracked {
		records[rec.Path] = rec
	}
	changes := newChangedFilesSet()
	candidatePaths := map[string]struct{}{}
	for _, group := range []map[string]struct{}{candidates.added, candidates.modified, candidates.removed} {
		for path := range group {
			candidatePaths[path] = struct{}{}
		}
	}
	for path := range candidatePaths {
		rec, indexed := records[path]
		fullPath := filepath.Join(idx.root, filepath.FromSlash(path))
		if _, err := os.Stat(fullPath); err != nil {
			if indexed {
				changes.removed[path] = struct{}{}
			}
			continue
		}
		content, err := os.ReadFile(fullPath)
		if err != nil {
			// Preserve the candidate rather than claiming a fresh index when a
			// file could not be inspected. SyncFiles will surface its read error.
			if indexed {
				changes.modified[path] = struct{}{}
			} else {
				changes.added[path] = struct{}{}
			}
			continue
		}
		if !indexed {
			changes.added[path] = struct{}{}
		} else if rec.ContentHash != HashContent(content) {
			changes.modified[path] = struct{}{}
		}
	}

	// A formerly-untracked file can disappear without either a diff against
	// HEAD or status output. Find those by checking only persisted paths that
	// git says are not tracked now, rather than statting every indexed file.
	for _, rec := range tracked {
		if trackedNow[rec.Path] {
			continue
		}
		if _, candidate := candidatePaths[rec.Path]; candidate {
			continue
		}
		if _, err := os.Stat(filepath.Join(idx.root, filepath.FromSlash(rec.Path))); err != nil {
			changes.removed[rec.Path] = struct{}{}
		}
	}
	return changes.result(), true
}

func (idx *Indexer) hasEmbeddedRepository(records []model.FileRecord) bool {
	seen := map[string]struct{}{}
	for _, rec := range records {
		path := filepath.ToSlash(rec.Path)
		for dir := filepath.Dir(path); dir != "." && dir != "/"; dir = filepath.Dir(dir) {
			if _, ok := seen[dir]; ok {
				continue
			}
			seen[dir] = struct{}{}
			if _, err := os.Stat(filepath.Join(idx.root, filepath.FromSlash(dir), ".git")); err == nil {
				return true
			}
		}
	}
	return false
}

// MarkCurrentGitHead records the repository revision only after a caller has
// successfully refreshed every candidate it chose. It is public so the CLI's
// freshness path can make that completion explicit without making SyncFiles
// (which deliberately accepts arbitrary subsets) claim whole-tree freshness.
func (idx *Indexer) MarkCurrentGitHead() error { return idx.markCurrentGitHead() }

func (idx *Indexer) markCurrentGitHead() error {
	head, ok := gitHead(idx.root)
	if !ok {
		return nil
	}
	return idx.markGitHead(head)
}

func (idx *Indexer) markGitHead(head string) error {
	for _, s := range idx.Stores() {
		if err := s.SetMetadata(indexedGitHeadKey, head); err != nil {
			return err
		}
	}
	return nil
}

func (idx *Indexer) markGitHeadIfCurrent(observed string) error {
	current, ok := gitHead(idx.root)
	if !ok || current != observed {
		return fmt.Errorf("repository HEAD changed during refresh; no freshness stamp written")
	}
	return idx.markGitHead(observed)
}

type changedFilesSet struct {
	added, modified, removed map[string]struct{}
}

func newChangedFilesSet() changedFilesSet {
	return changedFilesSet{added: map[string]struct{}{}, modified: map[string]struct{}{}, removed: map[string]struct{}{}}
}

func (s changedFilesSet) merge(changes gitChanges) {
	for _, path := range changes.added {
		s.added[path] = struct{}{}
	}
	for _, path := range changes.modified {
		s.modified[path] = struct{}{}
	}
	for _, path := range changes.deleted {
		s.removed[path] = struct{}{}
	}
}

func (s changedFilesSet) result() ChangedFiles {
	// A delete followed by an add is a modification (for example an unstaged
	// replacement after a committed rename), while an add followed by a delete
	// is absent from both candidates.
	for path := range s.added {
		if _, removed := s.removed[path]; removed {
			delete(s.added, path)
			delete(s.removed, path)
			s.modified[path] = struct{}{}
		}
	}
	out := ChangedFiles{}
	for path := range s.added {
		out.Added = append(out.Added, path)
	}
	for path := range s.modified {
		out.Modified = append(out.Modified, path)
	}
	for path := range s.removed {
		out.Removed = append(out.Removed, path)
	}
	sort.Strings(out.Added)
	sort.Strings(out.Modified)
	sort.Strings(out.Removed)
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
func (idx *Indexer) deleteFileEverywhere(path string) (bool, error) {
	deleted := false
	for _, s := range idx.Stores() {
		rec, err := s.GetFileByPath(path)
		if err != nil {
			return false, err
		}
		if rec == nil {
			continue
		}
		if err := s.DeleteFile(path); err != nil {
			return false, err
		}
		deleted = true
	}
	return deleted, nil
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

// gitChangedFiles parses the NUL-delimited porcelain format. Filenames are
// opaque byte sequences here: unlike line porcelain, spaces and quotes never
// need C-style unquoting.
func gitChangedFiles(rootDir string) (gitChanges, bool) {
	out, err := gitOutputRaw(rootDir, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all")
	if err != nil {
		return gitChanges{}, false
	}
	return parsePorcelainZ(out), true
}

func parsePorcelainZ(out string) gitChanges {
	changes := gitChanges{}
	for record := range strings.SplitSeq(out, "\x00") {
		if len(record) < 4 {
			continue // minimum: "XY file"
		}
		statusCode := record[:2]
		filePath := filepath.ToSlash(record[3:])
		switch {
		case statusCode == "??":
			changes.added = append(changes.added, filePath)
		case strings.Contains(statusCode, "D"):
			changes.deleted = append(changes.deleted, filePath)
		default:
			changes.modified = append(changes.modified, filePath)
		}
	}
	return changes
}

func gitHead(rootDir string) (string, bool) {
	head, err := gitOutput(rootDir, "rev-parse", "HEAD")
	return head, err == nil && head != ""
}

func gitTrackedFiles(rootDir string) (map[string]bool, bool) {
	out, err := gitOutputRaw(rootDir, "ls-files", "-z")
	if err != nil {
		return nil, false
	}
	files := map[string]bool{}
	for path := range strings.SplitSeq(out, "\x00") {
		if path != "" {
			files[filepath.ToSlash(path)] = true
		}
	}
	return files, true
}

func gitDiffFiles(rootDir, from, to string) (gitChanges, bool) {
	out, err := gitOutputRaw(rootDir, "diff", "--name-status", "-z", "--no-renames", from, to)
	if err != nil {
		return gitChanges{}, false
	}
	changes := gitChanges{}
	parts := strings.Split(out, "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		status, path := parts[i], parts[i+1]
		if status == "" || path == "" {
			continue
		}
		path = filepath.ToSlash(path)
		switch status[0] {
		case 'A':
			changes.added = append(changes.added, path)
		case 'D':
			changes.deleted = append(changes.deleted, path)
		default:
			changes.modified = append(changes.modified, path)
		}
	}
	return changes, true
}
