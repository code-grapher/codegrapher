package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/model"
)

func TestFindCallPathUsesShortestDeterministicCallEdges(t *testing.T) {
	_, idx := initFixture(t)
	defer func() { _ = idx.Close() }()
	result, err := findCallPath(idx, nil, "Warm", "Set", 2, false, NodeFreshness{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || len(result.Steps) != 2 {
		t.Fatalf("path Warm -> Set = %+v", result)
	}
	if result.Steps[0].Symbol.Name != "Warm" || result.Steps[1].Symbol.Name != "Set" || result.Steps[1].Edge == nil || result.Steps[1].Edge.Kind != "calls" {
		t.Fatalf("path steps = %+v", result.Steps)
	}
	limited, err := findCallPath(idx, nil, "Warm", "Set", 0, false, NodeFreshness{})
	if err != nil || limited.Status != "not_found" || !limited.Truncated {
		t.Fatalf("max-hop path = %+v, %v", limited, err)
	}
}

func TestFindCallPathRejectsAmbiguousEndpoint(t *testing.T) {
	_, idx := initFixture(t)
	defer func() { _ = idx.Close() }()
	result, err := findCallPath(idx, nil, "Get", "Set", 3, false, NodeFreshness{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ambiguous" || len(result.StartCandidates) < 2 {
		t.Fatalf("ambiguous path = %+v", result)
	}
}

func TestFindCallPathBoundsAndDoesNotCallCycleOnlyAtHopLimitTruncated(t *testing.T) {
	_, idx := initFixture(t)
	defer func() { _ = idx.Close() }()
	sets, err := findNodeMatches(idx.Stores(), "Set")
	if err != nil || len(sets) != 1 {
		t.Fatalf("Set = %+v, %v", sets, err)
	}
	warms, err := findNodeMatches(idx.Stores(), "Warm")
	if err != nil || len(warms) != 1 {
		t.Fatalf("Warm = %+v, %v", warms, err)
	}
	original, err := sets[0].store.GetOutgoingEdges(sets[0].node.ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sets[0].store.DeleteEdgesBySource(sets[0].node.ID); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sets[0].store.InsertEdges(original) }()
	if err := sets[0].store.InsertEdge(model.Edge{Source: sets[0].node.ID, Target: warms[0].node.ID, Kind: model.EdgeCalls}); err != nil {
		t.Fatal(err)
	}
	result, err := findCallPathWithLimits(idx, nil, "Warm", "Len", 1, pathLimits{maxNodes: 10, maxEdges: 10}, false, NodeFreshness{})
	if err != nil || result.Status != "not_found" || result.Truncated {
		t.Fatalf("cycle-only hop limit = %+v, %v", result, err)
	}
	limited, err := findCallPathWithLimits(idx, nil, "Warm", "Set", 3, pathLimits{maxNodes: 1, maxEdges: 10}, false, NodeFreshness{})
	if err != nil || limited.Status != "not_found" || !limited.Truncated {
		t.Fatalf("node bound = %+v, %v", limited, err)
	}
}

func TestPathFooterUsesRawGoFenceAndJSONRejectsFooter(t *testing.T) {
	root, idx := initFixture(t)
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := newPathCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"Warm", "Set", "--source", "--path", root})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "```go\nfunc (c *Cache) Warm") {
		t.Fatalf("footer source = %s", out.String())
	}
	bad := newPathCmd()
	bad.SetArgs([]string{"Warm", "Set", "--source=footer", "--format=json"})
	if err := bad.Execute(); err == nil || !strings.Contains(err.Error(), "text-only") {
		t.Fatalf("footer JSON error = %v", err)
	}
}

