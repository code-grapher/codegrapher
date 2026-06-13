package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

const pkgjsonFixture = `{
  "name": "widget",
  "version": "1.2.3",
  "engines": { "node": ">=18" },
  "dependencies": { "left-pad": "^1.3.0" },
  "devDependencies": { "vitest": "^1.0.0", "left-pad": "^1.3.0" },
  "peerDependencies": { "react": ">=18" },
  "optionalDependencies": { "fsevents": "^2.3.0" }
}`

func TestExtractPackageJSON(t *testing.T) {
	res, err := ExtractFile("package.json", []byte(pkgjsonFixture), model.LangPackageJSON)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}

	var file, main *model.Node
	mods := 0
	for i := range res.Nodes {
		n := &res.Nodes[i]
		switch {
		case n.Kind == model.KindFile:
			file = n
		case n.Kind == model.KindModule && n.Name == "widget":
			main = n
		case n.Kind == model.KindModule:
			mods++
		}
	}
	if file == nil || file.Language != model.LangPackageJSON {
		t.Fatalf("missing/incorrect file node: %+v", file)
	}
	if main == nil {
		t.Fatal("missing main module node")
	}
	if main.Language != model.LangPackageJSON {
		t.Errorf("main module Language = %q, want package.json", main.Language)
	}
	if want := "version 1.2.3; engines: node>=18"; main.Signature != want {
		t.Errorf("main.Signature = %q, want %q", main.Signature, want)
	}
	if mods != 4 { // left-pad, vitest, react, fsevents (left-pad deduped)
		t.Errorf("dependency module nodes = %d, want 4", mods)
	}

	requires := 0
	cats := map[string]int{}
	for _, e := range res.Edges {
		if e.Kind == model.EdgeRequires {
			requires++
			if c, ok := e.Metadata["category"].(string); ok {
				cats[c]++
			}
		}
	}
	if requires != 5 { // prod:left-pad, dev:vitest+left-pad, peer:react, optional:fsevents
		t.Errorf("EdgeRequires = %d, want 5", requires)
	}
	if cats["prod"] != 1 || cats["dev"] != 2 || cats["peer"] != 1 || cats["optional"] != 1 {
		t.Errorf("category tally = %v, want prod:1 dev:2 peer:1 optional:1", cats)
	}

	containsFromFile := 0
	for _, e := range res.Edges {
		if e.Kind == model.EdgeContains && e.Source == model.FileNodeID("package.json") {
			containsFromFile++
		}
	}
	if containsFromFile != 1 {
		t.Errorf("contains edges from file = %d, want 1", containsFromFile)
	}
}
