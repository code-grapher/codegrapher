package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/specscore/codegrapher/model"
)

// toolHandlers holds all tool handler functions bound to a backend.
type toolHandlers struct {
	backend GraphBackend
}

func (h *toolHandlers) handleStatus(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	stats, err := h.backend.GetStats()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("failed to get stats: %v", err)), nil
	}

	sizeMB := float64(stats.DBSizeBytes) / (1024 * 1024)
	var sb strings.Builder
	fmt.Fprintf(&sb, "## CodeGraph Status\n\n")
	fmt.Fprintf(&sb, "**Files indexed:** %d\n", stats.FileCount)
	fmt.Fprintf(&sb, "**Total nodes:** %d\n", stats.NodeCount)
	fmt.Fprintf(&sb, "**Total edges:** %d\n", stats.EdgeCount)
	fmt.Fprintf(&sb, "**Database size:** %.2f MB\n", sizeMB)
	fmt.Fprintf(&sb, "**Backend:** modernc.org/sqlite — pure Go, WAL + FTS5\n")
	fmt.Fprintf(&sb, "**Journal mode:** %s (concurrent reads safe)\n", stats.JournalMode)

	if len(stats.NodesByKind) > 0 {
		fmt.Fprintf(&sb, "\n### Nodes by Kind:\n")
		// Sort keys for stable output.
		kinds := make([]string, 0, len(stats.NodesByKind))
		for k := range stats.NodesByKind {
			kinds = append(kinds, string(k))
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			fmt.Fprintf(&sb, "- %s: %d\n", k, stats.NodesByKind[model.NodeKind(k)])
		}
	}

	return mcplib.NewToolResultText(sb.String()), nil
}

func (h *toolHandlers) handleFiles(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	pathFilter := mcplib.ParseString(req, "path", "")
	format := mcplib.ParseString(req, "format", "tree")

	files, err := h.backend.GetFiles()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("failed to get files: %v", err)), nil
	}

	// Apply path filter.
	if pathFilter != "" {
		var filtered []FileInfo
		for _, f := range files {
			if strings.HasPrefix(f.Path, pathFilter) {
				filtered = append(filtered, f)
			}
		}
		files = filtered
	}

	if format == "flat" {
		return mcplib.NewToolResultText(formatFilesFlat(files)), nil
	}
	if format == "grouped" {
		return mcplib.NewToolResultText(formatFilesGrouped(files)), nil
	}
	return mcplib.NewToolResultText(formatFilesTree(files)), nil
}

func (h *toolHandlers) handleSearch(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	query := mcplib.ParseString(req, "query", "")
	kind := mcplib.ParseString(req, "kind", "")
	limit := mcplib.ParseInt(req, "limit", 10)

	if query == "" {
		return mcplib.NewToolResultError("query is required"), nil
	}

	var kinds []string
	if kind != "" {
		kinds = []string{kind}
	}

	results, err := h.backend.SearchNodes(query, kinds, limit)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	return mcplib.NewToolResultText(formatSearchResults(query, results)), nil
}

