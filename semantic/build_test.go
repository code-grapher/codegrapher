package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

const testModel = `entity "Invoice" {
  key = ["id"]
  property "id" { type = "uuid" }
  property "total" { type = "decimal" }
}
`
const testMeaning = `format: meaning/draft-1
id: demo
name: Demo
description: Demo concepts.
models:
  chinook: chinook.modelspec.hcl
concepts:
  - id: invoice-total
    kind: attribute
    labels: {en: Invoice total}
    synonyms: {en: [revenue]}
    description: The total of an invoice.
    bindings:
      - model: modelspec:///chinook.Invoice
        property: total
        role: value
`

func write(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
func findKind(g Graph, kind model.NodeKind) []model.Node {
	var out []model.Node
	for _, n := range g.Nodes {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}
func TestBuildBindingAndExplicitMapping(t *testing.T) {
	root := t.TempDir()
	write(t, root, "chinook.modelspec.hcl", testModel)
	write(t, root, "demo.meaning.yaml", testMeaning)
	write(t, root, "invoice.go", "package demo\n// modelspec: implements chinook.Invoice.total\nfunc Total() int { return 1 }\n")
	st, err := store.Initialize(filepath.Join(root, "code.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	}()
	code := model.Node{ID: model.GenerateNodeID("invoice.go", model.KindFunction, "Total", 3), Kind: model.KindFunction, Name: "Total", QualifiedName: "Total", FilePath: "invoice.go", Language: model.LangGo, StartLine: 3, EndLine: 3}
	if err := st.InsertNode(code); err != nil {
		t.Fatal(err)
	}
	g, err := Build(root, []string{"chinook.modelspec.hcl", "demo.meaning.yaml", "invoice.go"}, []*store.Store{st})
	if err != nil {
		t.Fatal(err)
	}
	concepts := findKind(g, model.KindMeaningConcept)
	if len(concepts) != 1 {
		t.Fatalf("concepts = %d", len(concepts))
	}
	if concepts[0].Metadata["labels"] == nil || concepts[0].Metadata["synonyms"] == nil {
		t.Fatal("semantic words missing")
	}
	if len(findKind(g, model.KindModelMember)) != 2 {
		t.Fatal("model members missing")
	}
	bridges := findKind(g, model.KindSemanticCode)
	if len(bridges) != 1 || bridges[0].Metadata["canonicalCodeId"] != code.ID {
		t.Fatalf("bridge = %+v", bridges)
	}
	var binding, mapping bool
	for _, e := range g.Edges {
		if e.Kind == model.EdgeBindsTo {
			binding = true
			if e.Metadata["role"] != "value" {
				t.Fatalf("binding role = %v", e.Metadata)
			}
		}
		if e.Kind == model.EdgeMapsToCode {
			mapping = true
			if e.Provenance != "explicit_annotation" {
				t.Fatalf("mapping provenance = %q", e.Provenance)
			}
		}
	}
	if !binding || !mapping {
		t.Fatalf("binding=%v mapping=%v nodes=%+v", binding, mapping, g.Nodes)
	}
}

func TestBuildWithoutAnnotationHasNoCodeMapping(t *testing.T) {
	root := t.TempDir()
	write(t, root, "chinook.modelspec.hcl", testModel)
	write(t, root, "demo.meaning.yaml", testMeaning)
	g, err := Build(root, []string{"chinook.modelspec.hcl", "demo.meaning.yaml"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range g.Edges {
		if e.Kind == model.EdgeMapsToCode {
			t.Fatal("inferred code mapping")
		}
	}
}

func TestSameNamedModulesKeepLocalReferences(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"left", "right"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, root, filepath.Join(dir, "chinook.modelspec.hcl"), `component "Amount" {
  field "value" { type = "decimal" }
}

entity "Invoice" {
  key = ["id"]
  property "id" { type = "uuid" }
  property "total" { component = "Amount" }
}
`)
		write(t, root, filepath.Join(dir, "demo.meaning.yaml"), testMeaning)
	}
	g, err := Build(root, []string{"left/chinook.modelspec.hcl", "left/demo.meaning.yaml", "right/chinook.modelspec.hcl", "right/demo.meaning.yaml"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(findKind(g, model.KindModelEntity)); got != 2 {
		t.Fatalf("entities=%d", got)
	}
	if got := len(findKind(g, model.KindMeaningConcept)); got != 2 {
		t.Fatalf("concepts=%d", got)
	}
	var binds, components int
	byID := map[string]model.Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	for _, e := range g.Edges {
		if e.Kind == model.EdgeBindsTo {
			binds++
			if filepath.Dir(byID[e.Source].FilePath) != filepath.Dir(byID[e.Target].FilePath) {
				t.Fatalf("crossed module scope: %+v", e)
			}
		}
		if e.Kind == model.EdgeReferences && e.Metadata["attribute"] == "component" {
			components++
			if filepath.Dir(byID[e.Source].FilePath) != filepath.Dir(byID[e.Target].FilePath) {
				t.Fatalf("crossed component scope: %+v", e)
			}
		}
	}
	if binds != 2 || components != 2 {
		t.Fatalf("binds=%d components=%d", binds, components)
	}
}

func TestBlockEndIgnoresCommentAndHeredocBraces(t *testing.T) {
	root := t.TempDir()
	write(t, root, "fixture.modelspec.hcl", `entity "Invoice" {
  /* } a comment */
  query = <<-SQL
  SELECT '{' AS brace, '}' AS other
  SQL
  property "id" { type = "uuid" }
}

entity "Other" {}
`)
	if got := blockEnd(filepath.Join(root, "fixture.modelspec.hcl"), 1); got != 7 {
		t.Fatalf("block end=%d, want 7", got)
	}
}

func TestPinnedReferenceStaysUnresolvedWithoutExactCheckout(t *testing.T) {
	root := t.TempDir()
	write(t, root, "demo.meaning.yaml", strings.Replace(testMeaning, "    description: The total of an invoice.", "    description: The total of an invoice.\n    extends: meaning://github.com/example/other/foo?ref=abc123", 1))
	g, err := Build(root, []string{"demo.meaning.yaml"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	concepts := findKind(g, model.KindMeaningConcept)
	if len(concepts) != 1 {
		t.Fatalf("concepts=%d", len(concepts))
	}
	refs, ok := concepts[0].Metadata["unresolvedReferences"].([]map[string]any)
	if !ok || len(refs) != 1 || refs[0]["revision"] != "abc123" {
		t.Fatalf("pinned reference lost: %+v", concepts[0].Metadata)
	}
	for _, e := range g.Edges {
		if e.Kind == model.EdgeExtends {
			t.Fatalf("unverified external edge: %+v", e)
		}
	}
}

func TestHCLJSONTwinOneSemanticObjectWithProvenance(t *testing.T) {
	root := t.TempDir()
	write(t, root, "chinook.modelspec.hcl", testModel)
	write(t, root, "chinook.modelspec.json", `{
  "modelspec": "1.0-draft",
  "module": {"id": "example/chinook", "name": "chinook", "version": "1.0.0"},
  "entities": {"Invoice": {
    "key": ["id"],
    "properties": {
      "id": {"type": "uuid"},
      "total": {"type": "decimal"}
    }
  }}
}`)
	g, err := Build(root, []string{"chinook.modelspec.hcl", "chinook.modelspec.json"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	entities := findKind(g, model.KindModelEntity)
	if len(entities) != 1 {
		t.Fatalf("twin duplicated entity: %d", len(entities))
	}
	reps, ok := entities[0].Metadata["representations"].([]map[string]any)
	if !ok || len(reps) != 2 {
		t.Fatalf("twin provenance missing: %+v", entities[0].Metadata)
	}
	if entities[0].FilePath != "chinook.modelspec.hcl" {
		t.Fatalf("source form = %s", entities[0].FilePath)
	}
}

func TestLayoutModuleSpansFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "modules", "billing", "models")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, root, "modules/billing/models/amount.modelspec.hcl", `component "Amount" {
  field "value" {
    type = "decimal"
  }
}`)
	write(t, root, "modules/billing/models/invoice.modelspec.hcl", `entity "Invoice" {
 key = ["id"]
 property "id" { type = "uuid" }
 property "total" { component = "Amount" }
}`)
	g, err := Build(root, []string{"modules/billing/models/amount.modelspec.hcl", "modules/billing/models/invoice.modelspec.hcl"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(findKind(g, model.KindModelModule)); got != 1 {
		t.Fatalf("module split across files: %d", got)
	}
	var linked bool
	for _, e := range g.Edges {
		if e.Kind == model.EdgeReferences && e.Metadata["attribute"] == "component" {
			linked = true
		}
	}
	if !linked {
		t.Fatalf("cross-file component relation absent: nodes=%+v edges=%+v", g.Nodes, g.Edges)
	}
}

func TestRepoAddressRejectsLocalAndMalformedRemotes(t *testing.T) {
	cases := map[string]string{
		"https://github.com/owner/repo.git":      "github.com/owner/repo",
		"git@github.com:owner/repo.git":          "github.com/owner/repo",
		"ssh://git@github.com/owner/repo.git":    "github.com/owner/repo",
		"file:///tmp/owner/repo":                 "",
		"/tmp/owner/repo":                        "",
		"https://github.com/extra/owner/repo":    "",
		"https://github.com/owner/../repo":       "",
		"https://github.com/owner/repo?ref=main": "",
		"git@github.com:owner/repo/extra":        "",
	}
	for input, want := range cases {
		if got := parseRepoAddress(input); got != want {
			t.Errorf("%q => %q, want %q", input, got, want)
		}
	}
}

func TestMultiFileTwinUsesOwningLayoutDirectory(t *testing.T) {
	root := t.TempDir()
	var files []string
	for _, side := range []string{"left", "right"} {
		dir := filepath.Join(side, "modules", "chinook", "models")
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, root, filepath.Join(dir, "invoice.modelspec.hcl"), testModel)
		write(t, root, filepath.Join(dir, "amount.modelspec.hcl"), `component "Amount" {
 field "value" { type = "decimal" }
}`)
		write(t, root, filepath.Join(dir, "chinook.modelspec.json"), `{
 "modelspec":"1.0-draft",
 "module":{"id":"example/chinook","name":"chinook","version":"1.0.0"},
 "entities":{"Invoice":{"key":["id"],"properties":{"id":{"type":"uuid"},"total":{"type":"decimal"}}}}
}`)
		files = append(files, filepath.Join(dir, "invoice.modelspec.hcl"), filepath.Join(dir, "amount.modelspec.hcl"), filepath.Join(dir, "chinook.modelspec.json"))
	}
	for run := 0; run < 5; run++ {
		g, err := Build(root, files, nil)
		if err != nil {
			t.Fatal(err)
		}
		entities := findKind(g, model.KindModelEntity)
		if len(entities) != 2 {
			t.Fatalf("run %d: entities=%d", run, len(entities))
		}
		if entities[0].ID == entities[1].ID {
			t.Fatal("same-name modules merged")
		}
		for _, n := range entities {
			reps, ok := n.Metadata["representations"].([]map[string]any)
			if !ok || len(reps) != 2 {
				t.Fatalf("run %d: representations=%+v", run, n.Metadata)
			}
			for _, r := range reps {
				if filepath.Dir(r["filePath"].(string)) != filepath.Dir(n.FilePath) {
					t.Fatalf("run %d: cross-scope twin %+v on %s", run, r, n.FilePath)
				}
			}
		}
	}
}

func TestBindingAliasUsesDeclaredModelSource(t *testing.T) {
	root := t.TempDir()
	write(t, root, "chinook.modelspec.hcl", testModel)
	meaningWithAlias := strings.ReplaceAll(testMeaning, "chinook: chinook.modelspec.hcl", "sales: chinook.modelspec.hcl")
	meaningWithAlias = strings.ReplaceAll(meaningWithAlias, "modelspec:///chinook.Invoice", "modelspec:///sales.Invoice")
	write(t, root, "demo.meaning.yaml", meaningWithAlias)
	g, err := Build(root, []string{"chinook.modelspec.hcl", "demo.meaning.yaml"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for _, e := range g.Edges {
		if e.Kind == model.EdgeBindsTo {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("aliased model binding count=%d", count)
	}
}

func TestInvalidBindingRoleDoesNotProduceAcceptedEdge(t *testing.T) {
	root := t.TempDir()
	write(t, root, "chinook.modelspec.hcl", testModel)
	write(t, root, "demo.meaning.yaml", strings.Replace(testMeaning, "role: value", "role: invalid", 1))
	g, err := Build(root, []string{"chinook.modelspec.hcl", "demo.meaning.yaml"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range g.Edges {
		if e.Kind == model.EdgeBindsTo {
			t.Fatalf("accepted invalid role: %+v", e)
		}
	}
	concepts := findKind(g, model.KindMeaningConcept)
	if len(concepts) != 1 || concepts[0].Metadata["diagnostics"] == nil {
		t.Fatal("invalid role diagnostic missing")
	}
}
