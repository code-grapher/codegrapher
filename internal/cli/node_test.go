package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
)

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:unrelated-nonfatal-candidates-do-not-block-read
func TestNodeCommandReturnsSourceWithUnrelatedNonfatalCandidates(t *testing.T) {
	root := t.TempDir()
	runGitForWatchTest(t, root, "init", "-q")
	runGitForWatchTest(t, root, "config", "user.email", "codegrapher-test@example.invalid")
	runGitForWatchTest(t, root, "config", "user.name", "CodeGrapher Test")
	mustWrite := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(root, "main.go"), "package main\nfunc Good() {}\n")
	mustWrite(filepath.Join(root, "generated"), "old file\n")
	mustWrite(filepath.Join(root, "cache.db"), "initial\n")
	mustWrite(filepath.Join(root, "spec", "features", "README.md"), "plain markdown\n")
	runGitForWatchTest(t, root, "add", ".")
	runGitForWatchTest(t, root, "commit", "-qm", "initial")
	headOutput, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headOutput))
	idx, _, err := indexer.Init(root, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "generated")); err != nil {
		t.Fatal(err)
	}
	mustWrite(filepath.Join(root, "generated", "child.go"), "package generated\nfunc Child() {}\n")
	mustWrite(filepath.Join(root, "cache.db"), strings.Repeat("x", indexer.MaxFileSize+1))
	mustWrite(filepath.Join(root, "spec", "features", "README.md"), "---\nformat: https://specscore.md/broken-specification\n---\n")

	cmd := newNodeCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"Good", "--source=inline", "--path", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("node command failed: %v\n%s", err, out.String())
	}
	if got := out.String(); !strings.Contains(got, "func Good()") {
		t.Fatalf("node output = %q", got)
	}

	idx, err = indexer.Open(root, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, graphStore := range idx.Stores() {
		if revision, getErr := graphStore.GetMetadata("indexed_git_head"); getErr != nil || revision != head {
			t.Fatalf("successful refresh revision = %q, %v; want %q", revision, getErr, head)
		}
		if stale, getErr := graphStore.GetFileByPath("generated"); getErr != nil || stale != nil {
			t.Fatalf("stale directory file record = %+v, %v", stale, getErr)
		}
	}
	children, err := idx.Store().GetNodesByName("Child")
	if err != nil || len(children) == 0 {
		t.Fatalf("directory descendant Child = %d, %v", len(children), err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	// A self-referential symlink is a deterministic stat failure on platforms
	// that permit symlink creation. It exercises the actual CLI refresh boundary
	// without a test-only indexer seam.
	if err := os.Symlink("fatal.go", filepath.Join(root, "fatal.go")); err != nil {
		t.Logf("symlink unavailable; fatal CLI tail is covered by indexer error tests: %v", err)
		return
	}
	fatalCmd := newNodeCmd()
	fatalCmd.SilenceUsage = true
	fatalCmd.SilenceErrors = true
	fatalCmd.SetArgs([]string{"Good", "--source=inline", "--path", root})
	if err := fatalCmd.Execute(); err == nil || !strings.Contains(err.Error(), "fatal.go") {
		t.Fatalf("fatal candidate error = %v", err)
	}
	idx, err = indexer.Open(root, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	for _, graphStore := range idx.Stores() {
		if revision, getErr := graphStore.GetMetadata("indexed_git_head"); getErr != nil || revision != "" {
			t.Fatalf("fatal refresh retained revision = %q, %v", revision, getErr)
		}
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:read-command-refuses-foreign-worktree-index
func TestNodeRefusesForeignWorktreeIndexAndInitCreatesLocalIndex(t *testing.T) {
	outer := t.TempDir()
	runGitForWatchTest(t, outer, "init", "-q")
	if err := os.WriteFile(filepath.Join(outer, "main.go"), []byte("package main\nfunc Outer() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, _, err := indexer.Init(outer, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(outer, "nested-worktree")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitForWatchTest(t, nested, "init", "-q")
	if err := os.WriteFile(filepath.Join(nested, "main.go"), []byte("package main\nfunc Nested() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	nodeCmd := newNodeCmd()
	nodeCmd.SilenceUsage = true
	nodeCmd.SilenceErrors = true
	nodeCmd.SetArgs([]string{"Nested", "--path", nested})
	if err := nodeCmd.Execute(); err == nil || !strings.Contains(err.Error(), "different git worktree's index") {
		t.Fatalf("foreign worktree node error = %v", err)
	}

	initCmd := newInitCmd()
	initCmd.SetArgs([]string{nested})
	if err := initCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !indexer.IsInitialized(nested) {
		t.Fatal("init did not create a worktree-local index")
	}
}

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
