---
format: https://specscore.md/feature-specification
status: Implementing
---

# Feature: Test-writing context bundle

> [SpecScore.**Studio**](https://specscore.studio): | [Explore](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/test-context?op=explore) | [Edit](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/test-context?op=edit) | [Ask question](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/test-context?op=ask) | [Request change](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/test-context?op=request-change) |
**Status:** Implementing
**Source Ideas:** —

## Summary

`codegrapher context <symbol...>` returns everything a test writer needs
about a set of symbols in one bounded read: source, the types each symbol
touches, its direct callees as signatures, existing tests that already
exercise it, and (with `--for test`) the package's test helpers and fakes.
One call replaces the turn-by-turn sequence of `node --source`, `node
--relations`, and `callees` that a coverage-writing agent currently repeats
per symbol.

## Problem

Coverage lanes spend 70-100 tool calls per ~40 covered statements: read a
function, then its types, then its helpers, turn by turn, re-reading the
growing context on every turn. There is no single bounded command that
assembles the bundle a test writer actually needs.

## Behavior

### REQ: ordered-sections-deduplicated-across-symbols

For each requested symbol, `context` MUST resolve it the same way `node`
does (exact id, then exact name, then qualified-name/suffix match,
narrowed by `--file`/`--line`) and assemble, in this order, a bundle
deduplicated across every symbol in the call:

a. the symbol's line-bounded source (as `node --source`). When a requested
   function's own body is a single statement — a bare call or a `return`'d
   call — `context` MUST also include that callee's full source here, as if
   the callee had been requested too (a thin forwarding wrapper's own body
   tells a test writer nothing about the real logic it forwards to);
b. full declarations of types it touches — receiver, parameter and result
   types, and types of fields it reads, when declared in the same index
   scope — plus constructors of the receiver type (`New<T>`,
   `Default<T>...`, or functions/methods returning `T` or `*T`);
c. its direct callees as signatures only (never bodies), each flagged when
   it is a seam (an interface method; a call through a struct field whose
   declared type is a func or interface; a call matching a func-typed
   parameter of the caller);
d. existing test functions (declared in `_test.go` files) that call the
   symbol directly, by name and file. `--test-hops 2` also includes tests
   that reach the symbol through one intermediate function (default 1 —
   direct callers only; the wider match dilutes more than it adds);
e. with `--for test`: signatures of the package's own test helpers and
   fakes (non-`Test`/`Benchmark`/`Fuzz`/`Example` functions and types
   declared in the package's `_test.go` files) plus exported symbols of
   sibling `*test`/`*fake*` packages its test files import, ranked and
   capped per REQ: for-test-helpers-ranked-and-capped.

### REQ: line-resolves-nested-function-or-literal

A requested "symbol" that does not resolve by id, name, or qualified name —
the only way to name an anonymous function literal (a closure) or a
switch/loop body, neither of which has a name of its own — MUST, when
`--file` and `--line` are both given, resolve to the innermost indexed
Function/Method node in that file whose own line range contains `--line`.

When `--line` additionally falls inside a function literal nested in that
node's body, section (a)'s source for that symbol MUST be narrowed to the
literal's own line-bounded source (not the whole enclosing node), and MUST
be accompanied by the enclosing named function's signature for orientation
(`ContextSource.closureOf` in JSON). Every other section (b/c/d/e) MUST
still be built from the resolved enclosing node, since a closure is not
itself indexed and its calls/types are attributed to that node. A `--line`
that does not fall inside any nested literal (e.g. a switch/loop body, which
is not a literal) leaves section (a) as the enclosing node's full source.

### REQ: for-test-helpers-ranked-and-capped

Section (e)'s candidate helpers/fakes (as scoped by the existing
ordered-sections-deduplicated-across-symbols rule) MUST be ordered into two
ranked tiers, each internally sorted by file path then line, superseding the
plain file/line sort every other section uses:

1. helpers directly called (a `calls` edge) by any function/method declared
   in the same `_test.go` file as one of section (d)'s existing tests;
2. every remaining helper, ordered by name similarity to the requested
   symbols' own names (highest first).

`--helper-bodies N` (default 3 when `--for test` is set) MUST include the
full source of the top N ranked helpers from that order; every helper beyond
N MUST render signature-only, as section (e) always did before this REQ.
This ranked order is also section (e)'s budget-fill order, so an omitted
item under REQ: budget-caps-output-and-names-what-was-cut is always the
lowest-ranked helper that did not fit, not an arbitrary one.

### REQ: symbols-file-targets-one-call-per-package

`--symbols-file PATH` MUST read additional targets from PATH, one per
non-blank, non-`#`-prefixed line, each formatted `file<TAB>symbol-or-line`:
a line whose second field parses as an integer is a bare source line
(resolved per REQ: line-resolves-nested-function-or-literal); any other
value is a symbol name scoped to that file (as `--file` narrows a positional
argument). These targets MUST be resolved alongside any positional symbol
arguments in the same call, and section (e) MUST still be assembled once per
package (per the existing dedup rule) no matter how many of the call's
resolved symbols/files share that package.

Most of (b) and (c) are graph-verified facts (a real edge or node lookup):
receiver, parameter/result, and constructor roles in (b); the interface seam
flag in (c). The "field" role in (b) and the "field"/"parameter" seam flags
in (c) are instead a best-effort text scan over the symbol's own source
(struct fields are not indexed as first-class nodes, so this is the only
route to them) — a shadowed receiver-name local variable, an unparsed
embedded/generic struct field, or a same-named function that isn't actually
reached through the field/parameter can each produce a wrong or missing
result. `context` MUST mark every such text-scan-derived fact as inferred,
distinctly from graph-verified facts, in both output formats: JSON via
`ContextType.heuristicRoles` (the subset of `roles` that are text-scan-
derived) and `ContextCallee.seamSource` (`"graph"` for `interface`,
`"inferred"` for `field`/`parameter`); markdown via an `(inferred)` tag
appended to the specific role or seam flag. Graph-verified facts carry no
such marker (or, for `seamSource`, the explicit value `"graph"`).

A symbol that does not resolve (not found, or ambiguous) MUST be reported
by name with that status and MUST NOT block the other requested symbols.

### REQ: budget-caps-output-and-names-what-was-cut

`--budget N` (default 20000) MUST cap the rendered output at approximately
N tokens, estimated as `len(text)/4`. Sections fill in the order a, b, c,
d, e across all requested symbols; once a section would exceed the
remaining budget, `context` MUST stop filling and append an "omitted" list
naming every item (by kind and symbol/type/callee/test name) that did not
fit, instead of silently truncating mid-item. Output MUST be otherwise
deterministic: symbols in requested order, every section sorted by file
path then line — except section e, whose rank order is defined by
REQ: for-test-helpers-ranked-and-capped and is itself deterministic.

### REQ: uncovered-marks-source-from-ingested-profile

With `--uncovered`, source lines (section a) that the ingested coverage
profile (the `coverage` command's own per-file data) reports as not hit
MUST carry a trailing `// UNCOVERED` marker. Lines with no coverage record
(file never ingested) are left unmarked, not treated as uncovered.

## Acceptance Criteria

### AC: dedup-shared-type-across-two-symbols

**Requirements:** test-context#req:ordered-sections-deduplicated-across-symbols

**Given** two requested functions that share a parameter type
**When** running `context fnA fnB`
**Then** that type's declaration appears exactly once in section b.

### AC: budget-cutoff-lists-omitted

**Requirements:** test-context#req:budget-caps-output-and-names-what-was-cut

**Given** a symbol whose bundle exceeds a small `--budget`
**When** running `context <symbol> --budget 200`
**Then** the output is at most a small overage of the requested budget and
ends with an omitted list naming the sections/items that did not fit.

### AC: seam-flagged-callee

**Requirements:** test-context#req:ordered-sections-deduplicated-across-symbols

**Given** a function that calls an interface method
**When** running `context <function>`
**Then** section c lists that callee's signature flagged as an interface
seam.

### AC: uncovered-lines-marked-from-ingested-profile

**Requirements:** test-context#req:uncovered-marks-source-from-ingested-profile

**Given** a coverage profile already ingested via `codegrapher coverage`
that reports one line of a function as a miss
**When** running `context <function> --uncovered`
**Then** that source line carries the uncovered marker and hit lines do
not.

### AC: for-test-lists-package-helpers

**Requirements:** test-context#req:ordered-sections-deduplicated-across-symbols

**Given** a package whose `_test.go` file declares a non-`Test` helper
function
**When** running `context <function-in-package> --for test`
**Then** section e lists that helper's signature and file.

### AC: unknown-symbol-reported-without-blocking-others

**Requirements:** test-context#req:ordered-sections-deduplicated-across-symbols

**Given** one resolvable symbol and one symbol name that does not exist in
the index
**When** running `context <known> <unknown>`
**Then** the output reports `<unknown>` as not found and still returns the
full bundle for `<known>`.

### AC: thin-wrapper-surfaces-callee-source

**Requirements:** test-context#req:ordered-sections-deduplicated-across-symbols

**Given** a requested function whose body is a single `return`'d call to
another function
**When** running `context <wrapper-function>`
**Then** section a includes both the wrapper's own source and the called
function's full source, as if the callee had been requested too.

### AC: line-resolves-nested-closure

**Requirements:** test-context#req:line-resolves-nested-function-or-literal

**Given** a named function whose body contains a nested anonymous function
literal, and a `--line` inside that literal
**When** running `context <any-name> --file <file> --line <line>`
**Then** the symbol resolves to the enclosing named function, section a's
source is narrowed to the literal's own line-bounded body, and it is
accompanied by the enclosing function's signature for orientation.

### AC: test-hops-default-direct-only

**Requirements:** test-context#req:ordered-sections-deduplicated-across-symbols

**Given** a symbol called directly by one test and indirectly (through one
intermediate production function) by a second test
**When** running `context <symbol>` with no `--test-hops` flag
**Then** section d lists the direct test only; running the same call with
`--test-hops 2` also lists the indirect test, marked as 2 hops.

### AC: helpers-ranked-and-capped

**Requirements:** test-context#req:for-test-helpers-ranked-and-capped

**Given** a package with one helper called directly by a section-d test and
another helper that is never called by any test and shares no name
similarity with the requested symbol
**When** running `context <symbol> --for test --helper-bodies 1`
**Then** the called helper is ranked ahead of the unrelated one, and only
the top-ranked (called) helper's full source is included — the unrelated
helper renders signature-only.

### AC: symbols-file-spans-several-targets

**Requirements:** test-context#req:symbols-file-targets-one-call-per-package

**Given** a `--symbols-file` naming one symbol by name and one by file:line,
both in the same file
**When** running `context --symbols-file <path>` with no positional symbol
arguments
**Then** both targets resolve and the bundle includes both symbols' source.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/feature-specification*
