package extract

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot is two levels up from this package dir (internal/extract).
const repoRoot = "../.."

// readRepoFile reads a file relative to the repo root, failing the test if it
// cannot be read.
func readRepoFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return b
}

func TestDetectSpecScore(t *testing.T) {
	// A real SpecScore artifact under spec/ with the specscore frontmatter.
	artifactPath := "spec/ideas/specscore-artifact-extraction.md"
	if got := DetectSpecScore(artifactPath, readRepoFile(t, artifactPath)); !got {
		t.Errorf("DetectSpecScore(%q) = false, want true", artifactPath)
	}

	// A plain repo-root README is not a SpecScore artifact.
	if got := DetectSpecScore("README.md", readRepoFile(t, "README.md")); got {
		t.Errorf("DetectSpecScore(README.md) = true, want false")
	}

	// A .go file is never a SpecScore artifact (wrong extension).
	if got := DetectSpecScore("internal/extract/detect.go", []byte("package extract\n")); got {
		t.Errorf("DetectSpecScore(.go) = true, want false")
	}

	// A .md under spec/ but WITHOUT the specscore format frontmatter.
	plainUnderSpec := "---\ntitle: notes\n---\n\n# Notes\n"
	if got := DetectSpecScore("spec/features/notes.md", []byte(plainUnderSpec)); got {
		t.Errorf("DetectSpecScore(spec md without frontmatter) = true, want false")
	}

	// A SpecScore-frontmatter .md NOT under a spec/ tree is not classified.
	if got := DetectSpecScore("docs/notes.md", readRepoFile(t, artifactPath)); got {
		t.Errorf("DetectSpecScore(.md outside spec/) = true, want false")
	}
}
