# Elixir full-intelligence extraction — design (delta)

**Date:** 2026-06-14
**Sub-project:** wave 5 (Elixir). Branches from `integration/languages`.
Functional, module+function paradigm — **no classes**. Modules namespace
functions; `defprotocol`/`defimpl` are the nearest analog to interfaces.

## Integration points
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangElixir` → `grammars.ElixirLanguage()`. |
| `model/model.go` | `LangElixir Language = "elixir"`. No new node/edge kinds. |
| `internal/extract/detect.go` | `.ex`, `.exs` → `LangElixir`. |
| `internal/extract/extract.go` | Parse + dispatch `LangElixir` → `walkElixir`. |
| `internal/extract/walk_elixir.go` (new) | AST visitor. |
| `resolve/resolve.go` | `case model.LangElixir` → `resolveElixirRef` (alias/import + by-name). |
| `scope/scope.go` | Elixir scope; version = fallback `v0`. |

## The grammar reality (IMPORTANT)
tree-sitter-elixir is a LOW-LEVEL grammar: nearly everything is a `call` node
(`defmodule`, `def`, `defp`, `defmacro`, `defstruct`, `import`, `alias`, `use`,
`defprotocol`, `defimpl` are all macro `call`s with a target identifier + args /
`do_block`). Your walker must recognize these by the call target's text, not by
distinct node types. PROBE the grammar first to confirm the call/arguments/
do_block/alias structure before implementing.

## Symbol model (recognized macro calls → model kinds)
- `defmodule Name do … end` → `KindModule` (Name is a dotted alias `A.B.C`; push on nodeStack so member functions qualify as `A.B.C::func`)
- `def name(...)` → `KindFunction` (public); `defp name(...)` → `KindFunction` with private visibility
- `defmacro`/`defmacrop` → `KindFunction` (macro)
- `defstruct [...]` → `KindStruct` named after the enclosing module (fields = the keyword list → `KindField`)
- `defprotocol Name do … end` → `KindInterface`; its `def` signatures → `KindMethod`
- `defimpl Proto, for: Type do … end` → emit an `implements` ref (Type → Proto) and the impl's `def`s as methods
- module attributes `@name value` → `KindConstant` when it's a constant-style attribute (`@moduledoc`/`@doc` → skip; `@x 1` const → KindConstant)
- `import`/`alias`/`require`/`use` calls → `KindImport`

Flags: `visibility` (def=public, defp=private). No static/async/generics.

## Edges
`contains` (module → its functions), `calls` (`call` whose target is a function
— `Module.func(...)` keeps `Module.func`; bare `func(...)` is a local call;
strip nothing special), `imports` (`alias A.B.C [, as: D]`, `import M`,
`require M`, `use M` → the module; through-import to the real module/function),
`implements` (`defimpl Proto, for: Type` → Type implements Proto),
`references` (best-effort). No extends/instantiates/decorates (no inheritance,
no constructors — `%Struct{}` literal could be `references`/skip; `Struct.new`
is just a call).

## Resolution (alias/import + by-name)
`resolveElixirRef`:
1. `alias A.B.C` binds the last segment `C` → the full module `A.B.C`; `alias
   A.B.C, as: D` binds `D`. `import M` brings M's functions into scope. Resolve
   imported/aliased names THROUGH to the real module/function definition (reuse
   the through-import pattern).
2. `Module.func` calls resolve to that module's function by name (the module may
   be an alias → expand first). Bare `func` calls resolve to a same-module or
   imported function. Same-module → imported/aliased → any preference.
3. `defimpl ... for: Type` → `implements` edge resolves Type and Proto by name.
Elixir/Kernel auto-imported builtins (`is_nil`, `def`, `case`, `if`, `IO`,
`Enum`, `String`, `Map`, etc. — keep a small skip set for the most common
Kernel funcs that would create noise). Determinism preserved.

## Fixtures (`elixir-small`) + goldens + external corpus
A `defprotocol` (e.g. `Shape` with `def area(s)`); a `defmodule` with a
`defstruct` + functions, a `defimpl Shape, for: ThatModule`; a second module that
`alias`es the first and calls a `Module.func`. Register in the four harnesses +
`rebaseline-golden.sh` (+ MCP branch). Self-baselined goldens. Opt-in external
corpus: one small real Elixir repo pinned by verified sha (`lang: "elixir"`),
env-gated, non-golden.

## Out of scope
mix.exs/version detection (fallback `v0`); macro expansion; behaviours
(`@behaviour`/`@callback`) beyond best-effort; pattern-matching multi-clause
function merging (each `def name` clause may emit a node — dedup by name+arity
where cheap, else accept multiple, document); pipe-operator dataflow;
`%Struct{}` type inference.
