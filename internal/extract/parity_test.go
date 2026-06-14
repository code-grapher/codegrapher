package extract_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/specscore/codegrapher/internal/extract"
	"github.com/specscore/codegrapher/model"
)

const repoRoot = "../.."

// goldenNode mirrors the SQLite JSON output for comparison.
type goldenNode struct {
	ID             string  `json:"id"`
	Kind           string  `json:"kind"`
	Name           string  `json:"name"`
	QualifiedName  string  `json:"qualified_name"`
	FilePath       string  `json:"file_path"`
	Language       string  `json:"language"`
	StartLine      int     `json:"start_line"`
	EndLine        int     `json:"end_line"`
	StartColumn    int     `json:"start_column"`
	EndColumn      int     `json:"end_column"`
	Docstring      *string `json:"docstring"`
	Signature      *string `json:"signature"`
	Visibility     *string `json:"visibility"`
	IsExported     int     `json:"is_exported"`
	IsAsync        int     `json:"is_async"`
	IsStatic       int     `json:"is_static"`
	IsAbstract     int     `json:"is_abstract"`
	Decorators     *string `json:"decorators"`
	TypeParameters *string `json:"type_parameters"`
	ReturnType     *string `json:"return_type"`
}

type goldenEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

// TestParityGoSmall runs our extractor over all files in testdata/fixtures/go-small
// and compares node IDs, kinds, names, and lines against the golden.
func TestParityGoSmall(t *testing.T) {
	testParity(t, "go-small")
}

// TestParityTsSmall runs our extractor over all files in testdata/fixtures/ts-small
// and compares against the golden.
func TestParityTsSmall(t *testing.T) {
	testParity(t, "ts-small")
}

// TestParityPySmall runs our extractor over all files in testdata/fixtures/py-small
// and compares against the golden.
func TestParityPySmall(t *testing.T) {
	testParity(t, "py-small")
}

// TestParityCsSmall runs our extractor over all files in testdata/fixtures/cs-small
// and compares against the golden.
func TestParityCsSmall(t *testing.T) {
	testParity(t, "cs-small")
}

// TestParityJavaSmall runs our extractor over all files in testdata/fixtures/java-small
// and compares against the golden.
func TestParityJavaSmall(t *testing.T) {
	testParity(t, "java-small")
}

// TestParityKtSmall runs our extractor over all files in testdata/fixtures/kt-small
// and compares against the golden.
func TestParityKtSmall(t *testing.T) {
	testParity(t, "kt-small")
}

// TestParityRbSmall runs our extractor over all files in testdata/fixtures/rb-small
// and compares against the golden.
func TestParityRbSmall(t *testing.T) {
	testParity(t, "rb-small")
}

// TestParityRsSmall runs our extractor over all files in testdata/fixtures/rs-small
// and compares against the golden.
func TestParityRsSmall(t *testing.T) {
	testParity(t, "rs-small")
}

// TestParityPhpSmall runs our extractor over all files in testdata/fixtures/php-small
// and compares against the golden.
func TestParityPhpSmall(t *testing.T) {
	testParity(t, "php-small")
}

// TestParityCSmall runs our extractor over all files in testdata/fixtures/c-small
// and compares against the golden.
func TestParityCSmall(t *testing.T) {
	testParity(t, "c-small")
}

// TestParityScalaSmall runs our extractor over all files in testdata/fixtures/scala-small
// and compares against the golden.
func TestParityScalaSmall(t *testing.T) {
	testParity(t, "scala-small")
}

// TestParitySwiftSmall runs our extractor over all files in testdata/fixtures/swift-small
// and compares against the golden.
func TestParitySwiftSmall(t *testing.T) {
	testParity(t, "swift-small")
}

// TestParityCppSmall runs our extractor over all files in testdata/fixtures/cpp-small
// and compares against the golden.
func TestParityCppSmall(t *testing.T) {
	testParity(t, "cpp-small")
}

// TestParityDartSmall runs our extractor over all files in testdata/fixtures/dart-small
// and compares against the golden.
func TestParityDartSmall(t *testing.T) {
	testParity(t, "dart-small")
}

// TestParityLuaSmall runs our extractor over all files in testdata/fixtures/lua-small
// and compares against the golden.
func TestParityLuaSmall(t *testing.T) {
	testParity(t, "lua-small")
}

// TestParityElixirSmall runs our extractor over all files in testdata/fixtures/elixir-small
// and compares against the golden.
func TestParityElixirSmall(t *testing.T) {
	testParity(t, "elixir-small")
}

