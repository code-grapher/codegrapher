package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
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
		"at Acme.Service.Run() in /work/Service.cs:line 19",
		"at com.acme.Main.run(Main.java:7)",
		"   4: crate::run at src/main.rs:9:2",
	}, "\n")
	frames := parseStacktrace(input)
	if len(frames) != 6 {
		t.Fatalf("frames = %#v", frames)
	}
	for _, frame := range frames {
		if frame.file == "" || frame.line < 1 {
			t.Fatalf("unparsed frame = %#v", frame)
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
	if len(result.Frames) != 2 || result.Frames[0].Status != "matched" || result.Frames[0].Symbol.Name != "Warm" {
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
}
