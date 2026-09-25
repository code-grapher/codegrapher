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

a. the symbol's line-bounded source (as `node --source`);
b. full declarations of types it touches — receiver, parameter and result
   types, and types of fields it reads, when declared in the same index
   scope — plus constructors of the receiver type (`New<T>`,
   `Default<T>...`, or functions/methods returning `T` or `*T`);
c. its direct callees as signatures only (never bodies), each flagged when
   it is a seam (an interface method; a call through a struct field whose
   declared type is a func or interface; a call matching a func-typed
   parameter of the caller);
d. existing test functions (declared in `_test.go` files) that call the
   symbol directly or through one hop, by name and file;
e. with `--for test`: signatures of the package's own test helpers and
   fakes (non-`Test`/`Benchmark`/`Fuzz`/`Example` functions and types
   declared in the package's `_test.go` files) plus exported symbols of
   sibling `*test`/`*fake*` packages its test files import.

A symbol that does not resolve (not found, or ambiguous) MUST be reported
by name with that status and MUST NOT block the other requested symbols.

### REQ: budget-caps-output-and-names-what-was-cut

`--budget N` (default 20000) MUST cap the rendered output at approximately
N tokens, estimated as `len(text)/4`. Sections fill in the order a, b, c,
d, e across all requested symbols; once a section would exceed the
remaining budget, `context` MUST stop filling and append an "omitted" list
naming every item (by kind and symbol/type/callee/test name) that did not
fit, instead of silently truncating mid-item. Output MUST be otherwise
deterministic: symbols in requested order, and every section sorted by
file path then line.

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

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/feature-specification*
