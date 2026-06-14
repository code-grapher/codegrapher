# Lua full-intelligence extraction — design (delta off Python)

**Date:** 2026-06-14
**Sub-project:** wave 5 (Lua). Branches from `integration/languages`. Dynamic;
borrows Python's dynamic resolver. **LOW structural payoff** (no native classes;
OOP via tables/metatables — partial recovery only). Sets expectations: functions,
calls, and `require` edges are the meat; the class/method graph is thin.

## Integration points
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangLua` → `grammars.LuaLanguage()`. |
| `model/model.go` | `LangLua Language = "lua"`. No new node/edge kinds. |
| `internal/extract/detect.go` | `.lua` → `LangLua`. |
| `internal/extract/extract.go` | Parse + dispatch `LangLua` → `walkLua`. |
| `internal/extract/walk_lua.go` (new) | AST visitor. |
| `resolve/resolve.go` | `case model.LangLua` → `resolveLuaRef` (by-name + through-require). |
| `scope/scope.go` | Lua scope; version = fallback `v0`. |

## Symbol model (tree-sitter-lua node kinds → model kinds)
- `function_declaration` with a plain name (`function f() … end`) → `KindFunction`
- `function_declaration` with a dotted/method name (`function M.f()` / `function M:f()`) → `KindMethod` (qualified `M::f`; `:` form is a method with implicit `self`); the table `M` is the closest thing to a class — emit M (if assigned `local M = {}`) as `KindModule` and attach M.f under it
- `local function` → `KindFunction` (local)
- top-level `local x = …` / assignment → `KindVariable`; UPPER or `local X = {}` module table → `KindConstant`/`KindModule` (heuristic: a table that later receives `function T.x()` defs → `KindModule`)
- `field` in a table constructor that is a function → `KindMethod`/`KindFunction`
- `require("mod")` call → `KindImport`

Flags: none meaningful (Lua has no visibility/static/async). `local` → mark as
private visibility (best-effort).

## Edges
`contains` (module table → its functions; enclosing function → nested), `calls`
(`function_call` — for `obj:method()`/`obj.method()` strip the receiver to the
method name like Python's `self`; `M.f()` keeps `M.f`), `imports`
(`require "mod"` / `require("mod")` → the required module; resolve to the
in-repo file when the module path maps to a file), `references` (best-effort),
`instantiates` n/a (no constructors — `Foo.new()`/`setmetatable` patterns are
just calls; a call to a `*.new` function may resolve as a normal call — do NOT
synthesize instantiates this pass). No extends/implements/decorates.

## Resolution (dynamic, simplest — by-name + require)
`resolveLuaRef` mirrors the simplest Python path:
1. `require "mod"` resolves to the in-repo module file when the name maps to a
   file path (Lua module names use `.`/`/` separators → file path); through-
   require so a call to a function exported by a required module resolves to its
   definition.
2. Bare and `M.f` names resolve by name against the global table (same-file/dir
   preference). Lua stdlib globals (`print`, `pairs`, `ipairs`, `type`,
   `tostring`, `setmetatable`, `require`, `table`, `string`, `math`, `io`, `os`)
   are skipped — no edges.
Determinism preserved. No constructor inference (tables are too dynamic to infer
deterministically — documented).

## Fixtures (`lua-small`) + goldens + external corpus
A module file `shape.lua` that does `local Shape = {}`, defines
`function Shape.new(...)` and `function Shape:area()`, `return Shape`; a second
file `main.lua` that `require "shape"`, calls `Shape.new(...)` and a method.
Register in the four harnesses + `rebaseline-golden.sh` (+ MCP branch).
Self-baselined goldens. Opt-in external corpus: one small real Lua repo pinned by
verified sha (`lang: "lua"`), env-gated, non-golden.

## Out of scope (and explicitly thin)
Metatable/`setmetatable`-based inheritance recovery; `self`-type inference;
`Foo.new()` → instantiates synthesis; OOP-library conventions (middleclass,
etc.); dynamic `_G`/`rawset`; coroutines; the class/method graph is intentionally
shallow (functions + calls + require only).
