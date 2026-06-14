# C full-intelligence extraction — design (delta)

**Date:** 2026-06-14
**Sub-project:** wave 4 Track A, language 1 (C). Branches from
`integration/languages`. **C++ (next) reuses this `walk_c.go`** — keep the C
constructs factored so the C++ walker can call into them.

C is the SIMPLEST resolver of the batch: no namespaces, no classes. A global
symbol table + `#include` file-dependency resolution is the whole story.

## Integration points
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangC` → `grammars.CLanguage()`. |
| `model/model.go` | `LangC Language = "c"`. No new node/edge kinds. |
| `internal/extract/detect.go` | **content-aware** detection (see below): `.c` → C; `.h` → sniff. |
| `internal/extract/extract.go` | Parse + dispatch `LangC` → `walkC`. |
| `internal/extract/walk_c.go` (new) | AST visitor (factored for C++ reuse). |
| `resolve/resolve.go` | `case model.LangC` → `resolveCRef` (global-name + through-include). |
| `scope/scope.go` | C scope; version = fallback `v0`. |

## Content-aware detection (the one shared architectural change)
`DetectLanguage(path)` is path-only today and stays as-is (defaults `.h`→C for
back-compat callers). Add `DetectLanguageContent(path string, content []byte)
model.Language` that:
- delegates to `DetectLanguage` for unambiguous extensions;
- for `.h`: sniff `content` for C++ markers (`\bclass\b`, `\bnamespace\b`,
  `\btemplate\b`, `::`, `\bpublic:|private:|protected:`) → return `LangCPP`
  (constant added now as `model.LangCPP = "cpp"` so the sniff target exists even
  before the C++ walker lands; a `.h` sniffed as C++ with no C++ walker yet just
  yields a file node) else `LangC`.
- `.hpp/.hh/.hxx` (handled when C++ lands) → C++.
Call `DetectLanguageContent` at the content-available sites: `ExtractFile`'s
caller in the indexer/scan path and the parity test. Keep `DetectLanguage`
(path-only) for scan-time filtering (`IsSourceFile`) where content isn't read —
`.h` counts as source either way.

## Symbol model (tree-sitter-c node kinds → model kinds)
- `function_definition` / `declaration` of a function → `KindFunction`
- `struct_specifier` → `KindStruct`; `union_specifier` → `KindStruct`; `enum_specifier` → `KindEnum`; `enumerator` → `KindEnumMember`
- `field_declaration` (struct member) → `KindField`
- `type_definition` (`typedef`) → `KindTypeAlias`
- top-level `declaration` of a variable → `KindVariable`; `const`/`#define` object-like macro → `KindConstant`
- `preproc_include` (`#include "x.h"` / `<x.h>`) → `KindImport`
- `preproc_def`/`preproc_function_def` (macros) → `KindConstant`/`KindFunction` (function-like macro)

Flags: `isStatic` (`static` storage class). No visibility/async/generics in C.

## Edges
`contains`, `calls` (`call_expression`), `imports` (`#include` → the included
file/header node, resolved to a real header in-repo when the path matches),
`references` (type usages in fields/params/returns), `instantiates` n/a for C
(no constructors — `struct` literals are just initializers; skip). No
extends/implements/decorates.

## Resolution (simplest — global + include)
`resolveCRef`: C has a single global namespace.
1. `#include "foo.h"` resolves to the in-repo header file node when the relative
   path matches (through-include: a call to a function declared in an included
   header resolves to that function's definition anywhere in the repo).
2. Bare names (calls, type refs) resolve by name against the global symbol
   table; deterministic same-file/dir preference on ambiguity. C built-ins
   (`printf`, `malloc`, `sizeof`, `NULL`, fixed-width int types, etc.) are
   skipped (no edges) — keep a small builtin set like Go's.
Determinism preserved.

## Fixtures (`c-small`) + goldens + external corpus
A header `shape.h` declaring a struct + function prototypes; `shape.c` defining
them; `main.c` that `#include "shape.h"`, calls the functions, uses the struct.
Register in the four harnesses + `rebaseline-golden.sh` (+ MCP branch).
Self-baselined goldens. Opt-in external corpus: one small real C repo pinned by
verified sha (`lang: "c"`), env-gated, non-golden.

## Out of scope
Full preprocessor evaluation (`#if`/conditional compilation — extract the text
as-seen); macro expansion; K&R-style definitions; cross-TU linkage analysis
beyond name matching; `compile_commands.json` awareness.
