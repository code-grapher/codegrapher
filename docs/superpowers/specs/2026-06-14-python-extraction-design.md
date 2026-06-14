# Python full-intelligence extraction — design

**Date:** 2026-06-14
**Status:** Approved (design); implementation plan pending
**Sub-project:** 1 of 3 (Python; C# and Java follow as separate spec cycles)

## 1. Goal & scope

Add **Python 3** as a first-class indexed language in codegrapher, with both
symbol extraction and deterministic cross-file resolution (including
best-effort attribute/type inference). The result must support the full
code-intelligence surface — search, callers/callees, impact — not just
file-level records.

In scope:
- Python 3 grammar binding, language detection, symbol extraction, resolution,
  scope/version detection, and a hand-built golden fixture corpus.

Out of scope (this spec):
- C# and Java (each gets its own spec; their resolution models differ
  substantially — namespaces, generics, overloads, static inheritance).
- Python 2 as a supported extraction target (see §7).
- `.pyi` stub cross-linking beyond treating a `.pyi` file as ordinary source.
- Decorator *semantics* beyond emitting a `decorates` edge.

## 2. Decisions captured during brainstorming

| Decision | Choice |
|---|---|
| Support depth | Full intelligence: symbols **and** cross-file edges. |
| Resolution depth | Import + name matching + class inheritance **plus** best-effort attribute/type inference. |
| Symbol granularity | Maximal: core defs, module vars + class attrs, nested funcs + properties, imports as nodes. |
| Test corpus | Hand-built `py-small` (golden net) **plus** a vendored real project cloned by pinned sha (informational, opt-in). |
| Python 2 | Py3 primary; a Py2 project is cloned as an informational degradation corpus only — no Py2 extractor code. |
| Parity baseline | None. Upstream codegraph is not used as a reference; we design fresh against our own deterministic goldens (per the 2026-06-11 mandate change). |

## 3. Architecture — integration points

Python follows the existing **TypeScript** extraction path (tree-sitter based),
not the Go path. Go uses `go/parser` as its primary scanner with gotreesitter
only as a test oracle (ADR-003); Python has no Go-stdlib parser, so the
gotreesitter Python grammar is the primary and only parser.

| Layer | File | Change |
|---|---|---|
| Grammar binding | `internal/tsparse/tsparse.go` | Add `LangPython` const → `grammars.PythonLanguage()` in `NewParser`. |
| Language model | `model/model.go` | Add `LangPython Language = "python"`. No new node/edge kinds (existing set suffices). |
| Detection | `internal/extract/detect.go` | `.py`/`.pyi` → `model.LangPython`. Generated-file regexes (`_pb2.py`, `_pb2_grpc.py`, `_pb2.pyi`) already present. |
| Extraction dispatch | `internal/extract/extract.go` | Parse `LangPython` with the Python grammar; dispatch to `walkPython`. |
| Symbol extraction | `internal/extract/walk_python.go` (new) | AST visitor — the bulk of the work. |
| Resolution | `resolve/resolve.go` | New Python resolution branch alongside Go/TS. |
| Scope/version | `scope/scope.go` | Python scope; version detection (default `v3`). |

No changes to `model.go` node/edge **kind** sets — the full kind set is already
retained from upstream and covers every Python construct below.

## 4. Node & edge model

### Nodes (maximal granularity)

| Construct | NodeKind | Notes |
|---|---|---|
| File | `file` | Already emitted by `emitFileNode`. |
| Module | covered by the file node | Python module == file; no separate `module` node. |
| Class | `class` | |
| Function (module-level) | `function` | |
| Method (class body) | `method` | |
| Async function/method | `function`/`method` | `isAsync = true`. |
| Nested function / closure | `function` | Contained by its enclosing function. |
| `@property` accessor | `property` | |
| Module-level assignment | `constant` if name is UPPER_SNAKE or `__all__`-style; else `variable` | |
| Class attribute / dataclass field / `self.x` | `field` | |
| Import statement | `import` | Both `import x` and `from x import y`. |

