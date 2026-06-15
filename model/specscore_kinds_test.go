package model

import (
	"slices"
	"testing"
)

func TestSpecScoreKindsExist(t *testing.T) {
	if LangSpecScore != "specscore" {
		t.Errorf("LangSpecScore = %q", LangSpecScore)
	}

	nodeKinds := map[NodeKind]string{
		KindFeature:             "feature",
		KindIdea:                "idea",
		KindPlan:                "plan",
		KindRequirement:         "requirement",
		KindAcceptanceCriterion: "acceptance_criterion",
		KindTask:                "task",
	}
	for k, want := range nodeKinds {
		if string(k) != want {
			t.Errorf("node kind = %q, want %q", k, want)
		}
		if !slices.Contains(NodeKinds, k) {
			t.Errorf("node kind %q missing from NodeKinds slice", k)
		}
	}

	edgeKinds := map[EdgeKind]string{
		EdgePromotesTo: "promotes_to",
		EdgeSupersedes: "supersedes",
		EdgeDependsOn:  "depends_on",
	}
	for k, want := range edgeKinds {
		if string(k) != want {
			t.Errorf("edge kind = %q, want %q", k, want)
		}
	}
}
