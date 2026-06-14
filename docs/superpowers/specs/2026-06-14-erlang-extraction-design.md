# Erlang extraction design (2026-06-14)

Full-intelligence Erlang support, modeled on the Elixir walker
(`walk_elixir.go` + `resolveElixirRef`). Erlang is functional BEAM:
one file = one module, modules namespace functions, no classes.

## Grammar (tree-sitter-erlang, probed)

`source_file` → top-level `form` children. Relevant kinds:

- `module_attribute` `.name`=atom → module name.
- `export_attribute` `.funs`=`fa`* ; each `fa` has `.fun`=atom, `.arity`/`value`=integer.
- `import_attribute` `.module`=atom, `.funs`=`fa`*.
- `behaviour_attribute` `.name`=atom. (Both `-behaviour` and `-behavior`
  parse to this same kind.)
- `record_decl` `.name`=atom, `.fields`=`record_field`* (each `.name`=atom).
- `pp_define` `.lhs`=`macro_lhs` (`.name`=var), `.replacement`=expr.
- `pp_include` `.file`=string ; `pp_include_lib` `.file`=string (distinct kind).
- `fun_decl` `.clause`=`function_clause` (`.name`=atom, `.args`=`expr_args`,
  `.body`=`clause_body`). MULTI-CLAUSE: each clause is its OWN top-level
  `fun_decl` (not grouped) → must dedup by name+arity.
- Call sites inside bodies: `call` `.expr`. Remote `mod:func` → `.expr`=`remote`
  (`.module`→`remote_module`→atom, `.fun`=atom). Local `func(...)` → `.expr`=atom.

Arity = named-child count of the clause's `expr_args`.

## Symbol model

- `module_attribute` → KindModule (pushed so functions qualify `foo::bar`).
- `fun_decl` → KindFunction, deduped per scope by `name/arity` map. Private
  unless its `name/arity` is in an `-export`. (Exports collected in a
  pre-pass before walking functions, since `-export` precedes/varies.)
- `record_decl` → KindStruct + one KindField per `record_field`.
- `pp_define` → KindConstant (macro name).
- `pp_include` / `pp_include_lib` → KindImport (file), EdgeImports.
- `import_attribute` → KindImport per imported module, EdgeImports.
- `behaviour_attribute` → EdgeImplements (module → behaviour module).

No new node/edge kinds. No `instantiates` (record literal `#st{}` → skip).

## Edges

- contains: module → functions/records/constants (via createNode stack).
- calls: remote `mod:func` → `mod`'s function by name; bare `func` →
  same-module / imported / any.
- imports: `-include`/`-include_lib`/`-import` through-import to real def.
- implements: `-behaviour` → behaviour module by name.

BIF skip set (auto-imported, bare): lists/io aside, the bare builtins
`length element is_list is_atom hd tl self spawn ...` (small set).

## Resolver `resolveErlangRef`

Mirrors `resolveElixirRef`:
- EdgeImplements: behaviour atom → KindModule named like it.
- EdgeImports: through to real KindModule by name; else generic fallback.
- EdgeCalls: `mod:func` → function whose qualified-name parent = mod; bare
  `func` → through local `-import` shadow, else same-module/any by name.

## Wiring

`model.LangErlang="erlang"`; `tsparse.LangErlang`→`grammars.ErlangLanguage()`;
`detect.go` `.erl`/`.hrl`→LangErlang; `extract.go` parse+walk dispatch;
`scope.go` fallback `v0`. Fixtures `erlang-small` (shape.erl, app.erl) +
external corpus entry. Self-baselined goldens only for erlang-small.
