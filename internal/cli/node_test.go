package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
)

func TestNodeSourceIsBoundedAndHashVerified(t *testing.T) {
	root, idx := initFixture(t)
	defer func() { _ = idx.Close() }()
	matches, err := findNodeMatches(idx.Stores(), "normalize")
	if err != nil || len(matches) != 1 {
		t.Fatalf("findNodeMatches(normalize) = %d, %v", len(matches), err)
	}
	got, rec, err := readIndexedNodeSource(root, matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if rec.ContentHash == "" || indexer.HashContent(got.all) != rec.ContentHash || !strings.HasPrefix(string(got.node), "func normalize(") {
		t.Fatalf("node source = %q, file hash = %q", got.node, rec.ContentHash)
	}
	if strings.Contains(string(got.node), "func (s *Store) Len(") {
		t.Fatal("node source included following symbol")
	}
}

func TestNodeFileHintNeverSelectsMismatchedDefinition(t *testing.T) {
	_, idx := initFixture(t)
	defer func() { _ = idx.Close() }()
	matches, err := findNodeMatches(idx.Stores(), "normalize")
	if err != nil || len(matches) != 1 {
		t.Fatalf("findNodeMatches(normalize) = %d, %v", len(matches), err)
	}
	if got := narrowNodeMatches(matches, "other.go", 0); len(got) != 0 {
		t.Fatalf("mismatched file hint selected %+v", got)
	}
}

func TestNodeIDSelectsOneDuplicateNameWithoutGuessing(t *testing.T) {
	_, idx := initFixture(t)
	defer func() { _ = idx.Close() }()
	duplicates, err := findNodeMatches(idx.Stores(), "Get")
	if err != nil || len(duplicates) < 2 {
		t.Fatalf("findNodeMatches(Get) = %d, %v", len(duplicates), err)
	}
	selected, err := findNodeMatches(idx.Stores(), duplicates[0].node.ID)
	if err != nil || len(selected) != 1 || selected[0].node.ID != duplicates[0].node.ID {
		t.Fatalf("ID lookup = %+v, %v", selected, err)
	}
	result, err := resolveNode(idx, nil, "Get", "", 0, true, false, 12, NodeFreshness{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != len(duplicates) || result.Source != "" {
		t.Fatalf("ambiguous Get result = %+v", result)
	}
}

func TestNodeCmdAmbiguityWritesCandidatesBeforeReturningNonZero(t *testing.T) {
	root, idx := initFixture(t)
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := newNodeCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"Get", "--path", root})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("ambiguous node command unexpectedly succeeded")
	}
	text := out.String()
	if !strings.Contains(text, "ambiguous symbol") || !strings.Contains(text, "codegrapher node \"<exact id>\" --source") || !strings.Contains(text, "method:") {
		t.Fatalf("ambiguity output = %q", text)
	}
}

func TestNodeCmdAmbiguityJSONHasNoZeroValuedSymbol(t *testing.T) {
	root, idx := initFixture(t)
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := newNodeCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"Get", "--format=json", "--path", root})
	if err := cmd.Execute(); err == nil {
		t.Fatal("ambiguous JSON command unexpectedly succeeded")
	}
	var results []NodeResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("JSON output: %v\n%s", err, out.String())
	}
	if len(results) != 1 || results[0].Status != "ambiguous" || results[0].Symbol != nil || len(results[0].Candidates) < 2 || !strings.Contains(results[0].Hint, "exact candidate id") {
		t.Fatalf("ambiguity JSON = %+v", results)
	}
}

func TestCodeFenceOutgrowsSourceBackticks(t *testing.T) {
	if got := codeFence("x\n````\ny"); got != "`````" {
		t.Fatalf("codeFence = %q, want five backticks", got)
	}
}

func TestNodeSourceFlagDefaultsToFooterAndRejectsFooterJSON(t *testing.T) {
	cmd := newNodeCmd()
	if got := cmd.Flags().Lookup("source").NoOptDefVal; got != "footer" {
		t.Fatalf("--source no-opt value = %q, want footer", got)
	}
	cmd.SetArgs([]string{"Foo", "--source=footer", "--format=json"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "text-only") {
		t.Fatalf("footer JSON error = %v", err)
	}
}

func TestBriefSearchResultsOmitVerboseNodeFields(t *testing.T) {
	got := briefSearchResults([]model.SearchResult{{Node: model.Node{
		ID: "function:one", Kind: model.KindFunction, Name: "One", QualifiedName: "pkg::One",
		FilePath: "one.go", Language: model.LangGo, StartLine: 4, EndLine: 6, Docstring: "verbose",
	}}})
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || strings.Contains(string(data), "verbose") || got[0].Language != model.LangGo || got[0].EndLine != 6 {
		t.Fatalf("briefSearchResults = %+v", got)
	}
}
