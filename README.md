# codegrapher

A code intelligence tool that builds and queries a SQLite knowledge graph of every
symbol, edge, and file in a codebase. Written in Go; single static binary, no
runtime dependencies.

Website: https://codegrapher.dev

## Usage

```
codegrapher init [path]      Initialize .codegraph/ and build the initial index
codegrapher uninit [path]    Remove .codegraph/ from a project
codegrapher index [path]     Full re-index
codegrapher sync [path]      Incremental re-index since last index
codegrapher status [path]    Index stats                         (--json)
codegrapher query <search>   Symbol search                       (-l limit, -k kind, --json)
codegrapher files            Indexed file tree                   (--json)
codegrapher callers <symbol> What calls this symbol              (--json)
codegrapher callees <symbol> What this symbol calls              (--json)
codegrapher impact <symbol>  Blast-radius analysis               (--json)
codegrapher affected [files] Test files affected by changed sources (--json)
codegrapher serve            MCP server (stdio)
codegrapher export [path]    Export index as INGR snapshot files
codegrapher import [path]    Import an INGR snapshot into the local store
codegrapher unlock [path]    Remove a stale lock file
codegrapher version          Print version
```

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
require/replace/exclude relationships.

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
