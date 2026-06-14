# PHP full-intelligence extraction — design (delta off Python)

**Date:** 2026-06-14
**Status:** Approved (lean delta); implement directly.
**Sub-project:** 6 of N (PHP). Branches from the integrated trunk
(Python + C# + Java + Kotlin + Ruby). **Dynamically typed with namespaces** —
reuse Python's dynamic resolver + constructor-assignment inference, and add
PHP namespace/`use`-statement resolution.

Delta off the Python design. Same five integration points + golden/external
machinery.

## Integration points
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangPHP` → `grammars.PhpLanguage()`. |
| `model/model.go` | `LangPHP Language = "php"`. No new node/edge kinds. |
| `internal/extract/detect.go` | `.php` → `LangPHP` (`.phtml` optional; `.php` only this pass). |
| `internal/extract/extract.go` | Parse + dispatch `LangPHP` → `walkPHP`. |
| `internal/extract/walk_php.go` (new) | AST visitor. |
| `resolve/resolve.go` | `case model.LangPHP` → `resolvePHPRef` (Python-style dynamic + `use`-alias resolution). |
| `scope/scope.go` | PHP scope; version = fallback `v0` this pass (no composer.json parsing yet). |

## Symbol model (tree-sitter-php node kinds → model kinds)
- `namespace_definition` → `KindNamespace`
- `class_declaration` → `KindClass`; `interface_declaration` → `KindInterface`; `trait_declaration` → `KindTrait`; `enum_declaration` → `KindEnum`; `enum_case` → `KindEnumMember`
- `method_declaration` → `KindMethod`; `function_definition` (top-level) → `KindFunction`
- `property_declaration` → `KindField`; `const_declaration`/`class_const_declaration` → `KindConstant`
- `$this->prop = …` inside a method → `KindField` on the enclosing class (analog of Python `self.x`)
- `namespace_use_declaration` (`use A\B\C;`, `use A\B\C as D;`) → `KindImport`

Flags: `visibility` (public/private/protected modifiers), `isStatic`,
`isAbstract`, `decorators` ← PHP 8 **attributes** (`#[Route(...)]` → head name)
when the grammar exposes them (document if it doesn't). `typeParameters` n/a.

## Edges
`contains`, `calls` (`function_call_expression` / `member_call_expression` /
`scoped_call_expression` — strip `$this`/`self`/`static`/`parent` receiver like
Python strips `self`), `imports` (`use` → the imported class/namespace),
`extends` (`extends` clause), `implements` (`implements` clause; a trait `use`
INSIDE a class body → `implements`/`references` to the trait — document the
choice), `references`, `instantiates` (`new T(...)` / `new \Ns\T(...)` →
instantiates), `decorates` (attribute → attribute class).

## Resolution (Python-style dynamic + namespace/use)
`resolvePHPRef` mirrors `resolvePythonRef`:
1. **`use` declarations** establish import aliases (`use A\B\C [as D]`) — build a
   per-file alias→FQN map. Resolve imported names THROUGH to the real cross-file
   definition (reuse the through-import pattern), so `use App\Dog; new Dog()`
   lands on the real class as `instantiates`.
2. **Bare/namespaced names** resolve by name (last `\` segment) against the
   global table, preferring same-namespace → used aliases → any. `calls`→
   `instantiates` promotion for class targets.
3. **Conservative type inference**: per file, build `$var → ClassName` from
   `$x = new ClassName(...)` assignments, then resolve `$x->method()` to
   `ClassName::method`. Order-independent (build before resolving). Unknown
   receiver → unresolved.
4. **extends/implements/trait-use** resolve names to their types.
Determinism preserved as in the Python resolver.

## Fixtures (`php-small`) + goldens + external corpus
Mirror `py-small`: a namespaced base class + interface, a derived class
implementing it with a `$this->name` property and a method, a trait, an enum; a
second file with `use` + `new` + an inferred `$obj->method()` call. Register in
the four harnesses + `rebaseline-golden.sh` (+ MCP branch). Self-baselined
goldens. Opt-in external corpus: one real PHP repo pinned by verified sha
(`lang: "php"`), env-gated, non-golden.

## Out of scope (this pass)
composer.json version detection (fallback `v0`); dynamic
`$$var`/variable-variables; magic methods (`__call`, `__get`); trait
conflict-resolution (`insteadof`/`as`); heredoc/nowdoc internals; mixed
HTML/PHP template files beyond the PHP islands the grammar parses.
