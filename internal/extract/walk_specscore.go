package extract

import (
	"strings"
	"time"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/specdoc"
)

// extractSpecScore parses a SpecScore artifact (Feature, Idea, or Plan) via the
// specdoc adapter and emits an artifact node (contained by the already-emitted
// file node) plus one child node per structural Item, joined by `contains`
// edges. Cross-file references (Promotes To / Supersedes / Depends-On / Related
// / Source / …) are NOT resolved here: they are recorded as
// model.UnresolvedReference values carrying the raw target string, for the
// resolution pass (Task 5) to rewrite into real edges. Called by ExtractFile
// for LangSpecScore. A parse error is recorded as a warning and leaves only the
// file node (mirrors extractGoMod's parse-error path).
func (e *extractor) extractSpecScore(content []byte) {
	d, err := specdoc.ParseContent(e.filePath, content)
	if err != nil {
		e.errors = append(e.errors, model.ExtractionError{
			Message:  err.Error(),
			FilePath: e.filePath,
			Severity: "warning",
			Code:     "specscore_parse_error",
		})
		return
	}
	now := time.Now().UnixMilli()

	// Artifact node. Identity is the artifact slug (stable, spec-meaningful);
	// Status/Grade are encoded on the Signature, mirroring how extractGoMod
	// encodes directive metadata on the module node's Signature.
	artifactKind := specScoreNodeKind(d.Kind)
	name := d.Slug
	if name == "" {
		name = d.Title
	}
	artifactID := model.GenerateNodeID(e.filePath, artifactKind, name, 1)
	e.nodes = append(e.nodes, model.Node{
		ID:            artifactID,
		Kind:          artifactKind,
		Name:          name,
		QualifiedName: name,
		FilePath:      e.filePath,
		Language:      e.lang,
		StartLine:     1,
		EndLine:       1,
		Signature:     specScoreSignature(d),
		UpdatedAt:     now,
	})
	e.edges = append(e.edges, model.Edge{
		Source: model.FileNodeID(e.filePath), Target: artifactID,
		Kind: model.EdgeContains, Provenance: "specscore",
	})

	// Child nodes. The specdoc adapter exposes child structure as flat Items:
	// real tasks for Plans, but only section headings for Features and Ideas
	// (it does not surface per-REQ/per-AC IDs). We therefore emit:
	//   - Plan items   → KindTask (genuine tasks, numeric within-doc IDs)
	//   - Feature items → KindRequirement (section-granular; see CONCERN below)
	//   - Idea items   → none (Idea sections are narrative prose, not spec
	//     structure — emitting them would invent data)
	//
	// CONCERN: Feature acceptance-criterion granularity (KindAcceptanceCriterion)
	// is not reachable through the current specdoc adapter, which yields flat
	// section headings rather than individual AC IDs. Feature children are
	// therefore mapped to KindRequirement at section granularity. Finer AC-level
	// extraction needs a richer specdoc API and is out of scope for this task.
	childKind, emitChildren := specScoreChildKind(d.Kind)
	if emitChildren {
		for _, it := range d.Items {
			cid := model.GenerateNodeID(e.filePath, childKind, it.ID, 1)
			e.nodes = append(e.nodes, model.Node{
				ID:            cid,
				Kind:          childKind,
				Name:          it.Title,
				QualifiedName: name + "::" + it.ID,
				FilePath:      e.filePath,
				Language:      e.lang,
				StartLine:     1,
				EndLine:       1,
				UpdatedAt:     now,
			})
			e.edges = append(e.edges, model.Edge{
				Source: artifactID, Target: cid,
				Kind: model.EdgeContains, Provenance: "specscore",
			})
		}
	}

	// Cross-file references — RECORDED, not resolved. Each Ref becomes an
	// UnresolvedReference whose ReferenceName is the raw target string exactly as
	// written in the artifact (a slug, AC id, or task number) and whose
	// ReferenceKind is the SpecScore edge kind it maps to. Task 5 resolves
	// ReferenceName to a concrete target node id and emits the final edge.
	for _, r := range d.Refs {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    artifactID,
			ReferenceName: r.Target,
			ReferenceKind: specScoreRefKind(r.Kind),
			FilePath:      e.filePath,
			Language:      e.lang,
		})
	}
}

// specScoreNodeKind maps a specdoc artifact Kind to the artifact NodeKind.
func specScoreNodeKind(k specdoc.Kind) model.NodeKind {
	switch k {
	case specdoc.KindFeature:
		return model.KindFeature
	case specdoc.KindIdea:
		return model.KindIdea
	case specdoc.KindPlan:
		return model.KindPlan
	default:
		return model.KindFeature
	}
}

// specScoreChildKind maps an artifact Kind to the NodeKind of its children and
// whether children are emitted at all (Ideas emit none — see extractSpecScore).
func specScoreChildKind(k specdoc.Kind) (model.NodeKind, bool) {
	switch k {
	case specdoc.KindPlan:
		return model.KindTask, true
	case specdoc.KindFeature:
		return model.KindRequirement, true
	default:
		return "", false
	}
}

// specScoreRefKind maps a specdoc Ref.Kind to the EdgeKind the resolved edge
// will carry. SpecScore-specific relationships get their own edge kinds;
// everything else (related/source/links/…) falls back to EdgeReferences.
func specScoreRefKind(kind string) model.EdgeKind {
	switch kind {
	case "promotes_to":
		return model.EdgePromotesTo
	case "supersedes":
		return model.EdgeSupersedes
	case "depends_on":
		return model.EdgeDependsOn
	default:
		return model.EdgeReferences
	}
}

// specScoreSignature encodes Status and Grade on the artifact node's Signature,
// mirroring how extractGoMod encodes directives on the module node's Signature.
func specScoreSignature(d *specdoc.Doc) string {
	var parts []string
	if d.Status != "" {
		parts = append(parts, "status "+d.Status)
	}
	if d.Grade != "" {
		parts = append(parts, "grade "+d.Grade)
	}
	return strings.Join(parts, "; ")
}
