---
name: codegrapher
description: Use CodeGrapher to index a repository and trace symbol relationships before changing code.
---

# CodeGrapher

Use CodeGrapher when you need evidence about code relationships in the current
repository: symbol definitions, callers, callees, or the impact of a change.

Start by checking whether the repository is indexed:

```sh
codegrapher status --format json
```

If it is not initialized, index it once from the repository root:

```sh
codegrapher init
```

Refresh a previously initialized index after source changes:

```sh
codegrapher sync
```

Read commands (`status`, `query`, `node`, `callers`, `callees`, `impact`,
`path`, `files`, `affected`, `context`, `stacktrace`, `trace`,
`coverage targets`) open the index read-only and never modify it or scan for
changes; they answer from the index as it is. Add `--refresh` to run `sync`
first. `codegrapher status` reports pending changes (use `--no-pending` for a
cheap stats-only call; `pendingChanges` is then `null`). An index from an older
release fails with an "index needs upgrade" error (exit code 3); run
`codegrapher sync` or add `--refresh`.

For continuous freshness, compose the foreground server or start the durable
background owner:

```sh
codegrapher serve --watch
codegrapher daemon start
codegrapher daemon status --format json
codegrapher daemon stop
```

`serve` without capability flags enables every capability available in that
release; explicit flags select only those capabilities. `codegrapher watch`
remains a compatibility entry point for foreground watching.

Discover symbols with compact machine-readable metadata before loading source:

```sh
codegrapher query "<symbol>" --brief --format json
```

Then retrieve the selected semantic unit rather than a surrounding file chunk:

```sh
codegrapher node "<qualified-symbol>" --source --relations
codegrapher node "<symbol>" --file path/to/file.go --source
codegrapher node "<id-from-query-brief>" --source=footer
```

`node` writes Markdown by default. With `--source`, it first writes compact
JSON-shaped metadata in a fenced `json` block, then raw unescaped language
fences for the source; this is easier for an agent to read than code escaped
inside JSON. `--format json` is available for automation and always returns an
array (also for one requested symbol). Source ranges are line-bounded from the
index, not universal AST byte ranges.

For a batch, `--source=footer` prints all metadata and relationships first,
then a `## Sources` section. `--source` is shorthand for `--source=footer`.
Use `--source=inline` when each source should follow its symbol header.
Footer mode is text-only; JSON may use only inline source.

If a bare name has multiple definitions, `node` returns compact candidates and
never guesses a body. It exits non-zero as a disambiguation response; retry it
with `node "<exact id>" --source` (or `--file` / `--line`) using a candidate
from `query --brief`. Do not fall back to grep or a surrounding-file read.

`node` reads the index as it is and does not refresh it. When `--source` is
requested it verifies the current file bytes against the indexed hash: if the
file changed since indexing it withholds the source, marks the result
`"stale": true`, and tells you to rerun with `--refresh`, which syncs the index
first and then returns current source. It never slices a stale range.

In a Git worktree, `node` refuses an index owned by another checkout. Run
`codegrapher init` from the current worktree to create the local index; explicit
initialization paths are exact and do not walk up to an initialized ancestor.

When an agent knows an entry point and a target but not the intermediate
implementation route, ask CodeGrapher for one bounded static path before
loading bodies:

```sh
codegrapher path "main" "NewRootCmd" --max-hops 8
codegrapher path "<start-id>" "<target-id>" --source=footer
```

`path` follows only directed `calls` edges and returns a deterministic shortest
route with call-site locations and provenance. It does not guess ambiguous
endpoint names; use the exact IDs in its compact candidates. Default output is
metadata/signatures. `--source=footer` emits that metadata before deduplicated
raw Markdown code blocks; `--source=inline --format json` is available when
automation needs code in JSON. It is bounded by `--max-hops`, `--max-nodes`,
and `--max-edges`; respect a truncated result before requesting a wider search.

When debugging a runtime failure, map the supplied trace directly instead of
searching each `file:line` frame by hand:

```sh
codegrapher stacktrace panic.txt
pbpaste | codegrapher stacktrace --source=footer
```

`stacktrace` recognizes Go, V8 JS/TS, Python, JVM, .NET, Rust, and generic
location frames. It preserves runtime frame order and labels unmatched or
ambiguous frames instead of guessing. It selects the smallest enclosing
callable for a unique file+line match and deduplicates recursive/repeated
source bodies. Runtime stack adjacency is not claimed to be a static graph
edge. Use `--revision <deployed-git-sha>` when the trace must match a known
checkout. A `mismatch` or `stale` frame is evidence to investigate, not a
source-read instruction; CodeGrapher withholds source in those cases.
Both commands take a short consistent read lock after freshness checking. If
an index writer is active, retry rather than trusting a graph/source
combination from a partial update.

Use relationship verbs when you need a wider or transitive answer:

```sh
codegrapher callers "<symbol>" --format json
codegrapher callees "<symbol>" --format json
codegrapher impact "<symbol>" --format json
```

Treat the graph as investigation evidence. `node --source` is the preferred
source read for indexed symbols; use ordinary file reads for docs, configs, or
when CodeGrapher reports a freshness error.

Writing or extending tests, or covering uncovered lines from a coverage
profile? Use the `codegrapher-test-context` skill's `context` command instead
of repeating `node`/`callees` per symbol — see that skill for the workflow.
