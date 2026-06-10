# Golden-parity harness

`testdata/golden/` holds outputs captured from the **original** codegraph CLI
(version recorded in `golden/VERSION`) run against `testdata/fixtures/{go-small,ts-small}`.
They are the behavioral spec for the Go port: every `codegrapher` query verb must
produce equivalent output. Re-capture only when deliberately re-baselining:

```sh
tools/parity/capture-golden.sh   # requires the original `codegraph` on PATH
```

## Comparison rules (implemented by the Go test helper `internal/paritytest`)

Per AGENT-BRIEF.md, differences must be deliberate and documented; ordering that
came from JS object/Map iteration is not functionally meaningful — canonicalize
both sides before diffing:

1. **Normalize machine-specific fields**: `projectPath` (absolute), `updatedAt`,
   `dbSizeBytes`, `lastIndexed`, `version`, `indexPath` → replaced with `"<NORM>"`.
2. **Node IDs compare exactly** — `kind:sha256(file:kind:name:line)[:32]` is a
   deterministic contract both implementations must satisfy.
3. **Sort arrays** whose order is not meaningful: `callers[]`, `callees[]`,
   `affected[]` (by `filePath`, `startLine`, `name`), `files[]` (by `path`),
   object keys everywhere.
4. **`query[]` order IS meaningful** (descending score) — compare order; compare
   `score` exactly first, relax to 1e-6 tolerance only if float formatting
   differs (document if relaxed).
5. **Text (non-JSON) output**: data parity only — strip ANSI, compare extracted
   fields, not layout (terminal UI is explicitly allowed to differ).

## Fixture design notes

- `go-small`: structs (pointer + value receivers), interface, cross-package calls,
  unexported helper, `net/http` route registrations (exercises the go framework
  resolver → 2 `route` nodes), go.mod module path resolution.
  Golden: 27 nodes / 45 edges.
- `ts-small`: classes, interface, type alias, arrow function const, getter,
  async method, named + type-only imports, cross-file calls.
  Golden: 21 nodes / 37 edges.
