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
		if isTracked {
			fi, err := os.Stat(fullPath)
			if err != nil {
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
	// Keep the cross-scope trace projection aligned with the exact working tree
	// revision even when only directives or spec files changed.
	_ = idx.indexTrace()

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
	_ = idx.indexTrace()

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
	if len(ir.Errors) > 0 {
		// Surface every extraction warning/error to freshness callers. Restore
		// still ran, but only for targets proven replaced; that preserves
		// unchanged callers when a parser returned a partial valid result while
		// avoiding duplicate edges when it left the old nodes intact.
		result.Errors = append(result.Errors, ir.Errors...)
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
	}
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
		for _, file := range changedFiles {
			nodes, err := s.GetNodesByFile(file)
			if err != nil {
				return nil, fmt.Errorf("read changed nodes for %s: %w", file, err)
			}
			for _, target := range nodes {
				edges, err := s.GetIncomingEdges(target.ID, nil)
				if err != nil {
					return nil, fmt.Errorf("read incoming edges for %s: %w", target.ID, err)
				}
				for _, edge := range edges {
					source, err := s.GetNodeByID(edge.Source)
					if err != nil {
						return nil, fmt.Errorf("read edge source %s: %w", edge.Source, err)
					}
					if source == nil {
						continue
					}
					if _, isChanging := changed[source.FilePath]; isChanging {
						continue
					}
					out = append(out, incomingEdgeBackup{store: s, target: target, edge: edge})
				}
			}
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
		// extractAndStore can fail after capture (for example a parse/read
		// failure) and intentionally leaves the old file record intact. Only
		// restore an edge when its original target was actually replaced.
		old, err := backup.store.GetNodeByID(backup.target.ID)
		if err != nil {
			return fmt.Errorf("check replaced target %s: %w", backup.target.ID, err)
		}
		if old != nil {
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
	if changed, ok := idx.gitChangedFilesSinceIndex(); ok {
		return changed
	}
	return idx.getChangedFilesByScan()
}

func (idx *Indexer) getChangedFilesByScan() ChangedFiles {
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
		_, forceHash := dirty[filePath]
		if !forceHash && fi.Size() == rec.Size && statMtimeMs(fi) == rec.ModifiedAt {
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
	for _, s := range idx.Stores() {
		if err := s.SetMetadata(indexedGitHeadKey, head); err != nil {
			return err
		}
	}
	return nil
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
