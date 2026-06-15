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

func TestParsePlanDirectoryStyleSlug(t *testing.T) {
	// Directory-style plans live at spec/plans/<slug>/README.md; the slug is the
	// containing directory name, not the file stem ("README").
	dir := t.TempDir()
	planDir := filepath.Join(dir, "spec", "plans", "my-cool-plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nformat: https://specscore.md/plan-specification\nstatus: Approved\n---\n# Plan: My Cool Plan\n\n**Status:** Approved\n**Source:** idea:something\n"
	if err := os.WriteFile(filepath.Join(planDir, "README.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Parse(filepath.Join(planDir, "README.md"))
	if err != nil {
		t.Fatalf("Parse directory-style plan: %v", err)
	}
	if d.Slug != "my-cool-plan" {
		t.Errorf("Slug = %q, want %q", d.Slug, "my-cool-plan")
	}
}

func TestParseContentDirectoryStylePlanSlug(t *testing.T) {
	// The extractor calls ParseContent with the project-relative logical path
	// and in-memory bytes; a directory-style plan must still yield the directory
	// slug, independent of the indexer's working directory.
	content := []byte("---\nformat: https://specscore.md/plan-specification\nstatus: Approved\n---\n# Plan: My Cool Plan\n\n**Status:** Approved\n**Source:** idea:something\n")
	d, err := ParseContent("spec/plans/my-cool-plan/README.md", content)
	if err != nil {
		t.Fatalf("ParseContent directory-style plan: %v", err)
	}
	if d.Kind != KindPlan {
		t.Errorf("Kind = %q, want %q", d.Kind, KindPlan)
	}
	if d.Slug != "my-cool-plan" {
		t.Errorf("Slug = %q, want %q", d.Slug, "my-cool-plan")
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
