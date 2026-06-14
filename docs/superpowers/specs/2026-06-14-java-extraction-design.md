# Java full-intelligence extraction — design (delta off Python)

**Date:** 2026-06-14
**Status:** Approved (lean delta); implementation plan to follow
**Sub-project:** 3 of N (Java). **Kotlin (sub-project 4) branches from Java**
and reuses this resolver — keep JVM resolution helpers factorable.

Delta off the Python design. Architecture, five integration points,
`createNode` machinery, golden harness, and opt-in external corpus are
identical. Java is **statically typed**; resolver is Go-like (declared types,
no dynamic inference).

## Integration points (same five as Python)
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangJava` → `grammars.JavaLanguage()`. |
| `model/model.go` | `LangJava Language = "java"`. No new node/edge kinds. |
| `internal/extract/detect.go` | `.java` → `LangJava`. Generated regexes `OuterClass\.java$`, `Grpc\.java$` already present. |
| `internal/extract/extract.go` | Parse + dispatch `LangJava` → `walkJava`. |
| `internal/extract/walk_java.go` (new) | The AST visitor. |
| `resolve/resolve.go` | `case model.LangJava` branch. Factor JVM-name-resolution helpers so Kotlin can reuse them. |
| `scope/scope.go` | Java scope; version = fallback `v0` this pass (no pom/gradle parsing yet). |

## Symbol model (tree-sitter-java node kinds → model kinds)
- `package_declaration` → `KindNamespace` (one per file; like Python module-as-file but Java names it)
- `class_declaration` → `KindClass`; `record_declaration` → `KindClass`
- `interface_declaration` → `KindInterface`
- `enum_declaration` → `KindEnum`; `enum_constant` → `KindEnumMember`
- `annotation_type_declaration` → `KindInterface` (document) 
- `method_declaration` / `constructor_declaration` → `KindMethod`
- `field_declaration` → `KindField`; `static final` → `KindConstant`
- `import_declaration` → `KindImport`
- Local `local_variable_declaration` in method bodies → `KindVariable` (call-scoping only; minimal)

Flags: `isStatic`, `isAbstract`, `visibility` (public/private/protected/
package-default), `decorators` ← Java **annotations** (`@Override`,
`@RequestMapping` → head name). Generic type parameters → `typeParameters`.
(Java has no `async`.)

## Edges
`contains`, `calls` (method_invocation), `imports` (import → type/package),
`extends` (superclass; for interfaces, the `extends` interface list),
`implements` (`implements` clause), `references` (type usages), `instantiates`
(object_creation_expression `new T()`), `decorates` (annotation → annotation
type).

## Resolution (static, Go-like)
1. **import declarations** establish type imports (`import a.b.C;`) and
   on-demand wildcards (`import a.b.*;` → namespace import). Build per-file
   imported-type map + wildcard package set. `java.lang.*` is implicit.
2. **Bare/qualified names** resolve by name against the global symbol table,
   preferring same-package → explicit imports → wildcard packages → any. Reuse
   Python's "resolve through import nodes to the real definition" so an
   imported type's constructor/call lands on the real cross-file type.
3. **extends/implements** resolve base names to their types.
4. **Overloads:** resolve by name; multiple → deterministic pick (first by start
   line), documented. No signature matching this pass.
Determinism: build per-file import maps before resolving.

## Fixtures (`java-small`) + goldens
Hand-built corpus mirroring `py-small`: a package, an interface, a base class,
a derived class implementing the interface with an `@Override` method and a
field, an enum, a cross-file `import` + `new` construction + method call.
Register in extraction parity test, indexer golden-init list, cmd binary-parity
fixtures, and `rebaseline-golden.sh` (+ MCP branch). Self-baselined goldens.
Opt-in external corpus: one real Java repo pinned by sha (informational,
env-gated, non-golden).

## Kotlin reuse note (sub-project 4)
Kotlin branches from the merged Java work. The JVM resolution helpers (import
maps, package-preference name resolution, extends/implements) should be written
so Kotlin's resolver can call them with minimal Kotlin-specific additions
(top-level functions, `object`/`companion object`, nullable types, primary
constructors, extension functions). Keep them on shared, non-Java-named helpers
where practical.

## Out of scope (this pass)
pom.xml/build.gradle version detection (fallback `v0`); signature-based overload
resolution; nested/inner-class `this$0` capture semantics; lambda/method-ref
target typing beyond calls; module-system (`module-info.java`) resolution.