func (h *toolHandlers) handleCallers(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	symbol := mcplib.ParseString(req, "symbol", "")
	limit := mcplib.ParseInt(req, "limit", 20)

	if symbol == "" {
		return mcplib.NewToolResultError("symbol is required"), nil
	}

	nodes, err := h.backend.GetNodesByName(symbol)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("lookup failed: %v", err)), nil
	}
	if len(nodes) == 0 {
		return mcplib.NewToolResultText(fmt.Sprintf("No symbols found named %q.", symbol)), nil
	}

	// Aggregate callers across all matching nodes.
	var allCallers []NodeInfo
	seen := make(map[string]struct{})
	for _, n := range nodes {
		callers, err := h.backend.GetCallers(n.ID)
		if err != nil {
			continue
		}
		for _, c := range callers {
			if _, ok := seen[c.ID]; ok {
				continue
			}
			seen[c.ID] = struct{}{}
			allCallers = append(allCallers, c)
			if len(allCallers) >= limit {
				break
			}
		}
		if len(allCallers) >= limit {
			break
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Callers of %s (%d found)\n\n", symbol, len(allCallers))
	for _, c := range allCallers {
		fmt.Fprintf(&sb, "- %s (%s) - %s:%d\n", c.Name, c.Kind, c.FilePath, c.StartLine)
	}
	if len(nodes) > 1 {
		parts := make([]string, len(nodes))
		for i, n := range nodes {
			parts[i] = fmt.Sprintf("%s at %s:%d", n.Kind, n.FilePath, n.StartLine)
		}
		fmt.Fprintf(&sb, "\n> **Note:** Aggregated results across %d symbols named %q: %s",
			len(nodes), symbol, strings.Join(parts, ", "))
	}
	return mcplib.NewToolResultText(sb.String()), nil
}

func (h *toolHandlers) handleCallees(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	symbol := mcplib.ParseString(req, "symbol", "")
	limit := mcplib.ParseInt(req, "limit", 20)

	if symbol == "" {
		return mcplib.NewToolResultError("symbol is required"), nil
	}

	nodes, err := h.backend.GetNodesByName(symbol)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("lookup failed: %v", err)), nil
	}
	if len(nodes) == 0 {
		return mcplib.NewToolResultText(fmt.Sprintf("No symbols found named %q.", symbol)), nil
	}

	// Aggregate callees across all matching nodes.
	var allCallees []NodeInfo
	seen := make(map[string]struct{})
	for _, n := range nodes {
		callees, err := h.backend.GetCallees(n.ID)
		if err != nil {
			continue
		}
		for _, c := range callees {
			if _, ok := seen[c.ID]; ok {
				continue
			}
			seen[c.ID] = struct{}{}
			allCallees = append(allCallees, c)
			if len(allCallees) >= limit {
				break
			}
		}
		if len(allCallees) >= limit {
			break
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Callees of %s (%d found)\n\n", symbol, len(allCallees))
	for _, c := range allCallees {
		fmt.Fprintf(&sb, "- %s (%s) - %s:%d\n", c.Name, c.Kind, c.FilePath, c.StartLine)
	}
	if len(nodes) > 1 {
		parts := make([]string, len(nodes))
		for i, n := range nodes {
			parts[i] = fmt.Sprintf("%s at %s:%d", n.Kind, n.FilePath, n.StartLine)
		}
		fmt.Fprintf(&sb, "\n> **Note:** Aggregated results across %d symbols named %q: %s",
			len(nodes), symbol, strings.Join(parts, ", "))
	}
	return mcplib.NewToolResultText(sb.String()), nil
}

func (h *toolHandlers) handleImpact(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	symbol := mcplib.ParseString(req, "symbol", "")
	depth := mcplib.ParseInt(req, "depth", 2)

	if symbol == "" {
		return mcplib.NewToolResultError("symbol is required"), nil
	}

	nodes, err := h.backend.GetNodesByName(symbol)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("lookup failed: %v", err)), nil
	}
	if len(nodes) == 0 {
		return mcplib.NewToolResultText(fmt.Sprintf("No symbols found named %q.", symbol)), nil
	}

	// Aggregate impact across all matching nodes.
	allImpact := map[string]NodeInfo{}
	for _, n := range nodes {
		entries, err := h.backend.GetImpact(n.ID, depth)
		if err != nil {
			continue
		}
		for _, e := range entries {
			allImpact[e.Node.ID] = e.Node
		}
	}

	// Group by file.
	byFile := map[string][]NodeInfo{}
	for _, n := range allImpact {
		byFile[n.FilePath] = append(byFile[n.FilePath], n)
	}
	// Sort within each file by line.
	for fp := range byFile {
		sort.Slice(byFile[fp], func(i, j int) bool {
			return byFile[fp][i].StartLine < byFile[fp][j].StartLine
		})
	}

	// Sort files for stable output.
	files := make([]string, 0, len(byFile))
	for fp := range byFile {
		files = append(files, fp)
	}
	sort.Strings(files)

	total := len(allImpact)
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Impact: %q affects %d symbols\n\n", symbol, total)
	for _, fp := range files {
		fmt.Fprintf(&sb, "**%s:**\n", fp)
		parts := make([]string, 0, len(byFile[fp]))
		for _, n := range byFile[fp] {
			parts = append(parts, fmt.Sprintf("%s:%d", n.Name, n.StartLine))
		}
		fmt.Fprintf(&sb, "%s\n\n", strings.Join(parts, ", "))
	}

	if len(nodes) > 1 {
		parts := make([]string, len(nodes))
		for i, n := range nodes {
			parts[i] = fmt.Sprintf("%s at %s:%d", n.Kind, n.FilePath, n.StartLine)
		}
		fmt.Fprintf(&sb, "\n> **Note:** Aggregated results across %d symbols named %q: %s",
			len(nodes), symbol, strings.Join(parts, ", "))
	}
	return mcplib.NewToolResultText(sb.String()), nil
}

