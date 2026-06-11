package mcp

import (
	"context"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const serverName = "codegraph"
const serverVersion = "0.1.0"

// Server is the codegrapher MCP server.
type Server struct {
	backend     GraphBackend
	mcpSrv      *server.MCPServer
	projectPath string
}

// NewServer creates a new MCP server backed by backend.
// It queries GetStats() to determine whether to apply tiny-repo tool gating
// (< 500 files → only expose 3 core tools).
func NewServer(backend GraphBackend, projectPath string) *Server {
	opts := []server.ServerOption{
		server.WithInstructions(ServerInstructions),
	}
	mcpSrv := server.NewMCPServer(serverName, serverVersion, opts...)

	// Determine file count for tiny-repo gating.
	fileCount := 0
	if stats, err := backend.GetStats(); err == nil {
		fileCount = stats.FileCount
	}

	h := &toolHandlers{backend: backend}
	registerTools(mcpSrv, h, fileCount)

	return &Server{
		backend:     backend,
		mcpSrv:      mcpSrv,
		projectPath: projectPath,
	}
}

// Serve runs the server over stdio until stdin closes.
func (s *Server) Serve(ctx context.Context) error {
	stdioSrv := server.NewStdioServer(s.mcpSrv)
	return stdioSrv.Listen(ctx, nil, nil)
}

// textResult is a helper to create a text CallToolResult.
func textResult(text string) *mcplib.CallToolResult {
	return mcplib.NewToolResultText(text)
}
