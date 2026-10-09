package semantic

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/modelspec-org/cli/pkg/modelspec"
	"github.com/specscore/codegrapher/model"
)

// The same small shop model in every spelling the ModelSpec library reads.
const (
	shopOldHCL = `component "Amount" {
  field "value" { type = "decimal" }
}

enum "Status" {
  values = ["open", "paid"]
}

entity "Customer" {
  key = ["id"]
  property "id" { type = "uuid" }
}

entity "Invoice" {
  key = ["id"]
  use = ["Amount"]
  property "id" { type = "uuid" }
  property "customer" { entity = "Customer" }
  property "total" { component = "Amount" }
  property "status" {
    type = "string"
    enum = "Status"
  }
}
`
	shopNewHCL = `component "Amount" {
  field "value" { type = "decimal" }
}

enum "Status" {
  values = ["open", "paid"]
}

record "Customer" {
  key = ["id"]
  field "id" { type = "uuid" }
}

record "Invoice" {
  key = ["id"]
  use = ["Amount"]
  field "id" { type = "uuid" }
  field "customer" { record = "Customer" }
  field "total" { component = "Amount" }
  field "status" {
    type = "string"
    enum = "Status"
  }
}
`
	// Old and new words in one file: allowed in HCL, still one deprecation notice.
	shopMixedHCL = `component "Amount" {
  field "value" { type = "decimal" }
}

enum "Status" {
  values = ["open", "paid"]
}

record "Customer" {
  key = ["id"]
  field "id" { type = "uuid" }
}

entity "Invoice" {
  key = ["id"]
  use = ["Amount"]
  field "id" { type = "uuid" }
  property "customer" { entity = "Customer" }
  field "total" { component = "Amount" }
  field "status" {
    type = "string"
    enum = "Status"
  }
}
`
	shopOldJSON = `{
  "modelspec": "1.0-draft",
  "module": {"id": "example/shop", "name": "shop", "version": "1.0.0"},
  "components": {"Amount": {"fields": {"value": {"type": "decimal"}}}},
  "enums": {"Status": {"values": ["open", "paid"]}},
  "entities": {
    "Customer": {"key": ["id"], "properties": {"id": {"type": "uuid"}}},
    "Invoice": {"key": ["id"], "use": ["Amount"], "properties": {
      "id": {"type": "uuid"},
      "customer": {"entity": "Customer"},
      "total": {"component": "Amount"},
      "status": {"type": "string", "enum": "Status"}
    }}
  }
}`
	shopNewJSON = `{
  "modelspec": "1.0-draft-2",
  "module": {"id": "example/shop", "name": "shop", "version": "1.0.0"},
  "components": {"Amount": {"fields": {"value": {"type": "decimal"}}}},
  "enums": {"Status": {"values": ["open", "paid"]}},
  "records": {
    "Customer": {"key": ["id"], "fields": {"id": {"type": "uuid"}}},
    "Invoice": {"key": ["id"], "use": ["Amount"], "fields": {
      "id": {"type": "uuid"},
      "customer": {"record": "Customer"},
      "total": {"component": "Amount"},
      "status": {"type": "string", "enum": "Status"}
    }}
  }
}`
)