func (h *toolHandlers) handleNode(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	symbol := mcplib.ParseString(req, "symbol", "")
	includeCode := mcplib.ParseBoolean(req, "includeCode", false)
	fileArg := mcplib.ParseString(req, "file", "")

	// File mode: no symbol, just file path.
	if symbol == "" && fileArg != "" {
		return h.handleNodeFileMode(fileArg, req)
	}

	if symbol == "" {
		return mcplib.NewToolResultError("symbol or file is required"), nil
	}

	nodes, err := h.backend.GetNodesByName(symbol)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("lookup failed: %v", err)), nil
	}

	// Filter by file if provided.
	if fileArg != "" {
		var filtered []NodeInfo
		for _, n := range nodes {
			if strings.HasSuffix(n.FilePath, fileArg) || strings.Contains(n.FilePath, fileArg) {
				filtered = append(filtered, n)
			}
		}
		if len(filtered) > 0 {
			nodes = filtered
		}
	}

	if len(nodes) == 0 {
		return mcplib.NewToolResultText(fmt.Sprintf("No symbols found named %q.", symbol)), nil
	}

	var sb strings.Builder
	if len(nodes) > 1 {
		fmt.Fprintf(&sb, "**%d definitions named %q**\nReturning %d in full — pick the one you need (no Read required).\n\n",
			len(nodes), symbol, len(nodes))
	}

	for i, n := range nodes {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&sb, "## %s (%s)\n\n", n.Name, n.Kind)
		fmt.Fprintf(&sb, "**Location:** %s:%d\n", n.FilePath, n.StartLine)
		if n.Signature != "" {
			fmt.Fprintf(&sb, "**Signature:** `%s`\n", n.Signature)
		}
		if n.Docstring != "" {
			fmt.Fprintf(&sb, "\n%s\n", n.Docstring)
		}

		if includeCode {
			src, err := h.readNodeSource(n)
			if err == nil && src != "" {
				lang := languageID(n.Language)
				fmt.Fprintf(&sb, "\n```%s\n%s\n```\n", lang, src)
			}
		}

		// Trail: callers and callees.
		callers, _ := h.backend.GetCallers(n.ID)
		callees, _ := h.backend.GetCallees(n.ID)
		if len(callers) > 0 || len(callees) > 0 {
			sb.WriteString("### Trail — codegraph_node any of these to follow it (no Read needed)\n")
			for _, c := range callees {
				fmt.Fprintf(&sb, "**Calls →** %s (%s:%d)\n", c.Name, c.FilePath, c.StartLine)
			}
			for _, c := range callers {
				fmt.Fprintf(&sb, "**Called by ←** %s (%s:%d)\n", c.Name, c.FilePath, c.StartLine)
			}
		}
	}

	return mcplib.NewToolResultText(sb.String()), nil
}

