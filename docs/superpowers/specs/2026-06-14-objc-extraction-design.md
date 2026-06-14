# Objective-C full-intelligence extraction — design (delta off C)

**Date:** 2026-06-14
**Sub-project:** Objective-C support. Objective-C is a strict superset of C, so
this **REUSES `walk_c.go`'s `extractC*` helpers** for the C subset (functions,
structs, enums, typedefs, `#include`/`#import`, C calls, fields) exactly like
C++ does, and adds the Obj-C object layer on top. New `LangObjC` everywhere;
NO new node/edge kinds.

## Integration points
| Layer | Change |
|---|---|
| `internal/tsparse/tsparse.go` | `LangObjC` → `grammars.ObjcLanguage()`. |
| `model/model.go` | `LangObjC Language = "objc"`. No new node/edge kinds. |
| `internal/extract/detect.go` | `.m`/`.mm` → `LangObjC`. `.h` content-sniff: a header with `@interface`/`@protocol`/`@implementation`/`#import` → `LangObjC`, checked BEFORE the existing C++ marker sniff so Obj-C wins; otherwise the existing C/C++ `.h` logic is untouched. |
| `internal/extract/extract.go` | `case model.LangObjC` in BOTH switches (parse → `tsparse.LangObjC`; walk → `e.walkObjC`). |
| `internal/extract/walk_objc.go` (new) | Obj-C walker — calls the `walk_c.go` `extractC*` helpers for the shared C subset and adds Obj-C constructs. |
| `resolve/resolve.go` | `case model.LangObjC` → `resolveObjCRef` (static by-name + through-import like `resolveCRef`/`resolvePythonRef`; selector resolution by name; `alloc`/`new` → instantiates). |
| `scope/scope.go` | `LangObjC` → fallback `v0`. |

## tree-sitter-objc node kinds (AST-probe confirmed)
- `class_interface` — `identifier` (name), optional `superclass` field, optional
  `category` field (`@interface X (Cat)`), optional
  `protocol_reference_list`/`parameterized_arguments` (`<P1,P2>` adopted
  protocols), `instance_variables`, `property_declaration`,
  `method_declaration` members.
- `class_implementation` — `identifier` (name), `implementation_definition`
  wrapping `method_definition`.
- `protocol_declaration` — `identifier` (name), `protocol_reference_list`
  (adopted protocols), `method_declaration` members.
- `method_declaration` / `method_definition` — leading `-`/`+` token in text =
  instance/class; `method_type` (return type), `identifier` selector parts,
  `method_parameter` (`:(T)name`). Selector = the joined `identifier` keyword
  parts with `:` kept where parameters follow (e.g. `initWithName:`,
  `doThing:with:`); a unary selector has no colon (`describe`, `alloc`).
- `property_declaration` — `property_attributes_declaration` + a
  `struct_declaration` carrying the type + declared name.
- `instance_variables` → `instance_variable` → `struct_declaration` (the ivar
  type + name).
- `message_expression` — `receiver` field + one or more `method` fields
  (`identifier`s). Multi-part keyword selector → join with `:`. Nested receiver
  is itself a `message_expression` (`[[Foo alloc] init]`).
- `preproc_include` covers both `#include` and `#import` (reused from C).

## Obj-C symbol model (on top of reused C constructs)
- `class_interface` (no category) and `class_implementation` → `KindClass`,
  **merged by class name**: createNode IDs include the start line, so the
  interface and implementation produce two nodes; we keep both but resolve
  members/edges by class NAME so both parts contribute (the @interface node is
  the canonical extends/implements target).
- `class_interface` WITH a `category` → attach the category's methods to the
  base class X by qualified name `X::selector`; no separate category node.
- `protocol_declaration` → `KindInterface` (per spec).
- `method_declaration`/`method_definition` → `KindMethod`, name = selector
  (colons kept), `isStatic` for `+` class methods. Method bodies are walked for
  calls/instantiates.
- `property_declaration` → `KindProperty`.
- instance variables → `KindField`.
- C functions/structs/enums/typedefs/macros via reused `walk_c.go` helpers.
- `#import`/`#include` → `KindImport` (reused `extractCInclude`).

## Edges
- `contains` — via the node stack (class → methods/properties/fields).
- `calls` — `message_expression` selector → calls to the selector method;
  `self`/`super` receivers stripped (resolve by selector name). C
  `call_expression` (e.g. `NSLog`) reused from `visitCBody`.
- `instantiates` — `[Class alloc]` / `[[Class alloc] init]` / `[Class new]` →
  instantiates the receiver class. Detected on the `message_expression` whose
  method is `alloc`/`new` with a plain `identifier` (class) receiver.
- `imports` — `#import`/`#include` → in-repo header file node or local import
  node (reused C resolution).
- `extends` — `@interface X : Super` superclass → extends Super.
- `implements` — adopted protocols `<P1,P2>` → implements each protocol.
- `references` — C type references (reused).

## Resolver (`resolveObjCRef`)
- `imports` → reuse C header-file/import-node resolution.
- `extends`/`implements` → resolve the named class/interface by name.
- `instantiates` → resolve the class by name.
- `calls` → resolve the selector to a `KindMethod` by name (best/proximity
  pick); through-import aware (whole-repo by-name search, like C).
- bare `references` → resolve type by name.

## Flags / determinism
`isStatic` (class `+` methods), `visibility` n/a (Obj-C methods are public),
`isAbstract` n/a. Selector names are deterministic (source order). Reuses
`createNode`, the node stack, and the C helpers; no per-run nondeterminism.

## Reuse vs. additions
Reused: `extractCInclude`, `extractCFunctionDefinition`/`extractCDeclaration`,
`extractCStruct`/`extractCEnum`/`extractCTypedef`, `visitCBody`/`extractCCall`,
`emitCTypeRef`, all `cDeclaratorName`/`cTypeName`/`cBuiltinTypes` helpers, and
the C include resolver. Added: the Obj-C walker (`walk_objc.go`), the selector
builder, message-send call/instantiate extraction, and `resolveObjCRef`.
