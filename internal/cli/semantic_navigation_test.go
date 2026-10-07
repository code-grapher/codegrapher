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
