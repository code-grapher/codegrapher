#!/usr/bin/env bash
# capture-mcp-golden.sh — capture golden JSON-RPC response pairs from the original TS MCP server.
#
# Usage: ./tools/parity/capture-mcp-golden.sh
#
# Idempotent: re-running overwrites the golden files.
# Requires: fnm (Node Version Manager), node 22, python3, the original TS codegraph server.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CODEGRAPH_JS="/Users/alexandertrakhimenok/projects/specscore/codegraph/dist/bin/codegraph.js"
MAIN_REPO="/Users/alexandertrakhimenok/projects/specscore/codegrapher"
FIXTURES_DIR="$MAIN_REPO/testdata/fixtures"
GOLDEN_DIR="$REPO_ROOT/testdata/golden"

if [[ ! -f "$CODEGRAPH_JS" ]]; then
  echo "ERROR: codegraph.js not found at $CODEGRAPH_JS" >&2
  exit 1
fi

run_node() {
  fnm exec --using 22 node "$@"
}

# Init each fixture
for fixture in go-small ts-small; do
  fixture_dir="$FIXTURES_DIR/$fixture"
  tmp_dir="/tmp/mcp-golden-$fixture"

  echo "Initializing $fixture..."
  rm -rf "$tmp_dir"
  cp -r "$fixture_dir" "$tmp_dir"
  (cd "$tmp_dir" && CODEGRAPH_NO_WATCH=1 run_node "$CODEGRAPH_JS" init 2>/dev/null) || {
    echo "Init failed for $fixture, retrying:"
    (cd "$tmp_dir" && CODEGRAPH_NO_WATCH=1 run_node "$CODEGRAPH_JS" init)
  }
done

mkdir -p "$GOLDEN_DIR/go-small/mcp" "$GOLDEN_DIR/ts-small/mcp"

# Use Python to drive the MCP server interactively (one message at a time)
python3 - "$GOLDEN_DIR" << 'PYEOF'
import subprocess, json, sys, os, threading, queue, time

def run_capture(fixture_dir, golden_dir, messages):
    env = {**os.environ, 'CODEGRAPH_NO_DAEMON': '1', 'CODEGRAPH_NO_WATCH': '1'}
    codegraph_js = '/Users/alexandertrakhimenok/projects/specscore/codegraph/dist/bin/codegraph.js'

    proc = subprocess.Popen(
        ['fnm', 'exec', '--using', '22', 'node', codegraph_js, 'serve', '--mcp', '--path', fixture_dir],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        cwd=fixture_dir, env=env,
    )

    line_queue = queue.Queue()

    def reader():
        for line in proc.stdout:
            line_queue.put(line.decode('utf-8', errors='replace').rstrip('\n'))
        line_queue.put(None)

    threading.Thread(target=reader, daemon=True).start()

    id_to_file = {
        1: "initialize.json", 2: "tools-list.json", 3: "status.json",
        4: "files.json", 5: "search-1.json", 6: "callers-1.json",
        7: "callees-1.json", 8: "impact-1.json", 9: "node-1.json",
        10: "explore-1.json", 11: "explore-2.json", 12: "explore-3.json",
    }

    written = 0
    for msg in messages:
        msg_str = json.dumps(msg) + '\n'
        try:
            proc.stdin.write(msg_str.encode())
            proc.stdin.flush()
        except BrokenPipeError:
            print(f"  Broken pipe at id={msg.get('id','notif')}", file=sys.stderr)
            break
        if 'id' not in msg:
            time.sleep(0.05)
            continue
        msg_id = msg['id']
        deadline = time.time() + 30
        found = False
        while time.time() < deadline:
            try:
                line = line_queue.get(timeout=1.0)
                if line is None:
                    line_queue.put(None)
                    break
                if not line.strip():
                    continue
                try:
                    obj = json.loads(line)
                    if obj.get('id') == msg_id:
                        if msg_id in id_to_file:
                            fname = os.path.join(golden_dir, id_to_file[msg_id])
                            with open(fname, 'w') as f:
                                json.dump(obj, f, indent=2)
                                f.write('\n')
                            print(f"  Wrote {id_to_file[msg_id]}")
                            written += 1
                        found = True
                        break
                except json.JSONDecodeError:
                    pass
            except queue.Empty:
                pass
        if not found:
            print(f"  Timeout/closed waiting for id={msg_id}", file=sys.stderr)
    try:
        proc.stdin.close()
    except: pass
    proc.wait(timeout=5)
    print(f"  Total: {written} files written")

go_msgs = [
    {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}},
    {"jsonrpc":"2.0","method":"notifications/initialized","params":{}},
    {"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}},
    {"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"codegraph_status","arguments":{}}},
    {"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"codegraph_files","arguments":{}}},
    {"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"codegraph_search","arguments":{"query":"store","limit":20}}},
    {"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"codegraph_callers","arguments":{"symbol":"Get"}}},
    {"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"codegraph_callees","arguments":{"symbol":"Get"}}},
    {"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"codegraph_impact","arguments":{"symbol":"Get"}}},
    {"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"codegraph_node","arguments":{"symbol":"Get","includeCode":True}}},
    {"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"how does the store work"}}},
    {"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"Get Set Lookup"}}},
    {"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"normalize"}}},
]

ts_msgs = [
    {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}},
    {"jsonrpc":"2.0","method":"notifications/initialized","params":{}},
    {"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}},
    {"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"codegraph_status","arguments":{}}},
    {"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"codegraph_files","arguments":{}}},
    {"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"codegraph_search","arguments":{"query":"store","limit":20}}},
    {"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"codegraph_callers","arguments":{"symbol":"get"}}},
    {"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"codegraph_callees","arguments":{"symbol":"get"}}},
    {"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"codegraph_impact","arguments":{"symbol":"get"}}},
    {"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"codegraph_node","arguments":{"symbol":"get","includeCode":True}}},
    {"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"how does the cache work"}}},
    {"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"get set lookup"}}},
    {"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"codegraph_explore","arguments":{"query":"normalize"}}},
]

golden_dir = sys.argv[1]
print("=== Capturing go-small ===")
run_capture("/tmp/mcp-golden-go-small", golden_dir + "/go-small/mcp", go_msgs)
print("=== Capturing ts-small ===")
run_capture("/tmp/mcp-golden-ts-small", golden_dir + "/ts-small/mcp", ts_msgs)
print("Done.")
PYEOF

echo ""
echo "=== Golden capture complete ==="
echo "go-small/mcp:"
ls "$GOLDEN_DIR/go-small/mcp/" | sort
echo "ts-small/mcp:"
ls "$GOLDEN_DIR/ts-small/mcp/" | sort
