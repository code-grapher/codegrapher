package extract

import (
	"fmt"
	"strings"
	"time"

	"github.com/specscore/codegrapher/gomod"
	"github.com/specscore/codegrapher/model"
)

// extractGoMod parses a go.mod and emits a main module node (contained by the
// already-emitted file node) plus one KindModule node per dependency, joined by
// requires/replaces/excludes edges. The go/toolchain/retract directives are
// encoded on the main module node's Signature. Called by ExtractFile for
// LangGoMod. A parse error is recorded as a warning and leaves only the file
// node (mirrors the file-level parse-error path).
func (e *extractor) extractGoMod(content []byte) {
	f, err := gomod.Parse(e.filePath, content)
	if err != nil {
		e.errors = append(e.errors, model.ExtractionError{
			Message:  err.Error(),
			FilePath: e.filePath,
			Severity: "warning",
			Code:     "gomod_parse_error",
		})
		return
	}
	if f.Module == "" {
		return
	}
	now := time.Now().UnixMilli()

	// Main module node.
	mainID := e.addModuleNode(f.Module, moduleSignature(f), 1, now)
	e.edges = append(e.edges, model.Edge{
		Source: model.FileNodeID(e.filePath), Target: mainID, Kind: model.EdgeContains, Provenance: "modfile",
	})

	// depNode returns (creating once) the KindModule node for a dep path.
	depIDs := map[string]string{}
	depNode := func(path, version string, line int) string {
		if id, ok := depIDs[path]; ok {
			return id
		}
		id := e.addModuleNode(path, version, line, now)
		depIDs[path] = id
		return id
	}

	for _, r := range f.Requires {
		id := depNode(r.Path, r.Version, r.Line)
		e.edges = append(e.edges, model.Edge{
			Source: mainID, Target: id, Kind: model.EdgeRequires, Line: r.Line,
			Provenance: "modfile",
			Metadata:   map[string]any{"version": r.Version, "indirect": r.Indirect},
		})
	}
	for _, r := range f.Replaces {
		oldID := depNode(r.OldPath, r.OldVersion, r.Line)
		// A filesystem replace (NewVersion == "") targets a local directory, not
		// a module, so there is no separate module node for it — the edge loops
		// back to the replaced dep. A module replace gets its own target node.
		newID := oldID
		if r.NewVersion != "" {
			newID = depNode(r.NewPath, r.NewVersion, r.Line)
		}
		e.edges = append(e.edges, model.Edge{
			Source: oldID, Target: newID, Kind: model.EdgeReplaces, Line: r.Line,
			Provenance: "modfile",
			Metadata:   map[string]any{"local": r.NewVersion == "", "newPath": r.NewPath, "newVersion": r.NewVersion},
		})
	}
	for _, x := range f.Excludes {
		id := depNode(x.Path, x.Version, x.Line)
		e.edges = append(e.edges, model.Edge{
			Source: mainID, Target: id, Kind: model.EdgeExcludes, Line: x.Line,
			Provenance: "modfile",
			Metadata:   map[string]any{"version": x.Version},
		})
	}
}

// addModuleNode appends a KindModule node and returns its ID.
func (e *extractor) addModuleNode(path, signature string, line int, now int64) string {
	id := model.GenerateNodeID(e.filePath, model.KindModule, path, line)
	e.nodes = append(e.nodes, model.Node{
		ID:            id,
		Kind:          model.KindModule,
		Name:          path,
		QualifiedName: path,
		FilePath:      e.filePath,
		Language:      e.lang,
		StartLine:     line,
		EndLine:       line,
		Signature:     signature,
		UpdatedAt:     now,
	})
	return id
}

// moduleSignature encodes go/toolchain/retract on the main module node.
func moduleSignature(f *gomod.File) string {
	var parts []string
	if f.Go != "" {
		parts = append(parts, "go "+f.Go)
	}
	if f.Toolchain != "" {
		parts = append(parts, "toolchain "+f.Toolchain)
	}
	if len(f.Retracts) > 0 {
		var rs []string
		for _, r := range f.Retracts {
			if r.Low == r.High {
				rs = append(rs, r.Low)
			} else {
				rs = append(rs, fmt.Sprintf("[%s, %s]", r.Low, r.High))
			}
		}
		parts = append(parts, "retract ["+strings.Join(rs, ", ")+"]")
	}
	return strings.Join(parts, "; ")
}
