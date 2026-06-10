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
