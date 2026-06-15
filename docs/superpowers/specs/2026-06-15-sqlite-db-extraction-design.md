# SQLite `.db` extraction design (2026-06-15)

## Goal (owner, verbatim)

> add support for graph creation for SQLite .db files: tables/views (columns,
> constraints, indexes, etc). What else? Let's add all meta-information we can.
> records count for tables

Extract the **full schema catalog** of a binary SQLite database file into the
knowledge graph: tables, views, columns, constraints, indexes, triggers, and
foreign-key relationships — plus best-effort row counts.

This is distinct from the existing **text-SQL** extraction
(`2026-06-14-sql-extraction-design.md`), which parses `.sql` source text with
tree-sitter. This pass introspects a **live binary database file**.

## Reader: `modernc.org/sqlite` (pure Go, CGO off)

`CGO_ENABLED=0` is a launch requirement (single static binary,
cross-compilation). The standard `mattn/go-sqlite3` driver is CGO and is
therefore **excluded**. We use **`modernc.org/sqlite`** — a pure-Go SQLite
engine exposing the standard `database/sql` interface (PRAGMAs, `COUNT(*)`),
which builds and cross-compiles with CGO disabled. Cost: a large module in
`go.mod`/`go.sum`, amortized by the Go build cache.

The database is opened **read-only and immutable**
(`file:<path>?mode=ro&immutable=1`).

### Integration wrinkle: path, not bytes

`ExtractFile(path, content []byte, lang)` is built around file *contents*. A
binary `.db` cannot be driven from a `[]byte` — `modernc.org/sqlite` opens a
**file path**. The SQLite walker therefore opens by the `path` argument (which
`ExtractFile` already receives) and **ignores the byte slice**. The indexer is
special-cased so it does not read a whole (possibly multi-GB) `.db` into memory
only to discard it. This deviation from the walker contract is isolated to
`walk_sqlite.go` and the one indexer dispatch site.

## Detection

- New language `LangSQLite = "sqlite"`. Extensions: `.db`, `.sqlite`, `.sqlite3`.
- `.db` is ambiguous (used by many unrelated tools), so detection **sniffs the
  16-byte magic header** `"SQLite format 3\0"`. A `.db`/`.sqlite*` file without
  that header is not treated as SQLite (falls through to unknown).
- One `.db` file → one file node containing all schema objects.

## New node kinds (model change)

Three SQL-native kinds are added to `model`:

- `KindIndex   = "index"`
- `KindTrigger = "trigger"`
- `KindConstraint = "constraint"`

These touch all node consumers (store, query, MCP, goldens) but are additive.

## Activate `Node.Metadata` (structured attributes)

The `nodes` table already has an **unused `metadata TEXT` (JSON) column**
(vestigial from upstream; the Go `Node` struct never exposed it). We activate
it:

- Add `Metadata map[string]any` to `model.Node` (JSON, `omitempty`).
- Wire it through `store/nodes.go` insert/scan into the existing `metadata`
  column — **no DB migration** (column already present).
- The golden node serializer emits `metadata` (sorted keys for determinism),
  **except** it omits the single volatile key `rowCount` (precedent: the golden
  serializer already omits `updatedAt`).

`Signature` keeps a human-readable summary (e.g. `CREATE TABLE users`, column
type text) for continuity with the text-SQL extractor; structured facts live in
`Metadata`.

## Symbol model

Introspection sources: `sqlite_master` (type/name/tbl_name/sql),
`PRAGMA table_xinfo`, `PRAGMA foreign_key_list`, `PRAGMA index_list`,
`PRAGMA index_xinfo`, and — for CHECK constraints and constraint names — the
stored `CREATE TABLE` DDL text (see "CHECK constraints" below).

### File node

`Metadata`: `userVersion`, `applicationId`, `textEncoding`
(`PRAGMA user_version` / `application_id` / `encoding`).

### Table → `KindStruct`

- `Signature`: `CREATE TABLE <name>`.
- `Metadata`: `objectType` = `table` | `virtual`; `withoutRowid` (bool);
  `strict` (bool); `module` (for virtual tables, e.g. `fts5`);
  `rowCount` (int, **volatile**, from `SELECT COUNT(*)`).
- `contains` → its columns, indexes, and constraints.

### View → `KindStruct`

- `Signature`: `CREATE VIEW <name>`.
- `Metadata`: `objectType` = `view`.
- `contains` → its columns.
- `references` → each table/view in its `FROM`/`JOIN`, recovered by feeding the
  view's stored `SELECT` DDL to the **existing tree-sitter SQL ref logic**.

### Column → `KindField`

- `Signature`: declared type text (e.g. `INTEGER`).
- `Metadata`: `typeAffinity`, `notNull` (bool), `default` (text|null),
  `pkPosition` (int, 0 = not part of PK), `generated` (bool), `hidden` (bool),
  `collation`.

