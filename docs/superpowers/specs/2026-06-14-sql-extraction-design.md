# SQL extraction design (2026-06-14)

## Goal (owner, verbatim)

> detect CREATE TABLE/VIEW and be able to link SELECT to them.

Optimize for exactly that: a **schema + query-reference graph**. Table/view-level
linking only — column-level linking is out of scope this pass.

## Grammar probe findings (tree-sitter `sql` via gotreesitter)

Probed with `internal/tsparse/sql_probe_test.go`. The grammar parses our
fixture corpus with **no ERROR nodes** in BOTH upper- and lower-case keyword
forms, and handles schema-qualified names and `CREATE OR REPLACE FUNCTION`
cleanly. Relevant node vocabulary:

- `source_file` — root; statements are direct children.
- `create_table_statement` — name child is `identifier` (bare) or `dotted_name`
  (`schema.name`); columns under `table_parameters` → `table_column`, each with
  fields `name` (identifier) and `type` (a `type` node).
- `create_view_statement` — name child `identifier`/`dotted_name`; body under
  `view_body` → `select_statement`.
- `create_function_statement` — name child `identifier`; params under
  `create_function_parameters`. (Parses cleanly → we emit a KindFunction.)
- `select_statement` — `select_clause` + `from_clause` (+ `where_clause` etc).
- `from_clause` — table targets are bare `identifier` / `dotted_name`, OR a
  `join_clause` whose children are `identifier`/`alias`/`join_condition`
  (aliases and join conditions are skipped; only table identifiers count).
- `insert_statement` — `identifier` after `INTO`.
- `update_statement` — `identifier` after `UPDATE`.
- `delete_statement` — `from_clause`.
- `dotted_name` (`schema.tbl`, also `alias.col` in SELECT lists) — `identifier .
  identifier`.

## Symbol model (reuse existing kinds — NO new node/edge kinds)

- `CREATE TABLE name (...)` → **KindStruct** named `name`. Each `table_column`
  → **KindField** (name; column type text into `Signature`).
- Schema-qualified `schema.name` → Name = `name`, QualifiedName = `schema.name`.
- `CREATE VIEW name AS SELECT ...` → **KindStruct** named `name`. A view is a
  virtual table, so we deliberately reuse KindStruct rather than add a kind.
  The view node is the **container** for its SELECT's table references.
- `CREATE [OR REPLACE] FUNCTION/PROCEDURE name` → **KindFunction**. The generic
  grammar parses this cleanly (no ERROR), so we keep it. We do not descend into
  procedural bodies (dialect-specific, out of scope).

## Edges (the link the owner wants) — all `references` (EdgeReferences)

A `SELECT ... FROM t [JOIN u ...]` emits a `references` edge per referenced
table/view (`t`, `u`), including tables in subqueries and DML
(`INSERT INTO t` / `UPDATE t` / `DELETE FROM t`). Source of the edge:

- Inside `CREATE VIEW v AS SELECT ... FROM t` → edge is emitted **from the view
  node** `v` (so `v → t`).
- A standalone top-level statement (SELECT/INSERT/UPDATE/DELETE, no enclosing
  view) → edge is emitted **from the file node** (so `file → t`). This
  guarantees "link SELECT to the table" even for bare queries.

References resolve by table/view **name** to the `CREATE TABLE`/`CREATE VIEW`
node (KindStruct), **including cross-file** — a SELECT in one `.sql` referencing
a table created in another `.sql` resolves to that table node. Same-file
preference, then any file (the `pickBestNode` / through-name pattern shared with
`resolveCRef`/`resolveGenericRef`).

Unknown tables (system tables, or tables not defined in-repo) are left
unresolved — no edge, no guessing. Resolution is deterministic.

## Resolver

`resolveSqlRef` (case `model.LangSql`): resolve a `references` ref by name to the
best `KindStruct` SQL node (CREATE TABLE / CREATE VIEW), preferring same-file
then any file. SQL emits only `references` refs; no imports/calls.

## Limitations / deviations

- Column-level references are out of scope (table/view-level only).
- We only follow FROM/JOIN table identifiers and DML targets; CTE (`WITH`) names,
  set operations, and correlated-subquery aliases beyond the FROM list are not
  specially modeled — nested `select_statement`s are still walked, so their
  FROM tables are captured.
- View references are attributed to the view node; all other top-level query
  references are attributed to the file node (no per-statement node, matching
  the "no new node kinds" constraint).
- **Grammar quirk:** `UPDATE schema.table …` (schema-qualified UPDATE target)
  does NOT parse — the grammar emits ERROR nodes and treats `schema.table` as a
  bare identifier + stray `.`. Bare-name UPDATE parses fine, as do
  schema-qualified `INSERT INTO schema.t` / `DELETE FROM schema.t`. Fixtures use
  bare-name UPDATE. (CREATE TABLE/VIEW, SELECT, INSERT, DELETE all accept the
  schema-qualified form.)
- If a future dialect produces ERROR nodes, extraction degrades gracefully:
  we walk whatever parsed and never panic (unknown node kinds simply recurse).
