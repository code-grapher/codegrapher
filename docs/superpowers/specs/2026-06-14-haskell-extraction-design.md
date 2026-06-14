# Haskell extraction design (2026-06-14)

Full-intelligence Haskell support for codegrapher. Haskell is a functional,
module-oriented language: modules namespace top-level bindings; type classes
are the nearest analog to interfaces and `instance` declarations are their
implementations. No new node/edge kinds are introduced.

## Grammar reality (tree-sitter-haskell, verified by probe)

Root `haskell` has three relevant children:

- `header` — `module` field → `module` node (dotted `module_id` segments) =
  the module name. A file may omit the header (implicit `Main`).
- `imports` — list of `import` nodes. Each `import` has a `module` field (dotted
  `module_id`s), optional `qualified` token, optional `alias` field (the `as`
  rename, a `module` node), and an optional `import_list`/`hiding` list.
- `declarations` — the top-level decls:
  - `signature` — `name` field (`variable`) + `type`. A type signature `f :: …`.
  - `function` — `name` field (`variable`) + `patterns` + `match`
    (`match.expression` → `apply`/`infix`/…). A binding equation `f x = …`.
  - `bind` — pattern bindings `x = …` (treated like function, zero params).
  - `data_type` — `name` field + `constructors` → `data_constructors` →
    `data_constructor` → `prefix` (positional, `name` field=`constructor`) or
    `record` (`name`=`constructor` + `fields` → `field` with `field_name`).
  - `newtype` — same shape as `data_type` under a `newtype` node.
  - `type_synomym` — `name` field + `type`. (Grammar misspells "synonym".)
  - `class` — `name` field + `type_params` + `class_declarations` →
    `signature`s (the method signatures).
  - `instance` — `name` field (the class C) + `type_patterns` (the type T) +
    `instance_declarations` → `function`s (the method impls).

Call sites: `apply { function: variable|qualified, argument: … }` and
`infix { operator }`. A qualified callee (`Map.insert`) is a `qualified` node
holding `module` + `variable`/`id`.

## Symbol mapping

| Haskell                         | Node kind            |
|---------------------------------|----------------------|
| `module Foo.Bar where`          | KindModule (pushed)  |
| `f x = …` + `f :: …`            | KindFunction (sig merged into Signature; multi-equation deduped by node ID) |
| `data`/`newtype T = …`         | KindStruct           |
| positional constructor `A`      | KindEnumMember       |
| record field `recA :: Int`      | KindField            |
| `type Alias = …`               | KindTypeAlias        |
| `class C a where …`            | KindInterface        |
| class method signature          | KindMethod           |
| `instance C T where …`         | implements edge (T→C) + KindMethod impls under a synthetic `C.T` module scope |
| `import Foo.Bar (…)`           | KindImport           |

Members are qualified `Module::name` via the node stack (module pushed).

## Edges

- **contains** — module → its declarations (via createNode parent stack).
- **calls** — best-effort: a `variable`/`qualified` in `apply.function` (and the
  head of nested applies / infix operands) in a binding RHS → calls. Resolved to
  a top-level function; Prelude builtins skipped.
- **imports** — import → the real module def (through-import).
- **implements** — `instance C T` → T implements C (anchored on the synthetic
  instance module node, re-resolved to T's struct/type by name like Elixir).
- **references** — type usages in signatures (the referenced type names).

No **instantiates**: Haskell has no OO constructor-as-call. Data construction
(`T x`) surfaces as a `constructor` reference, not an instantiate. The
constructor name resolves to its KindEnumMember/KindStruct as a `references`
edge if anything; otherwise skipped. Documented divergence.

## Resolver `resolveHaskellRef`

- **implements**: resolve C (KindInterface) by name; anchor T by name
  (KindStruct/KindTypeAlias) — emit T→C.
- **imports**: resolve through to the real module (KindModule) by the imported
  module's last segment.
- **calls**: `B.func`/`Foo.Bar.func` → expand alias `B`→real module via the
  import node's QualifiedName, then resolve `func` on that module; bare `func`
  → same-module, then through-import, then unique-name fallback.
- Skip a small Prelude set (`map filter foldr foldl show return print ++ . $
  fmap mapM_ putStrLn` …) so builtins don't create noise.

## Determinism / reuse

Reuse `createNode`/`seenNodeIDs` (multi-equation functions collapse to one node
since name+startLine differ per equation — first equation wins the node, later
equations attach their calls to it by name-deferred lookup). All ordering is
AST source order. No new kinds; CGO_ENABLED=0 clean.
