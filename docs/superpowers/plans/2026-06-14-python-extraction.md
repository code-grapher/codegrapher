# Python Full-Intelligence Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Python 3 as a first-class indexed language — symbol extraction plus deterministic cross-file resolution (with conservative attribute/type inference) — gated by a hand-built `py-small` golden corpus.

**Architecture:** Python follows the existing TypeScript tree-sitter path (parse → `walkPython` AST visitor → nodes/edges/unresolved-refs → resolver). The gotreesitter Python grammar is the sole parser (no go/parser equivalent). Resolution adds a `LangPython` branch beside Go/TS in `resolve/`.

**Tech Stack:** Go, `github.com/odvcencio/gotreesitter` (patched fork) + `grammars.PythonLanguage()`, SQLite stores, bash+sqlite3 golden re-baseline.

**Spec:** `docs/superpowers/specs/2026-06-14-python-extraction-design.md`

---

## File map

| File | Change |
|---|---|
| `CLAUDE.md` | Phase 0: record the language-work un-park for Python/C#/Java. |
| `internal/tsparse/tsparse.go` | Add `LangPython` → `grammars.PythonLanguage()`. |
| `internal/tsparse/python_test.go` (new) | Parser smoke test on a Python sample. |
| `model/model.go` | Add `LangPython Language = "python"`. |
| `internal/extract/detect.go` | `.py`/`.pyi` → `LangPython`. |
| `internal/extract/detect_test.go` | Cases for `.py`/`.pyi`. |
| `internal/extract/extract.go` | Parse + dispatch `LangPython` → `walkPython`. |
| `internal/extract/walk_python.go` (new) | The AST visitor (bulk). |
| `internal/extract/walk_python_test.go` (new) | Unit tests per construct. |
| `scope/scope.go` | Python scope/version (default `v3`). |
| `scope/scope_test.go` | Python scope case. |
| `resolve/resolve.go` | `case model.LangPython` branch + inference. |
| `resolve/resolve_test.go` | Python resolution cases. |
| `testdata/fixtures/py-small/**` (new) | Hand-built corpus. |
| `testdata/golden/py-small/**` (new) | Generated goldens (script only). |
| `internal/extract/parity_test.go` | `TestParityPySmall`. |
| `indexer/golden_test.go` | Add `"py-small"` to fixture lists. |
| `cmd/codegrapher/parity_test.go` | Add py-small fixture entry. |
| `tools/parity/rebaseline-golden.sh` | `capture py-small …` + `rebaseline_mcp py-small`. |
| `testdata/external/manifest.json` (new) | Pinned {repo, sha} for opt-in corpus. |
| `internal/extract/external_corpus_test.go` (new) | Opt-in clone+parse test. |

---

## Phase 0 — Un-park policy

### Task 0: Record the policy change

**Files:** Modify `CLAUDE.md`

- [ ] **Step 1:** Under "Current focus", append a dated note that Python/C#/Java language work is un-parked by owner decision 2026-06-14 (Python first); TS bug fixes remain parked.

- [ ] **Step 2: Commit**
```bash
git add CLAUDE.md
git commit -m "docs: un-park Python/C#/Java language work (owner 2026-06-14)"
```

---

## Phase 1 — Scaffolding (grammar, model, detect, scope, dispatch)

### Task 1: Bind the Python grammar

**Files:** Modify `internal/tsparse/tsparse.go`; Test `internal/tsparse/python_test.go`

- [ ] **Step 1: Write the failing test** — `internal/tsparse/python_test.go`
```go
package tsparse_test

import (
	"testing"

	"github.com/specscore/codegrapher/internal/tsparse"
)

func TestPythonParseSmoke(t *testing.T) {
	src := []byte("class A:\n    def m(self):\n        return 1\n")
	p, err := tsparse.NewParser(tsparse.LangPython)
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	tree, err := p.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(collectByKind(tree.RootNode(), "class_definition")) != 1 {
		t.Fatalf("want 1 class_definition")
	}
	if len(collectByKind(tree.RootNode(), "function_definition")) != 1 {
		t.Fatalf("want 1 function_definition")
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`LangPython` undefined)
```bash
CGO_ENABLED=0 go test ./internal/tsparse/ -run TestPythonParseSmoke
```

- [ ] **Step 3: Implement** — add to the `const` block and `NewParser` switch in `internal/tsparse/tsparse.go`:
```go
// in const ( ... )
	// LangPython selects the tree-sitter `python` grammar (Python 3).
	LangPython
