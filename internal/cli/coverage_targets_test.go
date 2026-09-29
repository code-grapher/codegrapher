package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/coverage"
	"github.com/specscore/codegrapher/indexer"
)

func TestCoverageTargetsRankAndJSON(t *testing.T) {
	root := newContextFixture(t)
	idx, err := indexer.Open(root, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	profile := "mode: set\n" +
		"example.com/fx/widget.go:21.1,21.24 2 0\n" +
		"example.com/fx/widget.go:22.1,22.25 1 1\n" +
		"example.com/fx/widget.go:28.1,28.25 1 0\n"
	for _, s := range idx.Stores() {
		if _, err := coverage.NewIngestor().Ingest(context.Background(), s, strings.NewReader(profile), coverage.Options{Root: root, Ref: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := newCoverageTargetsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--root", root, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result coverageTargetsResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	targets := result.Targets
	if len(targets) == 0 || targets[0].QualifiedName != "Rename" || targets[0].StatementsTotal != 3 || targets[0].StatementsCovered != 1 || targets[0].Freshness != "current" || targets[0].Ref != "fixture" {
		t.Fatalf("targets %+v", targets)
	}
	path := filepath.Join(root, "widget.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("\n// changed after coverage\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	cmd = newCoverageTargetsCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--root", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	result = coverageTargetsResult{}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.StaleFiles) != 1 || result.StaleFiles[0] != "widget.go" {
		t.Fatalf("stale files %+v", result)
	}
	for _, target := range result.Targets {
		if target.FilePath == "widget.go" && target.Freshness == "current" {
			t.Fatalf("dirty source shown current: %+v", target)
		}
	}
}
