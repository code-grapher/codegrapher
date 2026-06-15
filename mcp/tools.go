package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/query"
)

// -----------------------------------------------------------------------
// Tool definitions and handlers — faithful port of src/mcp/tools.ts.
// -----------------------------------------------------------------------

const maxOutputLength = 15000
const maxInputLength = 10000
const maxPathLength = 4096

// toolResult mirrors ToolResult: text content blocks plus an error flag.
type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textResultOf(text string) toolResult {
	return toolResult{Content: []contentBlock{{Type: "text", Text: text}}}
}

func errorResult(message string) toolResult {
	return toolResult{
		Content: []contentBlock{{Type: "text", Text: "Error: " + message}},
		IsError: true,
	}
}

// toolDef is one tools/list entry. InputSchema is raw JSON copied verbatim
// from the upstream tool definitions so the listed schema matches the
// original byte-for-byte (after JSON canonicalization).
type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

const projectPathProperty = `{
  "type": "string",
  "description": "Path to a different project with .codegraph/ initialized. If omitted, uses current project. Use this to query other codebases."
}`

// toolDefs mirrors the upstream `tools` array (same order).
var toolDefs = []toolDef{
	{
		Name:        "codegraph_search",
		Description: `Quick symbol search by name. Returns locations only (no code). Use codegraph_explore instead to get the actual source / understand an area in one call.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Symbol name or partial name (e.g., \"auth\", \"signIn\", \"UserService\")"},
				"kind": {"type": "string", "description": "Filter by node kind", "enum": ["function", "method", "class", "interface", "type", "variable", "route", "component"]},
				"limit": {"type": "number", "description": "Maximum results (default: 10)", "default": 10},
				"projectPath": ` + projectPathProperty + `
			},
			"required": ["query"]
		}`),
	},
	{
		Name:        "codegraph_callers",
		Description: `List functions that call <symbol>. For the full flow, use codegraph_explore.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {"type": "string", "description": "Name of the function, method, or class to find callers for"},
				"limit": {"type": "number", "description": "Maximum number of callers to return (default: 20)", "default": 20},
				"projectPath": ` + projectPathProperty + `
			},
			"required": ["symbol"]
		}`),
	},
	{
		Name:        "codegraph_callees",
		Description: `List functions that <symbol> calls. For the full flow, use codegraph_explore.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {"type": "string", "description": "Name of the function, method, or class to find callees for"},
				"limit": {"type": "number", "description": "Maximum number of callees to return (default: 20)", "default": 20},
				"projectPath": ` + projectPathProperty + `
			},
			"required": ["symbol"]
		}`),
	},
	{
		Name:        "codegraph_impact",
		Description: `List symbols affected by changing <symbol>. Use before a refactor.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {"type": "string", "description": "Name of the symbol to analyze impact for"},
				"depth": {"type": "number", "description": "How many levels of dependencies to traverse (default: 2)", "default": 2},
				"projectPath": ` + projectPathProperty + `
			},
			"required": ["symbol"]
		}`),
	},
	{
		Name:        "codegraph_node",
		Description: "Two modes. (1) READ A FILE — use INSTEAD of the Read tool: pass `file` (a path or basename) with no `symbol` and it returns that file's current on-disk source with line numbers, exactly the shape Read gives you (`<n>\\t<line>`, safe to Edit from), narrowable with `offset`/`limit` just like Read — PLUS a one-line note of which files depend on it. Same bytes as Read, faster (served from the index), with the blast radius attached. Use it whenever you would Read a source file. (2) ONE SYMBOL you can name — its location, signature, verbatim source (includeCode=true) and caller/callee trail in one call, so before changing it you see what calls it and what your edit would break. For an AMBIGUOUS name it returns EVERY matching definition's body in one call (so you never Read a file to find the right overload); pass `file`/`line` to pin one. Use codegraph_explore for several related symbols or the full flow.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {"type": "string", "description": "Name of the symbol to read (symbol mode). Omit it and pass ` + "`file`" + ` alone to read a whole file like Read."},
				"includeCode": {"type": "boolean", "description": "Symbol mode: include the symbol's full body (default: false). Ignored in file mode, which always returns source unless ` + "`symbolsOnly`" + ` is set.", "default": false},
				"file": {"type": "string", "description": "A file path or basename (e.g. \"harness.rs\", \"src/auth/session.ts\"). Pass it ALONE (no symbol) to READ the file like the Read tool — its full source with line numbers + which files depend on it. Or pass it WITH a symbol to disambiguate an overloaded name to the definition in this file."},
				"offset": {"type": "number", "description": "File mode: 1-based line to start reading from, exactly like Read's offset. Defaults to the start of the file."},
				"limit": {"type": "number", "description": "File mode: maximum number of lines to return, exactly like Read's limit. Defaults to the whole file (capped at 2000 lines, like Read)."},
				"symbolsOnly": {"type": "boolean", "description": "File mode: return just the file's symbol map + dependents (a cheap structural overview) instead of its source.", "default": false},
				"line": {"type": "number", "description": "Symbol mode only: disambiguate to the definition at/around this line (use with the file:line a trail showed you)."},
				"projectPath": ` + projectPathProperty + `
			},
			"required": []
		}`),
	},
	{
		Name:        "codegraph_explore",
		Description: `PRIMARY TOOL — call FIRST for almost any question OR before an edit: how does X work, architecture, a bug, where/what is X, surveying an area, or the symbols you are about to change. Returns the verbatim source of the relevant symbols grouped by file in ONE capped call (Read-equivalent — treat the shown source as already Read; do NOT re-open those files), plus the call path among them. Query can be a natural-language question OR a bag of symbol/file names. Usually the ONLY call you need — more accurate context, in far fewer tokens and round-trips than a search/Read/Grep loop.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Symbol names, file names, or short code terms to explore (e.g., \"AuthService loginUser session-manager\", \"GraphTraverser BFS impact traversal.ts\"). Use codegraph_search first to find relevant names."},
				"maxFiles": {"type": "number", "description": "Maximum number of files to include source code from (default: 12)", "default": 12},
				"projectPath": ` + projectPathProperty + `
			},
			"required": ["query"]
		}`),
	},
	{
		Name:        "codegraph_status",
		Description: `Index health check (files / nodes / edges). Skip unless debugging.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"projectPath": ` + projectPathProperty + `
			}
		}`),
	},
	{
		Name:        "codegraph_files",
		Description: `Indexed file tree with language + symbol counts. Faster than Glob for project layout.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Filter to files under this directory path (e.g., \"src/components\"). Returns all files if not specified."},
				"pattern": {"type": "string", "description": "Filter files matching this glob pattern (e.g., \"*.tsx\", \"**/*.test.ts\")"},
				"format": {"type": "string", "description": "Output format: \"tree\" (hierarchical, default), \"flat\" (simple list), \"grouped\" (by language)", "enum": ["tree", "flat", "grouped"], "default": "tree"},
				"includeMetadata": {"type": "boolean", "description": "Include file metadata like language and symbol count (default: true)", "default": true},
				"maxDepth": {"type": "number", "description": "Maximum directory depth to show (default: unlimited)"},
				"projectPath": ` + projectPathProperty + `
			}
		}`),
	},
}

// tinyRepoCoreTools mirrors TINY_REPO_CORE_TOOLS (ITER4 set).
var tinyRepoCoreTools = map[string]bool{
	"codegraph_explore": true,
	"codegraph_search":  true,
	"codegraph_node":    true,
}

const tinyRepoFileThreshold = 500

// toolHandlers binds the tool handlers to a backend.
type toolHandlers struct {
	backend GraphBackend
}

// toolAllowlist mirrors the CODEGRAPH_MCP_TOOLS allowlist.
func toolAllowlist() map[string]bool {
	raw := strings.TrimSpace(os.Getenv("CODEGRAPH_MCP_TOOLS"))
	if raw == "" {
		return nil
	}
	set := make(map[string]bool)
	for s := range strings.SplitSeq(raw, ",") {
		short := strings.TrimPrefix(strings.TrimSpace(s), "codegraph_")
		if short != "" {
			set[short] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func isToolAllowed(name string) bool {
	allow := toolAllowlist()
	return allow == nil || allow[strings.TrimPrefix(name, "codegraph_")]
}

// getTools mirrors ToolHandler.getTools: allowlist filtering, tiny-repo
// gating, and the dynamic explore budget suffix.
func (h *toolHandlers) getTools() []toolDef {
	allow := toolAllowlist()
	var visible []toolDef
	for _, t := range toolDefs {
		if allow == nil || allow[strings.TrimPrefix(t.Name, "codegraph_")] {
			visible = append(visible, t)
		}
	}

	stats, err := h.backend.GetStats()
	if err != nil {
		return visible
	}
	budget := getExploreBudget(stats.FileCount)

	if stats.FileCount < tinyRepoFileThreshold {
		gated := visible[:0:0]
		for _, t := range visible {
			if tinyRepoCoreTools[t.Name] {
				gated = append(gated, t)
			}
		}
		visible = gated
	}

	out := make([]toolDef, len(visible))
	for i, t := range visible {
		if t.Name == "codegraph_explore" {
			t.Description = fmt.Sprintf("%s Budget: make at most %d calls for this project (%s files indexed).",
				t.Description, budget, localeString(stats.FileCount))
		}
		out[i] = t
	}
	return out
}

// validateString mirrors ToolHandler.validateString.
func (h *toolHandlers) validateString(value any, name string) (string, *toolResult) {
	s, ok := value.(string)
	if !ok || s == "" {
		r := errorResult(fmt.Sprintf("%s must be a non-empty string", name))
		return "", &r
	}
	if len(s) > maxInputLength {
		r := errorResult(fmt.Sprintf("%s exceeds maximum length of %d characters (got %d)", name, maxInputLength, len(s)))
		return "", &r
	}
	return s, nil
}

// validateOptionalPath mirrors ToolHandler.validateOptionalPath.
func validateOptionalPath(value any, name string) *toolResult {
	if value == nil {
		return nil
	}
	s, ok := value.(string)
	if !ok {
		r := errorResult(fmt.Sprintf("%s must be a string", name))
		return &r
	}
	if len(s) > maxPathLength {
		r := errorResult(fmt.Sprintf("%s exceeds maximum length of %d characters (got %d)", name, maxPathLength, len(s)))
		return &r
	}
	return nil
}

// intArg reads a numeric argument with a default (Number(x) || def semantics).
func intArg(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		if n == 0 {
			return def
		}
		return int(n)
	case int:
		if n == 0 {
			return def
		}
		return n
	default:
		return def
	}
}

// execute mirrors ToolHandler.execute.
func (h *toolHandlers) execute(toolName string, args map[string]any) toolResult {
	if !isToolAllowed(toolName) {
		return errorResult(fmt.Sprintf("Tool %s is disabled via CODEGRAPH_MCP_TOOLS", toolName))
	}
	if r := validateOptionalPath(args["projectPath"], "projectPath"); r != nil {
		return *r
	}
	if _, ok := args["path"]; ok {
		if r := validateOptionalPath(args["path"], "path"); r != nil {
			return *r
		}
	}
	if _, ok := args["pattern"]; ok {
		if r := validateOptionalPath(args["pattern"], "pattern"); r != nil {
			return *r
		}
	}

	switch toolName {
	case "codegraph_search":
		return h.handleSearch(args)
	case "codegraph_callers":
		return h.handleCallers(args)
	case "codegraph_callees":
		return h.handleCallees(args)
	case "codegraph_impact":
		return h.handleImpact(args)
	case "codegraph_explore":
		return h.handleExplore(args)
	case "codegraph_node":
		return h.handleNode(args)
	case "codegraph_status":
		return h.handleStatus(args)
	case "codegraph_files":
		return h.handleFiles(args)
	default:
		return errorResult(fmt.Sprintf("Unknown tool: %s", toolName))
	}
}

// -----------------------------------------------------------------------
// codegraph_search
// -----------------------------------------------------------------------

func (h *toolHandlers) handleSearch(args map[string]any) toolResult {
	queryStr, errRes := h.validateString(args["query"], "query")
	if errRes != nil {
		return *errRes
	}
	var kinds []model.NodeKind
	if kind, ok := args["kind"].(string); ok && kind != "" {
		kinds = []model.NodeKind{model.NodeKind(kind)}
	}
	limit := clamp(intArg(args, "limit", 10), 100)

	results, err := h.backend.SearchNodes(queryStr, kinds, limit)
	if err != nil {
		return errorResult(fmt.Sprintf("Tool execution failed: %s", err))
	}
	if len(results) == 0 {
		return textResultOf(fmt.Sprintf("No results found for %q", queryStr))
	}

	// Down-rank generated files (stable).
	ranked := append([]model.SearchResult(nil), results...)
	sort.SliceStable(ranked, func(i, j int) bool {
		gi, gj := 0, 0
		if query.IsGeneratedFile(ranked[i].Node.FilePath) {
			gi = 1
		}
		if query.IsGeneratedFile(ranked[j].Node.FilePath) {
			gj = 1
		}
		return gi < gj
	})

	return textResultOf(truncateOutput(formatSearchResults(ranked)))
}

func formatSearchResults(results []model.SearchResult) string {
	lines := []string{fmt.Sprintf("## Search Results (%d found)", len(results)), ""}
	for _, r := range results {
		n := r.Node
		location := ""
		if n.StartLine != 0 {
			location = fmt.Sprintf(":%d", n.StartLine)
		}
		lines = append(lines, fmt.Sprintf("### %s (%s)", n.Name, n.Kind))
		lines = append(lines, n.FilePath+location)
		if n.Signature != "" {
			lines = append(lines, "`"+n.Signature+"`")
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// -----------------------------------------------------------------------
// codegraph_callers / codegraph_callees / codegraph_impact
// -----------------------------------------------------------------------

func (h *toolHandlers) handleCallers(args map[string]any) toolResult {
	symbol, errRes := h.validateString(args["symbol"], "symbol")
	if errRes != nil {
		return *errRes
	}
	limit := clamp(intArg(args, "limit", 20), 100)

	allMatches := h.findAllSymbols(symbol)
	if len(allMatches.nodes) == 0 {
		return textResultOf(fmt.Sprintf("Symbol %q not found in the codebase", symbol))
	}

	seen := make(map[string]struct{})
	var allCallers []model.Node
	for _, node := range allMatches.nodes {
		callers, err := h.backend.GetCallers(node.ID)
		if err != nil {
			continue
		}
		for _, c := range callers {
			if _, ok := seen[c.Node.ID]; !ok {
				seen[c.Node.ID] = struct{}{}
				allCallers = append(allCallers, c.Node)
			}
		}
	}
	if len(allCallers) == 0 {
		return textResultOf(fmt.Sprintf("No callers found for %q%s", symbol, allMatches.note))
	}
	if len(allCallers) > limit {
		allCallers = allCallers[:limit]
	}
	return textResultOf(truncateOutput(formatNodeList(allCallers, "Callers of "+symbol) + allMatches.note))
}

func (h *toolHandlers) handleCallees(args map[string]any) toolResult {
	symbol, errRes := h.validateString(args["symbol"], "symbol")
	if errRes != nil {
		return *errRes
	}
	limit := clamp(intArg(args, "limit", 20), 100)

	allMatches := h.findAllSymbols(symbol)
	if len(allMatches.nodes) == 0 {
		return textResultOf(fmt.Sprintf("Symbol %q not found in the codebase", symbol))
	}

	seen := make(map[string]struct{})
	var allCallees []model.Node
	for _, node := range allMatches.nodes {
		callees, err := h.backend.GetCallees(node.ID)
		if err != nil {
			continue
		}
		for _, c := range callees {
			if _, ok := seen[c.Node.ID]; !ok {
				seen[c.Node.ID] = struct{}{}
				allCallees = append(allCallees, c.Node)
			}
		}
	}
	if len(allCallees) == 0 {
		return textResultOf(fmt.Sprintf("No callees found for %q%s", symbol, allMatches.note))
	}
	if len(allCallees) > limit {
		allCallees = allCallees[:limit]
	}
	return textResultOf(truncateOutput(formatNodeList(allCallees, "Callees of "+symbol) + allMatches.note))
}

func (h *toolHandlers) handleImpact(args map[string]any) toolResult {
	symbol, errRes := h.validateString(args["symbol"], "symbol")
	if errRes != nil {
		return *errRes
	}
	depth := clamp(intArg(args, "depth", 2), 10)

	allMatches := h.findAllSymbols(symbol)
	if len(allMatches.nodes) == 0 {
		return textResultOf(fmt.Sprintf("Symbol %q not found in the codebase", symbol))
	}

	merged := NewSubgraph()
	seenEdges := make(map[string]struct{})
	for _, node := range allMatches.nodes {
		impact, err := h.backend.GetImpactRadius(node.ID, depth)
		if err != nil {
			continue
		}
		for _, n := range impact.Values() {
			merged.Set(n)
		}
		for _, e := range impact.Edges {
			key := fmt.Sprintf("%s->%s:%s", e.Source, e.Target, e.Kind)
			if _, ok := seenEdges[key]; !ok {
				seenEdges[key] = struct{}{}
				merged.Edges = append(merged.Edges, e)
			}
		}
	}

	return textResultOf(truncateOutput(formatImpact(symbol, merged) + allMatches.note))
}

func formatNodeList(nodes []model.Node, title string) string {
	lines := []string{fmt.Sprintf("## %s (%d found)", title, len(nodes)), ""}
	for _, node := range nodes {
		location := ""
		if node.StartLine != 0 {
			location = fmt.Sprintf(":%d", node.StartLine)
		}
		lines = append(lines, fmt.Sprintf("- %s (%s) - %s%s", node.Name, node.Kind, node.FilePath, location))
	}
	return strings.Join(lines, "\n")
}

func formatImpact(symbol string, impact *Subgraph) string {
	lines := []string{
		fmt.Sprintf("## Impact: %q affects %d symbols", symbol, impact.Len()),
		"",
	}
	var fileOrder []string
	byFile := make(map[string][]model.Node)
	for _, node := range impact.Values() {
		if _, ok := byFile[node.FilePath]; !ok {
			fileOrder = append(fileOrder, node.FilePath)
		}
		byFile[node.FilePath] = append(byFile[node.FilePath], node)
	}
	for _, file := range fileOrder {
		lines = append(lines, fmt.Sprintf("**%s:**", file))
		var parts []string
		for _, n := range byFile[file] {
			parts = append(parts, fmt.Sprintf("%s:%d", n.Name, n.StartLine))
		}
		lines = append(lines, strings.Join(parts, ", "))
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// -----------------------------------------------------------------------
// Symbol resolution helpers
// -----------------------------------------------------------------------

var reQualified = regexp.MustCompile(`[./]|::`)

// rustPathPrefixes mirrors RUST_PATH_PREFIXES.
var rustPathPrefixes = map[string]bool{"crate": true, "super": true, "self": true}

// matchesSymbol mirrors ToolHandler.matchesSymbol.
func matchesSymbol(node model.Node, symbol string) bool {
	if node.Name == symbol {
		return true
	}
	if node.Kind == model.KindFile {
		if base := regexp.MustCompile(`\.[^.]+$`).ReplaceAllString(node.Name, ""); base == symbol {
			return true
		}
	}
	if !reQualified.MatchString(symbol) {
		return false
	}
	var parts []string
	for _, p := range reQualSplit.Split(symbol, -1) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) < 2 {
		return false
	}
	lastPart := parts[len(parts)-1]
	if node.Name != lastPart {
		return false
	}

	colonSuffix := strings.Join(parts, "::")
	if strings.Contains(node.QualifiedName, colonSuffix) {
		return true
	}

	var containerHints []string
	for _, p := range parts[:len(parts)-1] {
		if !rustPathPrefixes[p] {
			containerHints = append(containerHints, p)
		}
	}
	if len(containerHints) == 0 {
		return false
	}
	var segments []string
	for s := range strings.SplitSeq(node.FilePath, "/") {
		if s != "" {
			segments = append(segments, s)
		}
	}
	for _, hint := range containerHints {
		found := false
		for _, seg := range segments {
			if seg == hint || regexp.MustCompile(`\.[^.]+$`).ReplaceAllString(seg, "") == hint {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// findSymbolMatches mirrors ToolHandler.findSymbolMatches.
func (h *toolHandlers) findSymbolMatches(symbol string) []model.Node {
	isQualified := reQualified.MatchString(symbol)

	if !isQualified {
		exact, err := h.backend.GetNodesByName(symbol)
		if err == nil && len(exact) > 0 {
			out := append([]model.Node(nil), exact...)
			sort.SliceStable(out, func(i, j int) bool {
				gi, gj := 0, 0
				if query.IsGeneratedFile(out[i].FilePath) {
					gi = 1
				}
				if query.IsGeneratedFile(out[j].FilePath) {
					gj = 1
				}
				return gi < gj
			})
			return out
		}
		fuzzy, err := h.backend.SearchNodes(symbol, nil, 10)
		if err == nil && len(fuzzy) > 0 {
			return []model.Node{fuzzy[0].Node}
		}
		return nil
	}

	const limit = 50
	results, err := h.backend.SearchNodes(symbol, nil, limit)
	if err != nil {
		return nil
	}
	if len(results) == 0 {
		if tail := lastQualifierPart(symbol); tail != "" && tail != symbol {
			results, _ = h.backend.SearchNodes(tail, nil, limit)
		}
	}
	if len(results) == 0 {
		return nil
	}

	var exactMatches []model.Node
	for _, r := range results {
		if matchesSymbol(r.Node, symbol) {
			exactMatches = append(exactMatches, r.Node)
		}
	}
	if len(exactMatches) == 0 {
		return nil // qualified lookup must not fall back to a fuzzy hit
	}
	sort.SliceStable(exactMatches, func(i, j int) bool {
		gi, gj := 0, 0
		if query.IsGeneratedFile(exactMatches[i].FilePath) {
			gi = 1
		}
		if query.IsGeneratedFile(exactMatches[j].FilePath) {
			gj = 1
		}
		return gi < gj
	})
	return exactMatches
}

type symbolMatches struct {
	nodes []model.Node
	note  string
}

// findAllSymbols mirrors ToolHandler.findAllSymbols.
func (h *toolHandlers) findAllSymbols(symbol string) symbolMatches {
	results, err := h.backend.SearchNodes(symbol, nil, 50)
	if err != nil {
		return symbolMatches{}
	}
	if len(results) == 0 && reQualified.MatchString(symbol) {
		if tail := lastQualifierPart(symbol); tail != "" && tail != symbol {
			results, _ = h.backend.SearchNodes(tail, nil, 50)
		}
	}
	if len(results) == 0 {
		return symbolMatches{}
	}

	var exactMatches []model.SearchResult
	for _, r := range results {
		if matchesSymbol(r.Node, symbol) {
			exactMatches = append(exactMatches, r)
		}
	}

	if len(exactMatches) <= 1 {
		node := results[0].Node
		if len(exactMatches) == 1 {
			node = exactMatches[0].Node
		}
		return symbolMatches{nodes: []model.Node{node}}
	}

	ranked := append([]model.SearchResult(nil), exactMatches...)
	sort.SliceStable(ranked, func(i, j int) bool {
		gi, gj := 0, 0
		if query.IsGeneratedFile(ranked[i].Node.FilePath) {
			gi = 1
		}
		if query.IsGeneratedFile(ranked[j].Node.FilePath) {
			gj = 1
		}
		return gi < gj
	})

	var locations []string
	nodes := make([]model.Node, len(ranked))
	for i, r := range ranked {
		nodes[i] = r.Node
		locations = append(locations, fmt.Sprintf("%s at %s:%d", r.Node.Kind, r.Node.FilePath, r.Node.StartLine))
	}
	note := fmt.Sprintf("\n\n> **Note:** Aggregated results across %d symbols named %q: %s",
		len(ranked), symbol, strings.Join(locations, ", "))
	return symbolMatches{nodes: nodes, note: note}
}

// truncateOutput mirrors ToolHandler.truncateOutput.
func truncateOutput(text string) string {
	if len(text) <= maxOutputLength {
		return text
	}
	truncated := text[:maxOutputLength]
	lastNewline := strings.LastIndex(truncated, "\n")
	cutPoint := maxOutputLength
	if float64(lastNewline) > float64(maxOutputLength)*0.8 {
		cutPoint = lastNewline
	}
	return truncated[:cutPoint] + "\n\n... (output truncated)"
}

// -----------------------------------------------------------------------
// synthEdgeNote — dynamic-dispatch edge annotation
// -----------------------------------------------------------------------

type synthNote struct {
	label        string
	compact      string
	registeredAt string
}

// synthEdgeNote mirrors ToolHandler.synthEdgeNote. Upstream reads the
// synthesizedBy/via/registeredAt metadata persisted by its callback
// synthesizer; this port's resolve package synthesizes the same heuristic
// edges without metadata, so when metadata is absent the interface-impl
// annotation (the only heuristic `calls` kind the Go resolver emits) is
// derived from the edge target's location — the exact value upstream stores
// in registeredAt for interface-impl edges.
func (h *toolHandlers) synthEdgeNote(edge *model.Edge) *synthNote {
	if edge == nil || edge.Provenance != "heuristic" {
		return nil
	}
	m := edge.Metadata
	registeredAt := ""
	if m != nil {
		if s, ok := m["registeredAt"].(string); ok {
			registeredAt = s
		}
	}
	at := ""
	if registeredAt != "" {
		at = " @" + registeredAt
	}
	synthesizedBy := ""
	if m != nil {
		if s, ok := m["synthesizedBy"].(string); ok {
			synthesizedBy = s
		}
	}

	if synthesizedBy == "" && edge.Kind == model.EdgeCalls {
		// Derive the interface-impl annotation (see doc comment above).
		if target, err := h.backend.GetNodeByID(edge.Target); err == nil && target != nil {
			registeredAt = fmt.Sprintf("%s:%d", target.FilePath, target.StartLine)
			at = " @" + registeredAt
		}
		synthesizedBy = "interface-impl"
	}

	switch synthesizedBy {
	case "callback":
		via := "a registrar"
		if m != nil {
			if v, ok := m["via"]; ok {
				via = fmt.Sprintf("`%v`", v)
			}
		}
		field := ""
		if m != nil {
			if f, ok := m["field"]; ok {
				field = fmt.Sprintf(" on .%v", f)
			}
		}
		return &synthNote{
			label:        fmt.Sprintf("callback — registered via %s%s (dynamic dispatch)", via, field),
			compact:      fmt.Sprintf("dynamic: callback via %s%s", via, at),
			registeredAt: registeredAt,
		}
	case "event-emitter":
		ev := "an event"
		if m != nil {
			if e, ok := m["event"]; ok {
				ev = fmt.Sprintf("`%v`", e)
			}
		}
		return &synthNote{
			label:        fmt.Sprintf("event %s — emit → handler (dynamic dispatch)", ev),
			compact:      fmt.Sprintf("dynamic: event %s%s", ev, at),
			registeredAt: registeredAt,
		}
	case "react-render":
		return &synthNote{
			label:        "React re-render — `setState` re-runs render() (dynamic dispatch)",
			compact:      "dynamic: React re-render via setState" + at,
			registeredAt: registeredAt,
		}
	case "jsx-render":
		child := "a child component"
		if m != nil {
			if v, ok := m["via"]; ok {
				child = fmt.Sprintf("<%v>", v)
			}
		}
		return &synthNote{
			label:        fmt.Sprintf("renders %s (JSX child — dynamic dispatch)", child),
			compact:      fmt.Sprintf("dynamic: renders %s", child),
			registeredAt: registeredAt,
		}
	case "vue-handler":
		ev := "a template event"
		if m != nil {
			if e, ok := m["event"]; ok {
				ev = fmt.Sprintf("@%v", e)
			}
		}
		return &synthNote{
			label:        fmt.Sprintf("Vue template handler — bound to %s (dynamic dispatch)", ev),
			compact:      fmt.Sprintf("dynamic: Vue %s handler", ev),
			registeredAt: registeredAt,
		}
	case "interface-impl":
		return &synthNote{
			label:        "interface/abstract dispatch — runs the implementation override (dynamic dispatch)",
			compact:      "dynamic: interface → impl" + at,
			registeredAt: registeredAt,
		}
	case "closure-collection":
		field := "a collection"
		if m != nil {
			if f, ok := m["field"]; ok {
				field = fmt.Sprintf("`%v`", f)
			}
		}
		return &synthNote{
			label:        fmt.Sprintf("closure collection — runs handlers appended to %s (dynamic dispatch)", field),
			compact:      fmt.Sprintf("dynamic: runs %s handlers%s", field, at),
			registeredAt: registeredAt,
		}
	}
	return nil
}
