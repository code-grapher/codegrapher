package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

// extractSpecScore re-reads the artifact from disk via the specdoc adapter, so
// these tests point at the repo's own real SpecScore artifacts (relative to the
// internal/extract package dir).
const (
	ideaFixture    = "../../spec/ideas/specscore-artifact-extraction.md"
	planFixture    = "../../spec/plans/specscore-artifact-extraction.md"
	featureFixture = "../../spec/features/version-gated-reindex/README.md"
)

func ssFindNode(nodes []model.Node, kind model.NodeKind) *model.Node {
	for i := range nodes {
		if nodes[i].Kind == kind {
			return &nodes[i]
		}
	}
	return nil
}

func ssCountNodes(nodes []model.Node, kind model.NodeKind) int {
	c := 0
	for i := range nodes {
		if nodes[i].Kind == kind {
			c++
		}
	}
	return c
}

func ssContainsEdge(edges []model.Edge, source, target string) bool {
	for _, e := range edges {
		if e.Source == source && e.Target == target && e.Kind == model.EdgeContains {
			return true
		}
	}
	return false
}

func TestExtractSpecScorePlan(t *testing.T) {
	res, err := ExtractFile(planFixture, nil, model.LangSpecScore)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}

	file := ssFindNode(res.Nodes, model.KindFile)
	if file == nil || file.Language != model.LangSpecScore {
		t.Fatalf("missing/incorrect file node: %+v", file)
	}

	plan := ssFindNode(res.Nodes, model.KindPlan)
	if plan == nil {
		t.Fatal("missing plan artifact node")
	}
	if plan.Name != "specscore-artifact-extraction" {
		t.Errorf("plan.Name = %q, want slug", plan.Name)
	}
	if plan.Signature == "" {
		t.Errorf("plan.Signature should encode status, got empty")
	}

	// file → artifact contains edge.
	if !ssContainsEdge(res.Edges, model.FileNodeID(planFixture), plan.ID) {
		t.Error("missing file → plan contains edge")
	}

	// Plan items → KindTask, each with an artifact → task contains edge.
	tasks := ssCountNodes(res.Nodes, model.KindTask)
	if tasks == 0 {
		t.Fatal("expected at least one task node")
	}
	taskContains := 0
	for _, n := range res.Nodes {
		if n.Kind == model.KindTask && ssContainsEdge(res.Edges, plan.ID, n.ID) {
			taskContains++
		}
	}
	if taskContains != tasks {
		t.Errorf("plan → task contains edges = %d, want %d", taskContains, tasks)
	}

	// Cross-file refs recorded (not resolved) with raw targets, e.g. depends_on.
	if len(res.UnresolvedReferences) == 0 {
		t.Fatal("expected recorded cross-file references")
	}
	var sawDependsOn bool
	for _, r := range res.UnresolvedReferences {
		if r.FromNodeID != plan.ID {
			t.Errorf("ref FromNodeID = %q, want artifact id %q", r.FromNodeID, plan.ID)
		}
		if r.ReferenceName == "" {
			t.Error("ref ReferenceName (raw target) is empty")
		}
		if r.ReferenceKind == model.EdgeDependsOn {
			sawDependsOn = true
		}
	}
	if !sawDependsOn {
		t.Error("expected at least one depends_on reference")
	}
}

func TestExtractSpecScoreIdea(t *testing.T) {
	res, err := ExtractFile(ideaFixture, nil, model.LangSpecScore)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}

	idea := ssFindNode(res.Nodes, model.KindIdea)
	if idea == nil {
		t.Fatal("missing idea artifact node")
	}
	if idea.Name != "specscore-artifact-extraction" {
		t.Errorf("idea.Name = %q, want slug", idea.Name)
	}
	if !ssContainsEdge(res.Edges, model.FileNodeID(ideaFixture), idea.ID) {
		t.Error("missing file → idea contains edge")
	}
	// Idea sections are narrative prose, not spec structure: no child nodes.
	if n := ssCountNodes(res.Nodes, model.KindRequirement); n != 0 {
		t.Errorf("idea emitted %d requirement nodes, want 0", n)
	}
}

func TestExtractSpecScoreFeature(t *testing.T) {
	res, err := ExtractFile(featureFixture, nil, model.LangSpecScore)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}

	feat := ssFindNode(res.Nodes, model.KindFeature)
	if feat == nil {
		t.Fatal("missing feature artifact node")
	}
	if feat.Name != "version-gated-reindex" {
		t.Errorf("feat.Name = %q, want slug", feat.Name)
	}
	if !ssContainsEdge(res.Edges, model.FileNodeID(featureFixture), feat.ID) {
		t.Error("missing file → feature contains edge")
	}

	// Feature sections → KindRequirement (section granularity; see CONCERN in
	// walk_specscore.go), each with an artifact → requirement contains edge.
	reqs := ssCountNodes(res.Nodes, model.KindRequirement)
	if reqs == 0 {
		t.Fatal("expected at least one requirement node")
	}
	for _, n := range res.Nodes {
		if n.Kind == model.KindRequirement && !ssContainsEdge(res.Edges, feat.ID, n.ID) {
			t.Errorf("requirement %q missing feature → requirement contains edge", n.Name)
		}
	}
}

func TestExtractSpecScoreParseError(t *testing.T) {
	// A non-existent path makes specdoc.Parse fail; the extractor should record
	// a warning and leave only the file node.
	res, err := ExtractFile("spec/does-not-exist.md", nil, model.LangSpecScore)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	if ssFindNode(res.Nodes, model.KindFile) == nil {
		t.Error("expected file node to remain on parse error")
	}
	if len(res.Nodes) != 1 {
		t.Errorf("expected only the file node, got %d nodes", len(res.Nodes))
	}
	if len(res.Errors) != 1 || res.Errors[0].Code != "specscore_parse_error" {
		t.Errorf("expected one specscore_parse_error warning, got %+v", res.Errors)
	}
}
