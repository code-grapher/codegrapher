package resolve

import (
	"path"
	"strings"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// specScoreIndex maps the things a raw SpecScore cross-reference target can name
// to the artifact node it should resolve to. An artifact node's Name is its slug
// and its FilePath is the artifact file; relative-link references resolve through
// the file-path / directory keys.
type specScoreIndex struct {
	bySlug map[string]model.Node // artifact slug → node
	byPath map[string]model.Node // repo-relative artifact file path → node
	byDir  map[string]model.Node // directory containing the artifact file → node
}

// resolveSpecScoreRef resolves one SpecScore cross-reference into an edge.
//
// SpecScore references are discriminated by ref.Language == model.LangSpecScore
// (the extractor stamps every artifact ref with that language). The raw target
// (ref.ReferenceName) is whatever the artifact wrote: a bare slug, an
// `idea:`/`feature:`/`plan:`-prefixed slug, or a relative markdown link
// (e.g. `../plan/README.md`, `./feature.entity.md`). We normalize it and look it
// up in an index of SpecScore artifact nodes (kinds feature/idea/plan) built once
// per Resolve pass and cached on cache.
//
// Single-repo only: a target not present in this repo's index leaves the
// reference unresolved (no edge, no invented node), matching every other
// resolver's miss behavior. Within-doc plan task-number deps (e.g. "2") are not
// artifact references and simply miss.
func resolveSpecScoreRef(ref model.UnresolvedReference, s *store.Store, cache *specScoreIndex) *model.Edge {
	idx := cache
	if idx.bySlug == nil {
		*idx = buildSpecScoreIndex(s)
	}

	target, ok := idx.lookup(ref.ReferenceName, ref.FilePath)
	if !ok {
		return nil
	}
	// Don't emit a self-edge (an artifact referencing its own file/slug).
	if target.ID == ref.FromNodeID {
		return nil
	}
	return &model.Edge{
		Source:     ref.FromNodeID,
		Target:     target.ID,
		Kind:       ref.ReferenceKind,
		Line:       ref.Line,
		Column:     ref.Column,
		Provenance: "specscore",
	}
}

// buildSpecScoreIndex scans all nodes once and indexes the SpecScore artifact
// nodes (feature/idea/plan) by slug, by repo-relative file path, and by the
// directory that contains them.
func buildSpecScoreIndex(s *store.Store) specScoreIndex {
	idx := specScoreIndex{
		bySlug: map[string]model.Node{},
		byPath: map[string]model.Node{},
		byDir:  map[string]model.Node{},
	}
	nodes, err := s.AllNodes()
	if err != nil {
		return idx
	}
	for _, n := range nodes {
		if n.Language != model.LangSpecScore {
			continue
		}
		if n.Kind != model.KindFeature && n.Kind != model.KindIdea && n.Kind != model.KindPlan {
			continue
		}
		if n.Name != "" {
			if _, dup := idx.bySlug[n.Name]; !dup {
				idx.bySlug[n.Name] = n
			}
		}
		p := normalizePath(n.FilePath)
		idx.byPath[p] = n
		idx.byDir[path.Dir(p)] = n
	}
	return idx
}

// lookup resolves a raw target written in the artifact at fromPath to an artifact
// node. It tries a relative-link interpretation first (when the target looks like
// a path), then a bare-slug interpretation.
func (idx specScoreIndex) lookup(rawTarget, fromPath string) (model.Node, bool) {
	target := strings.TrimSpace(rawTarget)
	if target == "" {
		return model.Node{}, false
	}
	// Trim a trailing anchor/fragment (#section).
	if h := strings.IndexByte(target, '#'); h >= 0 {
		target = target[:h]
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return model.Node{}, false
	}

	// Relative markdown link → resolve against the source artifact's directory.
	if looksLikePath(target) {
		rel := path.Join(path.Dir(normalizePath(fromPath)), target)
		rel = strings.TrimPrefix(rel, "./")
		if n, ok := idx.byPath[rel]; ok {
			return n, true
		}
		// A link to a directory (e.g. ../plan/) resolves to the artifact in it.
		if n, ok := idx.byDir[strings.TrimSuffix(rel, "/")]; ok {
			return n, true
		}
		return model.Node{}, false
	}

	// Bare slug, possibly with an `idea:`/`feature:`/`plan:` prefix.
	slug := stripArtifactPrefix(target)
	if n, ok := idx.bySlug[slug]; ok {
		return n, true
	}
	return model.Node{}, false
}

// looksLikePath reports whether a raw target is a relative markdown link rather
// than a bare slug: it contains a path separator or ends in ".md".
func looksLikePath(target string) bool {
	return strings.ContainsRune(target, '/') || strings.HasSuffix(target, ".md")
}

// stripArtifactPrefix removes a leading `idea:`/`feature:`/`plan:` qualifier from
// a bare-slug target, leaving the slug.
func stripArtifactPrefix(target string) string {
	for _, p := range []string{"idea:", "feature:", "plan:"} {
		if strings.HasPrefix(target, p) {
			return strings.TrimSpace(target[len(p):])
		}
	}
	return target
}

// normalizePath converts a node FilePath to forward slashes for stable matching.
func normalizePath(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}
