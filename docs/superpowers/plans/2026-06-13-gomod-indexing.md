# go.mod Indexing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Parse `go.mod` through one robust `golang.org/x/mod/modfile`-based package, and index `go.mod` as traversable graph content (module + dependency nodes with require/replace/exclude edges).

**Architecture:** A new `gomod` package wraps `modfile` and becomes the single parser for the three existing hand-rolled readers (Part A). A new `LangGoMod` language, a `go.mod` extractor, and three new edge kinds turn each `go.mod` into a `KindFile` row + a main `KindModule` node + one `KindModule` node per dependency, all folded into the governing `go-vN` scope store (Part B).

**Tech Stack:** Go 1.26, `golang.org/x/mod/modfile` (already transitive in `go.sum`, promoted to direct), `modernc.org/sqlite`, existing `model`/`extract`/`scope`/`indexer` packages.

**Spec:** `docs/superpowers/specs/2026-06-13-gomod-indexing-design.md`

**Owner relaxations (2026-06-13):** Goldens and backward-compat are NOT constraints. Goldens are regenerated freely via `tools/parity/rebaseline-golden.sh`. Version bucketing may prefer `toolchain` over the `go` directive (was previously pinned to `go`).

---

## File Structure

- **Create** `gomod/gomod.go` — the `modfile` wrapper and result structs. One responsibility: turn `go.mod` bytes into a typed `File`.
- **Create** `gomod/gomod_test.go` — parser table tests.
- **Create** `internal/extract/walk_gomod.go` — `extractGoMod` builds nodes/edges from a parsed `go.mod`.
- **Create** `internal/extract/walk_gomod_test.go` — extractor tests.
- **Modify** `resolve/resolve.go` — `loadGoModulePath` delegates to `gomod`.
- **Modify** `scope/scope.go` — `detectGoVersion` delegates to `gomod`, prefers `toolchain`.
- **Modify** `mcp/queryutils.go` — module-token read delegates to `gomod`.
- **Modify** `model/model.go` — add `LangGoMod`, `EdgeRequires`, `EdgeReplaces`, `EdgeExcludes`.
- **Modify** `internal/extract/detect.go` — `DetectLanguage` maps basename `go.mod`.
- **Modify** `internal/extract/extract.go` — dispatch `LangGoMod` in the walk switch.
- **Modify** `indexer/init.go` — `scopeStoreForFile` folds `LangGoMod` into the `LangGo` scope.

---

## Task 1: `gomod` parser package

**Files:**
- Create: `gomod/gomod.go`
- Test: `gomod/gomod_test.go`
- Modify: `go.mod` (promote `golang.org/x/mod` to a direct require)

- [ ] **Step 1: Write the failing test**

Create `gomod/gomod_test.go`:

```go
package gomod

import "testing"

const sample = `module github.com/example/proj

go 1.22.3

toolchain go1.26.4

require (
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.9.0 // indirect
)

require golang.org/x/mod v0.33.0

replace github.com/spf13/cobra => ../forked-cobra

replace github.com/old/dep v1.0.0 => github.com/new/dep v2.0.0

exclude github.com/bad/dep v0.1.0

