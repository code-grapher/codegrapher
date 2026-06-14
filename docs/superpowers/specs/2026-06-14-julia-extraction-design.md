# Julia extraction design (2026-06-14)

Full-intelligence Julia support for codegrapher. Julia is dynamic with
modules + multiple dispatch. Template: `walk_python.go` (structure) and the
Ruby resolver (dynamic, module-scoped names).

## Grammar (tree-sitter-julia, verified via probe)

- `module_definition`: field `name` (identifier), `block` child → KindModule.
- `function_definition`: `function` token, `signature` (a `call_expression`
  whose first identifier is the name), `block` body → KindFunction.
- Short-form `f(x) = …`: a top-level `assignment` whose left is a
  `call_expression` → KindFunction (same as long form).
- `struct_definition`: optional `mutable` token, `type_head` (an identifier,
  or a `binary_expression` `T <: Super`), `block` of `typed_expression`
  fields → KindStruct + KindField.
- `abstract_definition`: `type_head` (identifier or `T <: B` binary_expression)
  → KindInterface (closest available kind; documented divergence).
- `const_statement`: wraps an `assignment` → KindConstant.
- top-level `assignment` `x = …` (left is identifier) → KindVariable.
- `using_statement` / `import_statement` (+ `selected_import` `Mod: f`) →
  KindImport.
- `call_expression`: first child identifier = callee, or a `field_expression`
  (`Mod.f`) whose `value` + attribute give `Mod.f`; `argument_list` child.
- `typed_expression`: `ident :: TypeIdent` — used for fields and `::T`
  param/var annotations → references to the type.

## Symbol model

| Julia | Kind |
|-------|------|
| `module Foo … end` | KindModule (pushed so members qualify `Foo::name`) |
| `function f(x)` / `f(x) = …` | KindFunction (multi-dispatch methods collapsed by name) |
| `struct T` / `mutable struct T` | KindStruct |
| struct field `x::T` | KindField |
| `abstract type A` | KindInterface (closest; no KindAbstract exists) |
| `const X = …` | KindConstant |
| top-level `x = …` | KindVariable |
| `using`/`import` | KindImport |

No new node/edge kinds are introduced.

## Multiple dispatch dedup

Multiple dispatch means many `f(...)` definitions (methods). We collapse all
methods of a name within one scope onto a single KindFunction node (first
occurrence wins for location; later ones are skipped). This is deterministic
(source order) and avoids duplicate node IDs. No per-overload signature
matching — call resolution picks by name. Documented divergence: we do not
model individual method signatures.

## Edges

- contains: createNode parent stack (module → members, struct → fields).
- calls: `f(args)`; `Mod.f(args)` qualified (`Mod.f`); resolved by name.
- instantiates: `T(args)` where T is a known struct (calls→instantiates promotion).
- imports: `using`/`import` → module; through-import to the real def when present.
- references: `::T` annotations, struct field types, `<: Super`.
- extends: `struct T <: Super` and `abstract type A <: B` → supertype.

## Resolver (`resolveJuliaRef`)

- `Mod.f` dotted call: resolve through using/import to the module's function
  (dotted fallback / member lookup).
- bare `f`: same-module → imported → any (through-import pattern, like Ruby).
- `T(...)`: calls→instantiates promotion when target is a struct.
- `<:` supertype: extends/references via generic name resolution.
- Skip a small set of Julia Base builtins (println, push!, length, map,
  filter, print, typeof, …) so they don't resolve to user nodes.

## Wiring

- `tsparse`: LangJulia → grammars.JuliaLanguage().
- `model`: LangJulia = "julia".
- `detect`: `.jl` → LangJulia.
- `extract`: parse + walk dispatch → walkJulia.
- `scope`: LangJulia → fallback v0.
- `resolve`: case LangJulia → resolveJuliaRef.