func (h *toolHandlers) handleNodeFileMode(filePath string, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	offset := mcplib.ParseInt(req, "offset", 0)
	limit := mcplib.ParseInt(req, "limit", 2000)

	src, err := h.backend.ReadFile(filePath)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	lines := strings.Split(src, "\n")
	total := len(lines)

	start := 0
	if offset > 0 {
		start = offset - 1
	}
	if start >= total {
		start = total - 1
	}
	end := total
	if limit > 0 && start+limit < total {
		end = start + limit
	}
	lines = lines[start:end]

	var sb strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&sb, "%d\t%s\n", start+i+1, line)
	}

	// Dependents.
	deps, _ := h.backend.GetFileDependents(filePath)
	if len(deps) > 0 {
		fmt.Fprintf(&sb, "\n> Depended on by: %s", strings.Join(deps, ", "))
	}

	return mcplib.NewToolResultText(sb.String()), nil
}

func (h *toolHandlers) handleExplore(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	query := mcplib.ParseString(req, "query", "")
	maxFiles := mcplib.ParseInt(req, "maxFiles", 4) // small-project default

	if query == "" {
		return mcplib.NewToolResultError("query is required"), nil
	}

	sg, err := h.backend.FindRelevantContext(query, maxFiles*10)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("explore failed: %v", err)), nil
	}

	if sg == nil || len(sg.Nodes) == 0 {
		return mcplib.NewToolResultText(fmt.Sprintf("## Exploration: %s\n\nNo relevant symbols found.", query)), nil
	}

	// Limit to maxFiles files.
	fileOrder := orderedFiles(sg.FileNodes, maxFiles)

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Exploration: %s\n\n", query)
	fmt.Fprintf(&sb, "Found %d symbols across %d files.\n\n", len(sg.Nodes), len(sg.FileNodes))

	// Blast radius section: show top nodes with caller info.
	topNodes := topNodesByFile(sg, fileOrder)
	if len(topNodes) > 0 {
		sb.WriteString("### Blast radius — what depends on these (update/verify before editing)\n\n")
		for _, n := range topNodes {
			callers, _ := h.backend.GetCallers(n.ID)
			callerInfo := ""
			if len(callers) > 0 {
				// Count unique files.
				callerFiles := map[string]struct{}{}
				for _, c := range callers {
					callerFiles[c.FilePath] = struct{}{}
				}
				callerInfo = fmt.Sprintf(" — %d caller in `%s`", len(callers), fileFromPath(callers[0].FilePath))
				if len(callerFiles) > 1 {
					callerInfo = fmt.Sprintf(" — %d callers across %d files", len(callers), len(callerFiles))
				}
			}
			fmt.Fprintf(&sb, "- `%s` (%s:%d)%s; ⚠️ no covering tests found\n",
				n.Name, n.FilePath, n.StartLine, callerInfo)
		}
		sb.WriteString("\n")
	}

	// Source code section.
	sb.WriteString("### Source Code\n\n")
	sb.WriteString("> The code below is the **verbatim, current on-disk source** of these files — re-read from disk on this call and line-numbered, byte-for-byte identical to what the Read tool returns. It is NOT a summary, outline, or stale cache. Treat each block as a Read you have already performed: do not Read a file shown here.\n\n")

	for _, fp := range fileOrder {
		nodes := sg.FileNodes[fp]
		src, ok := sg.FileSource[fp]
		if !ok {
			continue
		}

		// File header with node names.
		symNames := make([]string, 0, len(nodes))
		for _, n := range nodes {
			symNames = append(symNames, fmt.Sprintf("%s(%s)", n.Name, n.Kind))
		}
		extra := 0
		if len(symNames) > 5 {
			extra = len(symNames) - 5
			symNames = symNames[:5]
		}
		header := strings.Join(symNames, ", ")
		if extra > 0 {
			header += fmt.Sprintf(", +%d more", extra)
		}

		lang := languageIDFromPath(fp)
		fmt.Fprintf(&sb, "#### %s — %s\n\n", fp, header)
		sb.WriteString("```" + lang + "\n")
		sb.WriteString(numberedSource(src))
		sb.WriteString("```\n\n")
	}

	return mcplib.NewToolResultText(sb.String()), nil
}

// ---- formatting helpers ----

