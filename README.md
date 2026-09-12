# codegrapher

A code intelligence tool that builds and queries a SQLite knowledge graph of every
symbol, edge, and file in a codebase. Written in Go; single static binary, no
runtime dependencies.

Website: https://codegrapher.dev

<!-- dev-approach:v1 -->
## Our approach to development

We build with our own tooling:

- **[SpecScore](https://specscore.md)** — specify requirements as `SpecScore.md` artifacts
- **[SpecStudio](https://specscore.studio)** — author & manage specs across their lifecycle
- **[inGitDB](https://ingitdb.com)** — store structured data in Git where applicable
- **[DALgo](https://dalgo.io)** — data access layer for Go
- **[cover100.dev](https://cover100.dev)** — drive toward 100% test coverage
- **[DataTug](https://datatug.io)** — query & explore data
<!-- /dev-approach -->

## Usage

```
codegrapher init [path]      Initialize .codegraph/ and build the initial index
codegrapher uninit [path]    Remove .codegraph/ from a project
codegrapher index [path]     Full re-index
codegrapher sync [path]      Incremental re-index since last index
codegrapher status [path]    Index stats                         (--json)
codegrapher query <search>   Symbol search                       (-l limit, -k kind, --brief, --json)
codegrapher node <symbol>    Symbol metadata/source/relations    (--source[=footer|inline], --relations, --file, --line, --json)
codegrapher path <from> <to> One bounded static call path        (--max-hops, --max-nodes, --max-edges, --source[=footer|inline], --json)
codegrapher stacktrace [file] Map runtime frames to symbols       (--max-bytes, --max-frames, --revision, --source[=footer|inline], --json)
codegrapher files            Indexed file tree                   (--json)
codegrapher callers <symbol> What calls this symbol              (--json)
codegrapher callees <symbol> What this symbol calls              (--json)
codegrapher impact <symbol>  Blast-radius analysis               (--json)
codegrapher affected [files] Test files affected by changed sources (--json)
codegrapher watch [path]     Compatibility foreground watcher  (--verbose)
codegrapher serve [path]     Composable foreground server       (--watch, --mcp)
codegrapher daemon start     Background freshness owner
codegrapher daemon status    Daemon lifecycle/freshness health  (--json)
codegrapher daemon stop      Gracefully stop background owner
codegrapher export [path]    Export index as INGR snapshot files
codegrapher import [path]    Import an INGR snapshot into the local store
codegrapher unlock [path]    Remove a stale lock file
codegrapher version          Print version
```

### Trace a precise code route

Use `path` when the entry and target symbols are known but the intermediate
calls are not. It returns one deterministic shortest directed `calls` path;
the default is signatures and locations, not source bodies.

```sh
codegrapher path main NewRootCmd --max-hops 8 --max-nodes 1000 --max-edges 5000
codegrapher path "function:exact-start-id" "function:exact-target-id" --source=footer
```

Each hop includes edge kind, call-site line/column, and extraction provenance.
Ambiguous endpoints produce candidates and require an exact ID. `--source` is
Markdown-only: `footer` writes compact JSON metadata followed by deduplicated,
raw language fences; `inline` is also valid with `--format json`.
The traversal is explicitly bounded by hops, visited nodes, and inspected
edges; a truncated result says so rather than silently broadening the search.

### Read a runtime stack without file-chunk hunting

`stacktrace` maps file-and-line runtime frames to the smallest enclosing
indexed callable. It accepts a trace file, `-`, or stdin:

```sh
codegrapher stacktrace panic.txt
pbpaste | codegrapher stacktrace --source=footer
codegrapher stacktrace trace.txt --source=inline --format json
codegrapher stacktrace prod-panic.txt --revision "<deployed-git-sha>"
```

Go, V8 JavaScript/TypeScript, Python, JVM, .NET, Rust, and generic
`file:line[:column]` frames are recognized. Stack order and unmatched/external
frames are preserved. A stack is runtime evidence, so adjacent frames do not
need a static graph edge. Source is deduplicated for recursion and repeated
frames, and only returned after the indexed file hash is verified.
Input bytes and mapped-frame count are bounded. A runtime function name that
contradicts the symbol containing its `file:line` is reported as `mismatch`,
and a shifted line with a matching name in the same file is reported as
`stale`; neither silently returns source.
After refreshing, each command takes the index's short cross-process read lock
while it resolves relationships and verifies source. If indexing is active it
returns a busy/retry error rather than combining an old graph with new code.

## Snapshot / viewer

`codegrapher export` writes the index as [INGR](https://ingr.io) snapshot files
that can be committed to the repo. `codegrapher import` seeds the store from a
committed snapshot, turning cold-start indexing into seconds. Committed snapshots
also power the codegrapher.dev browser viewer for symbol search and callers/callees
navigation without a local index.

## Language support

Go and TypeScript/JavaScript. Go files are parsed by the standard library
`go/parser`; TypeScript/JavaScript are parsed via
[gotreesitter](https://github.com/odvcencio/gotreesitter) (pure Go, no CGO).
`go.mod` files are also indexed, producing module and dependency nodes with
require/replace/exclude relationships. `package.json` files are likewise
indexed, producing module and dependency nodes across `dependencies`,
`devDependencies`, `peerDependencies`, and `optionalDependencies`, stored in a
dedicated node scope merged into JS/TS queries.

## Build

```sh
CGO_ENABLED=0 go build ./cmd/codegrapher
```

No CGO, no runtime dependencies. Cross-compiles to any Go target.

## License

Apache-2.0. See LICENSE and NOTICE for attribution to the original
[codegraph](https://github.com/colbymchenry/codegraph) (MIT, Colby McHenry) and
the [gotreesitter](https://github.com/odvcencio/gotreesitter) fork (MIT, Oscar
Villavicencio) used for TypeScript/JavaScript parsing.