```
```go
	// in NewParser switch, before default:
	case LangPython:
		return &Parser{lang: grammars.PythonLanguage()}, nil
```

- [ ] **Step 4: Run — expect PASS**
```bash
CGO_ENABLED=0 go test ./internal/tsparse/ -run TestPythonParseSmoke
```

- [ ] **Step 5: Commit**
```bash
git add internal/tsparse/tsparse.go internal/tsparse/python_test.go
git commit -m "feat(tsparse): bind tree-sitter python grammar"
```

### Task 2: Add the language constant + detection

**Files:** Modify `model/model.go`, `internal/extract/detect.go`; Test `internal/extract/detect_test.go`

- [ ] **Step 1: Write the failing test** — append to `internal/extract/detect_test.go`:
```go
func TestDetectPython(t *testing.T) {
	cases := map[string]model.Language{
		"a/b/foo.py":  model.LangPython,
		"stubs/x.pyi": model.LangPython,
	}
	for path, want := range cases {
		if got := DetectLanguage(path); got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`LangPython` undefined)
```bash
CGO_ENABLED=0 go test ./internal/extract/ -run TestDetectPython
```

- [ ] **Step 3: Implement**
  - `model/model.go`: add `LangPython Language = "python"` to the const block.
  - `internal/extract/detect.go`: in the `switch ext` add:
```go
	case ".py", ".pyi":
		return model.LangPython
```

- [ ] **Step 4: Run — expect PASS**
```bash
CGO_ENABLED=0 go test ./internal/extract/ -run TestDetectPython
```

- [ ] **Step 5: Commit**
```bash
git add model/model.go internal/extract/detect.go internal/extract/detect_test.go
git commit -m "feat(extract): detect Python .py/.pyi files"
```

### Task 3: Python scope/version

**Files:** Modify `scope/scope.go`; Test `scope/scope_test.go`

- [ ] **Step 1: Write the failing test** — append to `scope/scope_test.go`:
```go
func TestDetectVersionPython(t *testing.T) {
	dir := t.TempDir()
	got := DetectVersion(dir, filepath.Join(dir, "m.py"), model.LangPython)
	if got != "v3" {
		t.Fatalf("python default version = %q, want v3", got)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (returns `v0`)
```bash
CGO_ENABLED=0 go test ./scope/ -run TestDetectVersionPython
```

- [ ] **Step 3: Implement** — in `DetectVersion`'s switch in `scope/scope.go`:
```go
	case model.LangPython:
		// Python graphs are grouped under a single major; no manifest parsing
		// in this pass (PEP 621 pyproject support is a later enhancement).
		ver = "3"
```

- [ ] **Step 4: Run — expect PASS**
```bash
CGO_ENABLED=0 go test ./scope/ -run TestDetectVersionPython
```

- [ ] **Step 5: Commit**
```bash
git add scope/scope.go scope/scope_test.go
git commit -m "feat(scope): map Python files to the v3 scope"
```

### Task 4: Dispatch + empty walkPython

**Files:** Modify `internal/extract/extract.go`; Create `internal/extract/walk_python.go`

- [ ] **Step 1: Write the failing test** — `internal/extract/walk_python_test.go`:
```go
package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractPy(t *testing.T, src string) ([]model.Node, []model.Edge, []model.UnresolvedReference) {
	t.Helper()
	res, err := ExtractFile("m.py", []byte(src), model.LangPython)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return res.Nodes, res.Edges, res.UnresolvedReferences
}

func TestPyFileNodeEmitted(t *testing.T) {
	nodes, _, _ := extractPy(t, "x = 1\n")
	if len(nodes) == 0 || nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", nodes)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (no parse path for Python; file node may still emit but `walkPython` undefined once referenced)
```bash
CGO_ENABLED=0 go test ./internal/extract/ -run TestPyFileNodeEmitted
```

- [ ] **Step 3: Implement**
  - In `extract.go`, extend the parse switch to build a tree for Python:
```go
	case model.LangPython:
		p, err := tsparse.NewParser(tsparse.LangPython)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message: err.Error(), FilePath: path,
					Severity: "error", Code: "parse_error",
				})
			}
		}
```
  - In `extract.go`, extend the walk switch:
```go
	case model.LangPython:
		if tree != nil {
			e.walkPython(tree.RootNode())
		}
```
  - Create `internal/extract/walk_python.go` with the package header, imports (`tsparse`, `model`, `strings`), and an initially minimal `walkPython` that iterates named children calling `visitNodePython` (added in Phase 2):
```go
package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkPython walks a parsed Python (tree-sitter `python`) file root and
// extracts symbols. Called by ExtractFile after the file node is emitted.
func (e *extractor) walkPython(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodePython(child)
		}
	}
}

// visitNodePython dispatches a single statement node. Construct handlers are
// added incrementally; unknown kinds descend into their block bodies so calls
// nested inside control flow are still seen.
func (e *extractor) visitNodePython(node *tsparse.Node) {
	switch node.Kind() {
	default:
		e.visitPyBody(node)
	}
}

// visitPyBody descends into a node's children looking for calls and nested
// definitions without emitting a node for the container itself.
func (e *extractor) visitPyBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodePython(child)
		}
	}
}

var _ = strings.TrimSpace // retained; used by handlers added in Phase 2
```

- [ ] **Step 4: Run — expect PASS**
```bash
CGO_ENABLED=0 go test ./internal/extract/ -run TestPyFileNodeEmitted
```

- [ ] **Step 5: Commit**
```bash
git add internal/extract/extract.go internal/extract/walk_python.go internal/extract/walk_python_test.go
git commit -m "feat(extract): parse Python and dispatch to walkPython skeleton"
```

---

## Phase 2 — Symbol extraction (`walk_python.go`)

Each task adds one construct, TDD-style: a unit test in `walk_python_test.go`, then the handler in `walk_python.go`. Tree-sitter `python` node kinds used:
`function_definition`, `class_definition`, `decorated_definition`, `import_statement`, `import_from_statement`, `expression_statement` → `assignment`, `call`, `attribute`, `identifier`, `block`, `argument_list`, `parameters`, `keyword: async`.

### Task 5: Functions & methods
- [ ] **Test:** `def f(): ...` at module level → one `function` node named `f`, contained by file; `async def g(): ...` → `function` with `IsAsync`. A `def` inside a `class_definition` body → `method`.
- [ ] **Implement** `extractPyFunction(node, isMethod)`: read `name` field; kind = `KindMethod` when `e.isInsideClassLike()` else `KindFunction`; `isAsync` if an `async` child token is present; signature = first line of `node.Text()` up to `:`; docstring via the block's first `expression_statement`→`string` (add `pyDocstring(node)`); push node ID on `nodeStack`, visit `body` block, pop.
- [ ] **Verify:** `go test ./internal/extract/ -run TestPyFunction` PASS. **Commit.**

### Task 6: Classes & inheritance
- [ ] **Test:** `class A(B, C): ...` → `class` node `A`; two `references` unresolved refs with `ReferenceKind = EdgeExtends` for `B` and `C`; methods inside are contained by `A`.
- [ ] **Implement** `extractPyClass(node)`: emit `KindClass`; for each name in the `superclasses` (`argument_list`) child emit an `UnresolvedReference{ReferenceKind: model.EdgeExtends, ReferenceName: <base>}` from the class node ID; push, visit body, pop.
- [ ] **Verify** `-run TestPyClass` PASS. **Commit.**

### Task 7: Decorators
- [ ] **Test:** `@dec\ndef f(): ...` → `f` carries `Decorators=["dec"]` and a `decorates` unresolved ref (`dec` → f). `decorated_definition` wraps the function/class.
- [ ] **Implement:** handle `decorated_definition` — collect `decorator` child names, then extract the inner `function_definition`/`class_definition` passing the decorator list into `nodeExtra.decorators`; emit `EdgeDecorates` unresolved refs.
- [ ] **Verify** `-run TestPyDecorator` PASS. **Commit.**

### Task 8: `@property` accessors
- [ ] **Test:** a method decorated `@property` → node kind `property` (not `method`).
- [ ] **Implement:** in the method path, if decorators contain `property`/`cached_property`, emit `KindProperty`.
- [ ] **Verify** `-run TestPyProperty` PASS. **Commit.**

### Task 9: Imports
- [ ] **Test:** `import os` → `import` node `os` + `imports` unresolved ref; `from a.b import c, d` → `import` node(s) and `imports` refs for `c` and `d`.
- [ ] **Implement** `extractPyImport` (handles both `import_statement` and `import_from_statement`): emit `KindImport` per module/name; emit `EdgeImports` unresolved refs from the current parent (file or enclosing scope).
- [ ] **Verify** `-run TestPyImport` PASS. **Commit.**

### Task 10: Module vars, constants, class attrs, `self.x`
- [ ] **Test:** module-level `X = 1` → `constant` (UPPER or dunder) ; `x = 1` → `variable`; class-body `n: int = 0` / `n = 0` → `field`; `self.n = 0` inside a method → `field` contained by the enclosing class.
- [ ] **Implement** `extractPyAssignment`: handle `assignment` inside `expression_statement`. Module scope → `KindConstant` if name is UPPER_SNAKE or `__dunder__`, else `KindVariable`. Class body → `KindField`. `self.<attr> = …` (left is `attribute` with `self` object) → `KindField` attributed to the enclosing class node; dedup via `createNode`'s `seenNodeIDs`. Record constructor-typed assignments (`name = ClassName(...)`) into a per-file `e.pyAssignTypes` map for the resolver hint (see Task 13).
- [ ] **Verify** `-run TestPyAssignment` PASS. **Commit.**

### Task 11: Calls & instantiations
- [ ] **Test:** inside `f`, `g()` → `calls` ref `g`; `obj.m()` → `calls` ref `obj.m`; `self.m()` → `calls` ref `m` (receiver stripped, like TS); `Foo()` where `Foo` is a class → resolves later to `instantiates`.
- [ ] **Implement** `extractPyCall`: mirror `extractTSCall` receiver handling (skip `self`/`cls`/`super`). Emit `EdgeCalls` unresolved ref from the top of `nodeStack`. Wire `call` and nested defs into `visitNodePython`/`visitPyBody` so calls inside function bodies and control flow are captured.
- [ ] **Verify** `-run TestPyCall` PASS. **Commit.**

### Task 12: Nested functions
- [ ] **Test:** a `def inner()` inside `def outer()` → `function` `inner` contained by `outer`.
- [ ] **Implement:** ensure `visitPyBody` recurses into `block` bodies so nested `function_definition` nodes are visited while `outer` is on the stack (already true if Task 5 pushes the stack and visits the body via `visitNodePython`). Add explicit test coverage.
- [ ] **Verify** `-run TestPyNested` PASS. **Commit.**

---

## Phase 3 — Resolution (`resolve/resolve.go`)

### Task 13: Python resolution branch with conservative type inference
**Files:** Modify `resolve/resolve.go`; Test `resolve/resolve_test.go`

- [ ] **Step 1: Tests** (`resolve_test.go`) for: (a) `from a import f; f()` resolves `calls`→`f` across files; (b) class base resolves `extends`; (c) `x = Foo(); x.m()` resolves `calls`→`Foo.m` via inference; (d) ambiguous `x.m()` with no type stays unresolved.
- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** `case model.LangPython: return resolvePythonRef(...)` in `resolveRef`. `resolvePythonRef`:
  1. dotted `recv.attr` → if `recv`'s type is known (from the stored assignment-type hint persisted as a node attribute or recomputed from same-file constructor assignments) resolve `attr` against that class's methods/fields; else fall back to `resolveDottedRef`.
  2. bare name → `resolveGenericRef` (import + global name table), with `calls`→`instantiates` promotion already handled there for class targets.
  Inference must be order-independent: build the var→class map per file from the stored constructor assignments before resolving that file's refs.
- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** `feat(resolve): Python import + inheritance + inferred attribute resolution`.

---

## Phase 4 — Golden corpus & registration

### Task 14: Build `py-small` fixtures
- [ ] Create `testdata/fixtures/py-small/` with small files exercising every construct and one cross-module import + inferred instance call. Suggested layout:
  - `models.py` — `class Animal:` base, `class Dog(Animal):` with `@property`, `__init__` setting `self.name`, dataclass via `@dataclass`.
  - `service.py` — `from models import Dog`; module constant `MAX = 10`; `def make() -> Dog:` returns `Dog()`; a function that does `d = Dog(); d.speak()`.
  - `util.py` — module-level function, nested function, decorator definition.
- [ ] **Commit** the fixtures (sources only).

### Task 15: Register py-small in the test harnesses
- [ ] `internal/extract/parity_test.go`: add `func TestParityPySmall(t *testing.T){ testParity(t,"py-small") }`.
- [ ] `indexer/golden_test.go:393`: change list to `{"go-small", "ts-small", "py-small"}`.
- [ ] `cmd/codegrapher/parity_test.go`: append a fixture entry `{name:"py-small", query:"dog", symbols:[]string{...}}` choosing real symbols from the fixtures.
- [ ] `tools/parity/rebaseline-golden.sh`: add a `capture py-small "<query>" <symbols...>` line and `rebaseline_mcp py-small`; extend the python MCP `id_to_file`/symbol branch with a `py-small` case.
- [ ] **Commit** the harness wiring (tests will fail until goldens exist — next task).

### Task 16: Generate goldens & make the gate green
- [ ] **Step 1:** Run `tools/parity/rebaseline-golden.sh` (requires `sqlite3`, `python3`).
- [ ] **Step 2:** Inspect `git diff --stat testdata/golden/py-small/` — confirm nodes/edges look right (spot-check a few IDs/kinds against intent; goldens are never hand-edited).
- [ ] **Step 3:** Full gate:
```bash
gofmt -l . ; CGO_ENABLED=0 go vet ./... ; CGO_ENABLED=0 go build ./... ; CGO_ENABLED=0 go test -count=1 ./...
```
Expected: all green incl. `TestParityPySmall`, `indexer` golden tests, `cmd` parity.
- [ ] **Step 4: Commit** `test(py-small): add fixtures and self-baselined goldens`.

---

## Phase 5 — Opt-in external corpus

### Task 17: Clone-by-sha informational corpus (NOT a golden)
**Files:** Create `testdata/external/manifest.json`, `internal/extract/external_corpus_test.go`; Modify `.gitignore`

- [ ] **Step 1:** `.gitignore`: add `testdata/external/cache/`.
- [ ] **Step 2:** `manifest.json`: pin one Py3 repo and one Py2 repo by `{repo, sha}`.
- [ ] **Step 3:** `external_corpus_test.go`: skip unless `os.Getenv("CODEGRAPH_EXTERNAL_CORPUS") == "1"`; shallow-clone each repo at its sha into the cache dir, walk `.py` files through `extract.ExtractFile`, assert zero panics and that parse-error rate is below a logged threshold. This is a coverage/perf check — it asserts no golden.
- [ ] **Step 4:** Verify default gate still hermetic (test skips without the env var); run once with the env var set to confirm it clones and parses.
- [ ] **Step 5: Commit** `test(external): opt-in clone-by-sha Python corpus (py3 + py2 informational)`.

---

## Self-review notes
- Every spec section maps to a task: grammar→T1, model/detect→T2, scope→T3, dispatch→T4, nodes/edges→T5–T12, resolution+inference→T13, py-small goldens→T14–T16, external corpus + Py2→T17, policy→T0.
- No new model kinds required (verified against `model.go`).
- Resolution determinism is called out explicitly in T13 (order-independent per-file var→class map).
- The default gate stays hermetic; the only network task (T17) is env-gated and non-golden.
