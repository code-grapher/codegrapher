# C# full-intelligence extraction — design (delta off Python)

**Date:** 2026-06-14
**Status:** Approved (lean delta); implementation plan to follow
**Sub-project:** 2 of N (C#). Independent of Java/Kotlin.

This is a **delta** off the Python design
(`2026-06-14-python-extraction-design.md`). The architecture, the five
integration points, the `createNode`/unresolved-ref machinery, the golden
harness, and the opt-in external-corpus pattern are all identical. Only the
language-specific pieces below differ. C# is **statically typed**, so its
resolver is closer to Go's than to Python's (no dynamic inference needed —
types are declared).

## Integration points (same five as Python)
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangCSharp` → `grammars.CSharpLanguage()`. |
| `model/model.go` | `LangCSharp Language = "csharp"`. No new node/edge kinds. |
| `internal/extract/detect.go` | `.cs` → `LangCSharp`. Generated regexes `\.g\.cs$`, `Grpc\.cs$` already present. |
| `internal/extract/extract.go` | Parse + dispatch `LangCSharp` → `walkCSharp`. |
| `internal/extract/walk_csharp.go` (new) | The AST visitor. |
| `resolve/resolve.go` | `case model.LangCSharp` branch. |
| `scope/scope.go` | C# scope; version = fallback `v0` this pass (no `.csproj` parsing yet). |

## Symbol model (tree-sitter-c-sharp node kinds → model kinds)
- `namespace_declaration` / `file_scoped_namespace_declaration` → `KindNamespace`
- `class_declaration` → `KindClass`; `record_declaration` → `KindClass`
- `struct_declaration` → `KindStruct`
- `interface_declaration` → `KindInterface`
- `enum_declaration` → `KindEnum`; `enum_member_declaration` → `KindEnumMember`
- `method_declaration` / `constructor_declaration` / `operator_declaration` → `KindMethod`
- `property_declaration` / `indexer_declaration` → `KindProperty`
- `field_declaration` → `KindField`; `const`-modified → `KindConstant`
- `delegate_declaration` / `event_declaration` → `KindField` (closest existing kind; document)
- `using_directive` → `KindImport`
- Local `variable_declaration` inside method bodies → `KindVariable` (for call-target scoping only; keep minimal)

Flags: `isAsync` (async modifier), `isStatic` (static modifier), `isAbstract`
(abstract modifier), `visibility` (public/private/protected/internal),
`decorators` ← C# **attributes** (`[Serializable]`, `[Route("…")]` → head name).
Generic type parameters → `typeParameters`.

## Edges
`contains` (lexical), `calls` (invocation_expression), `imports` (using →
namespace/type), `extends` (base class), `implements` (interfaces in base
list — C# base list mixes both; classify: first base that resolves to an
interface → implements, a class → extends; when unresolved, default class
position heuristic documented), `references` (type usages in fields/params/
returns), `instantiates` (object_creation_expression `new T()`), `decorates`
(attribute → attribute class).

## Resolution (static, Go-like — no dynamic inference)
1. **using directives** establish namespace imports; build a per-file imported-
   namespace set + alias map (`using Foo = A.B.C;`).
2. **Bare/qualified type & call names** resolve by name against the global
   symbol table, preferring: same namespace → imported namespaces → any. Mirror
   Python's "resolve through import nodes to the real definition" fix so a
   `using`-imported type call lands on the real cross-file type, not the using
   node.
3. **Inheritance / interface** edges resolve base-list names to their types.
4. **Overloads:** resolve by name to the type's member; if multiple overloads,
   pick deterministically (first by start line) and document — no signature
   matching this pass.
Determinism: order-independent; build per-file using-sets before resolving.

## Fixtures (`cs-small`) + goldens
Hand-built corpus mirroring `py-small`: a namespace with a base class +
interface, a derived class with a property and an async method, an attribute,
a `using` cross-file type construction + method call, an enum. Register in the
extraction parity test, indexer golden-init list, cmd binary-parity fixtures,
and `rebaseline-golden.sh` (+ MCP branch). Self-baselined goldens; never
hand-edited. Opt-in external corpus: one real C# repo pinned by sha
(informational, env-gated, non-golden).

## Out of scope (this pass)
`.csproj` `LangVersion` detection (fallback `v0`); partial-class merging across
files (each part is its own class node — document); signature-based overload
resolution; nullable-reference-type semantics; LINQ query-expression internals
beyond calls; preprocessor `#if` evaluation.