retract (
	v1.0.0
	[v1.1.0, v1.2.0]
)
`

func TestParse(t *testing.T) {
	f, err := Parse("go.mod", []byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Module != "github.com/example/proj" {
		t.Errorf("Module = %q", f.Module)
	}
	if f.Go != "1.22.3" {
		t.Errorf("Go = %q", f.Go)
	}
	if f.Toolchain != "go1.26.4" {
		t.Errorf("Toolchain = %q", f.Toolchain)
	}
	if len(f.Requires) != 3 {
		t.Fatalf("Requires = %d, want 3", len(f.Requires))
	}
	if !f.Requires[1].Indirect {
		t.Errorf("testify should be indirect")
	}
	if f.Requires[0].Path != "github.com/spf13/cobra" || f.Requires[0].Version != "v1.10.2" {
		t.Errorf("Requires[0] = %+v", f.Requires[0])
	}
	if len(f.Replaces) != 2 {
		t.Fatalf("Replaces = %d, want 2", len(f.Replaces))
	}
	if f.Replaces[0].NewPath != "../forked-cobra" || f.Replaces[0].NewVersion != "" {
		t.Errorf("local replace = %+v", f.Replaces[0])
	}
	if f.Replaces[1].NewPath != "github.com/new/dep" || f.Replaces[1].NewVersion != "v2.0.0" {
		t.Errorf("module replace = %+v", f.Replaces[1])
	}
	if len(f.Excludes) != 1 || f.Excludes[0].Path != "github.com/bad/dep" {
		t.Errorf("Excludes = %+v", f.Excludes)
	}
	if len(f.Retracts) != 2 {
		t.Fatalf("Retracts = %d, want 2", len(f.Retracts))
	}
	if f.Retracts[0].Low != "v1.0.0" || f.Retracts[0].High != "v1.0.0" {
		t.Errorf("single retract = %+v", f.Retracts[0])
	}
	if f.Retracts[1].Low != "v1.1.0" || f.Retracts[1].High != "v1.2.0" {
		t.Errorf("range retract = %+v", f.Retracts[1])
	}
}

func TestParseMalformed(t *testing.T) {
	if _, err := Parse("go.mod", []byte("this is not a go.mod {{{")); err == nil {
		t.Fatal("expected error for malformed go.mod")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./gomod/`
Expected: FAIL — package `gomod` does not exist / `Parse` undefined.

- [ ] **Step 3: Write the implementation**

Create `gomod/gomod.go`:

```go
// Package gomod parses go.mod files into a typed, line-annotated structure.
// It is the single go.mod parser shared by the scope, resolve, mcp, and
// extract packages, wrapping golang.org/x/mod/modfile.
package gomod

import "golang.org/x/mod/modfile"

// File is the parsed content of a go.mod.
type File struct {
	Module    string
	Go        string // e.g. "1.22.3"
	Toolchain string // e.g. "go1.26.4", or ""
	Requires  []Require
	Replaces  []Replace
	Excludes  []Exclude
	Retracts  []Retract
}

// Require is a single require directive entry.
type Require struct {
	Path     string
	Version  string
	Indirect bool
	Line     int // 1-indexed line in go.mod
}

// Replace is a single replace directive entry. NewVersion is "" for a
// filesystem-path replacement (e.g. => ../fork).
type Replace struct {
	OldPath    string
	OldVersion string
	NewPath    string
	NewVersion string
	Line       int
}

// Exclude is a single exclude directive entry.
type Exclude struct {
	Path    string
	Version string
	Line    int
}

// Retract is a single retract directive entry. Low == High for a single
// version; both are set for a range.
type Retract struct {
	Low       string
	High      string
	Rationale string
	Line      int
}

// Parse parses go.mod content. name is used only in error messages.
func Parse(name string, data []byte) (*File, error) {
	mf, err := modfile.Parse(name, data, nil)
	if err != nil {
		return nil, err
	}
	f := &File{}
	if mf.Module != nil {
		f.Module = mf.Module.Mod.Path
	}
	if mf.Go != nil {
		f.Go = mf.Go.Version
	}
	if mf.Toolchain != nil {
		f.Toolchain = mf.Toolchain.Name
	}
	for _, r := range mf.Require {
		f.Requires = append(f.Requires, Require{
			Path: r.Mod.Path, Version: r.Mod.Version, Indirect: r.Indirect,
			Line: lineOf(r.Syntax),
		})
	}
	for _, r := range mf.Replace {
		f.Replaces = append(f.Replaces, Replace{
			OldPath: r.Old.Path, OldVersion: r.Old.Version,
			NewPath: r.New.Path, NewVersion: r.New.Version,
			Line: lineOf(r.Syntax),
		})
	}
	for _, e := range mf.Exclude {
		f.Excludes = append(f.Excludes, Exclude{
			Path: e.Mod.Path, Version: e.Mod.Version, Line: lineOf(e.Syntax),
		})
	}
	for _, r := range mf.Retract {
		f.Retracts = append(f.Retracts, Retract{
			Low: r.Low, High: r.High, Rationale: r.Rationale, Line: lineOf(r.Syntax),
		})
	}
	return f, nil
}

func lineOf(s *modfile.Line) int {
	if s == nil {
		return 0
	}
	return s.Start.Line
}
```