### Edges

| Edge | Source → target |
|---|---|
| `contains` | Enclosing node → nested node (lexical nesting). |
| `calls` | Function/method → called function/method. |
| `imports` | File/module → imported symbol or module. |
| `extends` | Class → base class. |
| `references` | Node → referenced symbol (non-call name use). |
| `decorates` | Decorator → decorated function/class. |
| `instantiates` | Caller → class constructed via `Foo()`. |

## 5. Resolution design (deterministic)

Resolution must be a **pure function of the file set** — independent of file
processing order — so that the immutable self-goldens stay stable.

Two passes:

1. **Symbol table build.** From all extracted nodes plus import bindings,
   construct a global table keyed by module path and qualified name. Record
   each module's imported names and what they bind to.

2. **Reference resolution**, per unresolved reference, in priority order:
   1. **Explicit imports** — `from x import y` / `import x.y` bindings.
   2. **Same-module bindings** — names defined in the same file.
   3. **Class MRO by name** — walk base classes (resolved by name) to find
      inherited methods/attributes.
   4. **Best-effort inference** — track `name = ClassName(...)` and
      `self.attr = ClassName(...)` constructor assignments to assign a type to
      local variables and attributes; then resolve `var.method()` /
      `self.attr.method()` against that class's methods.

Inference is intentionally conservative: only direct constructor assignments
produce a type binding. Anything ambiguous or unresolvable becomes an
`UnresolvedReference` row (identical handling to Go/TS) — never a guessed edge.

## 6. Testing

### Default gate (hermetic, deterministic)
- **`testdata/.../py-small`** — hand-built corpus, one small file per construct:
  class, inheritance, async def, decorator, dataclass, `@property`,
  cross-module `from`/`import`, instance-method call resolved via inference.
- Deterministic self-goldens generated by the re-baseline scripts (never
  hand-edited).
- Gate: `gofmt`, `go vet ./...`, `CGO_ENABLED=0 go build ./...`,
  `CGO_ENABLED=0 go test -count=1 ./...`, all fixture goldens + the binary
  parity test green.

### External corpus (informational, opt-in, NOT a golden)
- Cloned at a **pinned commit sha** into a gitignored cache dir
  (`testdata/external/`), driven by a manifest of `{repo, sha}` entries.
- Gated behind `CODEGRAPH_EXTERNAL_CORPUS=1` (or a build tag) so the default
  gate stays hermetic and offline.
- Two projects: one Python 3 package (parse coverage + perf), one Python 2
  package (informational degradation measurement only).
- Used for parse-success and performance checks, **not** as a golden baseline
  (network + size make it a poor immutable reference).

## 7. Python 2 handling

The tree-sitter `python` grammar targets Python 3. Python-2-only syntax —
`print` statements, `except Exc, e:`, old-style classes, backtick repr, `<>` —
parses as `ERROR` nodes, yielding partial extraction. We accept this
degradation; there is **no Py2-specific extractor code**. The Py2 external
corpus exists solely to measure how lossy that degradation is.

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Type-inference noise / false edges | Conservative rule (direct constructor assignments only); uncertainty → `UnresolvedReference`. |
| gotreesitter Python grammar parse blow-ups | Bounded by the existing `parseTimeout` (KNOWN-BUGS D-2). |
| Golden churn on grammar updates | Real-world corpus is opt-in and non-golden; only the small hand-built corpus gates. |
| Policy: language work was parked | Owner reversed the park for Python/C#/Java (2026-06-14); `CLAUDE.md` to be updated as part of implementation Phase 0. |

## 9. Follow-on

C# and Java are tracked as separate sub-projects, each with its own
spec → plan → implementation cycle, using the Python implementation as the
structural template (grammar binding, detection, `walk_*.go`, resolver branch,
scope, goldens). Their resolution layers diverge enough (namespaces, generics,
overload resolution, static inheritance) to warrant independent design.
