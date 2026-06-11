# ADR-003: Parser strategy — native parsers in production, ported walk as test oracle

Status: ACCEPTED (owner decision, 2026-06-11) — Go flip pending edge-parity
evidence; TS adoption pending upstream API availability.

## Go: stdlib go/parser in production; tree-sitter walk kept for comparison

Decision: promote the `go/parser`+`go/ast` walk (today's D-2/D-3 fallback) to
the PRIMARY production scanner for Go files. The gotreesitter-based Go walk is
retained in the codebase as a **test oracle**: a differential test runs both
walks over the fixtures and a real corpus and asserts identical emission
(nodes, contains edges, unresolved refs). Parity with the original stays
enforced by the golden harness as before.

Why:
- go/parser parses all valid Go correctly and fast — the file that costs
  gotreesitter 330 s / 7.7 GB parses in milliseconds. Removes the parse
  timeout, the RSS spike, and the fork dependency from the Go path entirely.
- Feasibility proven: the fallback walk already achieves exact node parity
  (292/292 on the pathological file; 0/0 per-file across specscore-cli).
- Upstream quirks (UB-1 …) are reproduced deliberately in the walk — the
  golden harness keeps that honest.

Precondition to flip: the edge-parity work must first make both walks'
reference emission identical; flip lands only with the differential test
green plus the full golden suite.

## TypeScript: gotreesitter now; microsoft/typescript-go when its API opens

Decision: stay on gotreesitter for TS/JS in v1 (both pathological cases seen
so far were Go-grammar-specific). Adopt **microsoft/typescript-go** (the
official TS compiler natively in Go — Apache-2.0, the TS 7 / tsgo line) as
the TS scanner once Microsoft exposes a public Go API; today the entire
implementation sits under `internal/` and is deliberately unimportable.

Owner positioning: this is a **future competitive advantage** — when TS 7
codebases arrive, codegrapher parses them with the official, much faster
native parser while the Node-based original cannot. Same parity discipline
applies: the new walk must reproduce the spec'd shapes (incl. UB-2/UB-3)
until a deliberate divergence.

## Resolution: go/types as post-v1 opt-in accuracy mode

gopls is rejected as a vehicle (editor daemon, unsupported as a library,
breaks the single-binary story). `go/packages`+`go/types` is recorded as a
post-v1 OPT-IN resolver mode (e.g. `--resolver=types`): type-checked call
binding that is deliberately MORE accurate than the original's heuristics —
a documented divergence, never the default while drop-in parity is the goal.
Cost note: requires package loading (module downloads, much higher memory/
time) — another reason it stays opt-in.
