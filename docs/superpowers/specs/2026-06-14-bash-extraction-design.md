# Bash extraction design (2026-06-14)

Bash is shell scripting: structurally **thin**. No classes, no methods, no
namespaces. The graph is intentionally just **functions + calls + source
edges**, mirroring the Lua thin-dynamic template (`walk_lua.go` /
`resolveLuaRef`).

## Symbol model

| Construct                                   | Node kind            |
|---------------------------------------------|----------------------|
| `name() { ... }` / `function name { ... }`  | `KindFunction`       |
| top-level `VAR=value`                       | `KindVariable`       |
| `declare VAR=...`                            | `KindVariable`       |
| ALL-CAPS `readonly X=` / `declare -r X=`    | `KindConstant`       |
| ALL-CAPS plain `VAR=`                        | `KindConstant`       |
| `export VAR` / `export VAR=...`             | `KindVariable` (exported) |

No new node or edge kinds. No instantiates / extends / implements.

## Grammar (tree-sitter-bash, probe-verified)

- `function_definition` — field `name` (a `word`), field `body`
  (`compound_statement`). Both `foo()` and `function foo` forms produce this.
- `command` — field `name` (`command_name` → `word`), repeated `argument`s.
  A bare call `foo` and `echo hi` are both `command`s. `source "lib.sh"` and
  `. lib.sh` are commands whose `command_name` is `source` / `.`.
- `variable_assignment` — field `name` (`variable_name`), field `value`.
- `declaration_command` — wraps an anonymous `export`/`readonly`/`declare`
  keyword, optional flag `word`s (e.g. `-r`), then a `variable_assignment`
  (or a bare `variable_name` for `export EX`).

## Edges

- **contains**: function body symbols nest under the function (node stack).
- **calls**: a `command` whose `command_name` matches a function we
  extracted → `EdgeCalls`. **Key filter**: commands that are NOT in-repo
  functions (`ls`, `echo`, `grep`, external binaries, shell builtins)
  produce **no edge**. Calls are emitted as unresolved refs; the resolver
  is the gate that only matches in-repo functions.
- **imports**: `source file.sh` / `. file.sh` → a `KindImport` node + an
  `EdgeImports` ref to the sourced file (through-source resolution, like
  Lua's `require`), so a call to a function defined in a sourced file
  resolves cross-file.

## Resolver (`resolveBashRef`, mirrors `resolveLuaRef`)

- `imports`: target the in-repo file whose basename matches the sourced
  path; else fall back to the local import node.
- `calls`/`references`: resolve a bare name to an in-repo **function** node
  (same-file → through-source import → any). Commands with no matching
  in-repo function resolve to nothing (no edge). This is the deliberate
  Bash filter — we never emit edges to shell builtins/externals.

## Scope / detect

- `.sh` / `.bash` → `LangBash`. Shebang detection is **out of scope** this
  pass (path-only).
- Scope: fallback `v0` (no package manager to parse).

## Deviations from Lua

- No module/table OOP recovery (`local M = {}` has no Bash analogue).
- Constant detection also applies to plain top-level ALL-CAPS `VAR=`
  (Bash convention), not just `readonly`/`declare -r`.
