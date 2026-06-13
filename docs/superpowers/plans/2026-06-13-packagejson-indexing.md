# package.json Indexing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Parse `package.json` through one shared `encoding/json` package, and index it as graph content (a main module node + per-dependency nodes joined by `requires` edges) stored in a dedicated `node-vN` scope that the query client auto-merges into the JS/TS-family scopes.

**Architecture:** Mirrors the `go.mod` feature. A `pkgjson` parser replaces two hand-rolled readers (Part A). A `LangPackageJSON` detection language + `extractPackageJSON` emit module/dependency nodes reusing `KindModule`/`EdgeRequires`; storage folds into a new `LangNode` (`node-vN`) partition; `StoresFiltered` expands JS/TS-family scope requests to also include the version-matched `node` scope (Part E client merge).

**Tech Stack:** Go 1.26, `encoding/json`, existing `model`/`extract`/`scope`/`indexer` packages. **CGO_ENABLED=0 for all build/test.**

**Worktree:** All work happens in `/Users/alexandertrakhimenok/projects/code-grapher/codegrapher/.claude/worktrees/packagejson` on branch `feat/packagejson-indexing`.

**Spec:** `docs/superpowers/specs/2026-06-13-packagejson-indexing-design.md`

**Owner relaxations:** Goldens and backward-compat are NOT constraints; goldens are regenerated via `tools/parity/rebaseline-golden.sh` (never hand-edited).

---

## File Structure

- **Create** `pkgjson/pkgjson.go` — `encoding/json` wrapper → typed `File`. One responsibility: turn package.json bytes into a typed struct.
- **Create** `pkgjson/pkgjson_test.go` — parser table tests.
- **Create** `internal/extract/walk_packagejson.go` — `extractPackageJSON` builds nodes/edges.
- **Create** `internal/extract/walk_packagejson_test.go` — extractor tests.
- **Modify** `scope/scope.go` — `detectNodeVersion` delegates to `pkgjson`; `DetectVersion` handles `LangPackageJSON`.
- **Modify** `mcp/queryutils.go` — package-name read delegates to `pkgjson`.
- **Modify** `model/model.go` — add `LangPackageJSON`, `LangNode`.
- **Modify** `internal/extract/detect.go` — `DetectLanguage` maps basename `package.json`.
- **Modify** `internal/extract/extract.go` — dispatch `LangPackageJSON`.
- **Modify** `internal/extract/walk_gomod.go` — generalize `addModuleNode` to use `e.lang`.
- **Modify** `indexer/init.go` — `scopeLanguage` maps `LangPackageJSON`→`LangNode`.
- **Modify** `indexer/indexer.go` — `StoresFiltered` family→node expansion.
- **Create** `testdata/fixtures/ts-small/package.json` — fixture extension.
- **Modify** `tools/parity/rebaseline-golden.sh` — UNION dumps across multi-scope fixtures.

---

## Task 1: `pkgjson` parser package

**Files:**
- Create: `pkgjson/pkgjson.go`
- Test: `pkgjson/pkgjson_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkgjson/pkgjson_test.go`:

