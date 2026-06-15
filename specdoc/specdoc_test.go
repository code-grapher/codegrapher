package specdoc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseIdea(t *testing.T) {
	// The repo's own Idea artifact serves as the idea fixture.
	d, err := Parse("../spec/ideas/specscore-artifact-extraction.md")
	if err != nil {
		t.Fatalf("Parse idea: %v", err)
	}
	if d.Kind != KindIdea {
		t.Errorf("Kind = %q, want %q", d.Kind, KindIdea)
	}
	if d.Slug != "specscore-artifact-extraction" {
		t.Errorf("Slug = %q", d.Slug)
	}
	if d.Status == "" {
		t.Error("Status is empty")
	}
	if d.Title == "" {
		t.Error("Title is empty")
	}
}

func TestParsePlan(t *testing.T) {
	d, err := Parse("../spec/plans/specscore-artifact-extraction.md")
	if err != nil {
		t.Fatalf("Parse plan: %v", err)
	}
	if d.Kind != KindPlan {
		t.Errorf("Kind = %q, want %q", d.Kind, KindPlan)
	}
	if d.Slug != "specscore-artifact-extraction" {
		t.Errorf("Slug = %q", d.Slug)
	}
	if d.Status == "" {
		t.Error("Status is empty")
	}
	if len(d.Items) == 0 {
		t.Error("expected plan tasks as Items")
	}
}

func TestParseFeature(t *testing.T) {
	d, err := Parse("../spec/features/version-gated-reindex/README.md")
	if err != nil {
		t.Fatalf("Parse feature: %v", err)
	}
	if d.Kind != KindFeature {
		t.Errorf("Kind = %q, want %q", d.Kind, KindFeature)
	}
	if d.Slug != "version-gated-reindex" {
		t.Errorf("Slug = %q", d.Slug)
	}
	if d.Status == "" {
		t.Error("Status is empty")
	}
}

func TestParseUnknownFormat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plain.md")
	if err := os.WriteFile(p, []byte("# Just markdown\n\nno frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(p); err == nil {
		t.Error("expected error for unrecognized format")
	}
}
