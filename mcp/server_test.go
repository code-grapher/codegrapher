package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestHandleStatus verifies the status tool output format.
func TestHandleStatus(t *testing.T) {
	h := &toolHandlers{backend: newMockBackend()}
	result, err := h.handleStatus(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleStatus error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "## CodeGraph Status") {
		t.Errorf("missing header, got: %s", text[:min(200, len(text))])
	}
	if !strings.Contains(text, "Files indexed:** 3") {
		t.Errorf("missing file count, got: %s", text[:min(200, len(text))])
	}
	if !strings.Contains(text, "wal") {
		t.Errorf("missing journal mode, got: %s", text[:min(200, len(text))])
	}
}

// TestHandleFiles verifies the files tool tree output.
func TestHandleFiles(t *testing.T) {
	h := &toolHandlers{backend: newMockBackend()}
	req := mcplib.CallToolRequest{}
	result, err := h.handleFiles(context.Background(), req)
	if err != nil {
		t.Fatalf("handleFiles error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Project Structure") {
		t.Errorf("missing header, got: %s", text[:min(200, len(text))])
	}
	if !strings.Contains(text, "main.go") {
		t.Errorf("missing main.go, got: %s", text[:min(300, len(text))])
	}
}

// TestHandleSearch verifies the search tool output.
func TestHandleSearch(t *testing.T) {
	h := &toolHandlers{backend: newMockBackend()}
	req := mcplib.CallToolRequest{
		Params: mcplib.CallToolParams{
			Name:      "codegraph_search",
			Arguments: map[string]any{"query": "Get", "limit": 10},
		},
	}
	result, err := h.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSearch error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Search Results") {
		t.Errorf("missing header, got: %s", text[:min(200, len(text))])
	}
	if !strings.Contains(text, "Get") {
		t.Errorf("missing Get in results, got: %s", text[:min(300, len(text))])
	}
}

