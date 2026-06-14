# R extraction design (codegrapher)

Date: 2026-06-14. Status: implemented.

R is dynamically typed and **structurally thin**: it has no native classes.
OOP is convention via S3/S4/R5, recovered only best-effort. Like Lua, the
real graph is **functions + calls + library/source edges** — a thin class
graph is EXPECTED, not a defect. The Lua walker (`walk_lua.go`) and
`resolveLuaRef` are the templates; `source("x.R")` mirrors Lua's `require`
through-import resolution.

## Symbol model (thin)

- `name <- function(args) {}` (and `name = function`, `name <<- function`)
  → **KindFunction** — the dominant construct. Function bodies are walked
  for nested functions (contains) and calls.
- Top-level `X <- value` (non-function): **KindVariable**; ALL-CAPS name
  → **KindConstant**.
- `setClass("T", ...)` (S4): **KindClass**, best-effort, name = first string arg.
- `setGeneric("g", ...)` / `setMethod("g", ...)`: **KindMethod**, best-effort,
  name = first string arg.
- `library(pkg)` / `require(pkg)`: **KindImport** (package node).
- `source("file.R")`: **KindImport**, name = basename minus `.R` (so it
  resolves to the in-repo file, through-source).

No new node/edge kinds. No `instantiates`: `new("T")` / `T$new()` are just
calls and resolve (or not) as normal calls.

## Edges

- **contains**: nested function definitions (node stack).
- **calls**: `f(args)`; `pkg::f(args)` keeps the `pkg::f` name (usually
  external → import node); `obj$method(...)` → best-effort method call.
- **imports**: `library`/`require` → package import node; `source("x.R")` →
  the in-repo R file (through-source like Lua's require).
- **references**: non-call name uses (rare; same path as calls).

## Resolver (`resolveRRef`, mirrors `resolveLuaRef`)

- `source("x.R")` import → in-repo file node whose basename minus `.R`
  matches (through-source: a bare call to a function defined in a sourced
  file resolves cross-file).
- `pkg::f` keeps the namespace; usually external → resolves to the package
  import node (generic fallback) rather than an in-repo def.
- bare function names: same-file def → through-source def → any unambiguous.
- Skip a small R base-builtin set (c/length/print/cat/paste/lapply/sapply/
  vapply/return/list/data.frame/...).

## Thinness / deviations

- No constructor/type inference (R values too dynamic to infer
  deterministically) — documented, same stance as Lua.
- S4/R5 class graph is shallow (best-effort name capture only); methods are
  not linked to their class. This is intentional thinness.