func formatSearchResults(query string, results []SearchResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Search Results (%d found)\n\n", len(results))
	for _, r := range results {
		n := r.Node
		fmt.Fprintf(&sb, "### %s (%s)\n", n.Name, n.Kind)
		fmt.Fprintf(&sb, "%s:%d\n", n.FilePath, n.StartLine)
		if n.Signature != "" {
			fmt.Fprintf(&sb, "`%s`\n", n.Signature)
		}
		sb.WriteString("\n")
	}
	_ = query
	return strings.TrimRight(sb.String(), "\n")
}

func formatFilesTree(files []FileInfo) string {
	if len(files) == 0 {
		return "## Project Structure (0 files)\n\n(no files indexed)"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Project Structure (%d files)\n\n", len(files))

	// Build tree.
	type node struct {
		name     string
		children map[string]*node
		file     *FileInfo
	}
	root := &node{children: make(map[string]*node)}
	for i := range files {
		f := &files[i]
		parts := strings.Split(f.Path, "/")
		cur := root
		for j, p := range parts {
			if _, ok := cur.children[p]; !ok {
				cur.children[p] = &node{name: p, children: make(map[string]*node)}
			}
			cur = cur.children[p]
			if j == len(parts)-1 {
				cur.file = f
			}
		}
	}

	var printNode func(n *node, prefix string, last bool)
	printNode = func(n *node, prefix string, last bool) {
		if n.file != nil {
			connector := "├── "
			if last {
				connector = "└── "
			}
			fmt.Fprintf(&sb, "%s%s%s (%s, %d symbols)\n",
				prefix, connector, n.name,
				n.file.Language, n.file.NodeCount)
		} else if n.name != "" {
			connector := "├── "
			if last {
				connector = "└── "
			}
			fmt.Fprintf(&sb, "%s%s%s\n", prefix, connector, n.name+"/")
		}

		childPrefix := prefix
		if n.name != "" {
			if last {
				childPrefix += "    "
			} else {
				childPrefix += "│   "
			}
		}

		// Sort children: dirs first, then files.
		keys := make([]string, 0, len(n.children))
		for k := range n.children {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			printNode(n.children[k], childPrefix, i == len(keys)-1)
		}
	}

	// Sort root children.
	keys := make([]string, 0, len(root.children))
	for k := range root.children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		printNode(root.children[k], "", i == len(keys)-1)
	}

	return strings.TrimRight(sb.String(), "\n")
}

