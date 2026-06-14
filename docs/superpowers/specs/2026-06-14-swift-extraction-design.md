# Swift full-intelligence extraction — design (delta off the static langs)

**Date:** 2026-06-14
**Sub-project:** wave 4 Track B, language 2 (Swift). Branches from
`integration/languages`. Statically typed, protocol-oriented; **no namespaces**
(modules are compilation units, not declared in source). Borrows the C#/Java
static resolution patterns but with a flat (module-global) symbol space.

## Integration points
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangSwift` → `grammars.SwiftLanguage()`. |
| `model/model.go` | `LangSwift Language = "swift"`. No new node/edge kinds. |
| `internal/extract/detect.go` | `.swift` → `LangSwift`. |
| `internal/extract/extract.go` | Parse + dispatch `LangSwift` → `walkSwift`. |
| `internal/extract/walk_swift.go` (new) | AST visitor. |
| `resolve/resolve.go` | `case model.LangSwift` → `resolveSwiftRef` (flat by-name + through-import). |
| `scope/scope.go` | Swift scope; version = fallback `v0`. |

## Symbol model (tree-sitter-swift node kinds → model kinds)
- `class_declaration` → `KindClass`; `struct`/`actor` → `KindStruct`/`KindClass` (actor → class, document); `protocol_declaration` → `KindInterface`; `extension_declaration` → attach members to the extended type (qualified `Type::member`), like Rust impl blocks
- `enum_declaration` → `KindEnum`; `enum_entry`/case → `KindEnumMember`
- `function_declaration` → `KindMethod` inside a type, `KindFunction` at top level; `init` → `KindMethod` (constructor)
- `property_declaration` (`var`/`let` in a type) → `KindProperty` (computed) / `KindField` (stored); top-level `let` UPPER/const → `KindConstant`, else `KindVariable`
- `typealias_declaration` → `KindTypeAlias`
- `import_declaration` → `KindImport`

Flags: `visibility` (`public`/`private`/`fileprivate`/`internal`/`open`),
`isStatic` (`static`/`class` members), `isAsync` (`async` funcs),
`typeParameters` ← generics, `decorators` ← attributes (`@objc`, `@MainActor` →
head name) best-effort.

## Edges
`contains`, `calls` (`call_expression`, method calls — strip `self`),
`imports` (`import Foo` → module; usually external, so often unresolved/import-
node — that's fine), `extends` (superclass — the first inheritance-clause type
that is a class), `implements` (protocol conformances — the inheritance-clause
types that are protocols; deterministic rule: first parent if it resolves to a
class → extends, all protocol-resolving parents → implements; when unresolved,
default ALL inheritance-clause entries to `implements` EXCEPT a known class
first-parent — document the heuristic), `references`, `instantiates`
(`Type(...)` initializer call → instantiates to the type), `decorates`
(attribute → attribute type) best-effort. `extension Type: Protocol` adds an
`implements` edge.

## Resolution (flat, static, through-import)
Swift has no source-level namespaces, so the symbol space is module-flat:
`resolveSwiftRef` resolves names/calls/types by name against the global table
with same-file/dir preference (mirror C#'s by-name + through-import, minus the
namespace layer). `import Foo` rarely names an in-repo symbol — leave it at the
import node when no in-repo match. `extension`/`protocol` conformance and method
calls resolve by name; `Type(...)` → instantiates; method calls resolve to the
type's method (through superclass/protocol by name). Overloads → deterministic
name pick (documented). Determinism preserved.

## Fixtures (`swift-small`) + goldens + external corpus
A protocol, a base class with a method + a derived class overriding it, a struct
conforming to the protocol via an `extension`, an enum, an `init`; a second file
that constructs the types (`Type(...)`) and calls methods. Register in the four
harnesses + `rebaseline-golden.sh` (+ MCP branch). Self-baselined goldens.
Opt-in external corpus: one small real Swift repo pinned by verified sha
(`lang: "swift"`), env-gated, non-golden.

## Out of scope
SwiftPM/version detection (fallback `v0`); protocol-witness/associated-type
resolution; generic constraint solving; result-builders; property wrappers
beyond the attribute; `@objc`/runtime dynamism; operator declarations;
signature-based overload resolution.
