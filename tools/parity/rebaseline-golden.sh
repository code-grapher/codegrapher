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

# sqlite_union OUT SELECT TABLE WHERE ORDER DB1 [DB2 ...]
# Dumps `SELECT <SELECT> FROM <TABLE> [WHERE <WHERE>] ORDER BY <ORDER>` UNION-ed
# across all given scope DBs (identical schema) as a single -json array.
sqlite_union() {
  local out="$1" select="$2" table="$3" where="$4" order="$5"; shift 5
  local dbs=("$@")
  local main="${dbs[0]}"
  local attaches="" union=""
  union="SELECT $select FROM \"$table\""
  [ -n "$where" ] && union="$union WHERE $where"
  local i=0 db
  for db in "${dbs[@]:1}"; do
    i=$((i+1))
    attaches="$attaches ATTACH '$db' AS s$i;"
    local u="SELECT $select FROM s$i.\"$table\""
    [ -n "$where" ] && u="$u WHERE $where"
    union="$union UNION ALL $u"
  done
  sqlite3 -json "$main" "$attaches $union ORDER BY $order;" > "$out"
  normalize_json "$out"
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
  # Per-scope recordset layout: the DB filename is scope-dependent
  # (e.g. codegraph-go-v1.db, codegraph-typescript-v0.db). A fixture may
  # produce multiple scope DBs (e.g. ts-small now has typescript-v0 + node-v0);
  # extraction/resolution dumps UNION across all of them.
  local dbs=("$dir"/.codegraph/codegraph-*.db)

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
  sqlite_union "$out/extraction-nodes.json" "$NODE_COLS" "nodes" "" "id" "${dbs[@]}"
  sqlite_union "$out/extraction-contains.json" "source,target,kind" "edges" "kind='contains'" "source,target" "${dbs[@]}"
  sqlite_union "$out/extraction-unresolved.json" "from_node_id,reference_name,reference_kind,line,col,candidates,file_path,language" "unresolved_refs" "" "from_node_id,reference_name,line" "${dbs[@]}"

  # Resolution goldens (non-contains edges, matching capture-resolution-golden.sh).
  sqlite_union "$out/resolution-edges.json" "source,target,kind,provenance,line,col" "edges" "kind != 'contains'" "source,target,kind,line,col" "${dbs[@]}"

  echo "  CLI: status, files, query, callers/callees/impact for ${*}"
  echo "  DB:  extraction-nodes, extraction-contains, extraction-unresolved, resolution-edges"

  rm -rf "$dir/.codegraph"
}

capture go-small "store" "Get" "Set" "Lookup" "normalize" "handleGreet" "Store::Get"
capture ts-small "store" "get" "set" "lookup" "normalize" "describe" "Cache::lookup"
capture py-small "dog" "speak" "describe" "make_dog" "Dog" "label" "Dog::speak"
capture cs-small "dog" "Speak" "Describe" "MakeDog" "Dog" "Label" "Dog::Speak"
capture java-small "circle" "area" "label" "run" "Circle" "Shape" "Circle::area"
capture kt-small "circle" "area" "label" "run" "Circle" "Shape" "Circle::area"
capture rb-small "dog" "speak" "describe" "make_dog" "Dog" "breed" "Dog::speak"
capture rs-small "circle" "area" "label" "run" "Circle" "Shape" "Circle::area"
capture php-small "dog" "speak" "describe" "make_dog" "Dog" "walk" "Dog::speak"
capture c-small "shape" "area" "label" "run" "Shape" "Kind" "PI"
capture scala-small "circle" "area" "label" "run" "Circle" "Shape" "Circle::area"
capture swift-small "circle" "area" "label" "run" "Circle" "Shape" "Point::area"
capture cpp-small "shape" "area" "distanceTo" "run" "Circle" "Shape" "Point"
capture dart-small "circle" "area" "label" "run" "Circle" "Shape" "Circle::area"

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
elif fixture == "py-small":
    sym1 = "speak"
    explore2 = "speak describe make_dog"
    explore3 = "describe"
    q_explore1 = "how does the dog work"
elif fixture == "cs-small":
    sym1 = "Speak"
    explore2 = "Speak Describe MakeDog"
    explore3 = "Describe"
    q_explore1 = "how does the dog work"
elif fixture == "java-small":
    sym1 = "area"
    explore2 = "area label run"
    explore3 = "run"
    q_explore1 = "how does the circle work"
elif fixture == "kt-small":
    sym1 = "area"
    explore2 = "area label run"
    explore3 = "run"
    q_explore1 = "how does the circle work"
elif fixture == "rb-small":
    sym1 = "speak"
    explore2 = "speak describe make_dog"
    explore3 = "describe"
    q_explore1 = "how does the dog work"
elif fixture == "rs-small":
    sym1 = "area"
    explore2 = "area label run"
    explore3 = "run"
    q_explore1 = "how does the circle work"
elif fixture == "php-small":
    sym1 = "speak"
    explore2 = "speak describe make_dog"
    explore3 = "describe"
    q_explore1 = "how does the dog work"
elif fixture == "c-small":
    sym1 = "area"
    explore2 = "area label run"
    explore3 = "run"
    q_explore1 = "how does the shape work"
elif fixture == "scala-small":
    sym1 = "area"
    explore2 = "area label run"
    explore3 = "run"
    q_explore1 = "how does the circle work"
elif fixture == "swift-small":
    sym1 = "area"
    explore2 = "area label run"
    explore3 = "run"
    q_explore1 = "how does the circle work"
elif fixture == "cpp-small":
    sym1 = "area"
    explore2 = "area distanceTo run"
    explore3 = "run"
    q_explore1 = "how does the shape work"
elif fixture == "dart-small":
    sym1 = "area"
    explore2 = "area label run"
    explore3 = "run"
    q_explore1 = "how does the circle work"
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
rebaseline_mcp py-small
rebaseline_mcp cs-small
rebaseline_mcp java-small
rebaseline_mcp kt-small
rebaseline_mcp rb-small
rebaseline_mcp rs-small
rebaseline_mcp php-small
rebaseline_mcp c-small
rebaseline_mcp scala-small
rebaseline_mcp swift-small
rebaseline_mcp cpp-small
rebaseline_mcp dart-small

echo ""
echo "=== Rebaseline complete ==="
echo "Goldens are self-baselined: our output is the spec."
echo "Original capture-*.sh scripts are retained for comparison only."
echo ""
echo "Changed files (git diff --name-only):"
git -C "$ROOT" diff --name-only -- testdata/golden/ 2>/dev/null || true