func TestParseStacktraceCommonFormats(t *testing.T) {
	input := strings.Join([]string{
		"example.com/acme/pkg.(*Service).Run(...)\n\t/work/pkg/service.go:42 +0x12",
		"    at render (/work/web/render.ts:11:4)",
		"  File \"/work/app.py\", line 8, in run",
		"at Acme.Service.Run() in C:\\work\\Service.cs:line 19",
		"at com.acme.Main.run(Main.java:7)",
		"   4: crate::run\n             at src/main.rs:9:2",
		"at native",
	}, "\n")
	frames := parseStacktrace(input)
	if len(frames) != 7 {
		t.Fatalf("frames = %#v", frames)
	}
	want := []struct {
		function, file string
		line           int
	}{
		{"example.com/acme/pkg.(*Service).Run", "/work/pkg/service.go", 42},
		{"render", "/work/web/render.ts", 11},
		{"run", "/work/app.py", 8},
		{"Acme.Service.Run", "C:/work/Service.cs", 19},
		{"com.acme.Main.run", "Main.java", 7},
		{"crate::run", "src/main.rs", 9},
		{"native", "", 0},
	}
	for i, expected := range want {
		if got := frames[i]; got.function != expected.function || got.file != expected.file || got.line != expected.line {
			t.Fatalf("frame %d = %#v, want %#v", i, got, expected)
		}
	}
}

func TestMapStacktraceUsesSmallestEnclosingCallableAndDedupesFooterSource(t *testing.T) {
	root, idx := initFixture(t)
	defer func() { _ = idx.Close() }()
	trace := "example.com/go-small/internal/store.(*Cache).Warm(...)\n\t" + filepath.Join(root, "internal/store/cache.go") + ":24 +0x1\n" +
		"example.com/go-small/internal/store.(*Cache).Warm(...)\n\t" + filepath.Join(root, "internal/store/cache.go") + ":24 +0x1"
	result, err := mapStacktrace(idx, nil, trace, true, NodeFreshness{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Frames) != 2 || result.Frames[0].Status != "exact" || result.Frames[0].Symbol.Name != "Warm" {
		t.Fatalf("mapped trace = %+v", result)
	}
	var out bytes.Buffer
	if err := printStacktraceFooter(&out, result); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "func (c *Cache) Warm") != 1 || !strings.Contains(out.String(), "```go") {
		t.Fatalf("footer = %s", out.String())
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"frames\"") {
		t.Fatalf("JSON result = %s", data)
	}
}

func TestMapStacktraceNeverSilentlyAcceptsWrongNameOrOutOfRangeLocation(t *testing.T) {
	root, idx := initFixture(t)
	defer func() { _ = idx.Close() }()
	path := filepath.Join(root, "internal/store/cache.go")
	wrong := "example.com/go-small/internal/store.(*Store).Set(...)\n\t" + path + ":24 +0x1"
	result, err := mapStacktrace(idx, nil, wrong, false, NodeFreshness{})
	if err != nil || len(result.Frames) != 1 || result.Frames[0].Status != "mismatch" {
		t.Fatalf("wrong-name mapping = %+v, %v", result, err)
	}
	stale := "example.com/go-small/internal/store.(*Cache).Warm(...)\n\t" + path + ":2 +0x1"
	result, err = mapStacktrace(idx, nil, stale, true, NodeFreshness{})
	if err != nil || len(result.Frames) != 1 || result.Frames[0].Status != "stale" || result.Frames[0].Source != "" {
		t.Fatalf("out-of-range mapping = %+v, %v", result, err)
	}
	noMatch := "\t" + path + ":1 +0x1"
	result, err = mapStacktrace(idx, nil, noMatch, false, NodeFreshness{})
	if err != nil || len(result.Frames) != 1 || result.Frames[0].Status != "unmatched" {
		t.Fatalf("zero range mapping = %+v, %v", result, err)
	}
	noFrames, err := mapStacktrace(idx, nil, "fatal error without stack\n", false, NodeFreshness{})
	if err != nil || noFrames.Status != "no_frames" || len(noFrames.Frames) != 0 {
		t.Fatalf("no frames = %+v, %v", noFrames, err)
	}
}

