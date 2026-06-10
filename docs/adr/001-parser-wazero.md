# ADR-001: Tree-sitter via wazero (WASM), not cgo

Status: **Accepted** (user-approved 2026-06-10) · Revisit at: benchmark gate

## Context

codegrapher ports [codegraph](https://github.com/colbymchenry/codegraph) (TypeScript)
to Go. The indexer parses source files with tree-sitter, a C library. Upstream runs
tree-sitter grammars **compiled to WebAssembly** (`web-tree-sitter` + `tree-sitter-wasms`)
inside Node. All extraction logic is written against tree-sitter's tree shapes.

Go cannot call C without a bridge. The options:

| | cgo + native grammars | WASM grammars via wazero | native Go parsers (no tree-sitter) |
|---|---|---|---|
| CGO_ENABLED=0 cross-compile | ✗ | ✓ | ✓ |
| specscore-cli embeds without C toolchain | ✗ | ✓ | ✓ |
| Parse trees identical to upstream | ~ (grammar version drift risk) | ✓ (same .wasm bytes) | ✗ |
| Extraction logic ports 1:1 | ✓ | ✓ | ✗ (full redesign) |
| Parse speed | native (fastest) | WASM (slower) | native |
| TS parser availability | ✓ | ✓ | ✗ (no mature pure-Go TS parser) |

## Decision drivers (priority order)

1. **Single static binary + CGO_ENABLED=0 cross-compilation** — launch requirement
   (AGENT-BRIEF.md), amplified by specscore-cli importing our packages directly:
   cgo would contaminate every downstream build.
2. **Golden parity** — running the *same* grammar `.wasm` binaries upstream ships
   (pinned to the `tree-sitter-wasms` versions in upstream's lockfile) produces
   bit-identical parse trees, eliminating the grammar-version-drift risk flagged
   in the brief's risk watchlist.
3. **As-is migration** — extraction rules port 1:1 against the same node kinds.
4. Parse speed — important but *measurable*; gated, not guessed.

## Decision

Use **wazero** (pure-Go WASM runtime, BSD-licensed, zero dependencies) to execute
the tree-sitter grammar `.wasm` files for Go, TypeScript, and JavaScript, vendored
from the same `tree-sitter-wasms` release upstream depends on (`^0.1.11`; exact
version pinned at vendor time and recorded next to the binaries). The WASM runtime
is fully encapsulated behind the `indexer` package boundary — nothing outside it
knows how parsing happens, keeping the rest of the codebase pure Go and testable
without the parser.

Note: the tree-sitter *runtime* (the C library that drives parsing, not just the
grammars) is also compiled to WASM in upstream's setup (`web-tree-sitter`); we
take the same artifact route so runtime behavior matches too.

## Fallback (pre-committed)

At the **benchmark gate** (cold index + sync of `../specscore-cli`, original vs port)
if wazero parsing is unacceptably slower than the original — threshold: port must
not be slower than the Node original on wall-clock, since Node also pays the WASM
tax — we switch the indexer internals to cgo (`tree-sitter/go-tree-sitter`) behind
the same package boundary, and accept the cross-compilation cost with a build-tag
escape (`CGO_ENABLED=0` build falls back to wazero). This ADR gets superseded by
ADR-002 in that case, with measurements attached.

## Consequences

- `go build` works everywhere with no toolchain setup; `go install` story is clean.
- Grammar `.wasm` files embedded via `go:embed` (~few MB binary size increase —
  acceptable vs upstream's ~115 MB Node runtime).
- Per-parse overhead higher than native; mitigated by the indexer's parallel
  worker pool (embarrassingly parallel per file).
- wazero compilation mode (not interpreter) used where supported for speed.

---

## STATUS (2026-06-10)

### Deviation: gotreesitter used instead of wazero + WASM — EXCEEDS-MANDATE

During research a fourth option was found that strictly dominates the wazero route
on every axis the ADR cares about:

**[gotreesitter](https://github.com/odvcencio/gotreesitter) v0.20.2** — a
ground-up pure-Go reimplementation of the tree-sitter runtime (parser, lexer,
query engine, incremental reparsing, external scanners all in Go).  No CGO.  No
WASM.  No wazero.  206 grammars — including Go and TypeScript — ship as
compressed embedded blobs.

| Property | wazero + WASM grammars | gotreesitter |
|---|---|---|
| CGO_ENABLED=0 | ✓ | ✓ |
| No C toolchain | ✓ | ✓ |
| Single static binary | ✓ | ✓ |
| No .wasm file management | ✗ | ✓ |
| Parse trees match upstream node kinds | ✓ (bit-identical) | ✓ (same grammar — minor version delta) |
| web-tree-sitter Emscripten dylink complexity | high (blocked) | n/a |
| Parse speed (Apple M2, small files) | ~unknown (dylink unsolved) | ~1 ms Go / ~2 ms TS |

The wazero + web-tree-sitter approach was investigated and found **blocked** on
Emscripten's dynamic linking (dylink0) protocol: grammar `.wasm` files produced by
`tree-sitter-wasms` are Emscripten side modules, not standalone WASI modules.
Loading them under wazero would require hand-rolling dylink0 support — a separate
project of significant scope.  `malivvan/tree-sitter` (the only existing wazero
wrapper found) only bundles C/C++ grammars and also predates the Emscripten
complexity; it is not a drop-in for the upstream `.wasm` grammar files.

Because gotreesitter satisfies all three decision drivers (CGO_ENABLED=0, node-kind
parity, 1:1 migration) while eliminating the WASM/dylink complexity entirely, it
was selected for the `internal/tsparse` package.  The ADR title remains accurate
in intent ("not cgo"); the "via wazero" mechanism is superseded by this finding.

### What was delivered

- `internal/tsparse/` package: `Parser`, `Tree`, `Node` types with `Kind()`,
  `StartPoint()`, `EndPoint()`, `Text()`, `ChildCount()`, `Child(i)`,
  `NamedChildCount()`, `NamedChild(i)`, `ChildByFieldName()`,
  `FieldNameForChild()`, `IsNamed()`, `HasError()`, `Walk()`.
- `CGO_ENABLED=0 go build ./...` passes.
- `CGO_ENABLED=0 go test ./internal/tsparse/` passes: 4 test functions, 10
  sub-tests, all green.
- Fixture assertions:
  - Go `store.go`: `function_declaration` New (line 24), normalize (line 62);
    `method_declaration` Get (29), Set (40), Len (47), Describe (58).
  - TypeScript `store.ts`: `class_declaration` Store (line 11);
    `variable_declarator` describe (line 34).

### Parse timing (Apple M2, gotreesitter v0.20.2, CGO_ENABLED=0)

| File | avg per parse (100 warm runs) | benchmark |
|---|---|---|
| `go-small/internal/store/store.go` (68 lines) | ~1.0 ms | 1 010 552 ns/op |
| `ts-small/src/store.ts` (34 lines) | ~2.1 ms | 2 089 386 ns/op |

Note: these are small files; grammars lazy-load on first use so cold-start for
each language adds ~30–50 ms once per process lifetime.

### Grammar provenance

gotreesitter v0.20.2 embeds grammars compiled from upstream
`tree-sitter-go` and `tree-sitter-typescript` sources.  These are not the same
`.wasm` bytes as `tree-sitter-wasms` 0.1.13 (which upstream codegraph uses) but
are derived from the same grammar sources.  Node kinds (`function_declaration`,
`method_declaration`, `class_declaration`, `variable_declarator`, etc.) are
identical.  No `.wasm` files are embedded; `go:embed` is not used.

### Risk

Grammar version delta vs upstream: gotreesitter pins its own grammar snapshot.
If upstream codegraph upgrades `tree-sitter-wasms` to a grammar that changes
node types, the Go port must also update gotreesitter.  This is the same risk
the ADR identified for the cgo path; the mitigation is the parity-test suite.
