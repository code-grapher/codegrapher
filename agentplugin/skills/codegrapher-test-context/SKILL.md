---
name: codegrapher-test-context
description: Use when writing or extending unit tests for specific functions, covering uncovered lines from a coverage profile, or working out what a test for a function needs — types, seams, helpers — in one bounded read.
---

# CodeGrapher test context

`codegrapher context` assembles everything a test writer needs about a set of
symbols in one bounded, budget-capped read: source, the types each symbol
touches, its direct callees, existing tests that already exercise it, and
(with `--for test`) the package's own test helpers and fakes. Use it instead
of the turn-by-turn `node --source` / `node --relations` / `callees`
sequence — that sequence re-reads the same growing context on every symbol
and burns 70-100 tool calls per ~40 covered statements in practice.

## Prerequisites

```sh
codegrapher status --format json      # confirm the repo is indexed
codegrapher init                      # first time only
codegrapher sync                      # after source changes since init
codegrapher coverage <profile>        # ingest a go test -coverprofile file, only needed for --uncovered
```

`context` refuses to run against an uninitialized index or a different git
worktree's index; run `init`/`sync` from the current worktree.

## The workflow that measured best

Do not call `context` once per symbol. Group the symbols you need to cover by
file, then, per file (or in one `--symbols-file` call across files):

1. One `context` call with `--for test --uncovered --budget N`.
2. Save the output to one file and read it once.
3. Write all the new tests for that file in a small number of large edits.
4. Run the tests once with `-coverprofile`, re-ingest it, fix once.

Calling `context` per-symbol instead of per-file repeats section (e)'s
test-helper/fake bundle — often the biggest part of the output — for no
benefit; grouping by file (or `--symbols-file`) emits it once per package no
matter how many symbols/files that call covers.

### One call across several files: `--symbols-file`

Write a `path<TAB>symbol-or-line` file, one target per line (blank lines and
`#` comments skipped), and pass it once instead of repeating the call:

```
cmd/wb/agent_remote.go	runAgentRemote
cmd/wb/agent_remote.go	142
internal/deploy/deploy.go	Deployer.Run
```

A numeric second field is a bare source line (see "Naming symbols" below);
anything else is a symbol name scoped to that file.

```sh
codegrapher context --symbols-file targets.tsv --for test --uncovered --budget 30000
```

## Naming symbols

- A **method**: `Type.Method` (e.g. `Deployer.Run`), matching `node`'s
  convention.
- A **plain function**: its bare name; add `--file path/to/file.go` if the
  name is ambiguous across the repo.
- An **anonymous closure or a switch/loop body with no name of its own** (a
  `RunE: func(cmd, args) error {...}` callback is the common case — coverage
  profiles/worklists only ever give you a file:line for these): pass any
  placeholder symbol name plus `--file`/`--line` pointing at a line inside
  it. `context` resolves to the innermost NAMED enclosing function or
  method at that line — never the literal alone: a closure only makes sense
  read alongside the function that declares what it captures and wires it
  up. You get that function's whole source, with every line of the target
  literal (there may be several, if more than one of your targets lands in
  it) marked `// TARGET`; a switch/loop body (not a literal) resolves the
  same way but has no `// TARGET` marks, since it's already just part of
  the function's own source. Only when the enclosing function is longer
  than `--max-function-lines` (default 150) does `context` narrow instead —
  see "Long enclosing functions" below.

### Long enclosing functions

A closure inside a function longer than `--max-function-lines` (default
150) would otherwise dump the whole surrounding function for one small
target, so `context` narrows: it keeps the target literal(s) whole (still
marked `// TARGET`), the lines that declare every free variable the
literal captures from the enclosing function, and the statement that
registers or calls it (an assignment, a `return`, a bare call) — enough to
see what the closure does and what it closes over, not the unrelated rest
of a long function. Every gap it cuts becomes an explicit
`// … N lines elided (enclosing function F is M lines; rerun with
--max-function-lines 0 for all)` marker, never a silent drop. Pass
`--max-function-lines 0` to always get the whole function regardless of
length.

## Reading the output

Sections render in this fixed order, deduplicated across every symbol:

- **a. Source** — line-bounded source per symbol. `--uncovered` appends
  `// UNCOVERED` to lines the ingested profile reports missed; a `--line`
  target inside a nested closure appends `// TARGET` to that literal's
  lines (alongside `// UNCOVERED` when both apply) — see "Naming symbols"
  above. A requested function whose entire body is a single (optionally
  `return`'d) call — a thin wrapper — also gets its callee's full source
  here, unasked: the wrapper's own line tells you nothing about the real
  logic.
- **b. Types touched** — full declarations of receiver, parameter/result,
  and field types, plus constructors of the receiver type.
- **c. Direct callees** — signatures only, never bodies. `[seam: interface
  | field | parameter]` marks a point a test double can replace.
- **d. Existing tests** — direct (1-hop) callers only by default; pass
  `--test-hops 2` to also include tests reaching it through one
  intermediate function. Leave the default alone unless you need it — 2-hop
  matches dilute far more than they add.
- **e. Test helpers/fakes** (`--for test`) — the package's own
  non-`Test`/`Benchmark`/`Fuzz`/`Example` helpers and fakes, plus exported
  symbols of sibling `*test`/`*fake*` packages its test files import.
  Ranked: helpers section d's tests (and their file-mates) actually call
  come first, then the rest by name similarity to what you requested. The
  ranked list itself is capped at `--helper-signatures` N (default 40,
  independent of `--budget`) — anything beyond that doesn't appear at all,
  and the omitted list says how many were cut. Of what's kept, only the top
  `--helper-bodies` N (default 3) carry full source; read those before
  assuming you need to look elsewhere for a calling convention.

### Inferred markers

Graph-verified facts (a real edge/node lookup) never carry a marker. A
best-effort text-scan fact — the "field" role in (b), the "field"/
"parameter" seam flags in (c), needed because Go struct fields aren't
indexed as first-class nodes — is tagged `(inferred)` in markdown, and
`heuristicRoles`/`seamSource: "inferred"` in JSON. Treat it as a strong
hint, not a certainty, before relying on it to mock a seam.

### The omitted list

Sections fill in order (a, b, c, d, e, across all requested symbols) up to
`--budget` and stop the moment the next item would overflow; everything cut
is named, by kind and symbol/type/callee/test name, in a trailing omitted
list — never truncated mid-item. If something you need is missing, raise
`--budget` or narrow the call. A `--helper-signatures` cut is a separate
entry in the same list, but it isn't a budget cut — it fires regardless of
remaining budget, and names a count rather than each dropped helper; raise
`--helper-signatures` (or set it to 0) if you need more of them.

## Budget guidance

- Default `--budget` is 20000 (~4 chars/token) — enough for most
  single-file calls.
- For a large package, budget roughly 30000 per file so `--for test`'s
  ranked helper section isn't the first thing cut.
- A `--symbols-file` call spanning many files needs a budget sized to its
  total output, not per-file — section e is emitted once per package
  regardless; sections a/b/c across every requested symbol drive the rest.
