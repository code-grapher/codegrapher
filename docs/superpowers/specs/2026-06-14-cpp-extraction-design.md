# C++ full-intelligence extraction — design (delta off C)

**Date:** 2026-06-14
**Sub-project:** wave 4 Track A, language 2 (C++). Branches from the
C-inclusive `integration/languages`. **REUSES `walk_c.go`** for the shared C
subset (tree-sitter-cpp extends tree-sitter-c). The `LangCPP` constant,
`DetectLanguageContent`, and the `.h`-sniff already exist (from C) — this
sub-project fleshes out the C++ walker + resolver. The HARDEST resolver of the
batch (namespaces, overloads, templates, inheritance).

## Integration points
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangCPP` → `grammars.CppLanguage()` (constant exists; add the parser case). |
| `model/model.go` | `LangCPP` already added by C. No new node/edge kinds. |
| `internal/extract/detect.go` | `.cpp/.cc/.cxx/.hpp/.hh/.hxx` → `LangCPP`; `.h`-sniff already routes C++ content → `LangCPP` (done in C). Verify/extend. |
| `internal/extract/extract.go` | Replace the LangCPP file-node-only stub with a real `walkCPP` dispatch. |
| `internal/extract/walk_cpp.go` (new) | C++ walker — **calls the `walk_c.go` `extractC*` helpers for the shared subset** (functions, structs, enums, typedefs, #include, calls, fields) and adds the C++ constructs. |
| `resolve/resolve.go` | `case model.LangCPP` → `resolveCPPRef` (namespace + `using` + through-include + by-name with namespace preference). |
| `scope/scope.go` | CPP scope already fallback `v0` (from C). |

## C++-only symbol model (on top of the reused C constructs)
- `namespace_definition` → `KindNamespace`
- `class_specifier` → `KindClass`; `struct_specifier` with methods → `KindClass` (a C struct stays `KindStruct`; if it has methods/access-specifiers treat as class — document the heuristic)
- `function_definition` inside a class/`field_declaration` with a function declarator → `KindMethod`; constructors/destructors/operators → `KindMethod`
- `field_declaration` (data member) → `KindField`; `static const`/`constexpr` → `KindConstant`
- `enum_class`/`enum struct` → `KindEnum`
- `template_declaration` → unwrap to the templated class/function, recording `typeParameters` from `template_parameter_list`
- `using_declaration`/`alias_declaration` (`using X = Y;`) → `KindTypeAlias`; `using namespace N;` and `using N::sym;` → `KindImport`
- `base_class_clause` entries → extends/implements (see edges)

Flags: `visibility` (public/private/protected access specifiers — track the
current section like Ruby/PHP), `isStatic`, `isAbstract` (pure virtual `= 0`),
`isAsync` n/a, `typeParameters` ← template params, `decorators` n/a (C++
attributes `[[...]]` → skip or references, document).

## Edges
`contains`, `calls` (`call_expression`, method `field_expression` calls — strip
`this`), `imports` (`#include` reused from C + `using`), `extends` /
`implements` (C++ has no interface keyword — model EVERY base class as
`extends`; OR: a base class whose members are all pure-virtual → `implements`.
Simplest deterministic rule for this pass: **all base classes → `extends`**;
document that interface-vs-base distinction is out of scope), `references`
(type usages, template args), `instantiates` (`new T(...)`, stack `T x(...)` /
`T x{...}` construction → instantiates to the class — best-effort, document),
`overrides` (a virtual method redeclared in a derived class → `overrides` base
method, like Rust/Go; emit when a base method of the same name exists).

## Resolution (hardest — namespaces + overloads + through-include)
`resolveCPPRef`:
1. **`using namespace N` / `using N::x` / `namespace` scoping** build a per-file
   namespace-context (open namespaces + using-directives + using-declarations +
   `X = Y` alias map). Reuse the through-import pattern.
2. **Qualified names** `A::B::sym` resolve by walking the namespace path;
   **bare names** resolve with current-namespace → using-directives → global
   preference.
3. **`#include` through-resolution** reused from C: a call to a function/method
   declared in an included header resolves to its out-of-line definition.
4. **Overloads:** resolve by name to the class/namespace member; multiple
   overloads → deterministic pick (first by start line), documented — no
   signature/argument-type matching this pass.
5. **`Type::method` and `obj.method()`** resolve the method on the class
   (through base classes by name for inherited methods).
Determinism: build per-file namespace context + class/base table before
resolving.

## Fixtures (`cpp-small`) + goldens + external corpus
A header with a `namespace`, a base `class` with a virtual method + a derived
`class` overriding it, a `class` with a constructor + a data member + a method,
a template function/class (simple), an `enum class`; a `.cpp` that `#include`s
the header, `using namespace`, constructs objects (`new`/stack), and calls
methods (incl. an overridden virtual). Use `.hpp` for the header and `.cpp` for
the impl so detection is unambiguous; ALSO include one `.h` whose content has
`class`/`namespace` to exercise the C++ sniff. Register `cpp-small` in the four
harnesses + `rebaseline-golden.sh` (+ MCP branch). Self-baselined goldens.
Opt-in external corpus: one small real C++ repo pinned by verified sha
(`lang: "cpp"`), env-gated, non-golden.

## Reuse contract with walk_c.go
Call the existing `extractC*` helpers for: `#include`, free functions, C-style
structs/unions/enums/typedefs, fields, calls. Do NOT duplicate them. Add C++
handling alongside. Do NOT change C's behavior (LangC must keep producing
identical c-small goldens — verify after your changes).

## Out of scope
Template instantiation/specialization resolution; SFINAE/concepts; argument-
dependent lookup (ADL); signature-based overload resolution; multiple/virtual
inheritance diamond specifics beyond name-based base edges; macro expansion;
operator-overload call resolution beyond name; module (`import std;`) system.