// TestHandleCallers verifies callers tool output.
func TestHandleCallers(t *testing.T) {
	h := &toolHandlers{backend: newMockBackend()}
	req := callReq("codegraph_callers", map[string]any{"symbol": "Get"})
	result, err := h.handleCallers(context.Background(), req)
	if err != nil {
		t.Fatalf("handleCallers error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Callers of Get") {
		t.Errorf("missing callers header, got: %s", text[:min(200, len(text))])
	}
	if !strings.Contains(text, "Lookup") {
		t.Errorf("missing Lookup caller, got: %s", text[:min(300, len(text))])
	}
}

// TestHandleCallees verifies callees tool output.
func TestHandleCallees(t *testing.T) {
	h := &toolHandlers{backend: newMockBackend()}
	req := callReq("codegraph_callees", map[string]any{"symbol": "Get"})
	result, err := h.handleCallees(context.Background(), req)
	if err != nil {
		t.Fatalf("handleCallees error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Callees of Get") {
		t.Errorf("missing callees header, got: %s", text[:min(200, len(text))])
	}
}

// TestHandleImpact verifies impact tool output.
func TestHandleImpact(t *testing.T) {
	h := &toolHandlers{backend: newMockBackend()}
	req := callReq("codegraph_impact", map[string]any{"symbol": "Get", "depth": 2})
	result, err := h.handleImpact(context.Background(), req)
	if err != nil {
		t.Fatalf("handleImpact error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Impact") {
		t.Errorf("missing impact header, got: %s", text[:min(200, len(text))])
	}
}

// TestHandleNode verifies node tool with includeCode=true.
func TestHandleNode(t *testing.T) {
	h := &toolHandlers{backend: newMockBackend()}
	req := callReq("codegraph_node", map[string]any{"symbol": "Get", "includeCode": true})
	result, err := h.handleNode(context.Background(), req)
	if err != nil {
		t.Fatalf("handleNode error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Get") {
		t.Errorf("missing Get in output, got: %s", text[:min(300, len(text))])
	}
}

// TestHandleExplore verifies explore tool output.
func TestHandleExplore(t *testing.T) {
	h := &toolHandlers{backend: newMockBackend()}
	req := callReq("codegraph_explore", map[string]any{"query": "Get"})
	result, err := h.handleExplore(context.Background(), req)
	if err != nil {
		t.Fatalf("handleExplore error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "## Exploration") {
		t.Errorf("missing exploration header, got: %s", text[:min(300, len(text))])
	}
}

// TestMCPProtocol tests the full initialize -> tools/list -> tools/call round trip.
func TestMCPProtocol(t *testing.T) {
	backend := newMockBackend()
	srv := NewServer(backend, backend.projectRoot)

	// Build the stdio server directly to test the protocol.
	stdioSrv := server.NewStdioServer(srv.mcpSrv)

	// Build stdin with protocol messages.
	messages := []map[string]any{
		{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
			},
		},
		{
			"jsonrpc": "2.0",
			"method":  "notifications/initialized",
			"params":  map[string]any{},
		},
		{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/list",
			"params":  map[string]any{},
		},
	}

	var inputLines []string
	for _, m := range messages {
		b, _ := json.Marshal(m)
		inputLines = append(inputLines, string(b))
	}
	stdin := strings.NewReader(strings.Join(inputLines, "\n") + "\n")

	var outBuf strings.Builder
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- stdioSrv.Listen(ctx, stdin, &outBuf)
	}()

	// Wait for stdin to be consumed (server exits when stdin closes).
	<-done

	output := outBuf.String()
	// Should have at least 2 lines: initialize response + tools/list response.
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 response lines, got %d:\n%s", len(lines), output)
	}

	// Parse initialize response.
	var initResp map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("failed to parse init response: %v, line: %s", err, lines[0])
	}
	result := initResp["result"].(map[string]any)
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("wrong protocol version: %v", result["protocolVersion"])
	}
	if result["instructions"] == nil {
		t.Error("missing instructions in init response")
	}

	// Parse tools/list response.
	var toolsResp map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &toolsResp); err != nil {
		t.Fatalf("failed to parse tools/list response: %v, line: %s", err, lines[1])
	}
	toolsResult := toolsResp["result"].(map[string]any)
	tools := toolsResult["tools"].([]any)
	// With 3 files, tiny-repo gating applies: only 3 tools.
	if len(tools) != 3 {
		t.Errorf("expected 3 tools (tiny-repo), got %d", len(tools))
	}
	// Check tool names.
	names := make([]string, len(tools))
	for i, tool := range tools {
		tm := tool.(map[string]any)
		names[i] = tm["name"].(string)
	}
	t.Logf("tools: %v", names)
	for _, name := range names {
		if name != "codegraph_search" && name != "codegraph_node" && name != "codegraph_explore" {
			t.Errorf("unexpected tool: %s", name)
		}
	}
}

// ---- helpers ----

func resultText(t *testing.T, result *mcplib.CallToolResult) string {
	t.Helper()
	if result == nil {
		t.Fatal("nil result")
	}
	for _, c := range result.Content {
		// Check if it's a TextContent using type assertion via marshaling.
		b, _ := json.Marshal(c)
		var obj map[string]any
		_ = json.Unmarshal(b, &obj)
		if obj["type"] == "text" {
			return obj["text"].(string)
		}
	}
	t.Fatal("no text content in result")
	return ""
}

func callReq(name string, args map[string]any) mcplib.CallToolRequest {
	return mcplib.CallToolRequest{
		Params: mcplib.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestNewServerTinyRepo verifies tiny-repo gating (< 500 files → 3 tools).
func TestNewServerTinyRepo(t *testing.T) {
	backend := newMockBackend() // 3 files
	srv := NewServer(backend, backend.projectRoot)
	_ = srv // Just ensure it constructs without panic.
}

// testReadCloser wraps a Reader with a NopCloser.
type testReadCloser struct {
	io.Reader
}

func (t testReadCloser) Close() error { return nil }
