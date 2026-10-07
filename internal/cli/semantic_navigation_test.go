package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
)

func TestSemanticNodeSourceFromNestedGitModelTwins(t *testing.T) {
	root := t.TempDir()
	runGitForWatchTest(t, root, "init", "-q")
	runGitForWatchTest(t, root, "config", "user.email", "codegrapher-test@example.invalid")
	runGitForWatchTest(t, root, "config", "user.name", "CodeGrapher Test")
	runGitForWatchTest(t, root, "remote", "add", "origin", "https://github.com/example/geo.git")
	modelDir := filepath.Join(root, "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(modelDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	meaning := "format: meaning/draft-1\nid: geo\nname: Geo\nmodels:\n  geo: geo.modelspec.hcl\nconcepts:\n  - id: geo-country\n    kind: entity\n    labels: {en: Geo country}\n    bindings:\n      - model: modelspec:///geo.Countries\n        property: iso\n        role: identifier\n"
	hcl := "entity \"Countries\" {\n  key = [\"iso\"]\n  property \"iso\" { type = \"string\" }\n}\n"
	jsonTwin := "{\n  \"modelspec\": \"1.0-draft\",\n  \"module\": {\"id\": \"github.com/example/geo/model/geo\", \"name\": \"geo\", \"version\": \"0.1.0\"},\n  \"entities\": {\"Countries\": {\"key\": [\"iso\"], \"properties\": {\"iso\": {\"type\": \"string\"}}}}\n}\n"
	write("geo.meaning.yaml", meaning)
	write("geo.modelspec.hcl", hcl)
	write("geo.modelspec.json", jsonTwin)
	runGitForWatchTest(t, root, "add", ".")
	runGitForWatchTest(t, root, "commit", "-qm", "initial")
	idx, result, err := indexer.Init(root, indexer.Options{})
	if err != nil || !result.Success {
		t.Fatalf("init: %v %+v", err, result)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	node := func(name string, refresh bool) NodeResult {
		t.Helper()
		args := []string{name, "--source=inline", "--relations", "--format=json", "--path", root}
		if refresh {
			args = append(args, "--refresh")
		}
		out, err := runCmd(t, newNodeCmd(), args...)
		if err != nil {
			t.Fatalf("node %s: %v\n%s", name, err, out)
		}
		var got []NodeResult
		if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 {
			t.Fatalf("node JSON %s: %v\n%s", name, err, out)
		}
		return got[0]
	}
	for _, name := range []string{"geo-country", "geo.Countries", "geo.Countries.iso"} {
		got := node(name, false)
		if got.Status != "ok" || !got.Freshness.Verified || got.Source == "" || got.SourceRange != "line-bounded" {
			t.Fatalf("initial %s source = %+v", name, got)
		}
	}
	concept := node("geo-country", false)
	if concept.Symbol.FilePath != "model/geo.meaning.yaml" || !strings.Contains(concept.Source, "Geo country") {
		t.Fatalf("nested meaning source = %+v", concept)
	}
	if context := runContextText(t, root, "geo-country"); !strings.Contains(context, "Geo country") {
		t.Fatalf("semantic context omitted verified source: %s", context)
	}
	entity := node("geo.Countries", false)
	if entity.Symbol.FilePath != "model/geo.modelspec.hcl" || !strings.Contains(entity.Source, `entity "Countries"`) {
		t.Fatalf("HCL source = %+v", entity)
	}
	if !strings.Contains(stringMustMarshal(t, entity.Symbol.Metadata["representations"]), "geo.modelspec.json") {
		t.Fatalf("JSON twin missing: %+v", entity.Symbol.Metadata)
	}

	// A same-size, same-mtime edit must withhold the old semantic range until
	// the projection and its file record are refreshed together.
	meaningPath := filepath.Join(modelDir, "geo.meaning.yaml")
	stat, err := os.Stat(meaningPath)
	if err != nil {
		t.Fatal(err)
	}
	write("geo.meaning.yaml", strings.Replace(meaning, "Geo country", "New country", 1))
	if err := os.Chtimes(meaningPath, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	stale := node("geo-country", false)
	if !stale.Freshness.Stale || stale.Freshness.Verified || stale.Source != "" {
		t.Fatalf("stale meaning source exposed: %+v", stale)
	}
	updated := node("geo-country", true)
	if !updated.Freshness.Refreshed || !updated.Freshness.Verified || !strings.Contains(updated.Source, "New country") {
		t.Fatalf("refreshed meaning source = %+v", updated)
	}

	// Removing a layout twin keeps HCL as the source and drops the JSON
	// representation after refresh.
	if err := os.Remove(filepath.Join(modelDir, "geo.modelspec.json")); err != nil {
		t.Fatal(err)
	}
	entity = node("geo.Countries", true)
	if !entity.Freshness.Verified || !strings.Contains(entity.Source, `entity "Countries"`) || strings.Contains(stringMustMarshal(t, entity.Symbol.Metadata["representations"]), "geo.modelspec.json") {
		t.Fatalf("removed twin source = %+v", entity)
	}
	if err := os.Remove(meaningPath); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, newNodeCmd(), "geo-country", "--source=inline", "--format=json", "--refresh", "--path", root)
	if err == nil {
		t.Fatal("removed meaning node unexpectedly resolved")
	}
	var removed []NodeResult
	if err := json.Unmarshal([]byte(out), &removed); err != nil || len(removed) != 1 || removed[0].Status != "not_found" || removed[0].Source != "" {
		t.Fatalf("removed meaning node = %+v, %v\n%s", removed, err, out)
	}
}

func stringMustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSemanticBridgeSourceRejectsNewerFileRecordAndConflictingScope(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "geo.modelspec.hcl"), []byte("entity \"Countries\" { property \"iso\" { type = \"string\" } }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	codePath := filepath.Join(root, "country.go")
	code := "package geo\n// modelspec: implements geo.Countries.iso\nfunc ISO() string { return \"IE\" }\n"
	if err := os.WriteFile(codePath, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	idx, result, err := indexer.Init(root, indexer.Options{})
	if err != nil || !result.Success {
		t.Fatalf("init: %v %+v", err, result)
	}
	defer func() { _ = idx.Close() }()
	var bridge model.Node
	var projection, sourceStoreFound bool
	var semanticStore, codeStoreIndex int
	stores := idx.Stores()
	for i, s := range stores {
		nodes, err := s.AllNodes()
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range nodes {
			if n.Kind == model.KindSemanticCode {
				bridge, semanticStore, projection = n, i, true
			}
		}
		if rec, err := s.GetFileByPath("country.go"); err != nil {
			t.Fatal(err)
		} else if rec != nil {
			codeStoreIndex, sourceStoreFound = i, true
		}
	}
	if !projection || !sourceStoreFound || bridge.Metadata["indexedSourceHash"] == nil {
		t.Fatalf("bridge/source record missing: %+v", bridge)
	}
	match := matchedNode{node: bridge, store: stores[semanticStore]}
	content, rec, err := readIndexedNodeSource(root, match, stores...)
	if err != nil || !indexedNodeSourceMatches(content.all, rec, bridge) {
		t.Fatalf("initial bridge source: %v %+v", err, bridge)
	}
	changed := strings.Replace(code, `"IE"`, `"US"`, 1)
	if err := os.WriteFile(codePath, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	newRecord := *rec
	newRecord.ContentHash = indexer.HashContent([]byte(changed))
	if err := stores[codeStoreIndex].UpsertFile(newRecord); err != nil {
		t.Fatal(err)
	}
	content, rec, err = readIndexedNodeSource(root, match, stores...)
	if err != nil || indexedNodeSourceMatches(content.all, rec, bridge) {
		t.Fatalf("old projection trusted newer file record: %v %+v", err, bridge)
	}
	if err := stores[semanticStore].UpsertFile(model.FileRecord{Path: "country.go", ContentHash: "conflicting", Language: model.LangGo}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readIndexedNodeSource(root, match, stores...); err == nil || !strings.Contains(err.Error(), "conflicting indexed file records") {
		t.Fatalf("conflicting file records accepted: %v", err)
	}
}

func TestSemanticCLIContractAndHCLSync(t *testing.T) {
	root := t.TempDir()
	modelPath := filepath.Join(root, "chinook.modelspec.hcl")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("chinook.modelspec.hcl", "entity \"Invoice\" {\n  key = [\"id\"]\n  property \"id\" { type = \"uuid\" }\n  property \"total\" { type = \"decimal\" }\n}\n")
	write("demo.meaning.yaml", "format: meaning/draft-1\nid: demo\nname: Demo\ndescription: Demo concepts.\nmodels:\n  chinook: chinook.modelspec.hcl\nconcepts:\n  - id: invoice-total\n    kind: attribute\n    labels: {en: Invoice total}\n    synonyms: {en: [revenue]}\n    description: The total of an invoice.\n    bindings:\n      - model: modelspec:///chinook.Invoice\n        property: total\n        role: value\n        match: labels\n        note: verified total\n")
	write("invoice.go", "package demo\n// modelspec: implements chinook.Invoice.total\nfunc Total() int { return 1 }\n")
	idx, result, err := indexer.Init(root, indexer.Options{})
	if err != nil || !result.Success {
		t.Fatalf("init: %v %+v", err, result)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	node := func(symbol string, relations bool) NodeResult {
		t.Helper()
		args := []string{symbol, "--path", root, "--format=json"}
		if relations {
			args = append(args, "--relations")
		}
		out, err := runCmd(t, newNodeCmd(), args...)
		if err != nil {
			t.Fatalf("node %q: %v\n%s", symbol, err, out)
		}
		var results []NodeResult
		if err := json.Unmarshal([]byte(out), &results); err != nil || len(results) != 1 {
			t.Fatalf("node JSON %q: %v\n%s", symbol, err, out)
		}
		return results[0]
	}
	search := func(query string, extra ...string) []model.SearchResult {
		t.Helper()
		args := append([]string{query, "--path", root, "--format=json"}, extra...)
		out, err := runCmd(t, newQueryCmd(), args...)
		if err != nil {
			t.Fatalf("query %q: %v\n%s", query, err, out)
		}
		var results []model.SearchResult
		if err := json.Unmarshal([]byte(out), &results); err != nil {
			t.Fatalf("query JSON: %v\n%s", err, out)
		}
		return results
	}

	concept := node("invoice-total", true)
	if concept.Symbol == nil || concept.Symbol.Metadata["labels"] == nil || concept.Symbol.Metadata["synonyms"] == nil {
		t.Fatalf("concept metadata: %+v", concept)
	}
	var memberID string
	for _, relation := range concept.Relations {
		if relation.Kind != model.EdgeBindsTo {
			continue
		}
		if relation.Metadata["role"] != "value" || relation.Metadata["match"] != "labels" || relation.Metadata["note"] != "verified total" {
			t.Fatalf("binding evidence: %+v", relation)
		}
		memberID = relation.Symbol.ID
	}
	if memberID == "" {
		t.Fatalf("accepted binding missing: %+v", concept.Relations)
	}
	var conceptFound bool
	for _, result := range search("revenue") {
		if result.Node.ID == concept.Symbol.ID && result.Node.Metadata["synonyms"] != nil {
			conceptFound = true
		}
	}
	if !conceptFound {
		t.Fatal("synonym search lost concept metadata")
	}
	briefJSON, err := runCmd(t, newQueryCmd(), "revenue", "--path", root, "--format=json", "--brief")
	if err != nil {
		t.Fatalf("brief synonym query: %v\n%s", err, briefJSON)
	}
	var brief []BriefSymbol
	if err := json.Unmarshal([]byte(briefJSON), &brief); err != nil {
		t.Fatal(err)
	}
	var briefConcept bool
	for _, symbol := range brief {
		if symbol.ID == concept.Symbol.ID && symbol.Metadata["synonyms"] != nil {
			briefConcept = true
		}
	}
	if !briefConcept {
		t.Fatalf("brief synonym query lost semantic metadata: %+v", brief)
	}

	totalResults := search("Total")
	var codeID string
	var codeCount int
	for _, result := range totalResults {
		if result.Node.Kind == model.KindSemanticCode {
			t.Fatalf("bridge counted as normal search hit: %+v", totalResults)
		}
		if result.Node.Kind == model.KindFunction && result.Node.Name == "Total" {
			codeID = result.Node.ID
			codeCount++
		}
	}
	if codeCount != 1 {
		t.Fatalf("canonical code hits = %d: %+v", codeCount, totalResults)
	}
	bridgeResults := search("Total", "--kind=semantic_code")
	if len(bridgeResults) != 1 || bridgeResults[0].Node.Kind != model.KindSemanticCode {
		t.Fatalf("explicit bridge query: %+v", bridgeResults)
	}
	bridgeID := bridgeResults[0].Node.ID
	if alias := node(bridgeID, false); alias.Symbol == nil || alias.Symbol.ID != codeID {
		t.Fatalf("bridge alias did not navigate to canonical code: %+v", alias)
	}

	member := node(memberID, true)
	var mapped bool
	for _, relation := range member.Relations {
		if relation.Kind == model.EdgeMapsToCode {
			mapped = relation.Symbol.ID == codeID && relation.Symbol.Kind == model.KindFunction && relation.ViaSemanticCodeID == bridgeID && relation.Provenance == "explicit_annotation" && relation.Metadata["annotation"] != nil
		}
	}
	if !mapped {
		t.Fatalf("forward mapping to canonical code: %+v", member.Relations)
	}
	code := node("Total", true)
	if code.Symbol == nil || code.Symbol.ID != codeID || code.Status != "ok" {
		t.Fatalf("duplicate bridge made code name ambiguous: %+v", code)
	}
	var reverse bool
	for _, relation := range code.Relations {
		if relation.Kind == model.EdgeMapsToCode && relation.Symbol.ID == memberID && relation.ViaSemanticCodeID == bridgeID && relation.Metadata["annotation"] != nil {
			reverse = true
		}
	}
	if !reverse {
		t.Fatalf("reverse mapping missing: %+v", code.Relations)
	}
	text, err := runCmd(t, newNodeCmd(), "invoice-total", "--path", root, "--relations")
	if err != nil || !strings.Contains(text, "Invoice total") || !strings.Contains(text, `"role":"value"`) || !strings.Contains(text, `"match":"labels"`) {
		t.Fatalf("text node evidence: %v\n%s", err, text)
	}

	if err := os.WriteFile(modelPath, []byte("entity \"Invoice\" { property \"id\" { type = \"uuid\" } }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(t, newSyncCmd(), root); err != nil {
		t.Fatalf("sync after HCL edit: %v\n%s", err, out)
	}
	concept = node("invoice-total", true)
	for _, relation := range concept.Relations {
		if relation.Kind == model.EdgeBindsTo {
			t.Fatalf("stale binding after HCL edit: %+v", concept.Relations)
		}
	}
	code = node("Total", true)
	for _, relation := range code.Relations {
		if relation.Kind == model.EdgeMapsToCode {
			t.Fatalf("stale reverse mapping after HCL edit: %+v", code.Relations)
		}
	}
}
