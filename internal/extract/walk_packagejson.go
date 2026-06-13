package extract

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/pkgjson"
)

// extractPackageJSON parses a package.json and emits a main module node
// (contained by the already-emitted file node) plus one KindModule node per
// dependency across all four dependency categories, joined by EdgeRequires
// edges carrying {version, category} metadata. version/engines are encoded on
// the main module node's Signature. A parse error is recorded as a warning and
// leaves only the file node (mirrors the go.mod parse-error path).
func (e *extractor) extractPackageJSON(content []byte) {
	f, err := pkgjson.Parse(content)
	if err != nil {
		e.errors = append(e.errors, model.ExtractionError{
			Message:  err.Error(),
			FilePath: e.filePath,
			Severity: "warning",
			Code:     "packagejson_parse_error",
		})
		return
	}
	now := time.Now().UnixMilli()

	name := f.Name
	if name == "" {
		name = filepath.Base(filepath.Dir(e.filePath))
		if name == "." || name == string(filepath.Separator) || name == "" {
			name = "package.json"
		}
	}

	mainID := e.addModuleNode(name, pkgSignature(f), 1, now)
	e.edges = append(e.edges, model.Edge{
		Source: model.FileNodeID(e.filePath), Target: mainID,
		Kind: model.EdgeContains, Provenance: "package.json",
	})

	depIDs := map[string]string{}
	depNode := func(dep, version string) string {
		if id, ok := depIDs[dep]; ok {
			return id
		}
		id := e.addModuleNode(dep, version, 1, now)
		depIDs[dep] = id
		return id
	}

	addCategory := func(deps map[string]string, category string) {
		names := make([]string, 0, len(deps))
		for n := range deps {
			names = append(names, n)
		}
		sort.Strings(names) // deterministic node/edge order (json maps are unordered)
		for _, n := range names {
			id := depNode(n, deps[n])
			e.edges = append(e.edges, model.Edge{
				Source: mainID, Target: id, Kind: model.EdgeRequires,
				Provenance: "package.json",
				Metadata:   map[string]any{"version": deps[n], "category": category},
			})
		}
	}
	addCategory(f.Dependencies, "prod")
	addCategory(f.DevDependencies, "dev")
	addCategory(f.PeerDependencies, "peer")
	addCategory(f.OptionalDependencies, "optional")
}

// pkgSignature encodes version + engines on the main module node.
func pkgSignature(f *pkgjson.File) string {
	var parts []string
	if f.Version != "" {
		parts = append(parts, "version "+f.Version)
	}
	if len(f.Engines) > 0 {
		keys := make([]string, 0, len(f.Engines))
		for k := range f.Engines {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var es []string
		for _, k := range keys {
			es = append(es, k+f.Engines[k]) // e.g. "node>=18"
		}
		parts = append(parts, "engines: "+strings.Join(es, ", "))
	}
	return strings.Join(parts, "; ")
}
