package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

const gomodFixture = `module github.com/example/proj

go 1.22.3

toolchain go1.26.4

require github.com/spf13/cobra v1.10.2

require golang.org/x/mod v0.33.0 // indirect

replace github.com/spf13/cobra => ../forked-cobra

exclude github.com/bad/dep v0.1.0
`

func TestExtractGoMod(t *testing.T) {
	res, err := ExtractFile("go.mod", []byte(gomodFixture), model.LangGoMod)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}

	// File node + main module + 2 require deps + 1 exclude dep = 5 nodes.
	// (replace target ../forked-cobra reuses the existing cobra dep node.)
	var file, main *model.Node
	mods := 0
	for i := range res.Nodes {
		n := &res.Nodes[i]
		switch {
		case n.Kind == model.KindFile:
			file = n
		case n.Kind == model.KindModule && n.Name == "github.com/example/proj":
			main = n
		case n.Kind == model.KindModule:
			mods++
		}
	}
	if file == nil || file.Language != model.LangGoMod {
		t.Fatalf("missing/incorrect file node: %+v", file)
	}
	if main == nil {
		t.Fatal("missing main module node")
	}
	if want := "go 1.22.3; toolchain go1.26.4"; main.Signature != want {
		t.Errorf("main.Signature = %q, want %q", main.Signature, want)
	}
	if mods != 3 { // cobra, x/mod, bad/dep
		t.Errorf("dependency module nodes = %d, want 3", mods)
	}

	count := func(k model.EdgeKind) int {
		c := 0
		for _, e := range res.Edges {
			if e.Kind == k {
				c++
			}
		}
		return c
	}
	if count(model.EdgeRequires) != 2 {
		t.Errorf("EdgeRequires = %d, want 2", count(model.EdgeRequires))
	}
	if count(model.EdgeReplaces) != 1 {
		t.Errorf("EdgeReplaces = %d, want 1", count(model.EdgeReplaces))
	}
	if count(model.EdgeExcludes) != 1 {
		t.Errorf("EdgeExcludes = %d, want 1", count(model.EdgeExcludes))
	}
	if count(model.EdgeContains) < 1 {
		t.Errorf("expected a contains edge from file to module")
	}
}
