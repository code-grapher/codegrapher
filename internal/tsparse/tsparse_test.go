package tsparse_test

import (
	"os"
	"testing"

	"github.com/specscore/codegrapher/internal/tsparse"
)

// collectByKind walks the tree and collects all nodes whose Kind() matches kind.
func collectByKind(root *tsparse.Node, kind string) []*tsparse.Node {
	var found []*tsparse.Node
	tsparse.Walk(root, func(n *tsparse.Node) {
		if n.Kind() == kind {
			found = append(found, n)
		}
	})
	return found
}

// nodesByKindAndName returns nodes whose Kind() == kind and whose "name" field
// child has the given text. Pass name=="" to collect all of that kind.
func nodesByKindAndName(root *tsparse.Node, kind, name string) []*tsparse.Node {
	var found []*tsparse.Node
	tsparse.Walk(root, func(n *tsparse.Node) {
		if n.Kind() != kind {
			return
		}
		if name == "" {
			found = append(found, n)
			return
		}
		if nameNode := n.ChildByFieldName("name"); nameNode != nil && nameNode.Text() == name {
			found = append(found, n)
		}
	})
	return found
}

func TestGoFixture(t *testing.T) {
	src, err := os.ReadFile("../../testdata/fixtures/go-small/internal/store/store.go")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	p, err := tsparse.NewParser(tsparse.LangGo)
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	tree, err := p.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	root := tree.RootNode()
	if root == nil {
		t.Fatal("root is nil")
	}

	// function_declaration nodes: New (line 24) and normalize (line 62)
	funcDecls := nodesByKindAndName(root, "function_declaration", "")
	if len(funcDecls) == 0 {
		t.Fatal("no function_declaration nodes found")
	}

	tests := []struct {
		kind    string
		name    string
		wantRow uint32 // 1-indexed
	}{
		{"function_declaration", "New", 24},
		{"function_declaration", "normalize", 62},
		{"method_declaration", "Get", 29},
		{"method_declaration", "Set", 40},
		{"method_declaration", "Len", 47},
		{"method_declaration", "Describe", 58},
	}

	for _, tc := range tests {
		t.Run(tc.kind+"/"+tc.name, func(t *testing.T) {
			nodes := nodesByKindAndName(root, tc.kind, tc.name)
			if len(nodes) != 1 {
				t.Errorf("want 1 %s named %q, got %d", tc.kind, tc.name, len(nodes))
				return
			}
			n := nodes[0]
			// StartPoint is 0-indexed; convert to 1-indexed for comparison
			gotRow := n.StartPoint().Row + 1
			if gotRow != tc.wantRow {
				t.Errorf("%s %q: want line %d, got %d", tc.kind, tc.name, tc.wantRow, gotRow)
			}
		})
	}
}

func TestTypeScriptFixture(t *testing.T) {
	src, err := os.ReadFile("../../testdata/fixtures/ts-small/src/store.ts")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	p, err := tsparse.NewParser(tsparse.LangTypeScript)
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	tree, err := p.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	root := tree.RootNode()
	if root == nil {
		t.Fatal("root is nil")
	}

	t.Run("class_declaration/Store", func(t *testing.T) {
		nodes := nodesByKindAndName(root, "class_declaration", "Store")
		if len(nodes) != 1 {
			t.Fatalf("want 1 class_declaration named Store, got %d", len(nodes))
		}
		gotRow := nodes[0].StartPoint().Row + 1
		const wantRow = 11
		if gotRow != wantRow {
			t.Errorf("Store class: want line %d, got %d", wantRow, gotRow)
		}
	})

	// "describe" is an arrow-function const: a variable_declarator named "describe"
	t.Run("variable_declarator/describe", func(t *testing.T) {
		nodes := nodesByKindAndName(root, "variable_declarator", "describe")
		if len(nodes) != 1 {
			t.Fatalf("want 1 variable_declarator named describe, got %d", len(nodes))
		}
		gotRow := nodes[0].StartPoint().Row + 1
		const wantRow = 34
		if gotRow != wantRow {
			t.Errorf("describe const: want line %d, got %d", wantRow, gotRow)
		}
	})
}

func TestNodeAPI(t *testing.T) {
	src := []byte("package main\nfunc hello() {}\n")
	p, err := tsparse.NewParser(tsparse.LangGo)
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	tree, err := p.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	root := tree.RootNode()

	if root.Kind() != "source_file" {
		t.Errorf("root kind: want source_file, got %q", root.Kind())
	}
	if root.ChildCount() == 0 {
		t.Error("root has no children")
	}

	// Find function_declaration and verify field access
	nodes := nodesByKindAndName(root, "function_declaration", "hello")
	if len(nodes) != 1 {
		t.Fatalf("want 1 function_declaration hello, got %d", len(nodes))
	}
	fn := nodes[0]
	nameNode := fn.ChildByFieldName("name")
	if nameNode == nil {
		t.Fatal("name field child is nil")
	}
	if nameNode.Text() != "hello" {
		t.Errorf("name text: want hello, got %q", nameNode.Text())
	}
	if fn.StartPoint().Row != 1 { // line 2 (0-indexed row 1)
		t.Errorf("func row: want 1, got %d", fn.StartPoint().Row)
	}
}

func TestNewParserUnknownLang(t *testing.T) {
	_, err := tsparse.NewParser(tsparse.Language(99))
	if err == nil {
		t.Error("want error for unknown language, got nil")
	}
}

func BenchmarkGoFixtureParse(b *testing.B) {
	src, err := os.ReadFile("../../testdata/fixtures/go-small/internal/store/store.go")
	if err != nil {
		b.Fatalf("read fixture: %v", err)
	}
	p, _ := tsparse.NewParser(tsparse.LangGo)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Parse(src); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTypeScriptFixtureParse(b *testing.B) {
	src, err := os.ReadFile("../../testdata/fixtures/ts-small/src/store.ts")
	if err != nil {
		b.Fatalf("read fixture: %v", err)
	}
	p, _ := tsparse.NewParser(tsparse.LangTypeScript)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Parse(src); err != nil {
			b.Fatal(err)
		}
	}
}
