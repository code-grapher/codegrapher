# Ruby full-intelligence extraction — design (delta off Python)

**Date:** 2026-06-14
**Status:** Approved (lean delta); implement directly.
**Sub-project:** 5 of N (Ruby). Branches from the integrated trunk
(Python + C# + Java) and **reuses Python's dynamic resolver + conservative
type-inference pattern** (`resolvePythonRef` and its through-import helpers).

Delta off the Python design. Ruby is **dynamically typed** like Python, so the
resolver is heuristic (require-deps + name + conservative constructor-assignment
inference) — NOT the static JVM path used by C#/Java/Kotlin.

## Integration points
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangRuby` → `grammars.RubyLanguage()`. |
| `model/model.go` | `LangRuby Language = "ruby"`. No new node/edge kinds. |
| `internal/extract/detect.go` | `.rb` → `LangRuby` (also `.rake`, `Rakefile`? keep `.rb` only this pass). |
| `internal/extract/extract.go` | Parse + dispatch `LangRuby` → `walkRuby`. |
| `internal/extract/walk_ruby.go` (new) | AST visitor. |
| `resolve/resolve.go` | `case model.LangRuby` → `resolveRubyRef` mirroring `resolvePythonRef` (dynamic + inference + through-import). |
| `scope/scope.go` | Ruby scope; version = fallback `v0` this pass (no Gemfile/.ruby-version parsing yet). |

## Symbol model (tree-sitter-ruby node kinds → model kinds)
- `module` → `KindModule`
- `class` → `KindClass`
- `method` (`def`) → `KindMethod` inside a class/module, `KindFunction` at top level
- `singleton_method` (`def self.x` / `def obj.x`) → `KindMethod` with `isStatic`
- constant assignment (`CONST = …`, LHS is `constant`) → `KindConstant`
- module/top-level non-constant assignment → `KindVariable`
- instance/class variables (`@x`, `@@x`) assigned in a method → `KindField` attributed to the enclosing class (analogous to Python `self.x`)
- `attr_accessor`/`attr_reader`/`attr_writer` symarg → `KindProperty` (one per attribute name; document)
- `require` / `require_relative` / `load` calls → `KindImport`

Flags: `visibility` (track `private`/`protected`/`public` section markers →
apply to subsequent methods; best-effort, document), `isStatic` (singleton).
Ruby has no annotations → no `decorators`/`decorates`.

## Edges
`contains`, `calls` (`call` / `method_call` — strip `self.`/implicit receiver
like Python strips `self`), `imports` (`require`/`require_relative` → the
required file/module), `extends` (superclass in `class A < B`), `implements`
(NEAREST analog: `include`/`prepend`/`extend ModuleName` mixins → emit
`implements` to the module; document the choice), `references`, `instantiates`
(`Foo.new` → promote to instantiates on the class).

## Resolution (reuse Python's dynamic pattern)
`resolveRubyRef` mirrors `resolvePythonRef`:
1. **require/require_relative** establish file-level imports; resolve imported
   constant/class names through to the real definition (reuse the through-import
   pattern so `require_relative 'dog'; Dog.new` lands on the real cross-file
   class, not the require/import node).
2. **Bare/constant names** resolve by name against the global table (same-file →
   required files → any), with `calls`→`instantiates` promotion for class
   targets.
3. **Conservative type inference**: per file, build `localVar → ClassName` from
   `x = ClassName.new(...)` assignments (the Ruby analog of Python's
   `x = ClassName(...)`), then resolve `x.method()` to `ClassName#method`
   (stored qualified-name `ClassName::method`). Order-independent: build the map
   before resolving the file's refs. Unknown receiver → unresolved.
4. **include/extend mixins** resolve module names to the module node.
Determinism preserved exactly as in the Python resolver.

## Fixtures (`rb-small`) + goldens + external corpus
Mirror `py-small`: a module + base class, a derived class (`<`) with an
`attr_accessor`, an `initialize` setting `@name`, a method; a second file using
`require_relative` + `Klass.new` + an inferred instance-method call; a module
mixin (`include`). Register in the four harnesses + `rebaseline-golden.sh` (+ MCP
branch). Self-baselined goldens. Opt-in external corpus: one real Ruby repo
pinned by verified sha (`lang: "ruby"`), env-gated, non-golden.

## Out of scope (this pass)
Gemfile/.ruby-version detection (fallback `v0`); metaprogramming
(`define_method`, `method_missing`, `send`); monkey-patching reopened classes
merge semantics (each reopening is its own class node — document); blocks/procs
target typing beyond calls; `attr_*` with computed symbols.
