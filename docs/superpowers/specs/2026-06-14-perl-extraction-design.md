# Perl extraction design (2026-06-14)

Full-intelligence Perl support for codegrapher. Perl is dynamic and
package-based; resolution mirrors the Python/Ruby/PHP heuristic family
(name lookup + through-import + conservative constructor type inference).

## Grammar (tree-sitter-perl, verified via probe)

- `package_statement` — field `name` (a `package` node, text e.g. "Foo::Bar").
  Perl packages are flat-in-file: a `package` statement opens a scope that
  runs until the next `package` or EOF.
- `subroutine_declaration_statement` — field `name` (bareword), field `body`
  (block).
- `use_statement` — field `module` (a `package` node, e.g. "Dog", "parent",
  "base", "constant"); trailing args are `string_literal` /
  `quoted_word_list` (`qw(...)`) / `list_expression`.
- `method_call_expression` — field `invocant`, field `method` (a `method`
  node). Invocant is a `bareword` (`Foo->new`) or `scalar` (`$x->method`).
- `ambiguous_function_call_expression` — field `function` (a `function` node,
  text "foo" or "Foo::bar"), field `arguments`.
- `func1op_call_expression` — field `function` (builtins: shift/print/...).
- `assignment_expression` — field `left`, `operator`, `right`. A declared LHS
  is a `variable_declaration` (`my`/`our` token + field `variable`, which is a
  `scalar`/`array`/`hash` node). `@ISA = (...)` is `our @ISA` →
  variable_declaration with an `array` variable named ISA.

## Symbol model

- `package Foo::Bar;` → **KindModule**. The package opens a flat scope; the
  following subs/vars attribute to `Foo::Bar::sub` until the next package/EOF.
- `sub name { }` → **KindMethod** inside a package, **KindFunction** at file
  scope (no enclosing package).
- top-level `my`/`our` scalar/array/hash → **KindVariable**; ALL-CAPS name or
  `use constant NAME` → **KindConstant**.
- `use Foo::Bar` / `require Foo::Bar` → **KindImport** (name = last `::`
  segment so it matches a defining file/package). `use parent`/`use base`/
  `use constant` are special-cased and do NOT emit import nodes.

## Edges (no new kinds)

- **contains** — package→sub, package→var (via the node stack / createNode).
- **calls** — `foo(...)`, `Foo::bar(...)` (kept as `Foo::bar`), method calls
  `$obj->method(...)`. Receiver `$self`/`$class` is stripped (like Python self),
  so `$self->m` → bare `m`. `$x->method` stays `x.method` (dotted) for
  var-type inference.
- **instantiates** — `Foo->new` / `new Foo` → the package/class Foo.
- **imports** — use/require → the module; resolved through to the real package
  def when one exists.
- **extends** — `use parent 'Base'`, `use base qw(Base)`, `our @ISA =
  ('Base')` → extends edge to the Base package (best-effort).
- **references** — generic name refs.

Perl OOP is package-based: a package containing subs is the "class". There is
no separate class node — KindModule plays that role for resolution.

## Resolver `resolvePerlRef` (mirrors resolveRubyRef)

- dotted calls `recv.method`: `Foo->new` (constant receiver + `new`) →
  instantiates the package. Otherwise infer recv's package from same-file
  `my $x = Foo->new` bindings (var-type cache, shared map). When known,
  resolve method as a member of that package; else strict unambiguous
  method-name fallback.
- `Foo::bar` qualified calls: resolve `bar` as a member of package `Foo`.
- bare subs: resolve same-package → through-import → any.
- use/require imports: resolve through to the real package def.
- extends: generic name resolution to the Base package (KindModule).

Var-type inference reuses the shared `pyVarTypeCache` map (Perl never indexes
the same file as Python/Ruby/PHP). Constructor signature shape:
`= Foo->new(...)` → package Foo (last `::` segment).

## Builtins skip-set

print, shift, scalar, keys, values, push, pop, map, grep, join, split, bless,
ref, defined, return, die, warn, wantarray, sort, reverse — not emitted as
calls.

## Scope

`LangPerl` → fallback version `v0` (no cpanfile/Makefile.PL parsing this pass).
