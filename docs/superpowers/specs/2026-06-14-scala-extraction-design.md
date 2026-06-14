# Scala full-intelligence extraction — design (delta off the JVM languages)

**Date:** 2026-06-14
**Sub-project:** wave 4 Track B, language 1 (Scala). Branches from
`integration/languages`. **Reuses `resolve/resolve_jvm.go`** (the same helpers
Kotlin uses), parameterized by `model.LangScala`.

Scala is JVM + statically typed; package/import resolution falls out of the
existing JVM helpers, like Kotlin.

## Integration points
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangScala` → `grammars.ScalaLanguage()`. |
| `model/model.go` | `LangScala Language = "scala"`. No new node/edge kinds. |
| `internal/extract/detect.go` | `.scala`, `.sc` → `LangScala`. |
| `internal/extract/extract.go` | Parse + dispatch `LangScala` → `walkScala`. |
| `internal/extract/walk_scala.go` (new) | AST visitor. |
| `resolve/resolve.go` | `case model.LangScala` → `resolveScalaRef` delegating to `resolveJVMRef(ref, model.LangScala, …)` + Scala extras. |
| `scope/scope.go` | Scala scope; version = fallback `v0`. |

## Symbol model (tree-sitter-scala node kinds → model kinds)
- `package_clause` → `KindNamespace`
- `class_definition` → `KindClass`; `case class` → `KindClass`; `trait_definition` → `KindInterface`; `object_definition` / `companion`→ `KindClass` (singleton; document)
- `enum_definition` (Scala 3) → `KindEnum`; cases → `KindEnumMember`
- `function_definition` (`def`) → `KindMethod` inside a class/object/trait, `KindFunction` at package/top scope
- `val_definition`/`var_definition` → `KindField` in a template body, `KindConstant` (`val` UPPER/top-level) / `KindVariable` otherwise
- `type_definition` → `KindTypeAlias`
- `import_declaration` → `KindImport`

Flags: `visibility` (`private`/`protected` modifiers), `isAbstract`,
`typeParameters` ← generics. Scala `given`/`using`/implicits → OUT of scope
(extract `given` as a `KindField`/skip; document).

## Edges
`contains`, `calls` (`call_expression` / method calls — strip `this`/`self`),
`imports` (`import a.b.C`, `import a.b.{C, D}`, `import a.b._` wildcard →
through-import to real def), `extends` (superclass in `extends`),
`implements` (mixed-in traits via `with` → implements; classify the first
parent as extends if it's a class, the rest/`with` as implements — like
Kotlin's delegation-specifier logic), `references`, `instantiates` (`new T(...)`
→ instantiates; also `apply`-style `T(...)` companion construction promotes to
instantiates when `T` is a class). No `decorates` (annotations `@foo` →
`decorates`/`references` best-effort).

## Resolution (reuse JVM helpers)
`resolveScalaRef` builds per-file context via `getJVMContext` (package +
imports + wildcards; Scala `import a.b._` is the wildcard form, `import a.b.{x
=> y}` renames), then resolves names/calls/imports/extends/implements via the
existing `resolveJVMTypeByName`/`resolveJVMMethodByName`/`resolveJVMImportsRef`
with `model.LangScala`. Scala extras: top-level `def`/`val` (package scope) are
first-class call targets; `object` members resolve like static members.
Determinism preserved (build context before resolving). Reuse the JVM helpers'
through-import + calls→instantiates promotion.

## Fixtures (`scala-small`) + goldens + external corpus
A package with a `trait` + a base `class`, a derived `class` extending the base
`with` the trait and overriding a method + a `val` field, an `object`
(companion) with an `apply`, an `enum`; a second file that `import`s the class,
constructs it (`new`/`apply`), and calls a method. Register in the four
harnesses + `rebaseline-golden.sh` (+ MCP branch). Self-baselined goldens.
Opt-in external corpus: one small real Scala repo pinned by verified sha
(`lang: "scala"`), env-gated, non-golden.

## Out of scope
sbt/build version detection (fallback `v0`); implicits/`given`-`using`
resolution; trait linearization order; macro/`inline`; for-comprehension
desugaring; path-dependent types; signature-based overload resolution
(deterministic name pick, documented).
