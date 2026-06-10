#!/usr/bin/env bash
# Capture extraction-layer goldens from the ORIGINAL codegraph CLI: raw dumps of
# the nodes table, contains-edges, and unresolved_refs after a full index of each
# parity fixture. Used by internal/extract/parity_test.go.
#
# Usage: tools/parity/capture-extraction-golden.sh [codegraph-binary]
# Re-run only when intentionally re-baselining.
#
# NOTE: the dump is taken AFTER a full index, i.e. post-resolution. The original
# deletes unresolved_refs it managed to resolve, so for fixtures where everything
# resolves these files are empty — call-reference parity is instead verified at
# the resolution layer against callers-*/callees-* goldens.
set -euo pipefail

CODEGRAPH="${1:-codegraph}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
FIXTURES="$ROOT/testdata/fixtures"
GOLDEN="$ROOT/testdata/golden"

command -v "$CODEGRAPH" >/dev/null || { echo "original codegraph CLI not found" >&2; exit 1; }
command -v sqlite3 >/dev/null || { echo "sqlite3 not found" >&2; exit 1; }

NODE_COLS="id,kind,name,qualified_name,file_path,language,start_line,end_line,start_column,end_column,docstring,signature,visibility,is_exported,is_async,is_static,is_abstract,decorators,type_parameters,return_type"

capture() {
  local fixture="$1"
  local dir="$FIXTURES/$fixture" out="$GOLDEN/$fixture" db
  mkdir -p "$out"

  rm -rf "$dir/.codegraph"
  (cd "$dir" && CODEGRAPH_NO_WATCH=1 "$CODEGRAPH" init >/dev/null 2>&1)
  db="$dir/.codegraph/codegraph.db"

  sqlite3 -json "$db" "SELECT $NODE_COLS FROM nodes ORDER BY id" > "$out/extraction-nodes.json"
  sqlite3 -json "$db" "SELECT source,target,kind FROM edges WHERE kind='contains' ORDER BY source,target" > "$out/extraction-contains.json"
  sqlite3 -json "$db" "SELECT from_node_id,reference_name,reference_kind,line,col,candidates,file_path,language FROM unresolved_refs ORDER BY from_node_id,reference_name,line" > "$out/extraction-unresolved.json"

  # sqlite3 -json prints nothing for empty result sets; keep files valid JSON.
  local f
  for f in extraction-nodes extraction-contains extraction-unresolved; do
    [ -s "$out/$f.json" ] || echo "[]" > "$out/$f.json"
  done

  rm -rf "$dir/.codegraph"
}

capture go-small
capture ts-small
echo "extraction goldens captured to $GOLDEN"
