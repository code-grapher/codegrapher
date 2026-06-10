#!/usr/bin/env bash
# Capture golden outputs from the ORIGINAL codegraph CLI against the parity fixtures.
# Usage: tools/parity/capture-golden.sh [codegraph-binary]
# Re-run only when intentionally re-baselining (records original version in VERSION).
set -euo pipefail

CODEGRAPH="${1:-codegraph}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
FIXTURES="$ROOT/testdata/fixtures"
GOLDEN="$ROOT/testdata/golden"

command -v "$CODEGRAPH" >/dev/null || { echo "original codegraph CLI not found" >&2; exit 1; }
mkdir -p "$GOLDEN"
"$CODEGRAPH" --version > "$GOLDEN/VERSION" 2>/dev/null || true

# Symbols to exercise per fixture: <fixture> <query-term> <symbol1> <symbol2> <qualified>
capture() {
  local fixture="$1" query="$2"; shift 2
  local dir="$FIXTURES/$fixture" out="$GOLDEN/$fixture"
  mkdir -p "$out"

  # Fresh index every capture for determinism (CI-safe: no watcher).
  rm -rf "$dir/.codegraph"
  (cd "$dir" && CODEGRAPH_NO_WATCH=1 "$CODEGRAPH" init >/dev/null 2>&1)

  (cd "$dir" \
    && "$CODEGRAPH" status --json   > "$out/status.json" \
    && "$CODEGRAPH" files  --json   > "$out/files.json" \
    && "$CODEGRAPH" query "$query" --json -l 20 > "$out/query.json")

  local sym
  for sym in "$@"; do
    (cd "$dir" \
      && "$CODEGRAPH" callers "$sym" --json > "$out/callers-$sym.json" 2>/dev/null \
      && "$CODEGRAPH" callees "$sym" --json > "$out/callees-$sym.json" 2>/dev/null \
      && "$CODEGRAPH" impact  "$sym" --json > "$out/impact-$sym.json"  2>/dev/null) || true
  done

  rm -rf "$dir/.codegraph"
}

mkdir -p "$GOLDEN"
capture go-small  "store"  "Get" "Set" "Lookup" "normalize" "handleGreet" "Store::Get"
capture ts-small  "store"  "get" "set" "lookup" "normalize" "describe" "Cache::lookup"

echo "golden outputs captured under $GOLDEN"
