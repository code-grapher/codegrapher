#!/usr/bin/env bash
# rebaseline-golden.sh — regenerate all testdata/golden/{go-small,ts-small} goldens
# from OUR binary (CGO_ENABLED=0 go build ./cmd/codegrapher).
#
# Goldens are now self-baselined: our output IS the spec. The original capture-*.sh
# scripts are retained for comparison only (they require the upstream Node 22 CLI).
#
# Usage: tools/parity/rebaseline-golden.sh
#
# Prerequisites: Go (for the binary build), sqlite3 (for extraction/resolution dumps).
# CODEGRAPH_NO_WATCH=1 is set for determinism; each fixture gets a fresh index.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
FIXTURES="$ROOT/testdata/fixtures"
GOLDEN="$ROOT/testdata/golden"
BIN="/tmp/codegrapher-rebaseline"

# Build our binary.
echo "Building codegrapher binary..."
CGO_ENABLED=0 go build -o "$BIN" "$ROOT/cmd/codegrapher"
echo "Built: $BIN"

command -v sqlite3 >/dev/null || { echo "sqlite3 not found on PATH" >&2; exit 1; }

NODE_COLS="id,kind,name,qualified_name,file_path,language,start_line,end_line,start_column,end_column,docstring,signature,visibility,is_exported,is_async,is_static,is_abstract,decorators,type_parameters,return_type"

# normalize_json: replace empty sqlite3 output (no rows) with []
normalize_json() {
  local f="$1"
  [ -s "$f" ] || echo "[]" > "$f"
}

capture() {
  local fixture="$1" query="$2"; shift 2
  local dir="$FIXTURES/$fixture" out="$GOLDEN/$fixture"
  mkdir -p "$out"

  echo ""
  echo "=== Rebaselining $fixture ==="

  # Fresh index every time for determinism.
  rm -rf "$dir/.codegraph"
  (cd "$dir" && CODEGRAPH_NO_WATCH=1 "$BIN" init >/dev/null 2>&1)
  local db="$dir/.codegraph/codegraph.db"

  # CLI goldens.
  (cd "$dir" \
    && "$BIN" status --json   > "$out/status.json" \
    && "$BIN" files  --json   > "$out/files.json" \
    && "$BIN" query "$query" --json -l 20 > "$out/query.json")

  local sym
  for sym in "$@"; do
    (cd "$dir" \
      && "$BIN" callers "$sym" --json > "$out/callers-$sym.json" 2>/dev/null \
      && "$BIN" callees "$sym" --json > "$out/callees-$sym.json" 2>/dev/null \
      && "$BIN" impact  "$sym" --json > "$out/impact-$sym.json"  2>/dev/null) || true
  done

  # Extraction goldens (post-resolution DB dumps, matching capture-extraction-golden.sh).
  sqlite3 -json "$db" \
    "SELECT $NODE_COLS FROM nodes ORDER BY id" \
    > "$out/extraction-nodes.json"
  normalize_json "$out/extraction-nodes.json"

  sqlite3 -json "$db" \
    "SELECT source,target,kind FROM edges WHERE kind='contains' ORDER BY source,target" \
    > "$out/extraction-contains.json"
  normalize_json "$out/extraction-contains.json"

  sqlite3 -json "$db" \
    "SELECT from_node_id,reference_name,reference_kind,line,col,candidates,file_path,language FROM unresolved_refs ORDER BY from_node_id,reference_name,line" \
    > "$out/extraction-unresolved.json"
  normalize_json "$out/extraction-unresolved.json"

  # Resolution goldens (non-contains edges, matching capture-resolution-golden.sh).
  sqlite3 -json "$db" \
    "SELECT source,target,kind,provenance,line,col FROM edges WHERE kind != 'contains' ORDER BY source,target,kind,line,col" \
    > "$out/resolution-edges.json"
  normalize_json "$out/resolution-edges.json"

  echo "  CLI: status, files, query, callers/callees/impact for ${*}"
  echo "  DB:  extraction-nodes, extraction-contains, extraction-unresolved, resolution-edges"

  rm -rf "$dir/.codegraph"
}

capture go-small "store" "Get" "Set" "Lookup" "normalize" "handleGreet" "Store::Get"
capture ts-small "store" "get" "set" "lookup" "normalize" "describe" "Cache::lookup"