```go
package pkgjson

import "testing"

const sample = `{
  "name": "@acme/widget",
  "version": "1.2.3",
  "engines": { "node": ">=18" },
  "dependencies": { "left-pad": "^1.3.0", "react": "^18.2.0" },
  "devDependencies": { "vitest": "^1.0.0", "left-pad": "^1.3.0" },
  "peerDependencies": { "react": ">=18" },
  "optionalDependencies": { "fsevents": "^2.3.0" }
}`

func TestParse(t *testing.T) {
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Name != "@acme/widget" {
		t.Errorf("Name = %q", f.Name)
	}
	if f.Version != "1.2.3" {
		t.Errorf("Version = %q", f.Version)
	}
	if f.Engines["node"] != ">=18" {
		t.Errorf("Engines = %v", f.Engines)
	}
	if f.Dependencies["left-pad"] != "^1.3.0" || f.Dependencies["react"] != "^18.2.0" {
		t.Errorf("Dependencies = %v", f.Dependencies)
	}
	if f.DevDependencies["vitest"] != "^1.0.0" {
		t.Errorf("DevDependencies = %v", f.DevDependencies)
	}
	if f.PeerDependencies["react"] != ">=18" {
		t.Errorf("PeerDependencies = %v", f.PeerDependencies)
	}
	if f.OptionalDependencies["fsevents"] != "^2.3.0" {
		t.Errorf("OptionalDependencies = %v", f.OptionalDependencies)
	}
}

func TestParseMinimal(t *testing.T) {
	f, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Name != "" || len(f.Dependencies) != 0 {
		t.Errorf("expected empty File, got %+v", f)
	}
}

func TestParseMalformed(t *testing.T) {
	if _, err := Parse([]byte(`{ not json`)); err == nil {
		t.Fatal("expected error for malformed package.json")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./pkgjson/`
Expected: FAIL — package `pkgjson` / `Parse` undefined.

- [ ] **Step 3: Write the implementation**

Create `pkgjson/pkgjson.go`:

```go
// Package pkgjson parses package.json files into a typed structure. It is the
// single package.json parser shared by the scope, mcp, and extract packages,
// wrapping encoding/json.
package pkgjson

import "encoding/json"

// File is the parsed content of a package.json (only the fields we index).
type File struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Engines              map[string]string `json:"engines"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// Parse parses package.json content.
func Parse(data []byte) (*File, error) {
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./pkgjson/`
Expected: PASS.

- [ ] **Step 5: gofmt + vet**

Run: `gofmt -l pkgjson/` (expect no output) and `CGO_ENABLED=0 go vet ./pkgjson/` (expect clean).

- [ ] **Step 6: Commit**

```bash
git add pkgjson/
git commit -m "feat(pkgjson): add encoding/json-based package.json parser

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Rewire `scope.detectNodeVersion`

**Files:**
- Modify: `scope/scope.go` — function `detectNodeVersion` (~line 83)

- [ ] **Step 1: Replace the inline struct parse**

In `scope/scope.go`, replace the body of `detectNodeVersion`:

```go
func detectNodeVersion(projectRoot, filePath string) string {
	data := readNearest(projectRoot, filePath, "package.json")
	if data == nil {
		return ""
	}
	f, err := pkgjson.Parse(data)
	if err != nil {
		return ""
	}
	if v := f.DevDependencies["typescript"]; v != "" {
		return v
	}
	if v := f.Dependencies["typescript"]; v != "" {
		return v
	}
	return f.Engines["node"]
}
```

Add `"github.com/specscore/codegrapher/pkgjson"` to the import block. After editing, run `rg -n "encoding/json|json\\." scope/scope.go` — if `encoding/json` is now unused in the file, remove its import; if still used elsewhere, leave it.

- [ ] **Step 2: Build + test**

Run: `CGO_ENABLED=0 go build ./scope/ && CGO_ENABLED=0 go test ./scope/`
Expected: PASS (version-detection behavior unchanged).

- [ ] **Step 3: gofmt + vet**

Run: `gofmt -l scope/scope.go` (no output) and `CGO_ENABLED=0 go vet ./scope/` (clean).

- [ ] **Step 4: Commit**

```bash
git add scope/scope.go
git commit -m "refactor(scope): use pkgjson parser for node version detection

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Rewire `mcp/queryutils.go` package-name read

**Files:**
- Modify: `mcp/queryutils.go` — the `package.json` block (~line 330-337)

- [ ] **Step 1: Replace the inline struct parse**

In `mcp/queryutils.go`, replace this block:

```go
	if data, err := os.ReadFile(filepath.Join(projectRoot, "package.json")); err == nil {
		var pkg struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &pkg) == nil && pkg.Name != "" {
			add(regexp.MustCompile(`^@[^/]+/`).ReplaceAllString(pkg.Name, ""))
```

with:

```go
	if data, err := os.ReadFile(filepath.Join(projectRoot, "package.json")); err == nil {
		if f, perr := pkgjson.Parse(data); perr == nil && f.Name != "" {
			add(regexp.MustCompile(`^@[^/]+/`).ReplaceAllString(f.Name, ""))
```

Be careful with the closing braces: the original has two closing `}` after the `add(...)` line (one for the `if json.Unmarshal...` and one for the outer `if data...`). The replacement removes one nesting level (no inner `if` wrapping a struct), so the trailing braces must be adjusted to match — read the surrounding lines and ensure the block still closes correctly (the new form has the `if f, perr := ...` and the outer `if data` = two closes, same as before). Add `"github.com/specscore/codegrapher/pkgjson"` to the import block. Run `rg -n "encoding/json|json\\." mcp/queryutils.go` — remove the `encoding/json` import only if now unused in the file.

- [ ] **Step 2: Build + test**

Run: `CGO_ENABLED=0 go build ./mcp/ && CGO_ENABLED=0 go test ./mcp/`
Expected: PASS.

- [ ] **Step 3: gofmt + vet**

Run: `gofmt -l mcp/queryutils.go` (no output) and `CGO_ENABLED=0 go vet ./mcp/` (clean).

- [ ] **Step 4: Commit**

```bash
git add mcp/queryutils.go
git commit -m "refactor(mcp): use pkgjson parser for package name token

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Schema additions in `model`

**Files:**
- Modify: `model/model.go` — Language const block (~line 76-86)
- Test: `model/packagejson_kinds_test.go`

- [ ] **Step 1: Write the failing test**

Create `model/packagejson_kinds_test.go`:

```go
package model

import "testing"

func TestPackageJSONLanguagesExist(t *testing.T) {
	if LangPackageJSON != "package.json" {
		t.Errorf("LangPackageJSON = %q", LangPackageJSON)
	}
	if LangNode != "node" {
		t.Errorf("LangNode = %q", LangNode)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./model/ -run TestPackageJSONLanguagesExist`
Expected: FAIL — undefined identifiers.

- [ ] **Step 3: Add the constants**

In `model/model.go`, in the `Language` const block (which already contains `LangGoMod`), add:

```go
	LangPackageJSON Language = "package.json"
	LangNode        Language = "node"
```

(Match the block's existing gofmt alignment.)

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./model/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add model/model.go model/packagejson_kinds_test.go
git commit -m "feat(model): add LangPackageJSON and LangNode languages

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Detect `package.json` as `LangPackageJSON`

**Files:**
- Modify: `internal/extract/detect.go` — `DetectLanguage`
- Test: `internal/extract/detect_test.go` (append to existing file)

- [ ] **Step 1: Write the failing test**

Append to `internal/extract/detect_test.go` (the file already exists with package clause + `model` import from the go.mod work; add only the function):

```go
func TestDetectLanguagePackageJSON(t *testing.T) {
	cases := map[string]model.Language{
		"package.json":        model.LangPackageJSON,
		"sub/pkg/package.json": model.LangPackageJSON,
		"package-lock.json":   model.LangUnknown,
		"tsconfig.json":       model.LangUnknown,
	}
	for path, want := range cases {
		if got := DetectLanguage(path); got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./internal/extract/ -run TestDetectLanguagePackageJSON`
Expected: FAIL — `package.json` currently returns `LangUnknown`.

- [ ] **Step 3: Add the basename check**

In `internal/extract/detect.go`, `DetectLanguage` already has a basename check for `go.mod` at the top. Add a sibling check immediately after it:

```go
	if filepath.Base(filePath) == "package.json" {
		return model.LangPackageJSON
	}
```

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/extract/ -run TestDetectLanguagePackageJSON`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/extract/detect.go internal/extract/detect_test.go
git commit -m "feat(extract): detect package.json as LangPackageJSON

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: `extractPackageJSON` — build nodes & edges

**Files:**
- Modify: `internal/extract/walk_gomod.go` — generalize `addModuleNode` to use `e.lang`
- Create: `internal/extract/walk_packagejson.go`
- Create: `internal/extract/walk_packagejson_test.go`
- Modify: `internal/extract/extract.go` — dispatch `LangPackageJSON`

- [ ] **Step 1: Generalize the shared `addModuleNode`**

In `internal/extract/walk_gomod.go`, `addModuleNode` currently hardcodes `Language: model.LangGoMod`. Change that one field to use the extractor's language so the helper is reusable:

```go
		Language:      e.lang,
```

(For go.mod files `e.lang == model.LangGoMod`, so output is unchanged. `e.lang` is set in `ExtractFile`.)

- [ ] **Step 2: Write the failing test**

Create `internal/extract/walk_packagejson_test.go`:

```go
package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

const pkgjsonFixture = `{
  "name": "widget",
  "version": "1.2.3",
  "engines": { "node": ">=18" },
  "dependencies": { "left-pad": "^1.3.0" },
  "devDependencies": { "vitest": "^1.0.0", "left-pad": "^1.3.0" },
  "peerDependencies": { "react": ">=18" },
  "optionalDependencies": { "fsevents": "^2.3.0" }
}`

func TestExtractPackageJSON(t *testing.T) {
	res, err := ExtractFile("package.json", []byte(pkgjsonFixture), model.LangPackageJSON)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}

	var file, main *model.Node
	mods := 0
	for i := range res.Nodes {
		n := &res.Nodes[i]
		switch {
		case n.Kind == model.KindFile:
			file = n
		case n.Kind == model.KindModule && n.Name == "widget":
			main = n
		case n.Kind == model.KindModule:
			mods++
		}
	}
	if file == nil || file.Language != model.LangPackageJSON {
		t.Fatalf("missing/incorrect file node: %+v", file)
	}
	if main == nil {
		t.Fatal("missing main module node")
	}
	if main.Language != model.LangPackageJSON {
		t.Errorf("main module Language = %q, want package.json", main.Language)
	}
	if want := "version 1.2.3; engines: node>=18"; main.Signature != want {
		t.Errorf("main.Signature = %q, want %q", main.Signature, want)
	}
	// Distinct deps: left-pad, vitest, react, fsevents = 4 (left-pad deduped).
	if mods != 4 {
		t.Errorf("dependency module nodes = %d, want 4", mods)
	}

	// One requires edge per (name, category): left-pad appears in prod AND dev = 2,
	// plus vitest(dev), react(peer), fsevents(optional) = 5 total.
	requires := 0
	cats := map[string]int{}
	for _, e := range res.Edges {
		if e.Kind == model.EdgeRequires {
			requires++
			if c, ok := e.Metadata["category"].(string); ok {
				cats[c]++
			}
		}
	}
	if requires != 5 {
		t.Errorf("EdgeRequires = %d, want 5", requires)
	}
	if cats["prod"] != 1 || cats["dev"] != 2 || cats["peer"] != 1 || cats["optional"] != 1 {
		t.Errorf("category tally = %v, want prod:1 dev:2 peer:1 optional:1", cats)
	}

	containsFromFile := 0
	for _, e := range res.Edges {
		if e.Kind == model.EdgeContains && e.Source == model.FileNodeID("package.json") {
			containsFromFile++
		}
	}
	if containsFromFile != 1 {
		t.Errorf("contains edges from file = %d, want 1", containsFromFile)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./internal/extract/ -run TestExtractPackageJSON`
Expected: FAIL — `LangPackageJSON` currently yields only the file node (no dispatch yet).

- [ ] **Step 4: Implement `extractPackageJSON`**

Create `internal/extract/walk_packagejson.go`:

```go
package extract

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/pkgjson"
)

// extractPackageJSON parses a package.json and emits a main module node
// (contained by the already-emitted file node) plus one KindModule node per
// dependency across all four dependency categories, joined by EdgeRequires
// edges carrying {version, category} metadata. version/engines are encoded on
// the main module node's Signature. A parse error is recorded as a warning and
// leaves only the file node (mirrors the go.mod parse-error path).
func (e *extractor) extractPackageJSON(content []byte) {
	f, err := pkgjson.Parse(content)
	if err != nil {
		e.errors = append(e.errors, model.ExtractionError{
			Message:  err.Error(),
			FilePath: e.filePath,
			Severity: "warning",
			Code:     "packagejson_parse_error",
		})
		return
	}
	now := time.Now().UnixMilli()

	name := f.Name
	if name == "" {
		name = filepath.Base(filepath.Dir(e.filePath))
		if name == "." || name == string(filepath.Separator) || name == "" {
			name = "package.json"
		}
	}

	mainID := e.addModuleNode(name, pkgSignature(f), 1, now)
	e.edges = append(e.edges, model.Edge{
		Source: model.FileNodeID(e.filePath), Target: mainID,
		Kind: model.EdgeContains, Provenance: "package.json",
	})

	depIDs := map[string]string{}
	depNode := func(dep, version string) string {
		if id, ok := depIDs[dep]; ok {
			return id
		}
		id := e.addModuleNode(dep, version, 1, now)
		depIDs[dep] = id
		return id
	}

	addCategory := func(deps map[string]string, category string) {
		names := make([]string, 0, len(deps))
		for n := range deps {
			names = append(names, n)
		}
		sort.Strings(names) // deterministic node/edge order (json maps are unordered)
		for _, n := range names {
			id := depNode(n, deps[n])
			e.edges = append(e.edges, model.Edge{
				Source: mainID, Target: id, Kind: model.EdgeRequires,
				Provenance: "package.json",
				Metadata:   map[string]any{"version": deps[n], "category": category},
			})
		}
	}
	addCategory(f.Dependencies, "prod")
	addCategory(f.DevDependencies, "dev")
	addCategory(f.PeerDependencies, "peer")
	addCategory(f.OptionalDependencies, "optional")
}

// pkgSignature encodes version + engines on the main module node.
func pkgSignature(f *pkgjson.File) string {
	var parts []string
	if f.Version != "" {
		parts = append(parts, "version "+f.Version)
	}
	if len(f.Engines) > 0 {
		keys := make([]string, 0, len(f.Engines))
		for k := range f.Engines {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var es []string
		for _, k := range keys {
			es = append(es, k+f.Engines[k]) // e.g. "node>=18"
		}
		parts = append(parts, "engines: "+strings.Join(es, ", "))
	}
	return strings.Join(parts, "; ")
}
```

- [ ] **Step 5: Wire the dispatch in `ExtractFile`**

In `internal/extract/extract.go`, in the per-language walk switch (it already has `case model.LangGo:`, `case model.LangGoMod:`, and the TS/JS cases), add:

```go
	case model.LangPackageJSON:
		e.extractPackageJSON(content)
```

- [ ] **Step 6: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/extract/ -run TestExtractPackageJSON`
Expected: PASS.

- [ ] **Step 7: Run the full extract package + go.mod regression**

Run: `CGO_ENABLED=0 go test ./internal/extract/`
Expected: `TestExtractPackageJSON`, `TestExtractGoMod`, and all unit tests PASS. The golden parity test `TestParityGoSmall` should still pass (go-small has no package.json, and `addModuleNode` output for go.mod is unchanged because `e.lang == LangGoMod`). If `TestParityGoSmall` fails, the `e.lang` change altered go.mod output — investigate before proceeding.

- [ ] **Step 8: gofmt + vet + commit**

Run: `gofmt -l internal/extract/` (no output); `CGO_ENABLED=0 go vet ./internal/extract/` (clean).

```bash
git add internal/extract/walk_packagejson.go internal/extract/walk_packagejson_test.go internal/extract/extract.go internal/extract/walk_gomod.go
git commit -m "feat(extract): index package.json into module + dependency nodes

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Fold `package.json` into the dedicated `node-vN` scope

**Files:**
- Modify: `scope/scope.go` — `DetectVersion` switch
- Modify: `indexer/init.go` — `scopeLanguage` helper
- Test: `indexer/init_test.go`

- [ ] **Step 1: Write the failing test**

Add to `indexer/init_test.go`:

```go
func TestPackageJSONFoldsIntoNodeScope(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"name":"demo","dependencies":{"left-pad":"^1.3.0"}}`)
	write("index.js", "module.exports = 1;\n")

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !hasNodeNamed(t, idx, "demo") {
		t.Error("package.json module node not found in any store")
	}
	// package.json (no typescript dep / engines) buckets to v0 -> node-v0.
	scoped := idx.StoresFiltered([]string{"node-v0"})
	if len(scoped) != 1 {
		t.Fatalf("expected exactly one node-v0 store, got %d", len(scoped))
	}
}
```

(`hasNodeNamed`, `os`, `path/filepath` are already used in this test file from the go.mod work.)

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./indexer/ -run TestPackageJSONFoldsIntoNodeScope`
Expected: FAIL — package.json currently lands in a `package.json-v0` scope (not `node-v0`).

- [ ] **Step 3: Add the DetectVersion case**

In `scope/scope.go`, in `DetectVersion`'s switch, extend the Node/TS case to include `LangPackageJSON`. The current TS/JS case looks like `case model.LangTypeScript, model.LangJavaScript, model.LangTSX, model.LangJSX:` calling `detectNodeVersion`. Add `model.LangPackageJSON` to that case list:

```go
	case model.LangTypeScript, model.LangJavaScript, model.LangTSX, model.LangJSX, model.LangPackageJSON:
		ver = detectNodeVersion(projectRoot, filePath)
```

- [ ] **Step 4: Map the scope language in `scopeLanguage`**

In `indexer/init.go`, the `scopeLanguage` helper (added for go.mod) currently maps `LangGoMod`→`LangGo`. Add a mapping for `LangPackageJSON`→`LangNode`:

```go
func scopeLanguage(lang model.Language) model.Language {
	switch lang {
	case model.LangGoMod:
		return model.LangGo
	case model.LangPackageJSON:
		return model.LangNode
	default:
		return lang
	}
}
```

(If the existing helper is written as an `if`, convert to this `switch` form.)

- [ ] **Step 5: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./indexer/ -run TestPackageJSONFoldsIntoNodeScope`
Expected: PASS.

- [ ] **Step 6: Build + gofmt + vet + commit**

Run: `CGO_ENABLED=0 go build ./...`; `gofmt -l scope/ indexer/` (no output); `CGO_ENABLED=0 go vet ./scope/ ./indexer/` (clean).

```bash
git add scope/scope.go indexer/init.go indexer/init_test.go
git commit -m "feat(indexer): fold package.json into a dedicated node-vN scope

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Client merge — expand JS/TS-family scopes to include `node-vN`

**Files:**
- Modify: `indexer/indexer.go` — `StoresFiltered`
- Test: `indexer/init_test.go`

- [ ] **Step 1: Write the failing test**

Add to `indexer/init_test.go`:

```go
func TestStoresFilteredMergesNodeScope(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// package.json (no typescript dep) -> node-v0; a .ts file -> typescript-v0.
	write("package.json", `{"name":"demo","dependencies":{"left-pad":"^1.3.0"}}`)
	write("main.ts", "export const x = 1;\n")

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// Requesting only the typescript scope must auto-include node-v0 (the merge).
	got := idx.StoresFiltered([]string{"typescript-v0"})
	if len(got) != 2 {
		t.Fatalf("typescript-v0 filter returned %d stores, want 2 (typescript-v0 + node-v0)", len(got))
	}
	// Requesting node alone returns just node.
	if n := len(idx.StoresFiltered([]string{"node-v0"})); n != 1 {
		t.Errorf("node-v0 filter returned %d stores, want 1", n)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=0 go test ./indexer/ -run TestStoresFilteredMergesNodeScope`
Expected: FAIL — `typescript-v0` filter returns 1 store (no merge yet).

- [ ] **Step 3: Add family→node expansion in `StoresFiltered`**

In `indexer/indexer.go`, `StoresFiltered` builds a `want` set from `scopeKeys`. Change the loop that fills `want` to also add the version-matched node key for JS/TS-family keys, and add the helper:

```go
	want := make(map[string]struct{}, len(scopeKeys))
	for _, k := range scopeKeys {
		want[k] = struct{}{}
		if nodeKey, ok := nodeScopeForFamilyKey(k); ok {
			want[nodeKey] = struct{}{}
		}
	}
```

Add this function (e.g. directly below `StoresFiltered`):

```go
// nodeScopeForFamilyKey maps a JS/TS-family scope key to its version-matched
// node scope key (e.g. "typescript-v5" -> "node-v5"), so a scoped query for a
// TS/JS scope also surfaces the package.json dependency graph stored in the
// dedicated node scope. Returns false for non-family keys.
func nodeScopeForFamilyKey(key string) (string, bool) {
	for _, lang := range []model.Language{
		model.LangTypeScript, model.LangJavaScript, model.LangTSX, model.LangJSX,
	} {
		prefix := string(lang) + "-"
		if strings.HasPrefix(key, prefix) {
			return string(model.LangNode) + "-" + key[len(prefix):], true
		}
	}
	return "", false
}
```

Ensure `strings` and `github.com/specscore/codegrapher/model` are imported in `indexer/indexer.go` (model almost certainly already is; add `strings` if missing). The node key is only materialized into a store if that scope actually exists (the existing selection loop filters `want` against `idx.reg.Scopes()`), so adding a non-existent `node-vN` is harmless.

- [ ] **Step 4: Run to verify it passes**

Run: `CGO_ENABLED=0 go test ./indexer/ -run TestStoresFilteredMergesNodeScope`
Expected: PASS.

- [ ] **Step 5: Build + gofmt + vet + commit**

Run: `CGO_ENABLED=0 go build ./...`; `gofmt -l indexer/` (no output); `CGO_ENABLED=0 go vet ./indexer/` (clean).

```bash
git add indexer/indexer.go indexer/init_test.go
git commit -m "feat(indexer): merge node scope into JS/TS-family scoped queries

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Extend ts-small fixture, generalize rebaseline script, full gates

**Files:**
- Create: `testdata/fixtures/ts-small/package.json`
- Modify: `tools/parity/rebaseline-golden.sh`
- Modify: `testdata/golden/**` (regenerated)
- Modify: any hardcoded ts-small test assertion (test code) revealed by the suite

- [ ] **Step 1: Add the package.json fixture**

Create `testdata/fixtures/ts-small/package.json` (NO `typescript` dep and NO `engines.node`, so version detection stays `v0` and the existing TS files are NOT re-bucketed; `left-pad` is intentionally in both `dependencies` and `devDependencies` to exercise dedup):

```json
{
  "name": "ts-small",
  "version": "1.0.0",
  "dependencies": {
    "left-pad": "^1.3.0"
  },
  "devDependencies": {
    "vitest": "^1.0.0",
    "left-pad": "^1.3.0"
  },
  "peerDependencies": {
    "react": ">=18"
  },
  "optionalDependencies": {
    "fsevents": "^2.3.0"
  }
}
```

- [ ] **Step 2: Confirm the multi-scope break in the rebaseline script**

Read `tools/parity/rebaseline-golden.sh`. Its `capture()` function resolves a single DB via `local dbs=("$dir"/.codegraph/codegraph-*.db)` then `local db="${dbs[0]}"`, and dumps `nodes`/`edges`/`unresolved_refs` from that one `$db`. With ts-small now producing TWO scope DBs (`codegraph-node-v0.db`, `codegraph-typescript-v0.db`), `${dbs[0]}` is `codegraph-node-v0.db` (sorts first) — so the extraction/resolution dumps would lose all TypeScript rows. This must be generalized to UNION across all scope DBs.

- [ ] **Step 3: Generalize the DB dumps to UNION across scopes**

In `tools/parity/rebaseline-golden.sh`, add this helper near the top (after `normalize_json`):

```bash
# sqlite_union OUT SELECT TABLE WHERE ORDER DB1 [DB2 ...]
# Dumps `SELECT <SELECT> FROM <TABLE> [WHERE <WHERE>] ORDER BY <ORDER>` UNION-ed
# across all given scope DBs (same schema), as a single -json array.
sqlite_union() {
  local out="$1" select="$2" table="$3" where="$4" order="$5"; shift 5
  local dbs=("$@")
  local main="${dbs[0]}"
  local attaches="" union=""
  union="SELECT $select FROM \"$table\""
  [ -n "$where" ] && union="$union WHERE $where"
  local i=0 db
  for db in "${dbs[@]:1}"; do
    i=$((i+1))
    attaches="$attaches ATTACH '$db' AS s$i;"
    local u="SELECT $select FROM s$i.\"$table\""
    [ -n "$where" ] && u="$u WHERE $where"
    union="$union UNION ALL $u"
  done
  sqlite3 -json "$main" "$attaches $union ORDER BY $order;" > "$out"
  normalize_json "$out"
}
```

Then, in `capture()`, REMOVE the single-DB resolution (`local db="${dbs[0]}"` and any "exactly one" assumption) and KEEP the glob `local dbs=("$dir"/.codegraph/codegraph-*.db)`. Replace the four `sqlite3 -json "$db" "SELECT ..."` blocks with `sqlite_union` calls (same columns/filters/order as today):

```bash
  sqlite_union "$out/extraction-nodes.json" \
    "$NODE_COLS" "nodes" "" "id" "${dbs[@]}"

  sqlite_union "$out/extraction-contains.json" \
    "source,target,kind" "edges" "kind='contains'" "source,target" "${dbs[@]}"

  sqlite_union "$out/extraction-unresolved.json" \
    "from_node_id,reference_name,reference_kind,line,col,candidates,file_path,language" \
    "unresolved_refs" "" "from_node_id,reference_name,line" "${dbs[@]}"

  sqlite_union "$out/resolution-edges.json" \
    "source,target,kind,provenance,line,col" "edges" "kind != 'contains'" \
    "source,target,kind,line,col" "${dbs[@]}"
```

(The CLI goldens — `status`/`files`/`query`/`callers`/`callees`/`impact` — are produced by running `$BIN` and already fan across all stores, so they need no change.)

- [ ] **Step 4: Verify the script runs and produces sane output**

Run: `bash tools/parity/rebaseline-golden.sh`
Requires `sqlite3` and `python3` on PATH — if missing, STOP and report BLOCKED (do not hand-edit goldens).
Expected: finishes and prints a `git diff --name-only` list under `testdata/golden/`. Sanity-check:
- `testdata/golden/ts-small/extraction-nodes.json` must STILL contain the TypeScript symbol nodes (store/cache/etc.) AND now ALSO a `package.json` file node + `module` nodes (`ts-small`, `left-pad`, `vitest`, `react`, `fsevents`). If the TS nodes vanished, the UNION is wrong — fix before continuing.
- `go-small` goldens should be unchanged (still single-scope).

- [ ] **Step 5: Run the full suite; fix hardcoded fixture assertions (test code only)**

Run: `CGO_ENABLED=0 go test -count=1 ./...`
Expected failures are golden/fixture-baseline tests reacting to the new ts-small content. For each failing NON-golden assertion that hardcodes a ts-small count or file list (e.g. an `internal/cli` end-to-end file-count, or a `ScanDirectory(ts-small)` `want` list), update the test-code expectation to include the new `package.json` file / node — this is a legitimate consequence of indexing package.json, and editing test code (NOT goldens) is allowed. Do NOT hand-edit anything under `testdata/golden/**`. Re-run until `CGO_ENABLED=0 go test -count=1 ./...` is fully green.

- [ ] **Step 6: Final gates**

Run, all must pass:
- `gofmt -l .` (only the pre-existing `snapshot/ingitdb.go` nit is acceptable; nothing you touched).
- `CGO_ENABLED=0 go vet ./...` (clean).
- `CGO_ENABLED=0 go build ./...` (OK).
- `CGO_ENABLED=0 go test -count=1 ./...` (all packages PASS).

- [ ] **Step 7: Commit**

```bash
git add testdata/fixtures/ts-small/package.json tools/parity/rebaseline-golden.sh testdata/golden
# plus any test-code files you updated in Step 5
git commit -m "test: index package.json in ts-small fixture; multi-scope golden dumps

Adds a package.json to ts-small (new node-v0 scope) and generalizes the
rebaseline script to UNION extraction/resolution dumps across all scope DBs.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review notes

- **Spec coverage:** Part A (Tasks 1-3), Part B schema (Task 4), detection (Task 5), extractor + edges (Task 6), dedicated node scope (Task 7), client merge (Task 8), fixture + goldens + multi-scope tooling (Task 9). All spec sections mapped.
- **Type consistency:** `pkgjson.File` (Task 1) used unchanged in Tasks 2-3, 6. `LangPackageJSON`/`LangNode` (Task 4) used in 5-8. `extractPackageJSON`/`pkgSignature` defined once (Task 6). `scopeLanguage` (Task 7) and `nodeScopeForFamilyKey` (Task 8) defined once. The shared `addModuleNode` is generalized in Task 6 before reuse.
- **Determinism:** the extractor sorts dependency names per category (json maps are unordered) and the rebaseline UNION applies `ORDER BY`, so golden output is stable.
- **Open risk:** the `mcp/queryutils.go` brace adjustment in Task 3 (removing one nesting level) requires care — the implementer must read the surrounding lines, not blind-replace. Flagged in the task.
```
