# Golden-parity harness

> **Note (2026-06-11): Goldens are now self-baselined — our output is the spec.**
> After the UB-1 docstring fix and the go/parser flip (ADR-003), the goldens were
> re-generated from our own binary using `tools/parity/rebaseline-golden.sh`.
> The original `capture-*.sh` scripts are retained for comparison only and still
> require the upstream Node 22 CLI to run.

`testdata/golden/` holds CLI and DB-dump outputs that define the behavioral spec
for the Go port. Re-baseline with:

```sh
tools/parity/rebaseline-golden.sh   # uses OUR binary — no external CLI needed
```

To compare against the original upstream CLI (requires `codegraph` on PATH + Node 22):

```sh
tools/parity/capture-golden.sh          # CLI goldens from upstream
tools/parity/capture-extraction-golden.sh  # extraction DB dumps from upstream
tools/parity/capture-resolution-golden.sh  # resolution DB dumps from upstream
tools/parity/capture-mcp-golden.sh         # MCP goldens from upstream
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
