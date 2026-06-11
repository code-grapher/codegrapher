package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

const serverName = "codegraph"

// serverVersion mirrors the upstream codegraph release this port tracks
// (the same parity version the CLI status payload reports).
const serverVersion = "0.9.9"

// protocolVersion is the MCP protocol revision the server speaks.
const protocolVersion = "2024-11-05"

// Server is the codegrapher MCP server: a minimal JSON-RPC 2.0 loop over
// newline-delimited JSON (the MCP stdio transport), implementing initialize,
// tools/list and tools/call. Hand-rolled rather than an SDK so the wire
// responses match the captured goldens field-for-field (SDKs add fields like
// `annotations` that the original server never emitted).
type Server struct {
	handlers *toolHandlers
}

// NewServer creates a new MCP server backed by backend.
func NewServer(backend GraphBackend) *Server {
	return &Server{handlers: &toolHandlers{backend: backend}}
}

// jsonRPCRequest is an incoming JSON-RPC message.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// Serve reads JSON-RPC messages from r and writes responses to w until r is
// exhausted or ctx is cancelled.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var writeMu sync.Mutex

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue // not a JSON-RPC message — ignore
		}
		if req.ID == nil {
			continue // notification — no response
		}
		resp := s.handle(&req)
		out, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		writeMu.Lock()
		_, werr := fmt.Fprintf(w, "%s\n", out)
		writeMu.Unlock()
		if werr != nil {
			return werr
		}
	}
	return scanner.Err()
}

func (s *Server) handle(req *jsonRPCRequest) *jsonRPCResponse {
	resp := &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
			"instructions":    ServerInstructions,
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": s.handlers.getTools()}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &jsonRPCError{Code: -32602, Message: "invalid params"}
			return resp
		}
		args := params.Arguments
		if args == nil {
			args = map[string]any{}
		}
		result := s.executeSafely(params.Name, args)
		resp.Result = result
	default:
		resp.Error = &jsonRPCError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

// executeSafely wraps execute with the upstream catch-all (`Tool execution
// failed: ...`) so a panic in a handler degrades to an error result rather
// than killing the server.
func (s *Server) executeSafely(name string, args map[string]any) (result toolResult) {
	defer func() {
		if rec := recover(); rec != nil {
			result = errorResult(fmt.Sprintf("Tool execution failed: %v", rec))
		}
	}()
	return s.handlers.execute(name, args)
}
