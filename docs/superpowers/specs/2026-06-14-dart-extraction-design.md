# Dart full-intelligence extraction — design (delta off the static langs)

**Date:** 2026-06-14
**Sub-project:** wave 5 (Dart). Branches from `integration/languages`. Static/OO
(Flutter). Borrows the C#/Java static resolver patterns (library-level imports,
classes, mixins). Runs in parallel with Lua + Elixir.

## Integration points
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangDart` → `grammars.DartLanguage()`. |
| `model/model.go` | `LangDart Language = "dart"`. No new node/edge kinds. |
| `internal/extract/detect.go` | `.dart` → `LangDart`. (`.g.dart`/`.freezed.dart`/`.pb.dart` generated regexes already present.) |
| `internal/extract/extract.go` | Parse + dispatch `LangDart` → `walkDart`. |
| `internal/extract/walk_dart.go` (new) | AST visitor. |
| `resolve/resolve.go` | `case model.LangDart` → `resolveDartRef` (by-name + through-import). |
| `scope/scope.go` | Dart scope; version = fallback `v0`. |

## Symbol model (tree-sitter-dart node kinds → model kinds)
- `class_definition` → `KindClass`; `mixin_declaration` → `KindClass` (document; closest kind); `extension_declaration` → attach members to the extended type
- `enum_declaration` → `KindEnum`; `enum_constant` → `KindEnumMember`
- `method_signature`/`function_signature`+body, `declaration` of a method → `KindMethod` in a class, `KindFunction` at top level; constructors → `KindMethod`
- getters/setters → `KindProperty`; fields (`final`/`var`/typed) → `KindField`; top-level `const`/`final` → `KindConstant`, top-level `var` → `KindVariable`
- `type_alias` → `KindTypeAlias`
- `import_or_export` / `import_specification` (`import 'package:...';`, `import 'rel.dart';`) → `KindImport`

Flags: `visibility` (Dart uses leading-underscore privacy → mark `_name` as
private visibility), `isStatic` (`static`), `isAbstract` (`abstract`),
`isAsync` (`async`/`async*`), `typeParameters` ← generics, `decorators` ←
annotations (`@override`, `@immutable` → head name).

## Edges
`contains`, `calls` (method/function invocation — strip `this`), `imports`
(`import` → the imported library/file; relative `import 'x.dart'` resolves to
the in-repo file, `package:`/`dart:` usually stay at the import node),
`extends` (`extends` superclass), `implements` (`implements` clause; `with`
mixins also → `implements`/`references` — document: treat `with` mixins as
`implements`), `references` (type usages), `instantiates` (`Type(...)` /
`new Type(...)` / named constructors `Type.named(...)` → instantiates),
`decorates` (annotation → annotation class).

## Resolution (static, through-import, library-flat)
`resolveDartRef`: Dart symbol scope is library/file-based (no namespaces).
Resolve names/calls/types by name against the global table with same-file/dir
preference; relative `import 'x.dart'` establishes a file dependency and
resolves imported names THROUGH to the real def (reuse the through-import
pattern). `Type(...)`/`new`/named-constructor → instantiates. extends/implements/
mixin resolve by name. Method calls resolve to the type's method (through
super/mixins by name). Overloads ~ n/a (Dart has no overloading) — simplifies
resolution. Determinism preserved.

## Fixtures (`dart-small`) + goldens + external corpus
An abstract class / interface-style class, a base class with a method + a
derived class overriding it (`extends`), a `mixin` applied with `with`, an enum,
a class with a constructor + a field; a second file with a relative `import` +
construction + method call. Register in the four harnesses +
`rebaseline-golden.sh` (+ MCP branch). Self-baselined goldens. Opt-in external
corpus: one small real Dart repo pinned by verified sha (`lang: "dart"`),
env-gated, non-golden.

## Out of scope
pubspec.yaml/version detection (fallback `v0`); part/part-of file merging;
factory-constructor target resolution; null-safety/type-promotion semantics;
extension-method receiver-precise resolution; codegen `.g.dart` (already
filtered as generated).