// TestParityHaskellSmall runs our extractor over all files in testdata/fixtures/haskell-small
// and compares against the golden.
func TestParityHaskellSmall(t *testing.T) {
	testParity(t, "haskell-small")
}

// TestParityObjcSmall runs our extractor over all files in testdata/fixtures/objc-small
// and compares against the golden.
func TestParityObjcSmall(t *testing.T) {
	testParity(t, "objc-small")
}

// TestParityPerlSmall runs our extractor over all files in testdata/fixtures/perl-small
// and compares against the golden.
func TestParityPerlSmall(t *testing.T) {
	testParity(t, "perl-small")
}

// TestParityErlangSmall runs our extractor over all files in testdata/fixtures/erlang-small
// and compares against the golden.
func TestParityErlangSmall(t *testing.T) {
	testParity(t, "erlang-small")
}

// TestParityJuliaSmall runs our extractor over all files in testdata/fixtures/julia-small
// and compares against the golden.
func TestParityJuliaSmall(t *testing.T) {
	testParity(t, "julia-small")
}

// TestParityFsharpSmall runs our extractor over all files in testdata/fixtures/fsharp-small
// and compares against the golden.
func TestParityFsharpSmall(t *testing.T) {
	testParity(t, "fsharp-small")
}

// TestParityRSmall runs our extractor over all files in testdata/fixtures/r-small
// and compares against the golden.
func TestParityRSmall(t *testing.T) {
	testParity(t, "r-small")
}

// TestParityBashSmall runs our extractor over all files in testdata/fixtures/bash-small
// and compares against the golden.
func TestParityBashSmall(t *testing.T) {
	testParity(t, "bash-small")
}

// TestParityPowershellSmall runs our extractor over all files in
// testdata/fixtures/powershell-small and compares against the golden.
func TestParityPowershellSmall(t *testing.T) {
	testParity(t, "powershell-small")
}

