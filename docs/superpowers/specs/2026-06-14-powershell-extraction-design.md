# PowerShell extraction design (2026-06-14)

Full-intelligence PowerShell support for codegrapher, modelled on the existing
scripting walkers (`walk_lua.go`, `walk_r.go`) for functions/calls/imports and on
the static-class walkers for PS5 `class`/`enum`.

## Grammar (tree-sitter-powershell, gotreesitter fork)

Verified with a throwaway probe test. **The grammar is newline-sensitive**: a
brace block written on a single line (`function f { ... }`) parses to `ERROR`
nodes; multi-line blocks parse cleanly. Fixtures are therefore multi-line.

Relevant node kinds (named):

- `program` > `statement_list` > statements
- `function_statement` { field `function_name`; params either a `param_block`
  inside the `script_block`, or a `function_parameter_declaration` for the
  `function f($x)` form }
- `class_statement` { `simple_name` (class name); optional `: simple_name [, ...]`
  bases; `class_property_definition` { `attribute`>`type_literal`, `variable` };
  `class_method_definition` { optional `attribute` return type, `simple_name`
  method name, params, `script_block` } — a method whose name equals the class
  name is the constructor }
- `enum_statement` { `simple_name`; `enum_member` > `simple_name` }
- `command` { field `command_name`, field `command_elements` } — cmdlet/function
  invocation. Dot-source is a `command` with a `command_invokation_operator` "."
  and `command_name_expr` > `command_name` holding the path.
- `invokation_expression` — two shapes:
  - `[Type]::new()` : `type_literal`>`type_spec`>`type_name`, `::`, `member_name`,
    `argument_list`
  - `$obj.Method()` : `variable`, `.`, `member_name`>`simple_name`, `argument_list`
- `assignment_expression` { `left_assignment_expression`, `value` } — top-level
  `$Var = …`. The variable text carries scope sigils (`$global:GThing`).

## Symbol model (no new node/edge kinds)

- `function Name {…}` / `function Name($p){…}` → KindFunction
- `class C {…}` → KindClass; methods → KindMethod (ctor = method named like class);
  `[type]$Prop` → KindProperty
- `enum E {}` → KindEnum; members → KindEnumMember
- top-level `$Var = …` → KindVariable; ALL-CAPS or `$global:`/`$script:` →
  KindConstant (best-effort)
- `Import-Module Foo` / `using module Foo` / dot-source `. ./lib.ps1` → KindImport

## Edges

- contains (createNode), calls, imports, extends/implements, instantiates,
  references.
- **calls only to in-repo defined functions/classes.** A common-cmdlet skip set
  plus the resolver's "no edge when no in-repo match" rule keeps `Write-Host`,
  `Get-ChildItem`, etc. edge-free. `[C]::new`/`New-Object C` emit EdgeInstantiates;
  `$obj.Method()` strips the receiver to the bare method name.
- imports: dot-source `. ./lib.ps1` resolves through to the in-repo file (like
  Lua's `require`); `Import-Module`/`using module` keep a KindImport node.
- extends/implements: `class C : Base, IFoo` → first base extends, the rest
  implements (best-effort, like the other static langs).

## Resolver (`resolvePowerShellRef`)

Mirrors Lua/R: imports → through-source to in-repo file (basename match) else
local import node; bare calls/refs → same-file → through-import → any in-repo
definition; extends/implements/instantiates → generic name resolver. Unresolved
external cmdlets produce no edge.
