#!/usr/bin/env bash
set -euo pipefail
REPO="$(cd "$(dirname "$0")/../.." && pwd)"
CODEGRAPH="$REPO/../codegraph/dist/bin/codegraph.js"

FIXTURES=("go-small" "ts-small")

command -v sqlite3 >/dev/null || { echo "sqlite3 not found" >&2; exit 1; }
[ -f "$CODEGRAPH" ] || { echo "codegraph not found at $CODEGRAPH" >&2; exit 1; }

for fixture in "${FIXTURES[@]}"; do
  dir="$REPO/testdata/fixtures/$fixture"
  out="$REPO/testdata/golden/$fixture"
  mkdir -p "$out"

  echo "Capturing resolution goldens for $fixture..."
  rm -rf "$dir/.codegraph"
  (cd "$dir" && CODEGRAPH_ALLOW_UNSAFE_NODE=1 CODEGRAPH_NO_WATCH=1 node "$CODEGRAPH" init >/dev/null 2>&1)
  db="$dir/.codegraph/codegraph.db"
  sqlite3 -json "$db" "SELECT source,target,kind,provenance,line,col FROM edges WHERE kind != 'contains' ORDER BY source,target,kind,line,col" > "$out/resolution-edges.json"
  [ -s "$out/resolution-edges.json" ] || echo "[]" > "$out/resolution-edges.json"
  rm -rf "$dir/.codegraph"
  echo "Done: $out/resolution-edges.json"
done
