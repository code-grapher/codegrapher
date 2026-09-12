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

Before node lookup, CodeGrapher compares the persisted Git revision with the
current revision, then uses Git's dirty/untracked candidates and persisted file
hashes to refresh only relevant files. This also catches a clean commit made
after indexing. When `--source` is requested it verifies the current file bytes
against the indexed hash; it refreshes once or returns an explicit stale/lock/
read error rather than slicing a stale range.

Use relationship verbs when you need a wider or transitive answer:

```sh
codegrapher callers "<symbol>" --format json
codegrapher callees "<symbol>" --format json
codegrapher impact "<symbol>" --format json
```

Treat the graph as investigation evidence. `node --source` is the preferred
source read for indexed symbols; use ordinary file reads for docs, configs, or
when CodeGrapher reports a freshness error.