func testParity(t *testing.T, fixture string) {
	t.Helper()

	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", fixture)
	goldenDir := filepath.Join(repoRoot, "testdata", "golden", fixture)

	// Load golden nodes
	nodesFile := filepath.Join(goldenDir, "extraction-nodes.json")
	nodesData, err := os.ReadFile(nodesFile)
	if err != nil {
		t.Fatalf("read golden nodes: %v", err)
	}
	var goldenNodes []goldenNode
	if err := json.Unmarshal(nodesData, &goldenNodes); err != nil {
		t.Fatalf("parse golden nodes: %v", err)
	}

	// Load golden contains edges
	containsFile := filepath.Join(goldenDir, "extraction-contains.json")
	containsData, err := os.ReadFile(containsFile)
	if err != nil {
		t.Fatalf("read golden contains: %v", err)
	}
	var goldenContains []goldenEdge
	if err := json.Unmarshal(containsData, &goldenContains); err != nil {
		t.Fatalf("parse golden contains: %v", err)
	}

	// Collect all source files in fixture
	var sourceFiles []string
	err = filepath.Walk(fixtureDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		lang := extract.DetectLanguage(path)
		if lang != model.LangUnknown {
			sourceFiles = append(sourceFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixture: %v", err)
	}

	// Extract all files
	var allNodes []model.Node
	var allEdges []model.Edge

	for _, absPath := range sourceFiles {
		// Compute repo-relative path (the format used in goldens)
		relPath, err := filepath.Rel(fixtureDir, absPath)
		if err != nil {
			t.Fatalf("rel path: %v", err)
		}
		relPath = filepath.ToSlash(relPath)

		content, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatalf("read %s: %v", absPath, err)
		}

		lang := extract.DetectLanguageContent(absPath, content)
		result, err := extract.ExtractFile(relPath, content, lang)
		if err != nil {
			t.Fatalf("extract %s: %v", relPath, err)
		}
		allNodes = append(allNodes, result.Nodes...)
		allEdges = append(allEdges, result.Edges...)
		for _, e := range result.Errors {
			t.Logf("extraction error in %s: %s", relPath, e.Message)
		}
	}

	// Build maps for lookup
	gotByID := make(map[string]model.Node, len(allNodes))
	for _, n := range allNodes {
		gotByID[n.ID] = n
	}

	goldenByID := make(map[string]goldenNode, len(goldenNodes))
	for _, n := range goldenNodes {
		goldenByID[n.ID] = n
	}

	// --- Node parity check ---
	t.Run("node_count", func(t *testing.T) {
		if len(allNodes) != len(goldenNodes) {
			// Print differences for debugging
			for _, g := range goldenNodes {
				if _, ok := gotByID[g.ID]; !ok {
					t.Logf("MISSING node: %s %s %s L%d", g.Kind, g.Name, g.FilePath, g.StartLine)
				}
			}
			for _, n := range allNodes {
				if _, ok := goldenByID[n.ID]; !ok {
					t.Logf("EXTRA node: %s %s %s L%d", n.Kind, n.Name, n.FilePath, n.StartLine)
				}
			}
			t.Errorf("got %d nodes, want %d", len(allNodes), len(goldenNodes))
		}
	})

	t.Run("node_ids", func(t *testing.T) {
		for _, g := range goldenNodes {
			got, ok := gotByID[g.ID]
			if !ok {
				t.Errorf("missing node ID %s (kind=%s name=%s file=%s L%d)",
					g.ID, g.Kind, g.Name, g.FilePath, g.StartLine)
				continue
			}
			// Check key fields
			if string(got.Kind) != g.Kind {
				t.Errorf("node %s: kind got=%s want=%s", g.ID, got.Kind, g.Kind)
			}
			if got.Name != g.Name {
				t.Errorf("node %s: name got=%q want=%q", g.ID, got.Name, g.Name)
			}
			if got.StartLine != g.StartLine {
				t.Errorf("node %s (%s %s): startLine got=%d want=%d", g.ID, g.Kind, g.Name, got.StartLine, g.StartLine)
			}
			if got.EndLine != g.EndLine {
				t.Errorf("node %s (%s %s): endLine got=%d want=%d", g.ID, g.Kind, g.Name, got.EndLine, g.EndLine)
			}
			if got.QualifiedName != g.QualifiedName {
				t.Errorf("node %s (%s %s): qualifiedName got=%q want=%q", g.ID, g.Kind, g.Name, got.QualifiedName, g.QualifiedName)
			}
			if got.FilePath != g.FilePath {
				t.Errorf("node %s: filePath got=%q want=%q", g.ID, got.FilePath, g.FilePath)
			}
			// isExported
			gotExported := 0
			if got.IsExported {
				gotExported = 1
			}
			if gotExported != g.IsExported {
				t.Errorf("node %s (%s %s): isExported got=%d want=%d", g.ID, g.Kind, g.Name, gotExported, g.IsExported)
			}
			// signature
			if g.Signature != nil && (got.Signature != *g.Signature) {
				t.Errorf("node %s (%s %s): signature got=%q want=%q", g.ID, g.Kind, g.Name, got.Signature, *g.Signature)
			}
			if g.Signature == nil && got.Signature != "" {
				t.Errorf("node %s (%s %s): signature got=%q want=null", g.ID, g.Kind, g.Name, got.Signature)
			}
			// docstring
			if g.Docstring != nil && got.Docstring != *g.Docstring {
				t.Errorf("node %s (%s %s): docstring got=%q want=%q", g.ID, g.Kind, g.Name, got.Docstring, *g.Docstring)
			}
			if g.Docstring == nil && got.Docstring != "" {
				t.Errorf("node %s (%s %s): docstring got=%q want=null", g.ID, g.Kind, g.Name, got.Docstring)
			}
		}
	})

	t.Run("extra_nodes", func(t *testing.T) {
		for _, n := range allNodes {
			if _, ok := goldenByID[n.ID]; !ok {
				t.Errorf("unexpected node ID %s (kind=%s name=%s file=%s L%d)",
					n.ID, n.Kind, n.Name, n.FilePath, n.StartLine)
			}
		}
	})

	// --- Contains edge parity check ---
	t.Run("contains_edges", func(t *testing.T) {
		gotContains := make(map[string]bool)
		for _, e := range allEdges {
			if e.Kind == model.EdgeContains {
				key := e.Source + "->" + e.Target
				gotContains[key] = true
			}
		}

		for _, g := range goldenContains {
			key := g.Source + "->" + g.Target
			if !gotContains[key] {
				t.Errorf("missing contains edge: %s → %s", g.Source, g.Target)
			}
		}

		// Check for extra contains edges
		goldenContainsSet := make(map[string]bool)
		for _, g := range goldenContains {
			goldenContainsSet[g.Source+"->"+g.Target] = true
		}
		for _, e := range allEdges {
			if e.Kind != model.EdgeContains {
				continue
			}
			key := e.Source + "->" + e.Target
			if !goldenContainsSet[key] {
				t.Errorf("extra contains edge: %s → %s", e.Source, e.Target)
			}
		}
	})
}

// TestExtractFileDetectLanguage tests language detection.
func TestExtractFileDetectLanguage(t *testing.T) {
	cases := []struct {
		path string
		want model.Language
	}{
		{"foo.go", model.LangGo},
		{"foo.ts", model.LangTypeScript},
		{"foo.tsx", model.LangTSX},
		{"foo.js", model.LangJavaScript},
		{"foo.jsx", model.LangJSX},
		{"foo.py", model.LangPython},
		{"foo.cs", model.LangCSharp},
		{"Foo.java", model.LangJava},
		{"Foo.kt", model.LangKotlin},
		{"build.gradle.kts", model.LangKotlin},
		{"foo.rb", model.LangRuby},
		{"foo.R", model.LangR}, // R sources: .R and .r (lowercased by DetectLanguage)
		{"foo.r", model.LangR},
		{"foo.c", model.LangC},
		{"foo.h", model.LangC},     // path-only: .h defaults to C for back-compat
		{"foo.hpp", model.LangCPP}, // C++ header extensions
		{"foo.cpp", model.LangCPP},
		{"foo.cc", model.LangCPP},
		{"foo.cxx", model.LangCPP},
		{"foo.hh", model.LangCPP},
		{"foo.hxx", model.LangCPP},
		{"README.md", model.LangUnknown},
	}
	for _, c := range cases {
		got := extract.DetectLanguage(c.path)
		if got != c.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestDetectLanguageContent verifies content-aware disambiguation of .h headers.
func TestDetectLanguageContent(t *testing.T) {
	cases := []struct {
		path    string
		content string
		want    model.Language
	}{
		{"foo.c", "int main(void){return 0;}", model.LangC},
		{"foo.h", "struct S { int x; };\nvoid f(struct S *s);", model.LangC},
		{"foo.h", "class Widget {\npublic:\n  int x;\n};", model.LangCPP},
		{"foo.h", "namespace ns { int x; }", model.LangCPP},
		{"foo.h", "template <typename T> T id(T v);", model.LangCPP},
		{"foo.h", "int n = std::max(1, 2);", model.LangCPP},
		{"foo.hpp", "int x;", model.LangCPP}, // extension wins, no sniff needed
		{"foo.go", "package main", model.LangGo},
	}
	for _, c := range cases {
		got := extract.DetectLanguageContent(c.path, []byte(c.content))
		if got != c.want {
			t.Errorf("DetectLanguageContent(%q, %q) = %q, want %q", c.path, c.content, got, c.want)
		}
	}
}

// TestIsGeneratedFile tests generated file detection.
func TestIsGeneratedFile(t *testing.T) {
	generated := []string{
		"api/user.pb.go",
		"api/user_grpc.pb.go",
		"mock_service.go", // ^mock_[^/]+\.go$ requires no directory prefix
		"service_mock.go",
		"schema.generated.ts",
		"types.gen.js",
	}
	notGenerated := []string{
		"cmd/main.go",
		"internal/store/store.go",
		"src/app.ts",
		"src/cache.ts",
	}
	for _, p := range generated {
		if !extract.IsGeneratedFile(p) {
			t.Errorf("IsGeneratedFile(%q) = false, want true", p)
		}
	}
	for _, p := range notGenerated {
		if extract.IsGeneratedFile(p) {
			t.Errorf("IsGeneratedFile(%q) = true, want false", p)
		}
	}
}

// TestExtractFileNode tests that ExtractFile always emits a file node.
func TestExtractFileNode(t *testing.T) {
	content := []byte("package main\n\nfunc main() {}\n")
	result, err := extract.ExtractFile("cmd/main.go", content, model.LangGo)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) == 0 {
		t.Fatal("expected at least 1 node (file node)")
	}
	fileNode := result.Nodes[0]
	if fileNode.Kind != model.KindFile {
		t.Errorf("first node kind = %q, want %q", fileNode.Kind, model.KindFile)
	}
	if fileNode.ID != "file:cmd/main.go" {
		t.Errorf("file node ID = %q, want file:cmd/main.go", fileNode.ID)
	}
	if fileNode.Name != "main.go" {
		t.Errorf("file node name = %q, want main.go", fileNode.Name)
	}
	if fileNode.QualifiedName != "cmd/main.go" {
		t.Errorf("file node qualifiedName = %q, want cmd/main.go", fileNode.QualifiedName)
	}
}

// Suppress "imported and not used" errors.
var (
	_ = fmt.Sprintf
	_ = strings.TrimSpace
	_ = time.Now
)
