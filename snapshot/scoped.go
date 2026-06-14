package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/specscore/codegrapher/scope"
	"github.com/specscore/codegrapher/store"
)

// Manifest describes the scopes available for one ref of a repo. It is written
// as codegraph/manifest.json and served by the DB server / read by the viewer.
type Manifest struct {
	Ref    string          `json:"ref"`
	Scopes []ManifestScope `json:"scopes"`
}

// ManifestScope is one (language, version) partition entry.
type ManifestScope struct {
	Language  string `json:"language"`
	Version   string `json:"version"`
	Key       string `json:"key"`
	Counts    Counts `json:"counts"`
	IndexedAt string `json:"indexed_at,omitempty"` // RFC3339
}

// Counts are the row counts of a scope, used for tree labels and progress.
type Counts struct {
	Nodes        int `json:"nodes"`
	Files        int `json:"files"`
	Edges        int `json:"edges"`
	Coverage     int `json:"coverage,omitempty"`
	NodeCoverage int `json:"node_coverage,omitempty"`
}

// RecordsetSize reports the original and compressed byte sizes of one exported
// recordset, for the dogfood size report.
type RecordsetSize struct {
	Scope    string
	Name     string
	Original int
	Zstd     int
	Gzip     int
}

// scopedRecordsets are the recordsets exported per scope, paired with the
// exporter that writes each. coverage / node_coverage are emitted like the
// others (compressed *.ingr.zst + *.ingr.gz); when a scope has no coverage the
// recordset is simply empty.
var scopedRecordsets = []struct {
	name   string
	export func(*store.Store, string) error
}{
	{"nodes", exportNodes},
	{"edges", exportEdges},
	{"files", exportFiles},
	{"project_metadata", exportMetadata},
	{"coverage", exportCoverage},
	{"node_coverage", exportNodeCoverage},
}

// ExportScoped writes each scope's graph under baseOutDir/{lang}/{version}/ as
// compressed-only INGR recordsets (*.ingr.zst + *.ingr.gz, no plain .ingr) and
// writes baseOutDir/manifest.json describing ref. It returns the manifest and a
// per-recordset size report.
func ExportScoped(stores map[scope.Scope]*store.Store, baseOutDir, ref string) (Manifest, []RecordsetSize, error) {
	if err := os.MkdirAll(baseOutDir, 0o755); err != nil {
		return Manifest{}, nil, fmt.Errorf("snapshot: mkdir %s: %w", baseOutDir, err)
	}

	scopes := make([]scope.Scope, 0, len(stores))
	for sc := range stores {
		scopes = append(scopes, sc)
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Key() < scopes[j].Key() })

	manifest := Manifest{Ref: ref}
	var sizes []RecordsetSize

	for _, sc := range scopes {
		s := stores[sc]
		scopeDir := filepath.Join(baseOutDir, string(sc.Language), sc.Version)

		for _, rs := range scopedRecordsets {
			if err := rs.export(s, scopeDir); err != nil {
				return Manifest{}, nil, fmt.Errorf("snapshot: export %s/%s: %w", sc.Key(), rs.name, err)
			}
			sz, err := compressRecordset(scopeDir, rs.name, sc.Key())
			if err != nil {
				return Manifest{}, nil, err
			}
			sizes = append(sizes, sz)
		}

		entry, err := manifestScope(s, sc)
		if err != nil {
			return Manifest{}, nil, err
		}
		manifest.Scopes = append(manifest.Scopes, entry)
	}

	if err := writeManifest(baseOutDir, manifest); err != nil {
		return Manifest{}, nil, err
	}
	return manifest, sizes, nil
}

// compressRecordset reads the recordset the inGitDB exporter wrote at
// scopeDir/{name}/{name}.ingr, writes flat scopeDir/{name}.ingr.{zst,gz}
// variants, removes the inGitDB collection dir (the nested .ingr plus its
// .collection wrapper — neither is served), and returns its size report.
func compressRecordset(scopeDir, name, scopeKey string) (RecordsetSize, error) {
	nestedDir := filepath.Join(scopeDir, name)
	src := filepath.Join(nestedDir, name+".ingr")
	data, err := os.ReadFile(src)
	if err != nil {
		return RecordsetSize{}, fmt.Errorf("snapshot: read %s: %w", src, err)
	}
	zst, err := CompressZstd(data)
	if err != nil {
		return RecordsetSize{}, err
	}
	gz, err := CompressGzip(data)
	if err != nil {
		return RecordsetSize{}, err
	}
	flat := filepath.Join(scopeDir, name+".ingr")
	if err := os.WriteFile(flat+".zst", zst, 0o644); err != nil {
		return RecordsetSize{}, err
	}
	if err := os.WriteFile(flat+".gz", gz, 0o644); err != nil {
		return RecordsetSize{}, err
	}
	if err := os.RemoveAll(nestedDir); err != nil {
		return RecordsetSize{}, err
	}
	return RecordsetSize{
		Scope: scopeKey, Name: name,
		Original: len(data), Zstd: len(zst), Gzip: len(gz),
	}, nil
}

func manifestScope(s *store.Store, sc scope.Scope) (ManifestScope, error) {
	stats, err := s.GetStats()
	if err != nil {
		return ManifestScope{}, fmt.Errorf("snapshot: stats %s: %w", sc.Key(), err)
	}
	covCount, err := s.GetAllCoverage()
	if err != nil {
		return ManifestScope{}, fmt.Errorf("snapshot: coverage count %s: %w", sc.Key(), err)
	}
	nodeCovCount, err := s.GetAllNodeCoverage()
	if err != nil {
		return ManifestScope{}, fmt.Errorf("snapshot: node_coverage count %s: %w", sc.Key(), err)
	}
	entry := ManifestScope{
		Language: string(sc.Language),
		Version:  sc.Version,
		Key:      sc.Key(),
		Counts: Counts{
			Nodes: stats.NodeCount, Files: stats.FileCount, Edges: stats.EdgeCount,
			Coverage: len(covCount), NodeCoverage: len(nodeCovCount),
		},
	}
	if ms, err := s.GetLastIndexedAt(); err == nil && ms > 0 {
		entry.IndexedAt = time.UnixMilli(ms).UTC().Format(time.RFC3339)
	}
	return entry, nil
}

func writeManifest(baseOutDir string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(baseOutDir, "manifest.json"), data, 0o644); err != nil {
		return fmt.Errorf("snapshot: write manifest: %w", err)
	}
	return nil
}
