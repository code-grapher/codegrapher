# F# extraction design (codegrapher)

Full-intelligence F# support via tree-sitter-fsharp (`grammars.FsharpLanguage()`).
Modeled on `walk_scala.go` (modules + types + members) and `walk_csharp.go`.

## Grammar maturity

Probed `tree-sitter-fsharp` with representative idiomatic F# (namespace, nested
module, records, discriminated unions, abstract types, classes with
constructor/inherit/interface, member properties/methods, let-functions, let
values, application & dot calls, `new`). All parsed with `HasError()==false`.
The grammar is mature for idiomatic code. ERROR nodes still handled gracefully:
unknown kinds descend into named children so whatever parses is extracted.

## Node-kind map (verified by probe)

- `namespace` → `long_identifier` (name) + member decls as direct children.
- `module_defn` → `identifier` (name) + members (`;`-separated direct children).
- `import_decl` → `open` + `long_identifier`.
- `type_definition` → exactly one of:
  - `record_type_defn` → `type_name` + `record_fields`/`record_field`.
  - `union_type_defn` → `type_name` + `union_type_cases`/`union_type_case`.
  - `anon_type_defn` → `type_name`, optional `primary_constr_args`,
    `class_inherits_decl`, `type_extension_elements`. Used for both classes
    (have constructor/members) and abstract types (only `member_signature`s).
- `type_extension_elements` → `member_defn` | `interface_implementation`.
- `member_defn` → `member_signature` (abstract decl) OR `method_or_prop_defn`
  → `property_or_ident` (`this.Bark`, `_.Name`); `()` present = method.
- `interface_implementation` → `interface` + `simple_type` (iface name) + members.
- `class_inherits_decl` → `inherit` + `simple_type` (base name).
- `declaration_expression` → `function_or_value_defn` → `value_declaration_left`
  (`identifier_pattern`; extra nested `identifier_pattern` children = function
  params → it is a function; none = a value) + body expr.
- Calls: `application_expression` (callee `long_identifier_or_op` + args);
  `dot_expression`; `prefixed_expression` (`new` + application).

## Symbol mapping

| F# construct | Kind |
|---|---|
| `namespace A.B` | KindNamespace (pushed) |
| `module M` | KindModule (pushed) |
| `open A.B` | KindImport (+ EdgeImports) |
| record `type T = {…}` | KindStruct + fields KindField |
| union `type T = A | B of int` | KindEnum + cases KindEnumMember |
| abstract `type I = abstract …` | KindInterface |
| class `type T() = …` | KindClass |
| member method | KindMethod; member property | KindProperty |
| top-level `let f x =` | KindFunction; inside type | KindMethod |
| `let X = ` UPPER/simple value | KindConstant; lowercase top-level value | KindVariable; inside fn body | KindVariable |

abstract-vs-class disambiguation: `anon_type_defn` whose members are ALL
`member_signature` (no `method_or_prop_defn`, no constructor) → KindInterface;
otherwise KindClass.

## Edges

- contains (createNode parent stack), calls (application/dot — strip `this`/`self`),
  instantiates (`T(...)` capitalized callee / `new T(...)`),
  imports (`open`, through-import), extends (`inherit Base()`),
  implements (`interface I with …`), references (type usages: omitted for now,
  keep minimal like other recent langs).

## Resolver `resolveFSharpRef`

- module/namespace-qualified `A.B.func`: resolve through `open` imports +
  by-name; preference same-module → opened → any.
- through-import to the real def.
- `T(...)`/`new T` → EdgeInstantiates by type name.
- `inherit`/`interface` → EdgeExtends/EdgeImplements by name.
- Skip F# core builtins: `printfn`, `printf`, `sprintf`, `failwith`, `id`,
  `ignore`, `List.*`, `Seq.*`, `Array.*`, `Map.*`, `Option.*`, `Result.*`,
  `Async.*`, `string`, `int`, `float`, `not`, `box`, `unbox`, `ref`.

## Wiring

tsparse.LangFSharp → FsharpLanguage(); model.LangFSharp="fsharp";
detect: `.fs`/`.fsi`/`.fsx`; extract dispatch (parse + walkFSharp);
scope fallback v0. No new node/edge kinds.