- [ ] **Step 4: Promote x/mod to a direct dependency**

Run: `CGO_ENABLED=0 go mod tidy`
Expected: `golang.org/x/mod` moves out of the `// indirect` block in `go.mod`.

- [ ] **Step 5: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./gomod/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add gomod/ go.mod go.sum
git commit -m "feat(gomod): add x/mod-based go.mod parser"
```

---

## Task 2: Rewire `resolve.loadGoModulePath`

**Files:**
- Modify: `resolve/resolve.go:912-927`

- [ ] **Step 1: Replace the hand-rolled scanner**

In `resolve/resolve.go`, replace the body of `loadGoModulePath` (currently a `bufio.Scanner` loop) with:

```go
func loadGoModulePath(projectRoot string) string {
	data, err := os.ReadFile(filepath.Join(projectRoot, "go.mod"))
	if err != nil {
		return ""
	}
	f, err := gomod.Parse("go.mod", data)
	if err != nil {
		return ""
	}
	return f.Module
}
```

Add `"github.com/specscore/codegrapher/gomod"` to the import block. Remove the now-unused `"bufio"` import **only if** no other code in the file uses it (search first: `rg -n "bufio" resolve/resolve.go`).

- [ ] **Step 2: Build to verify imports**

Run: `CGO_ENABLED=0 go build ./resolve/`
Expected: success, no unused-import error.

- [ ] **Step 3: Run resolve tests**

Run: `CGO_ENABLED=0 go test ./resolve/`
Expected: PASS (module-path resolution unchanged).

- [ ] **Step 4: Commit**

```bash
git add resolve/resolve.go
git commit -m "refactor(resolve): use gomod parser for module path"
```

---

## Task 3: Rewire `scope.detectGoVersion` (prefer toolchain)

**Files:**
- Modify: `scope/scope.go:71-81` (and remove the now-unused `goDirective` regex if nothing else uses it)
- Test: `scope/scope_test.go`

- [ ] **Step 1: Write the failing test**

Add to `scope/scope_test.go`:

```go
func TestDetectGoVersionPrefersToolchain(t *testing.T) {
	dir := t.TempDir()
	gomodPath := filepath.Join(dir, "go.mod")
	content := "module x\n\ngo 1.22\n\ntoolchain go1.26.4\n"
	if err := os.WriteFile(gomodPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Both go 1.22 and toolchain go1.26.4 share major "v1"; assert the bucket.
	if got := DetectVersion(dir, src, model.LangGo); got != "v1" {
		t.Errorf("DetectVersion = %q, want v1", got)
	}
}
```

(Ensure `os`, `path/filepath`, and `model` are imported in the test file.)

- [ ] **Step 2: Run test to verify it compiles and passes against current code**

Run: `CGO_ENABLED=0 go test ./scope/ -run TestDetectGoVersionPrefersToolchain`
Expected: PASS already (v1 either way) — this test guards the refactor; it must stay green.

- [ ] **Step 3: Replace the regex parse with gomod**

In `scope/scope.go`, replace `detectGoVersion`:

```go
func detectGoVersion(projectRoot, filePath string) string {
	data := readNearest(projectRoot, filePath, "go.mod")
	if data == nil {
		return ""
	}
	f, err := gomod.Parse("go.mod", data)
	if err != nil {
		return ""
	}
	if f.Toolchain != "" {
		return strings.TrimPrefix(f.Toolchain, "go") // "go1.26.4" -> "1.26.4"
	}
	return f.Go
}
```

Add `"github.com/specscore/codegrapher/gomod"` to imports. Delete the `goDirective` regex var (lines ~33-34) — confirm no other use with `rg -n "goDirective" scope/`. Keep `versionPrefix` (still used by `majorVersion`).

- [ ] **Step 4: Run scope tests**

Run: `CGO_ENABLED=0 go test ./scope/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add scope/scope.go scope/scope_test.go
git commit -m "refactor(scope): use gomod parser, prefer toolchain for version bucket"
```

---

## Task 4: Rewire `mcp/queryutils.go` module read

**Files:**
- Modify: `mcp/queryutils.go:324-329`

- [ ] **Step 1: Replace the inline regex**

In `mcp/queryutils.go`, replace the `go.mod` block:

```go
	if data, err := os.ReadFile(filepath.Join(projectRoot, "go.mod")); err == nil {
		if f, perr := gomod.Parse("go.mod", data); perr == nil && f.Module != "" {
			parts := strings.Split(f.Module, "/")
			add(parts[len(parts)-1])
		}
	}
```

Add `"github.com/specscore/codegrapher/gomod"` to imports. After editing, run `rg -n "regexp" mcp/queryutils.go` — if `regexp` is now unused in the file, remove its import; if still used elsewhere, leave it.

- [ ] **Step 2: Build + test**

Run: `CGO_ENABLED=0 go build ./mcp/ && CGO_ENABLED=0 go test ./mcp/`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add mcp/queryutils.go
git commit -m "refactor(mcp): use gomod parser for module search token"
```

---

## Task 5: Schema additions in `model`

**Files:**
- Modify: `model/model.go` (Language consts ~76-82; EdgeKind consts ~55-68)

- [ ] **Step 1: Write the failing test**

Add `model/gomod_kinds_test.go`:

```go
package model

import "testing"

func TestGoModKindsExist(t *testing.T) {
	if LangGoMod != "go.mod" {
		t.Errorf("LangGoMod = %q", LangGoMod)
	}
	for _, k := range []EdgeKind{EdgeRequires, EdgeReplaces, EdgeExcludes} {
		if k == "" {
			t.Errorf("edge kind is empty")
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./model/ -run TestGoModKindsExist`
Expected: FAIL — `LangGoMod` / `EdgeRequires` undefined.

- [ ] **Step 3: Add the constants**

In `model/model.go`, add to the Language const block:

```go
	LangGoMod      Language = "go.mod"
```

Add to the EdgeKind const block:

```go
	EdgeRequires     EdgeKind = "requires"
	EdgeReplaces     EdgeKind = "replaces"
	EdgeExcludes     EdgeKind = "excludes"
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./model/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add model/model.go model/gomod_kinds_test.go
git commit -m "feat(model): add LangGoMod and require/replace/exclude edge kinds"
```

---

## Task 6: Detect `go.mod` as `LangGoMod`

**Files:**
- Modify: `internal/extract/detect.go:18-38`
- Test: `internal/extract/detect_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Add to `internal/extract/detect_test.go`:

```go
package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func TestDetectLanguageGoMod(t *testing.T) {
	cases := map[string]model.Language{
		"go.mod":           model.LangGoMod,
		"sub/dir/go.mod":   model.LangGoMod,
		"main.go":          model.LangGo,
		"go.sum":           model.LangUnknown,
		"gomod":            model.LangUnknown,
	}
	for path, want := range cases {
		if got := DetectLanguage(path); got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./internal/extract/ -run TestDetectLanguageGoMod`
Expected: FAIL — `go.mod` currently returns `LangUnknown`.

- [ ] **Step 3: Add the basename check**

In `internal/extract/detect.go`, at the top of `DetectLanguage` (before the `ext` switch), add:

```go
	if filepath.Base(filePath) == "go.mod" {
		return model.LangGoMod
	}
```

`filepath` is already imported.

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/extract/ -run TestDetectLanguageGoMod`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/extract/detect.go internal/extract/detect_test.go
git commit -m "feat(extract): detect go.mod as LangGoMod"
```

---

## Task 7: `extractGoMod` — build nodes & edges

**Files:**
- Create: `internal/extract/walk_gomod.go`
- Create: `internal/extract/walk_gomod_test.go`
- Modify: `internal/extract/extract.go:103-111` (walk switch)

- [ ] **Step 1: Write the failing test**

Create `internal/extract/walk_gomod_test.go`:

```go
package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

const gomodFixture = `module github.com/example/proj

go 1.22.3

toolchain go1.26.4

require github.com/spf13/cobra v1.10.2

require golang.org/x/mod v0.33.0 // indirect

replace github.com/spf13/cobra => ../forked-cobra

exclude github.com/bad/dep v0.1.0
`

func TestExtractGoMod(t *testing.T) {
	res, err := ExtractFile("go.mod", []byte(gomodFixture), model.LangGoMod)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}

	// File node + main module + 2 require deps + 1 exclude dep = 5 nodes.
	// (replace target ../forked-cobra reuses the existing cobra dep node.)
	var file, main *model.Node
	mods := 0
	for i := range res.Nodes {
		n := &res.Nodes[i]
		switch {
		case n.Kind == model.KindFile:
			file = n
		case n.Kind == model.KindModule && n.Name == "github.com/example/proj":
			main = n
		case n.Kind == model.KindModule:
			mods++
		}
	}
	if file == nil || file.Language != model.LangGoMod {
		t.Fatalf("missing/incorrect file node: %+v", file)
	}
	if main == nil {
		t.Fatal("missing main module node")
	}
	if want := "go 1.22.3; toolchain go1.26.4"; main.Signature != want {
		t.Errorf("main.Signature = %q, want %q", main.Signature, want)
	}
	if mods != 3 { // cobra, x/mod, bad/dep
		t.Errorf("dependency module nodes = %d, want 3", mods)
	}

	count := func(k model.EdgeKind) int {
		c := 0
		for _, e := range res.Edges {
			if e.Kind == k {
				c++
			}
		}
		return c
	}
	if count(model.EdgeRequires) != 2 {
		t.Errorf("EdgeRequires = %d, want 2", count(model.EdgeRequires))
	}
	if count(model.EdgeReplaces) != 1 {
		t.Errorf("EdgeReplaces = %d, want 1", count(model.EdgeReplaces))
	}
	if count(model.EdgeExcludes) != 1 {
		t.Errorf("EdgeExcludes = %d, want 1", count(model.EdgeExcludes))
	}
	// File contains the main module node.
	if count(model.EdgeContains) < 1 {
		t.Errorf("expected a contains edge from file to module")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./internal/extract/ -run TestExtractGoMod`
Expected: FAIL — `LangGoMod` produces only the file node today (no module/dep nodes, no edges).

- [ ] **Step 3: Implement `extractGoMod`**

Create `internal/extract/walk_gomod.go`:

```go
package extract

import (
	"fmt"
	"strings"
	"time"

	"github.com/specscore/codegrapher/gomod"
	"github.com/specscore/codegrapher/model"
)

// extractGoMod parses a go.mod and emits a main module node (contained by the
// already-emitted file node) plus one KindModule node per dependency, joined by
// requires/replaces/excludes edges. The go/toolchain/retract directives are
// encoded on the main module node's Signature. Called by ExtractFile for
// LangGoMod. A parse error is recorded as a warning and leaves only the file
// node (mirrors the file-level parse-error path).
func (e *extractor) extractGoMod(content []byte) {
	f, err := gomod.Parse(e.filePath, content)
	if err != nil {
		e.errors = append(e.errors, model.ExtractionError{
			Message:  err.Error(),
			FilePath: e.filePath,
			Severity: "warning",
			Code:     "gomod_parse_error",
		})
		return
	}
	if f.Module == "" {
		return
	}
	now := time.Now().UnixMilli()

	// Main module node.
	mainID := e.addModuleNode(f.Module, moduleSignature(f), 1, now)
	if fileID := e.fileNodeID(); fileID != "" {
		e.edges = append(e.edges, model.Edge{
			Source: fileID, Target: mainID, Kind: model.EdgeContains, Provenance: "modfile",
		})
	}

	// depNode returns (creating once) the KindModule node for a dep path.
	depIDs := map[string]string{}
	depNode := func(path, version string, line int) string {
		if id, ok := depIDs[path]; ok {
			return id
		}
		sig := version
		id := e.addModuleNode(path, sig, line, now)
		depIDs[path] = id
		return id
	}

	for _, r := range f.Requires {
		id := depNode(r.Path, r.Version, r.Line)
		e.edges = append(e.edges, model.Edge{
			Source: mainID, Target: id, Kind: model.EdgeRequires, Line: r.Line,
			Provenance: "modfile",
			Metadata:   map[string]any{"version": r.Version, "indirect": r.Indirect},
		})
	}
	for _, r := range f.Replaces {
		oldID := depNode(r.OldPath, r.OldVersion, r.Line)
		newID := depNode(r.NewPath, r.NewVersion, r.Line)
		e.edges = append(e.edges, model.Edge{
			Source: oldID, Target: newID, Kind: model.EdgeReplaces, Line: r.Line,
			Provenance: "modfile",
			Metadata:   map[string]any{"local": r.NewVersion == "", "newPath": r.NewPath, "newVersion": r.NewVersion},
		})
	}
	for _, x := range f.Excludes {
		id := depNode(x.Path, x.Version, x.Line)
		e.edges = append(e.edges, model.Edge{
			Source: mainID, Target: id, Kind: model.EdgeExcludes, Line: x.Line,
			Provenance: "modfile",
			Metadata:   map[string]any{"version": x.Version},
		})
	}
}

// addModuleNode appends a KindModule node and returns its ID.
func (e *extractor) addModuleNode(path, signature string, line int, now int64) string {
	id := model.GenerateNodeID(e.filePath, model.KindModule, path, line)
	e.nodes = append(e.nodes, model.Node{
		ID:            id,
		Kind:          model.KindModule,
		Name:          path,
		QualifiedName: path,
		FilePath:      e.filePath,
		Language:      model.LangGoMod,
		StartLine:     line,
		EndLine:       line,
		Signature:     signature,
		UpdatedAt:     now,
	})
	return id
}

// fileNodeID returns the file node's ID if one was emitted, else "".
func (e *extractor) fileNodeID() string {
	if len(e.nodes) > 0 && e.nodes[0].Kind == model.KindFile {
		return e.nodes[0].ID
	}
	return ""
}

// moduleSignature encodes go/toolchain/retract on the main module node.
func moduleSignature(f *gomod.File) string {
	var parts []string
	if f.Go != "" {
		parts = append(parts, "go "+f.Go)
	}
	if f.Toolchain != "" {
		parts = append(parts, "toolchain "+f.Toolchain)
	}
	if len(f.Retracts) > 0 {
		var rs []string
		for _, r := range f.Retracts {
			if r.Low == r.High {
				rs = append(rs, r.Low)
			} else {
				rs = append(rs, fmt.Sprintf("[%s, %s]", r.Low, r.High))
			}
		}
		parts = append(parts, "retract ["+strings.Join(rs, ", ")+"]")
	}
	return strings.Join(parts, "; ")
}
```

- [ ] **Step 4: Wire the dispatch in `ExtractFile`**

In `internal/extract/extract.go`, in the walk switch (currently `case model.LangGo:` / `case model.LangTypeScript…`), add a case:

```go
	case model.LangGoMod:
		e.extractGoMod(content)
```

(The file node is already emitted by `emitFileNode` at line ~100; `LangGoMod` is not file-level-only, so it falls through correctly.)

- [ ] **Step 5: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/extract/ -run TestExtractGoMod`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/extract/walk_gomod.go internal/extract/walk_gomod_test.go internal/extract/extract.go
git commit -m "feat(extract): index go.mod into module + dependency nodes"
```

---

## Task 8: Fold `go.mod` into the `go-vN` scope store

**Files:**
- Modify: `indexer/init.go:47-51` (`scopeStoreForFile`)
- Modify: `scope/scope.go:45-54` (`DetectVersion` switch)
- Test: `indexer/init_test.go`

- [ ] **Step 1: Write the failing test**

Add to `indexer/init_test.go`:

```go
func TestGoModFoldsIntoGoScope(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/proj\n\ngo 1.22\n\nrequire github.com/spf13/cobra v1.10.2\n")
	write("main.go", "package main\n\nfunc main() {}\n")

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// The go.mod module node must live in the same scope store as main.go (go-v1).
	if !hasNodeNamed(t, idx, "example.com/proj") {
		t.Error("module node not found in any store")
	}
	for _, s := range idx.Stores() {
		// No store should be keyed go.mod-*; go.mod folds into go-v1.
		_ = s
	}
	scoped := idx.StoresFiltered([]string{"go-v1"})
	if len(scoped) != 1 {
		t.Fatalf("expected exactly one go-v1 store, got %d", len(scoped))
	}
}
```

(Confirm `hasNodeNamed` exists in `indexer/sync_test.go:30` — it does; reuse it. Ensure `os`, `path/filepath` imported.)

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./indexer/ -run TestGoModFoldsIntoGoScope`
Expected: FAIL — go.mod currently lands in a `go.mod-v0` (or similar) scope, so `go-v1` filter or node lookup is off.

- [ ] **Step 3: Add the DetectVersion case**

In `scope/scope.go`, in `DetectVersion`'s switch, extend the Go case to include `LangGoMod`:

```go
	case model.LangGo, model.LangGoMod:
		ver = detectGoVersion(projectRoot, filePath)
```

- [ ] **Step 4: Map the scope language in scopeStoreForFile**

In `indexer/init.go`, change `scopeStoreForFile`:

```go
func (idx *Indexer) scopeStoreForFile(relPath string, lang model.Language) (*store.Store, error) {
	ver := scope.DetectVersion(idx.root, filepath.Join(idx.root, relPath), lang)
	return idx.reg.Store(scope.Scope{Language: scopeLanguage(lang), Version: ver})
}

// scopeLanguage maps a detection language to its storage-partition language.
// go.mod folds into the Go partition so module/dependency nodes are co-located
// with the Go source they govern.
func scopeLanguage(lang model.Language) model.Language {
	if lang == model.LangGoMod {
		return model.LangGo
	}
	return lang
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./indexer/ -run TestGoModFoldsIntoGoScope`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add indexer/init.go scope/scope.go indexer/init_test.go
git commit -m "feat(indexer): fold go.mod into the governing go-vN scope store"
```

---

## Task 9: Full gates + golden re-baseline

**Files:**
- Modify: `testdata/golden/**` (regenerated, never hand-edited)

- [ ] **Step 1: Run gofmt and vet**

Run: `gofmt -l . && CGO_ENABLED=0 go vet ./...`
Expected: `gofmt -l` prints nothing; `go vet` clean.

- [ ] **Step 2: Run the full suite (expect golden diffs)**

Run: `CGO_ENABLED=0 go test -count=1 ./...`
Expected: `gomod`, `model`, `scope`, `resolve`, `mcp`, `internal/extract` PASS. Golden/parity tests in `indexer/` and `internal/extract/` may FAIL because fixture repos containing a `go.mod` now emit extra nodes/edges — this is expected (owner relaxation).

- [ ] **Step 3: Re-baseline goldens**

Run: `bash tools/parity/rebaseline-golden.sh`
(If the script takes a scope or fixture argument, run `bash tools/parity/rebaseline-golden.sh --help` first and follow it. Do NOT hand-edit any file under `testdata/golden/`.)

- [ ] **Step 4: Re-run the full suite green**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test -count=1 ./...`
Expected: all PASS, including regenerated goldens and the binary parity test.

- [ ] **Step 5: Commit**

```bash
git add testdata/golden
git commit -m "test: re-baseline goldens for go.mod indexing"
```

---

## Self-Review notes

- **Spec coverage:** A (Tasks 1-4), B schema (Task 5), detection (Task 6), extractor + edges (Task 7), scope folding (Task 8), goldens (Task 9). All spec sections mapped.
- **Type consistency:** `gomod.File`/`Require`/`Replace`/`Exclude`/`Retract` defined in Task 1 are used unchanged in Tasks 2-4 and 7. `extractGoMod`, `addModuleNode`, `moduleSignature`, `scopeLanguage` are each defined once and referenced consistently. Edge kinds `EdgeRequires`/`EdgeReplaces`/`EdgeExcludes` and `LangGoMod` are defined in Task 5 before first use in Tasks 6-8.
- **Open risk:** the exact CLI of `rebaseline-golden.sh` (Task 9 Step 3) is not pinned here — inspect its `--help`/header before running. The `mcp` explore/impact traversal allowlists are intentionally NOT extended in this plan; dependency edges are stored and queryable by kind, but surfacing them in `codegraph_explore` graph-walks is deferred as a follow-up.
