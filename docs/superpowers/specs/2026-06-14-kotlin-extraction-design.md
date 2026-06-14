# Kotlin full-intelligence extraction — design (delta off Java)

**Date:** 2026-06-14
**Status:** Approved (lean delta); implement directly.
**Sub-project:** 4 of N (Kotlin). Branches from the integrated trunk that
already contains Python + C# + **Java**, and **reuses Java's JVM resolver
helpers** in `resolve/resolve_jvm.go`.

Delta off the Java design (`2026-06-14-java-extraction-design.md`). Same five
integration points, same `createNode`/golden/external-corpus machinery. Kotlin
is statically typed on the JVM, so its resolver should call the existing
`resolveJVMTypeByName` / `getJVMContext` / `resolveJVMImportsRef` helpers and add
only the Kotlin-specific pieces below.

## Integration points
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangKotlin` → `grammars.KotlinLanguage()`. |
| `model/model.go` | `LangKotlin Language = "kotlin"`. No new node/edge kinds. |
| `internal/extract/detect.go` | `.kt`, `.kts` → `LangKotlin`. |
| `internal/extract/extract.go` | Parse + dispatch `LangKotlin` → `walkKotlin`. |
| `internal/extract/walk_kotlin.go` (new) | AST visitor. |
| `resolve/resolve.go` | `case model.LangKotlin` → a thin `resolveKotlinRef` that delegates to the JVM helpers + Kotlin extras. |
| `scope/scope.go` | Kotlin scope; version = fallback `v0` this pass. |

## Symbol model (tree-sitter-kotlin node kinds → model kinds)
- `package_header` → `KindNamespace` (one per file)
- `class_declaration` → `KindClass`; when it declares `interface` → `KindInterface`; `object_declaration` / `companion_object` → `KindClass` (document: Kotlin singletons)
- `enum class` → `KindEnum`; `enum_entry` → `KindEnumMember`
- `function_declaration` → `KindFunction` at top level / file scope, `KindMethod` inside a class/object
- `property_declaration` → `KindProperty` (has accessors) else `KindField`; `const val`/top-level `val` UPPER → `KindConstant`
- primary-constructor `class_parameter` with `val`/`var` → `KindField`
- `import_header` → `KindImport`
- `type_alias` → `KindTypeAlias`

Flags: `isAsync` (suspend modifier → set isAsync, document), `isAbstract`,
`isStatic` (companion/object members), `visibility`, `decorators` ← annotations,
`typeParameters` ← generics. Strip trailing `?` from nullable type names before
resolution.

## Edges
`contains`, `calls` (call_expression), `imports`, `extends`/`implements`
(`delegation_specifier` list — classify constructor-invocation/superclass →
extends, plain interface type → implements, same as Java's base-list logic),
`references` (type usages), `instantiates` (Kotlin has no `new`: a
`call_expression` whose callee resolves to a class is promoted to `instantiates`
by the existing generic/JVM promotion), `decorates` (annotation → annotation
class).

## Resolution (reuse Java's JVM helpers)
`resolveKotlinRef` builds the per-file JVM context via `getJVMContext` (package
+ imports + wildcards; Kotlin `import a.b.C` and `import a.b.*` map identically),
then resolves names with `resolveJVMTypeByName` / `resolveJVMMethodByName` and
imports via `resolveJVMImportsRef`. Kotlin-specific additions:
- **Top-level functions** (no enclosing class) are first-class call targets — make sure bare-name call resolution finds them (they live at file/namespace scope).
- **Extension functions** (`fun Foo.bar()`): emit the function; resolve `recv.bar()` calls to it by name (best-effort, deterministic pick) — no receiver-type matching this pass (document).
- **Companion/object members**: resolve `Type.member()` through the type to its companion/object member.
Determinism: build per-file context before resolving; deterministic pick on ambiguity.

## Fixtures (`kt-small`) + goldens + external corpus
Mirror `java-small`: a package, an interface, a base class, a derived class
implementing it with an overridden method + a property, an `object`/companion,
an enum class, a top-level function, a cross-file `import` + constructor call +
method call. Register in the four harnesses + `rebaseline-golden.sh` (+ MCP
branch). Self-baselined goldens. Opt-in external corpus: one real Kotlin repo
pinned by verified sha (`lang: "kotlin"`), env-gated, non-golden.

## Out of scope (this pass)
build.gradle(.kts) version detection (fallback `v0`); receiver-type-precise
extension-function resolution; coroutine/flow semantics beyond calls; sealed-
class hierarchy specifics beyond extends/implements; `.kts` script top-level
statement nuances beyond declarations.