### Index → `KindIndex`

- `Metadata`: `unique` (bool), `partial` (bool), `origin`
  (`c` = CREATE INDEX | `u` = UNIQUE constraint | `pk` = PRIMARY KEY |
  `auto` = autoindex).
- `contains`-parent: the table. `references` → each indexed column.
- Internal `sqlite_autoindex_*` objects are summarized as the owning
  constraint's backing index (origin recorded), not emitted as standalone nodes.

### Trigger → `KindTrigger`

- `Signature`: `CREATE TRIGGER <name>`.
- `Metadata`: `timing` (`BEFORE` | `AFTER` | `INSTEAD OF`), `event`
  (`INSERT` | `UPDATE` | `DELETE`).
- `contains`-parent: its table (`tbl_name`). `references` → that table.

### Constraint → `KindConstraint` (child of its table)

One node per named/table-level constraint:

- `Metadata.subtype` = `primaryKey` | `foreignKey` | `unique` | `check`.
- `columns`: ordered list of participating column names.
- For `foreignKey`: `refTable`, `refColumns`, `onDelete`, `onUpdate`. The FK
  constraint node **emits `references` edges** column→column and table→table.
- For `check`: `expression` (text recovered from DDL — see below).

`NOT NULL` and `DEFAULT` are **column-intrinsic** and stay as column `Metadata`
(not constraint nodes), since PRAGMAs expose them directly per column.

### CHECK constraints — DDL parse required

SQLite exposes **no PRAGMA for CHECK constraints** (nor for constraint names).
The only source is the stored `CREATE TABLE` DDL in `sqlite_master.sql`. We feed
that DDL to the **existing tree-sitter SQL extractor** to recover CHECK
expressions and named constraints — reusing code we already have rather than
adding a second parser. PRAGMA-derived structure (PK/FK/UNIQUE) remains the
authority for those; the DDL parse augments it with CHECK + names.

## Edges (reuse existing kinds — no new edge kinds)

- `contains`: file → table/view; table → column/index/constraint/trigger.
- `references`:
  - FK constraint → referenced column (column→column) and referenced table.
  - Index → each indexed column.
  - Trigger → its table.
  - View → each `FROM`/`JOIN` table/view.

## Resolver & cross-linking

New `resolveSqliteRef` resolves `references` by name:

- **Intra-`.db`**: FK targets, view FROM tables, trigger tables, index columns
  resolve to nodes within the same `.db` file first.
- **Cross-link `.db` ↔ `.sql` by name** (owner decision): a `.db` table `users`
  also links to a `CREATE TABLE users` in any `.sql` file, and vice versa.
  Resolution considers both `LangSQLite` and `LangSql` `KindStruct` nodes,
  same-file-first then any-file (the shared `pickBestNode` / through-name
  pattern). Name collisions across schemas may produce extra links; this is the
  accepted cost of the requested migration/drift linkage.

Unknown/unresolved targets produce no edge (deterministic; no guessing).

## Determinism

- Iterate `sqlite_master` `ORDER BY type, name`; PRAGMA results by their natural
  (cid/seqno) order. Stable node/edge ordering.
- Skip internal objects (`sqlite_sequence`, `sqlite_stat*`,
  `sqlite_autoindex_*` as standalone nodes).
- `rowCount` is the only non-deterministic value and is excluded from goldens.

## Testing

- Committed binary fixture `testdata/fixtures/sqlite-small/app.db` **plus a
  committed generator** (`gen.sql` run through the `sqlite3` CLI, or a tiny Go
  program) so the fixture is reproducible — never hand-crafted. The fixture
  exercises: a table with PK/FK/UNIQUE/CHECK/NOT NULL/DEFAULT/generated column,
  a second table, a view selecting from both, a non-unique partial index, a
  unique index, a trigger, and a virtual (FTS5) table.
- Deterministic self-goldens over the extracted graph (`rowCount` excluded).
- Parity registration alongside the other languages; external-corpus hook
  (a real `.db`) for smoke coverage.

## Out of scope (v1)

- Deep analysis of trigger **bodies** and CHECK **expression internals** (we
  capture the trigger→table link and the CHECK expression *text*, but do not
  resolve column references inside those bodies).
- `ATTACH`ed databases, WAL/journal contents.
- Physical/file-config PRAGMAs (`page_size`, `journal_mode`, `auto_vacuum`) —
  not schema, not graph-relevant.
- `WITH` CTE / set-operation modeling beyond what the reused text-SQL ref logic
  already handles for view bodies.

## Gates

gofmt, `go vet ./...`, `CGO_ENABLED=0 go build ./...`,
`CGO_ENABLED=0 go test -count=1 ./...` — all green, including the new
`sqlite-small` goldens and the binary parity test. The `modernc.org/sqlite`
dependency must not reintroduce CGO (verify the build still succeeds with
`CGO_ENABLED=0`).
