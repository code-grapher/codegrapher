package indexer

import (
	"path/filepath"
	"strconv"
	"testing"
)

// These tests cover the version-gated reindex behavior (spec feature
// version-gated-reindex): Sync does an additive update when the stored
// scanner/extraction version matches the running binary, and escalates to a
// full from-scratch reindex when either differs or is missing.

func setMeta(t *testing.T, idx *Indexer, key, val string) {
	t.Helper()
	if err := idx.Store().SetMetadata(key, val); err != nil {
		t.Fatalf("SetMetadata(%q): %v", key, err)
	}
}

func storedMeta(t *testing.T, idx *Indexer, key string) string {
	t.Helper()
	v, err := idx.Store().GetMetadata(key)
	if err != nil {
		t.Fatalf("GetMetadata(%q): %v", key, err)
	}
	return v
}

// AC-1: matching version → additive sync, only changed files re-extracted.
func TestSyncAdditiveWhenVersionMatches(t *testing.T) {
	dir, idx := newSyncProject(t)
	target := filepath.Join(dir, "src", "index.ts")
	touchPast(t, target)
	writeFile(t, target, "export function goodbye() { return 'farewell'; }")

	res := idx.Sync(Options{})
	if res.FullReindex {
		t.Fatalf("FullReindex = true, want false when version matches")
	}
	if res.FilesModified != 1 {
		t.Fatalf("FilesModified = %d, want 1 (additive)", res.FilesModified)
	}
	if !hasNodeNamed(t, idx, "goodbye") {
		t.Error("goodbye not in graph after additive sync")
	}
}

// AC-2: stored scanner version differs → full reindex + re-stamp.
func TestSyncEscalatesOnScannerVersionMismatch(t *testing.T) {
	_, idx := newSyncProject(t)
	setMeta(t, idx, "indexed_with_version", "0.0.0-old")

	res := idx.Sync(Options{}) // no file changes

	if !res.FullReindex {
		t.Fatalf("FullReindex = false, want true when scanner version changed")
	}
	if got := storedMeta(t, idx, "indexed_with_version"); got != PackageVersion {
		t.Errorf("indexed_with_version = %q, want %q (re-stamped)", got, PackageVersion)
	}
	if !hasNodeNamed(t, idx, "hello") {
		t.Error("hello missing after full reindex (index should be rebuilt from disk)")
	}
}

// AC-3: stored extraction version differs → full reindex + re-stamp.
func TestSyncEscalatesOnExtractionVersionMismatch(t *testing.T) {
	_, idx := newSyncProject(t)
	setMeta(t, idx, "indexed_with_extraction_version", "0")

	res := idx.Sync(Options{})

	if !res.FullReindex {
		t.Fatalf("FullReindex = false, want true when extraction version changed")
	}
	want := strconv.Itoa(ExtractionVersion)
	if got := storedMeta(t, idx, "indexed_with_extraction_version"); got != want {
		t.Errorf("indexed_with_extraction_version = %q, want %q (re-stamped)", got, want)
	}
}

// AC-4: missing version metadata (pre-feature index) → full reindex.
func TestSyncEscalatesWhenVersionMetadataMissing(t *testing.T) {
	_, idx := newSyncProject(t)
	setMeta(t, idx, "indexed_with_version", "") // GetMetadata("") == absent

	res := idx.Sync(Options{})

	if !res.FullReindex {
		t.Fatalf("FullReindex = false, want true when version metadata is missing")
	}
}