func formatFilesFlat(files []FileInfo) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Files (%d)\n\n", len(files))
	for _, f := range files {
		fmt.Fprintf(&sb, "%s (%s, %d symbols)\n", f.Path, f.Language, f.NodeCount)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func formatFilesGrouped(files []FileInfo) string {
	byLang := map[string][]FileInfo{}
	for _, f := range files {
		byLang[string(f.Language)] = append(byLang[string(f.Language)], f)
	}
	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Files (%d)\n\n", len(files))
	for _, l := range langs {
		fmt.Fprintf(&sb, "### %s (%d files)\n", l, len(byLang[l]))
		for _, f := range byLang[l] {
			fmt.Fprintf(&sb, "- %s (%d symbols)\n", f.Path, f.NodeCount)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// readNodeSource reads the source lines for a node.
func (h *toolHandlers) readNodeSource(n NodeInfo) (string, error) {
	src, err := h.backend.ReadFile(n.FilePath)
	if err != nil {
		return "", err
	}
	lines := strings.Split(src, "\n")
	start := n.StartLine - 1
	end := n.EndLine
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end <= start {
		return "", nil
	}
	var sb strings.Builder
	for i, line := range lines[start:end] {
		fmt.Fprintf(&sb, "%d\t%s\n", start+i+1, line)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func languageID(lang model.Language) string {
	switch lang {
	case model.LangGo:
		return "go"
	case model.LangTypeScript, model.LangTSX:
		return "typescript"
	case model.LangJavaScript, model.LangJSX:
		return "javascript"
	default:
		return string(lang)
	}
}

func languageIDFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	default:
		return ""
	}
}

func numberedSource(src string) string {
	lines := strings.Split(src, "\n")
	var sb strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&sb, "%d\t%s\n", i+1, line)
	}
	return sb.String()
}

// orderedFiles returns file paths ordered by relevance (most nodes first), limited to maxFiles.
func orderedFiles(fileNodes map[string][]NodeInfo, maxFiles int) []string {
	type fp struct {
		path  string
		count int
	}
	fps := make([]fp, 0, len(fileNodes))
	for p, ns := range fileNodes {
		fps = append(fps, fp{p, len(ns)})
	}
	sort.Slice(fps, func(i, j int) bool {
		if fps[i].count != fps[j].count {
			return fps[i].count > fps[j].count
		}
		return fps[i].path < fps[j].path
	})
	if len(fps) > maxFiles {
		fps = fps[:maxFiles]
	}
	out := make([]string, len(fps))
	for i, f := range fps {
		out[i] = f.path
	}
	return out
}

// topNodesByFile returns the most relevant nodes for the blast radius section.
func topNodesByFile(sg *Subgraph, fileOrder []string) []NodeInfo {
	var out []NodeInfo
	seen := make(map[string]struct{})
	for _, fp := range fileOrder {
		nodes := sg.FileNodes[fp]
		for _, n := range nodes {
			if _, ok := seen[n.ID]; ok {
				continue
			}
			seen[n.ID] = struct{}{}
			// Only include functions/methods/structs (not imports, files, etc).
			switch n.Kind {
			case model.KindFunction, model.KindMethod, model.KindStruct,
				model.KindClass, model.KindInterface:
				out = append(out, n)
			}
		}
	}
	// Limit.
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func fileFromPath(p string) string {
	return filepath.Base(p)
}

// registerTools registers all tools onto the MCP server.
// Only the tiny-repo tools (search, node, explore) are registered when
// fileCount < 500 (matches the TS tiny-repo gating).
func registerTools(s *server.MCPServer, h *toolHandlers, fileCount int) {
	tinyRepo := fileCount < 500

	// codegraph_search — always registered.
	s.AddTool(mcplib.NewTool("codegraph_search",
		mcplib.WithDescription("Quick symbol search by name. Returns locations only (no code). Use codegraph_explore instead to get the actual source / understand an area in one call."),
		mcplib.WithString("query",
			mcplib.Description("Symbol name or partial name (e.g., \"auth\", \"signIn\", \"UserService\")"),
			mcplib.Required(),
		),
		mcplib.WithString("kind",
			mcplib.Description("Filter by node kind"),
			mcplib.Enum("function", "method", "class", "interface", "type", "variable", "route", "component"),
		),
		mcplib.WithNumber("limit",
			mcplib.Description("Maximum results (default: 10)"),
		),
	), h.handleSearch)

	// codegraph_node — always registered.
	s.AddTool(mcplib.NewTool("codegraph_node",
		mcplib.WithDescription("Two modes. (1) READ A FILE — use INSTEAD of the Read tool: pass `file` (a path or basename) with no `symbol` and it returns that file's current on-disk source with line numbers, exactly the shape Read gives you (`<n>\\t<line>`, safe to Edit from), narrowable with `offset`/`limit` just like Read — PLUS a one-line note of which files depend on it. Same bytes as Read, faster (served from the index), with the blast radius attached. Use it whenever you would Read a source file. (2) ONE SYMBOL you can name — its location, signature, verbatim source (includeCode=true) and caller/callee trail in one call, so before changing it you see what calls it and what your edit would break. For an AMBIGUOUS name it returns EVERY matching definition's body in one call (so you never Read a file to find the right overload); pass `file`/`line` to pin one. Use codegraph_explore for several related symbols or the full flow."),
		mcplib.WithString("symbol",
			mcplib.Description("Name of the symbol to read (symbol mode). Omit it and pass `file` alone to read a whole file like Read."),
		),
		mcplib.WithBoolean("includeCode",
			mcplib.Description("Symbol mode: include the symbol's full body (default: false). Ignored in file mode, which always returns source unless `symbolsOnly` is set."),
		),
		mcplib.WithString("file",
			mcplib.Description("A file path or basename. Pass it ALONE (no symbol) to READ the file like the Read tool."),
		),
		mcplib.WithNumber("offset",
			mcplib.Description("File mode: 1-based line to start reading from."),
		),
		mcplib.WithNumber("limit",
			mcplib.Description("File mode: maximum number of lines to return."),
		),
		mcplib.WithBoolean("symbolsOnly",
			mcplib.Description("File mode: return just the file's symbol map + dependents."),
		),
		mcplib.WithNumber("line",
			mcplib.Description("Symbol mode only: disambiguate to the definition at/around this line."),
		),
	), h.handleNode)

	// codegraph_explore — always registered.
	exploreDesc := "PRIMARY TOOL — call FIRST for almost any question OR before an edit: how does X work, architecture, a bug, where/what is X, surveying an area, or the symbols you are about to change. Returns the verbatim source of the relevant symbols grouped by file in ONE capped call (Read-equivalent — treat the shown source as already Read; do NOT re-open those files), plus the call path among them. Query can be a natural-language question OR a bag of symbol/file names. Usually the ONLY call you need — more accurate context, in far fewer tokens and round-trips than a search/Read/Grep loop."
	if tinyRepo {
		exploreDesc += " Budget: make at most 1 call for this project."
	}
	s.AddTool(mcplib.NewTool("codegraph_explore",
		mcplib.WithDescription(exploreDesc),
		mcplib.WithString("query",
			mcplib.Description("Symbol names, file names, or short code terms to explore."),
			mcplib.Required(),
		),
		mcplib.WithNumber("maxFiles",
			mcplib.Description("Maximum number of files to include source code from (default: 12)"),
		),
	), h.handleExplore)

	// Large-project-only tools.
	if tinyRepo {
		return
	}

	s.AddTool(mcplib.NewTool("codegraph_callers",
		mcplib.WithDescription("List functions that call <symbol>. For the full flow, use codegraph_explore."),
		mcplib.WithString("symbol",
			mcplib.Description("Name of the function, method, or class to find callers for"),
			mcplib.Required(),
		),
		mcplib.WithNumber("limit",
			mcplib.Description("Maximum number of callers to return (default: 20)"),
		),
	), h.handleCallers)

	s.AddTool(mcplib.NewTool("codegraph_callees",
		mcplib.WithDescription("List functions that <symbol> calls. For the full flow, use codegraph_explore."),
		mcplib.WithString("symbol",
			mcplib.Description("Name of the function, method, or class to find callees for"),
			mcplib.Required(),
		),
		mcplib.WithNumber("limit",
			mcplib.Description("Maximum number of callees to return (default: 20)"),
		),
	), h.handleCallees)

	s.AddTool(mcplib.NewTool("codegraph_impact",
		mcplib.WithDescription("List symbols affected by changing <symbol>. Use before a refactor."),
		mcplib.WithString("symbol",
			mcplib.Description("Name of the symbol to analyze impact for"),
			mcplib.Required(),
		),
		mcplib.WithNumber("depth",
			mcplib.Description("How many levels of dependencies to traverse (default: 2)"),
		),
	), h.handleImpact)

	s.AddTool(mcplib.NewTool("codegraph_status",
		mcplib.WithDescription("Index health check (files / nodes / edges). Skip unless debugging."),
	), h.handleStatus)

	s.AddTool(mcplib.NewTool("codegraph_files",
		mcplib.WithDescription("Indexed file tree with language + symbol counts. Faster than Glob for project layout."),
		mcplib.WithString("path",
			mcplib.Description("Filter to files under this directory path."),
		),
		mcplib.WithString("pattern",
			mcplib.Description("Filter files matching this glob pattern."),
		),
		mcplib.WithString("format",
			mcplib.Description("Output format: \"tree\" (default), \"flat\", or \"grouped\""),
			mcplib.Enum("tree", "flat", "grouped"),
		),
		mcplib.WithBoolean("includeMetadata",
			mcplib.Description("Include file metadata (default: true)"),
		),
		mcplib.WithNumber("maxDepth",
			mcplib.Description("Maximum directory depth to show."),
		),
	), h.handleFiles)
}