func TestStacktraceWindowsPathAndBoundedInputAndFrames(t *testing.T) {
	root, idx := initFixture(t)
	defer func() { _ = idx.Close() }()
	windowsPath := strings.ReplaceAll(filepath.Join(root, "internal/store/cache.go"), "/", "\\")
	trace := "at Acme.Cache.Warm() in " + windowsPath + ":line 24"
	result, err := mapStacktrace(idx, nil, trace, false, NodeFreshness{})
	if err != nil || len(result.Frames) != 1 || result.Frames[0].Status != "exact" {
		t.Fatalf("windows trace = %+v, %v", result, err)
	}
	if _, err := stacktraceInputLimited(strings.NewReader("12345"), nil, 4); err == nil || !strings.Contains(err.Error(), "max-bytes") {
		t.Fatalf("byte cap error = %v", err)
	}
	three := strings.Repeat(trace+"\n", 3)
	result, err = mapStacktraceWithLimit(idx, nil, three, 2, false, NodeFreshness{})
	if err != nil || len(result.Frames) != 2 || !result.Truncated {
		t.Fatalf("frame cap = %+v, %v", result, err)
	}
	resolver := &stackResolver{files: []string{"a/cache.go", "b/cache.go"}}
	if got := resolver.pathMatches("cache.go"); len(got) != 2 {
		t.Fatalf("suffix ambiguity = %v", got)
	}
	var text bytes.Buffer
	if err := printStacktraceMarkdown(&text, StackTraceResult{Status: "ok", Frames: []StackTraceFrame{{Index: 0, Raw: "at Wrong (cache.go:24)", Function: "Wrong", FilePath: "cache.go", Line: 24, Status: "ambiguous", Hint: "ambiguous", Candidates: []BriefSymbol{{ID: "function:x", FilePath: "cache.go", StartLine: 24}}}}}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "Raw:") || !strings.Contains(text.String(), "Candidate:") || !strings.Contains(text.String(), "Wrong") {
		t.Fatalf("ambiguous text = %s", text.String())
	}
}

func TestPathSourceNeverMixesChangedFileWithOldPath(t *testing.T) {
	root, idx := initFixture(t)
	defer func() { _ = idx.Close() }()
	file := filepath.Join(root, "internal/store/cache.go")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(data, []byte("\n// changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = findCallPath(idx, nil, "Warm", "Set", 2, true, NodeFreshness{})
	if err == nil || !strings.Contains(err.Error(), "changed during retrieval") {
		t.Fatalf("stale path source error = %v", err)
	}
}

func TestStacktraceCmdReadsInjectedStdinAndSupportsInlineJSON(t *testing.T) {
	root, idx := initFixture(t)
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	trace := "example.com/go-small/internal/store.(*Cache).Warm(...)\n\t" + filepath.Join(root, "internal/store/cache.go") + ":24 +0x1\n"
	cmd := newStacktraceCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetIn(strings.NewReader(trace))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--path", root, "--source=inline", "--format=json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result StackTraceResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stacktrace JSON: %v\n%s", err, out.String())
	}
	if len(result.Frames) != 1 || result.Frames[0].Symbol == nil || !strings.Contains(result.Frames[0].Source, "func (c *Cache) Warm") {
		t.Fatalf("stacktrace result = %+v", result)
	}
	bad := newStacktraceCmd()
	bad.SetArgs([]string{"--source=footer", "--format=json"})
	if err := bad.Execute(); err == nil || !strings.Contains(err.Error(), "text-only") {
		t.Fatalf("footer JSON error = %v", err)
	}
	revision := newStacktraceCmd()
	revision.SilenceUsage, revision.SilenceErrors = true, true
	revision.SetIn(strings.NewReader(trace))
	revision.SetArgs([]string{"--path", root, "--revision", "expected-sha"})
	if err := revision.Execute(); err == nil || !strings.Contains(err.Error(), "requires indexed revision") {
		t.Fatalf("revision assertion error = %v", err)
	}
}