func buildFiles(t *testing.T, files map[string]string) Graph {
	t.Helper()
	root := t.TempDir()
	var names []string
	for name, body := range files {
		write(t, root, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	g, err := Build(root, names, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

// graphWithoutDiagnostics is the nodes and edges of a graph with the
// diagnostics removed, so two spellings of one model can be compared.
func graphWithoutDiagnostics(g Graph) ([]model.Node, []model.Edge) {
	nodes := make([]model.Node, len(g.Nodes))
	for i, n := range g.Nodes {
		meta := map[string]any{}
		for k, v := range n.Metadata {
			if k != "diagnostics" {
				meta[k] = v
			}
		}
		n.Metadata = meta
		nodes[i] = n
	}
	return nodes, g.Edges
}

func diagnosticCodes(n model.Node) []string {
	var out []string
	ds, _ := n.Metadata["diagnostics"].([]map[string]any)
	for _, d := range ds {
		out = append(out, d["code"].(string))
	}
	return out
}

func moduleNode(t *testing.T, g Graph) model.Node {
	t.Helper()
	mods := findKind(g, model.KindModelModule)
	if len(mods) != 1 {
		t.Fatalf("modules = %d", len(mods))
	}
	return mods[0]
}

// A record type is written as model_entity and a reference keeps the entity
// attribute, whichever spelling the file uses; only the representation form
// differs between HCL and JSON.
func TestSpellingsExtractToTheSameNodesAndEdges(t *testing.T) {
	for _, tc := range []struct {
		name, file, body string
	}{
		{"old hcl", "shop.modelspec.hcl", shopOldHCL},
		{"new hcl", "shop.modelspec.hcl", shopNewHCL},
		{"mixed hcl", "shop.modelspec.hcl", shopMixedHCL},
		{"old json", "shop.modelspec.json", shopOldJSON},
		{"new json", "shop.modelspec.json", shopNewJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := buildFiles(t, map[string]string{tc.file: tc.body})
			for kind, want := range map[model.NodeKind]int{model.KindModelEntity: 2, model.KindModelComponent: 1, model.KindModelEnum: 1} {
				if got := len(findKind(g, kind)); got != want {
					t.Fatalf("%s nodes = %d, want %d", kind, got, want)
				}
			}
			for _, kind := range []model.NodeKind{"model_record", model.KindModelCollection, model.KindModelRecordset} {
				if got := len(findKind(g, kind)); got != 0 {
					t.Fatalf("%s nodes = %d", kind, got)
				}
			}
			var ref, used, enumRef int
			for _, n := range findKind(g, model.KindModelMember) {
				if n.Name == "customer" {
					if n.Metadata["entity"] != "Customer" || n.Metadata["record"] != nil {
						t.Fatalf("customer metadata = %+v", n.Metadata)
					}
					if n.Metadata["memberKind"] != "property" {
						t.Fatalf("memberKind = %v", n.Metadata["memberKind"])
					}
				}
				if n.Name == "value" && n.Metadata["memberKind"] != "field" {
					t.Fatalf("component memberKind = %v", n.Metadata["memberKind"])
				}
			}
			for _, e := range g.Edges {
				switch e.Metadata["attribute"] {
				case "entity":
					ref++
				case "record":
					t.Fatalf("edge attribute record: %+v", e)
				case "use":
					used++
				case "enum":
					enumRef++
				}
			}
			if ref != 1 || used != 1 || enumRef != 1 {
				t.Fatalf("entity edges = %d, use edges = %d, enum edges = %d", ref, used, enumRef)
			}
		})
	}
}

func TestEarlierSpellingExtractsLikeItsCurrentSpellingCopy(t *testing.T) {
	for _, tc := range []struct {
		name, file, old, current string
	}{
		{"hcl", "shop.modelspec.hcl", shopOldHCL, shopNewHCL},
		{"mixed hcl", "shop.modelspec.hcl", shopMixedHCL, shopNewHCL},
		{"json", "shop.modelspec.json", shopOldJSON, shopNewJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldNodes, oldEdges := graphWithoutDiagnostics(buildFiles(t, map[string]string{tc.file: tc.old}))
			newNodes, newEdges := graphWithoutDiagnostics(buildFiles(t, map[string]string{tc.file: tc.current}))
			if len(oldNodes) == 0 || len(oldEdges) == 0 {
				t.Fatal("empty graph")
			}
			if !reflect.DeepEqual(oldNodes, newNodes) {
				t.Fatalf("nodes differ:\nold=%+v\nnew=%+v", oldNodes, newNodes)
			}
			if !reflect.DeepEqual(oldEdges, newEdges) {
				t.Fatalf("edges differ:\nold=%+v\nnew=%+v", oldEdges, newEdges)
			}
		})
	}
}

func TestTwinInDifferentSpellingsIsOneModel(t *testing.T) {
	g := buildFiles(t, map[string]string{
		"shop.modelspec.hcl":  shopNewHCL,
		"shop.modelspec.json": shopOldJSON,
	})
	// The twin branch finds each concept by its written kind: a wrong kind there
	// leaves the concept with a single representation.
	for kind, want := range map[model.NodeKind]int{model.KindModelEntity: 2, model.KindModelComponent: 1, model.KindModelEnum: 1} {
		nodes := findKind(g, kind)
		if len(nodes) != want {
			t.Fatalf("%s nodes = %d, want %d", kind, len(nodes), want)
		}
		for _, n := range nodes {
			if reps, _ := n.Metadata["representations"].([]map[string]any); len(reps) != 2 {
				t.Fatalf("%s %s representations = %+v", kind, n.Name, n.Metadata["representations"])
			}
		}
	}
}

func TestEarlierSpellingIsReportedOncePerFileAndNeverFails(t *testing.T) {
	root := t.TempDir()
	dir := "modules/shop/models"
	write(t, root, "shop.modelspec.hcl", shopOldHCL)
	g, err := Build(root, []string{"shop.modelspec.hcl"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mod := moduleNode(t, g)
	if got := diagnosticCodes(mod); !reflect.DeepEqual(got, []string{modelspec.RuleDeprecated}) {
		t.Fatalf("diagnostics = %v", got)
	}
	notice := mod.Metadata["diagnostics"].([]map[string]any)[0]
	msg := notice["message"].(string)
	if !strings.Contains(msg, `modelspec rewrite --write "shop.modelspec.hcl"`) || strings.Contains(msg, root) {
		t.Fatalf("notice = %q", msg)
	}
	if want := strings.Count(shopOldHCL[:strings.Index(shopOldHCL, "entity \"Customer\"")], "\n") + 1; notice["line"] != want {
		t.Fatalf("notice line = %v, want %d", notice["line"], want)
	}
	if len(findKind(g, model.KindModelEntity)) != 2 {
		t.Fatal("a warning must not stop extraction")
	}

	// A module of two files holds one notice for each file that uses the old words.
	layout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(layout, filepath.FromSlash(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, layout, dir+"/customer.modelspec.hcl", "entity \"Customer\" {\n key = [\"id\"]\n property \"id\" { type = \"uuid\" }\n}\n")
	write(t, layout, dir+"/invoice.modelspec.hcl", "entity \"Invoice\" {\n key = [\"id\"]\n property \"id\" { type = \"uuid\" }\n}\n")
	write(t, layout, dir+"/order.modelspec.hcl", "record \"Order\" {\n key = [\"id\"]\n field \"id\" { type = \"uuid\" }\n}\n")
	g, err = Build(layout, []string{dir + "/customer.modelspec.hcl", dir + "/invoice.modelspec.hcl", dir + "/order.modelspec.hcl"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mod = moduleNode(t, g)
	if got := diagnosticCodes(mod); len(got) != 2 || got[0] != modelspec.RuleDeprecated || got[1] != modelspec.RuleDeprecated {
		t.Fatalf("diagnostics = %v", got)
	}
	var files []string
	for _, d := range mod.Metadata["diagnostics"].([]map[string]any) {
		msg := d["message"].(string)
		for _, f := range []string{"customer", "invoice"} {
			if strings.Contains(msg, dir+"/"+f+".modelspec.hcl") {
				files = append(files, f)
			}
		}
	}
	sort.Strings(files)
	if !reflect.DeepEqual(files, []string{"customer", "invoice"}) {
		t.Fatalf("notices name %v", files)
	}
	if len(findKind(g, model.KindModelEntity)) != 3 {
		t.Fatal("records of a mixed layout module are all written as entities")
	}
}

func TestEarlierJSONSpellingIsReportedOnTheModule(t *testing.T) {
	g := buildFiles(t, map[string]string{"shop.modelspec.json": shopOldJSON})
	mod := moduleNode(t, g)
	if got := diagnosticCodes(mod); !reflect.DeepEqual(got, []string{modelspec.RuleDeprecated}) {
		t.Fatalf("diagnostics = %v", got)
	}
	// The JSON notice sits on the line of the format identifier.
	if line := mod.Metadata["diagnostics"].([]map[string]any)[0]["line"]; line != 2 {
		t.Fatalf("notice line = %v", line)
	}
}

func TestCurrentSpellingIsNotReported(t *testing.T) {
	for file, body := range map[string]string{"shop.modelspec.hcl": shopNewHCL, "shop.modelspec.json": shopNewJSON} {
		g := buildFiles(t, map[string]string{file: body})
		if got := diagnosticCodes(moduleNode(t, g)); len(got) != 0 {
			t.Fatalf("%s: diagnostics = %v", file, got)
		}
	}
}

func TestRefusedModelsKeepTheLibraryFindingAndWriteNoConceptNode(t *testing.T) {
	for _, tc := range []struct {
		name, file, body, rule, word string
	}{
		{"collection", "m.modelspec.hcl", "collection \"Rows\" {\n source = \"Invoice\"\n}\n", modelspec.RuleRemoved, "collection"},
		{"recordset", "m.modelspec.hcl", "recordset \"Rows\" {\n}\n", modelspec.RuleRemoved, "recordset"},
		{"projection", "m.modelspec.hcl", "projection \"View\" {\n}\n", modelspec.RuleReservedWord, "projection"},
		{"migration", "m.modelspec.hcl", "migration \"Step\" {\n}\n", modelspec.RuleReservedWord, "migration"},
		{"index", "m.modelspec.hcl", "record \"Invoice\" {\n key = [\"id\"]\n field \"id\" { type = \"uuid\" }\n index \"ix\" {\n }\n}\n", modelspec.RuleReservedWord, "index"},
		{"old collection", "m.modelspec.hcl", "entity \"Invoice\" {\n key = [\"id\"]\n property \"id\" { type = \"uuid\" }\n}\ncollection \"Rows\" {\n}\n", modelspec.RuleRemoved, "collection"},
		{"json collections key", "m.modelspec.json", `{"modelspec":"1.0-draft-2","module":{"id":"example/m","name":"m","version":"1.0.0"},"collections":{"Rows":{}}}`, modelspec.RuleRemoved, "collections"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := buildFiles(t, map[string]string{tc.file: tc.body})
			for _, kind := range []model.NodeKind{model.KindModelCollection, model.KindModelRecordset} {
				if got := len(findKind(g, kind)); got != 0 {
					t.Fatalf("%s nodes = %d", kind, got)
				}
			}
			var found bool
			for _, d := range moduleNode(t, g).Metadata["diagnostics"].([]map[string]any) {
				if d["code"] == tc.rule && strings.Contains(d["message"].(string), tc.word) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no %s diagnostic naming %q: %+v", tc.rule, tc.word, moduleNode(t, g).Metadata)
			}
		})
	}
}

func TestMemberWithBothReferenceWordsIsADiagnosticNotAFailure(t *testing.T) {
	g := buildFiles(t, map[string]string{"m.modelspec.hcl": `record "Customer" {
  key = ["id"]
  field "id" { type = "uuid" }
}

record "Invoice" {
  key = ["id"]
  field "id" { type = "uuid" }
  field "customer" {
    entity = "Customer"
    record = "Customer"
  }
}
`})
	var found bool
	for _, d := range moduleNode(t, g).Metadata["diagnostics"].([]map[string]any) {
		if d["code"] == modelspec.RuleAttribute && strings.Contains(d["message"].(string), "both record and entity") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v", moduleNode(t, g).Metadata)
	}
}

func TestJSONIdentifierDecidesTheVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name, body, word string
	}{
		{"old identifier with records", `{"modelspec":"1.0-draft","module":{"id":"example/m","name":"m","version":"1.0.0"},"records":{"A":{"key":["id"],"properties":{"id":{"type":"uuid"}}}}}`, "records"},
		{"new identifier with entities", `{"modelspec":"1.0-draft-2","module":{"id":"example/m","name":"m","version":"1.0.0"},"entities":{"A":{"key":["id"],"fields":{"id":{"type":"uuid"}}}}}`, "entities"},
		{"old identifier with fields", `{"modelspec":"1.0-draft","module":{"id":"example/m","name":"m","version":"1.0.0"},"entities":{"A":{"key":["id"],"fields":{"id":{"type":"uuid"}}}}}`, "fields"},
		{"new identifier with properties", `{"modelspec":"1.0-draft-2","module":{"id":"example/m","name":"m","version":"1.0.0"},"records":{"A":{"key":["id"],"properties":{"id":{"type":"uuid"}}}}}`, "properties"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := buildFiles(t, map[string]string{"m.modelspec.json": tc.body})
			// Like every other error, the finding is retained on the module and
			// extraction goes on; nothing here fails a run.
			var found bool
			for _, d := range moduleNode(t, g).Metadata["diagnostics"].([]map[string]any) {
				if d["code"] == modelspec.RuleVersion && strings.Contains(d["message"].(string), `"`+tc.word+`"`) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no modelspec-version diagnostic names %q: %+v", tc.word, moduleNode(t, g).Metadata)
			}
		})
	}
}

// The mapping from the library's kind to the written kind is one table, so the
// later change to model_record is a single edit there.
func TestWrittenKindsComeFromOneTable(t *testing.T) {
	want := map[modelspec.Kind]model.NodeKind{
		modelspec.KindRecord:    model.KindModelEntity,
		modelspec.KindComponent: model.KindModelComponent,
		modelspec.KindEnum:      model.KindModelEnum,
	}
	if !reflect.DeepEqual(writtenModelKind, want) {
		t.Fatalf("writtenModelKind = %v", writtenModelKind)
	}
	if writtenMemberKind[modelspec.KindRecord] != "property" || writtenMemberKind[modelspec.KindComponent] != "field" {
		t.Fatalf("writtenMemberKind = %v", writtenMemberKind)
	}
}

func TestMemberAttrsWriteTheReferenceUnderItsEstablishedName(t *testing.T) {
	in := []modelspec.Attr{
		{Name: "record", Value: &modelspec.Node{Type: modelspec.NodeString, Str: "Customer"}},
		{Name: "required", Value: &modelspec.Node{Type: modelspec.NodeBool, Bool: true}},
	}
	got := memberAttrs(in)
	want := map[string]any{"entity": "Customer", "required": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("memberAttrs = %v", got)
	}
	if got := memberAttrs(nil); len(got) != 0 {
		t.Fatalf("memberAttrs(nil) = %v", got)
	}
}

// The notice is worded from the repository-relative path, so a path longer than
// the library's own cut still holds no checkout path.
func TestNoticeKeepsALongRelativePathWhole(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(strings.Repeat("a", 100), strings.Repeat("b", 100), strings.Repeat("c", 100))
	if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.ToSlash(filepath.Join(dir, "shop.modelspec.hcl"))
	if len(file) <= 255 {
		t.Fatalf("path too short for the test: %d", len(file))
	}
	write(t, root, filepath.FromSlash(file), shopOldHCL)
	g, err := Build(root, []string{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	msg := moduleNode(t, g).Metadata["diagnostics"].([]map[string]any)[0]["message"].(string)
	if !strings.Contains(msg, `--write "`+file+`" rewrites`) || strings.Contains(msg, root) {
		t.Fatalf("notice = %q", msg)
	}
}

// A finding is kept on the module whichever file of the module it is about: a
// removed construct in one file of a two-file module, or in the JSON twin.
func TestFindingsOfAnyFileOfAModuleAreKept(t *testing.T) {
	has := func(t *testing.T, g Graph, rule, word string) {
		t.Helper()
		for _, d := range moduleNode(t, g).Metadata["diagnostics"].([]map[string]any) {
			if d["code"] == rule && strings.Contains(d["message"].(string), word) {
				return
			}
		}
		t.Fatalf("no %s diagnostic naming %q: %+v", rule, word, moduleNode(t, g).Metadata)
	}
	t.Run("two-file module", func(t *testing.T) {
		dir := "modules/sales/models"
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, root, filepath.FromSlash(dir+"/a.modelspec.hcl"), "record \"A\" {\n key = [\"id\"]\n field \"id\" { type = \"uuid\" }\n}\ncollection \"Rows\" {\n}\n")
		write(t, root, filepath.FromSlash(dir+"/b.modelspec.hcl"), "record \"B\" {\n key = [\"id\"]\n field \"id\" { type = \"uuid\" }\n}\n")
		// b.modelspec.hcl is the last file, so it is the one the module node names.
		g, err := Build(root, []string{dir + "/a.modelspec.hcl", dir + "/b.modelspec.hcl"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		has(t, g, modelspec.RuleRemoved, "collection")
		if got := len(findKind(g, model.KindModelEntity)); got != 2 {
			t.Fatalf("entities = %d", got)
		}
	})
	t.Run("json twin", func(t *testing.T) {
		g := buildFiles(t, map[string]string{
			"shop.modelspec.hcl":  shopNewHCL,
			"shop.modelspec.json": strings.Replace(shopNewJSON, `"records"`, `"collections": {"Rows": {}}, "records"`, 1),
		})
		has(t, g, modelspec.RuleRemoved, "collections")
	})
}

// A concept kind the table does not know is written as model_<kind>, as before
// the table, with a diagnostic, never as an empty kind.
func TestUnmappedConceptKindFallsBackWithADiagnostic(t *testing.T) {
	saved := writtenModelKind[modelspec.KindEnum]
	delete(writtenModelKind, modelspec.KindEnum)
	defer func() { writtenModelKind[modelspec.KindEnum] = saved }()
	g := buildFiles(t, map[string]string{"shop.modelspec.hcl": shopNewHCL})
	enums := findKind(g, "model_enum")
	if len(enums) != 1 || enums[0].ID == "" || !strings.HasPrefix(enums[0].ID, "model_enum:") {
		t.Fatalf("enum nodes = %+v", enums)
	}
	if got := diagnosticCodes(enums[0]); !reflect.DeepEqual(got, []string{"unmapped-kind"}) {
		t.Fatalf("diagnostics = %v", got)
	}
	for _, n := range g.Nodes {
		if n.Kind == "" {
			t.Fatalf("node with an empty kind: %+v", n)
		}
	}
	if memberKindOf("other") != "field" || memberKindOf(modelspec.KindRecord) != "property" {
		t.Fatal("memberKindOf")
	}
}

// bind belonged to the removed collection and recordset; the library rejects it
// on a member, and no relation is made from it.
func TestBindAttributeOfAMemberMakesNoRelation(t *testing.T) {
	g := buildFiles(t, map[string]string{"m.modelspec.hcl": `record "A" {
  key = ["id"]
  field "id" {
    type = "uuid"
    bind = "A.id"
  }
}
`})
	for _, e := range g.Edges {
		if e.Metadata["attribute"] == "bind" {
			t.Fatalf("bind edge: %+v", e)
		}
	}
	var found bool
	for _, d := range moduleNode(t, g).Metadata["diagnostics"].([]map[string]any) {
		if d["code"] == modelspec.RuleAttribute && strings.Contains(d["message"].(string), "bind") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v", moduleNode(t, g).Metadata)
	}
}

// A MeaningGraph binding to a model resolves the same in either spelling.
func TestMeaningBindingSurvivesTheCurrentSpelling(t *testing.T) {
	const newModel = `record "Invoice" {
  key = ["id"]
  field "id" { type = "uuid" }
  field "total" { type = "decimal" }
}
`
	edges := func(g Graph) (binds []model.Edge, invalid int) {
		for _, e := range g.Edges {
			if e.Kind == model.EdgeBindsTo {
				binds = append(binds, e)
			}
		}
		for _, c := range findKind(g, model.KindMeaningConcept) {
			for _, code := range diagnosticCodes(c) {
				if code == "invalid-binding" {
					invalid++
				}
			}
		}
		return binds, invalid
	}
	files := []string{"chinook.modelspec.hcl", "demo.meaning.yaml"}
	oldG := buildFiles(t, map[string]string{files[0]: testModel, files[1]: testMeaning})
	newG := buildFiles(t, map[string]string{files[0]: newModel, files[1]: testMeaning})
	oldBinds, oldInvalid := edges(oldG)
	newBinds, newInvalid := edges(newG)
	if len(oldBinds) != 1 || oldInvalid != 0 || newInvalid != 0 {
		t.Fatalf("earlier: binds=%d invalid=%d; current: invalid=%d", len(oldBinds), oldInvalid, newInvalid)
	}
	if !reflect.DeepEqual(oldBinds, newBinds) {
		t.Fatalf("binds_to differ:\nearlier=%+v\ncurrent=%+v", oldBinds, newBinds)
	}

	// The earlier-spelling warning is on the module only; the graph holds none.
	if got := diagnosticCodes(moduleNode(t, oldG)); !reflect.DeepEqual(got, []string{modelspec.RuleDeprecated}) {
		t.Fatalf("module diagnostics = %v", got)
	}
	for _, g := range []Graph{oldG, newG} {
		for _, n := range findKind(g, model.KindMeaningGraph) {
			if got := diagnosticCodes(n); len(got) != 0 {
				t.Fatalf("graph diagnostics = %v", got)
			}
		}
	}
	if got := diagnosticCodes(moduleNode(t, newG)); len(got) != 0 {
		t.Fatalf("current spelling module diagnostics = %v", got)
	}
}

// A model file the meaning graph lists but that is not indexed here has no
// module to hold its warning, so the graph keeps it once, with a relative path.
func TestMeaningGraphKeepsTheWarningOfAModelThatIsNotIndexed(t *testing.T) {
	root := t.TempDir()
	write(t, root, "chinook.modelspec.hcl", testModel)
	write(t, root, "demo.meaning.yaml", testMeaning)
	g, err := Build(root, []string{"demo.meaning.yaml"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	graphs := findKind(g, model.KindMeaningGraph)
	if len(graphs) != 1 {
		t.Fatalf("graphs = %d", len(graphs))
	}
	if got := diagnosticCodes(graphs[0]); !reflect.DeepEqual(got, []string{modelspec.RuleDeprecated}) {
		t.Fatalf("graph diagnostics = %v", got)
	}
	msg := graphs[0].Metadata["diagnostics"].([]map[string]any)[0]["message"].(string)
	if !strings.Contains(msg, `"chinook.modelspec.hcl"`) || strings.Contains(msg, root) {
		t.Fatalf("notice = %q", msg)
	}
}