echo ""
echo "=== MCP goldens ==="
echo "Regenerating MCP goldens via our mcp server..."
rebaseline_mcp() {
  local fixture="$1"
  local dir="$FIXTURES/$fixture"
  local out="$GOLDEN/$fixture/mcp"
  mkdir -p "$out"

  rm -rf "$dir/.codegraph"
  (cd "$dir" && CODEGRAPH_NO_WATCH=1 "$BIN" init >/dev/null 2>&1)

  # Drive our MCP server with the same messages as capture-mcp-golden.sh
  # but using our binary instead of the original Node CLI.
  python3 - "$fixture" "$dir" "$out" "$BIN" << 'PYEOF'
import subprocess, json, sys, os, threading, queue, time

fixture = sys.argv[1]
fixture_dir = sys.argv[2]
golden_dir = sys.argv[3]
bin_path = sys.argv[4]

def run_capture(msgs, id_to_file):
    env = {**os.environ, 'CODEGRAPH_NO_WATCH': '1'}
    proc = subprocess.Popen(
        [bin_path, 'serve', '--mcp', '--path', fixture_dir],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        cwd=fixture_dir, env=env,
    )
    line_q = queue.Queue()
    def reader():
        for line in proc.stdout:
            line_q.put(line.decode('utf-8', errors='replace').rstrip('\n'))
        line_q.put(None)
    threading.Thread(target=reader, daemon=True).start()

    written = 0
    for msg in msgs:
        msg_str = json.dumps(msg) + '\n'
        try:
            proc.stdin.write(msg_str.encode())
            proc.stdin.flush()
        except BrokenPipeError:
            break
        if 'id' not in msg:
            time.sleep(0.05)
            continue
        msg_id = msg['id']
        deadline = time.time() + 30
        found = False
        while time.time() < deadline:
            try:
                line = line_q.get(timeout=1.0)
                if line is None:
                    line_q.put(None)
                    break
                if not line.strip():
                    continue
                try:
                    obj = json.loads(line)
                    if obj.get('id') == msg_id and msg_id in id_to_file:
                        fname = os.path.join(golden_dir, id_to_file[msg_id])
                        with open(fname, 'w') as f:
                            json.dump(obj, f, indent=2)
                            f.write('\n')
                        written += 1
                        found = True
                        break
                except json.JSONDecodeError:
                    pass
            except queue.Empty:
                pass
        if not found:
            print(f"  timeout/closed for id={msg_id}", file=sys.stderr)
    try:
        proc.stdin.close()
    except: pass
    try:
        proc.wait(timeout=5)
    except: pass
    print(f"  {written} MCP golden files written for {fixture}")

id_to_file = {
    1: "initialize.json", 2: "tools-list.json", 3: "status.json",
    4: "files.json", 5: "search-1.json", 6: "callers-1.json",
    7: "callees-1.json", 8: "impact-1.json", 9: "node-1.json",
    10: "explore-1.json", 11: "explore-2.json", 12: "explore-3.json",
}

if fixture == "go-small":
    sym1 = "Get"
    explore2 = "Get Set Lookup"
    explore3 = "normalize"
    q_explore1 = "how does the store work"
else:
    sym1 = "get"
    explore2 = "get set lookup"
    explore3 = "normalize"
    q_explore1 = "how does the cache work"

msgs = [
    {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}},
    {"jsonrpc":"2.0","method":"notifications/initialized","params":{}},
    {"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}},
    {"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"codegraph_status","arguments":{}}},
    {"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"codegraph_files","arguments":{}}},
    {"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"codegraph_search","arguments":{"query":"store","limit":20}}},
    {"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"codegraph_callers","arguments":{"symbol":sym1}}},
    {"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"codegraph_callees","arguments":{"symbol":sym1}}},
    {"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"codegraph_impact","arguments":{"symbol":sym1}}},
    {"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"codegraph_node","arguments":{"symbol":sym1,"includeCode":True}}},
    {"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":q_explore1}}},
    {"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":explore2}}},
    {"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":explore3}}},
]
run_capture(msgs, id_to_file)
PYEOF

  rm -rf "$dir/.codegraph"
  echo "  MCP goldens written to $out"
}

rebaseline_mcp go-small
rebaseline_mcp ts-small

echo ""
echo "=== Rebaseline complete ==="
echo "Goldens are self-baselined: our output is the spec."
echo "Original capture-*.sh scripts are retained for comparison only."
echo ""
echo "Changed files (git diff --name-only):"
git -C "$ROOT" diff --name-only -- testdata/golden/ 2>/dev/null || true
