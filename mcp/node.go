package mcp

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/model"
)

// -----------------------------------------------------------------------
// codegraph_node — faithful port of handleNode / handleFileView /
// renderNodeSection / formatTrail / buildContainerOutline.
// -----------------------------------------------------------------------

// containerNodeKinds mirrors CONTAINER_NODE_KINDS.
var containerNodeKinds = map[model.NodeKind]bool{
	model.KindClass: true, model.KindStruct: true, model.KindInterface: true,
	model.KindTrait: true, model.KindProtocol: true, model.KindEnum: true,
	model.KindNamespace: true, model.KindModule: true,
}

func (h *toolHandlers) handleNode(args map[string]any) toolResult {
	includeCode := args["includeCode"] == true
	fileHint := ""
	if s, ok := args["file"].(string); ok {
		fileHint = strings.TrimSpace(s)
	}
	lineHint := 0
	if n, ok := args["line"].(float64); ok && n > 0 {
		lineHint = int(n)
	}
	offset := 0
	if n, ok := args["offset"].(float64); ok && n > 0 {
		offset = int(n)
	}
	limit := 0
	if n, ok := args["limit"].(float64); ok && n > 0 {
		limit = int(n)
	}
	symbolsOnly := args["symbolsOnly"] == true
	symbolRaw := ""
	if s, ok := args["symbol"].(string); ok {
		symbolRaw = strings.TrimSpace(s)
	}

	// FILE READ MODE.
	if symbolRaw == "" && fileHint != "" {
		return h.handleFileView(fileHint, offset, limit, symbolsOnly)
	}

	symbol, errRes := h.validateString(args["symbol"], "symbol")
	if errRes != nil {
		return *errRes
	}

	matches := h.findSymbolMatches(symbol)
	if len(matches) == 0 {
		return textResultOf(fmt.Sprintf("Symbol %q not found in the codebase", symbol))
	}

	// Disambiguate via file/line hints.
	if len(matches) > 1 && (fileHint != "" || lineHint > 0) {
		norm := func(p string) string { return strings.ToLower(strings.ReplaceAll(p, "\\", "/")) }
		narrowed := matches
		if fileHint != "" {
			fh := norm(fileHint)
			var byFile []model.Node
			for _, n := range narrowed {
				if strings.HasSuffix(norm(n.FilePath), fh) || strings.Contains(norm(n.FilePath), fh) {
					byFile = append(byFile, n)
				}
			}
			if len(byFile) > 0 {
				narrowed = byFile
			}
		}
		if lineHint > 0 && len(narrowed) > 1 {
			var containing []model.Node
			for _, n := range narrowed {
				end := n.EndLine
				if end == 0 {
					end = n.StartLine
				}
				if n.StartLine <= lineHint && end >= lineHint {
					containing = append(containing, n)
				}
			}
			if len(containing) > 0 {
				narrowed = containing
			} else {
				sorted := append([]model.Node(nil), narrowed...)
				sort.SliceStable(sorted, func(i, j int) bool {
					di := absInt(sorted[i].StartLine - lineHint)
					dj := absInt(sorted[j].StartLine - lineHint)
					return di < dj
				})
				narrowed = sorted[:1]
			}
		}
		if len(narrowed) > 0 {
			matches = narrowed
		}
	}

	if len(matches) == 1 {
		return textResultOf(truncateOutput(h.renderNodeSection(matches[0], includeCode)))
	}

	header := fmt.Sprintf("**%d definitions named %q**", len(matches), symbol)
	if !includeCode {
		out := []string{header, "",
			"Re-query with `includeCode: true` to get every body in one call — no need to pick one first.", ""}
		for _, n := range matches {
			out = append(out, fmt.Sprintf("- `%s` (%s) — %s:%d", n.Name, n.Kind, n.FilePath, n.StartLine))
		}
		return textResultOf(truncateOutput(strings.Join(out, "\n")))
	}

	const bodyBudget = 12000
	const hardCap = 16
	var rendered []string
	var listed []model.Node
	used := 0
	for _, n := range matches {
		if len(rendered) >= hardCap {
			listed = append(listed, n)
			continue
		}
		section := h.renderNodeSection(n, true)
		if len(rendered) == 0 || used+len(section) <= bodyBudget {
			rendered = append(rendered, section)
			used += len(section)
		} else {
			listed = append(listed, n)
		}
	}

	moreSuffix := ""
	if len(listed) > 0 {
		moreSuffix = fmt.Sprintf("; %d more listed below", len(listed))
	}
	out := []string{
		header,
		fmt.Sprintf("Returning %d in full%s — pick the one you need (no Read required).", len(rendered), moreSuffix),
		"",
		strings.Join(rendered, "\n\n---\n\n"),
	}
	if len(listed) > 0 {
		const listCap = 20
		shown := listed
		if len(shown) > listCap {
			shown = shown[:listCap]
		}
		out = append(out, "", "### Other definitions")
		for _, n := range shown {
			out = append(out, fmt.Sprintf("- `%s` (%s) — %s:%d", n.Name, n.Kind, n.FilePath, n.StartLine))
		}
		if len(listed) > listCap {
			out = append(out, fmt.Sprintf("- … +%d more", len(listed)-listCap))
		}
		baseName := listed[0].FilePath
		if idx := strings.LastIndex(baseName, "/"); idx >= 0 {
			baseName = baseName[idx+1:]
		}
		out = append(out, "",
			fmt.Sprintf("> Need one of these in full? Call codegraph_node again with `file` (e.g. `%q`) or `line` — do NOT Read it.", baseName))
	}
	return textResultOf(truncateOutput(strings.Join(out, "\n")))
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// handleFileView mirrors handleFileView (file read mode).
func (h *toolHandlers) handleFileView(fileArg string, offset, limit int, symbolsOnly bool) toolResult {
	normalize := func(p string) string {
		p = strings.ReplaceAll(p, "\\", "/")
		for strings.HasPrefix(p, "./") || strings.HasPrefix(p, "/") {
			p = strings.TrimPrefix(p, "./")
			p = strings.TrimPrefix(p, "/")
		}
		return strings.TrimRight(p, "/")
	}
	wantLower := strings.ToLower(normalize(fileArg))
	allFiles, err := h.backend.GetFiles()
	if err != nil || len(allFiles) == 0 {
		return textResultOf("No files indexed. Run `codegraph index` first.")
	}

	var resolved *FileInfo
	var candidates []FileInfo
	for i := range allFiles {
		if strings.ToLower(allFiles[i].Path) == wantLower {
			resolved = &allFiles[i]
			break
		}
	}
	if resolved == nil {
		for i := range allFiles {
			if strings.HasSuffix(strings.ToLower(allFiles[i].Path), "/"+wantLower) {
				candidates = append(candidates, allFiles[i])
			}
		}
		if len(candidates) == 1 {
			resolved = &candidates[0]
		}
	}
	if resolved == nil && len(candidates) == 0 {
		for i := range allFiles {
			if strings.Contains(strings.ToLower(allFiles[i].Path), wantLower) {
				candidates = append(candidates, allFiles[i])
			}
		}
		if len(candidates) == 1 {
			resolved = &candidates[0]
		}
	}
	if resolved == nil && len(candidates) > 1 {
		out := []string{fmt.Sprintf("%q matches %d indexed files — pass a longer path:", fileArg, len(candidates)), ""}
		shown := candidates
		if len(shown) > 25 {
			shown = shown[:25]
		}
		for _, f := range shown {
			out = append(out, "- "+f.Path)
		}
		return textResultOf(strings.Join(out, "\n"))
	}
	if resolved == nil {
		return textResultOf(fmt.Sprintf(
			"No indexed file matches %q. Codegraph indexes source files; configs/docs it doesn't parse won't appear — Read those directly.", fileArg))
	}

	filePath := resolved.Path
	rawNodes, _ := h.backend.GetNodesInFile(filePath)
	var nodes []model.Node
	for _, n := range rawNodes {
		if n.Kind != model.KindFile && n.Kind != model.KindImport && n.Kind != model.KindExport {
			nodes = append(nodes, n)
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].StartLine < nodes[j].StartLine })
	dependents, _ := h.backend.GetFileDependents(filePath)

	depSummary := "no other indexed file depends on it"
	if len(dependents) > 0 {
		plural := "s"
		if len(dependents) == 1 {
			plural = ""
		}
		shown := dependents
		if len(shown) > 8 {
			shown = shown[:8]
		}
		more := ""
		if len(dependents) > 8 {
			more = fmt.Sprintf(", +%d more", len(dependents)-8)
		}
		depSummary = fmt.Sprintf("used by %d file%s: %s%s", len(dependents), plural, strings.Join(shown, ", "), more)
	}

	symbolMap := func(heading string) []string {
		const capN = 200
		lines := []string{heading}
		shown := nodes
		if len(shown) > capN {
			shown = shown[:capN]
		}
		for _, n := range shown {
			sig := ""
			if n.Signature != "" {
				sig = " " + strings.TrimSpace(strings.Join(strings.Fields(n.Signature), " "))
			}
			lines = append(lines, fmt.Sprintf("- `%s` (%s)%s — :%d", n.Name, n.Kind, sig, n.StartLine))
		}
		if len(nodes) > capN {
			lines = append(lines, fmt.Sprintf("- … +%d more", len(nodes)-capN))
		}
		return lines
	}

	plural := "s"
	if len(nodes) == 1 {
		plural = ""
	}

	if symbolsOnly {
		out := []string{fmt.Sprintf("**%s** — %d symbol%s, %s", filePath, len(nodes), plural, depSummary), ""}
		if len(nodes) > 0 {
			out = append(out, symbolMap("### Symbols")...)
		} else {
			out = append(out, "_No indexed symbols in this file._")
		}
		out = append(out, "", "> Drop `symbolsOnly` (or pass `offset`/`limit`) to read the source, like Read.")
		return textResultOf(truncateOutput(strings.Join(out, "\n")))
	}

	if configLeafLanguages[resolved.Language] {
		out := []string{fmt.Sprintf("**%s** — configuration/data file, %s", filePath, depSummary), ""}
		if len(nodes) > 0 {
			out = append(out, symbolMap("### Keys (values withheld for safety)")...)
		}
		out = append(out, "", "> Values may be secrets, so codegraph indexes keys only. Read the file directly if you need a value.")
		return textResultOf(truncateOutput(strings.Join(out, "\n")))
	}

	abs := validatePathWithinRoot(h.backend.GetProjectRoot(), filePath)
	var content string
	contentOK := false
	if abs != "" {
		if data, err := os.ReadFile(abs); err == nil {
			content = string(data)
			contentOK = true
		}
	}
	if !contentOK {
		out := []string{fmt.Sprintf("**%s** — could not read from disk (it may have moved since indexing). %s", filePath, depSummary), ""}
		if len(nodes) > 0 {
			out = append(out, symbolMap("### Symbols")...)
		}
		out = append(out, "", fmt.Sprintf("> Read `%s` directly for its current content.", filePath))
		return textResultOf(truncateOutput(strings.Join(out, "\n")))
	}

	fileLines := strings.Split(content, "\n")
	total := len(fileLines)

	const charBudget = 38000
	const defaultLimit = 2000
	if offset < 1 {
		offset = 1
	}
	if offset > total {
		linePlural := "s"
		if total == 1 {
			linePlural = ""
		}
		return textResultOf(fmt.Sprintf("**%s** has %d line%s — offset %d is past the end. %s",
			filePath, total, linePlural, offset, depSummary))
	}
	maxLines := limit
	if maxLines < 1 {
		maxLines = defaultLimit
	}
	start := offset - 1
	header := fmt.Sprintf("**%s** — %d lines, %d symbol%s · %s", filePath, total, len(nodes), plural, depSummary)

	var numbered []string
	used := len(header) + 8
	i := start
	for ; i < total && len(numbered) < maxLines; i++ {
		ln := fmt.Sprintf("%d\t%s", i+1, fileLines[i])
		if used+len(ln)+1 > charBudget && len(numbered) > 0 {
			break
		}
		numbered = append(numbered, ln)
		used += len(ln) + 1
	}
	shownEnd := start + len(numbered)
	complete := offset == 1 && shownEnd >= total

	out := append([]string{header, ""}, numbered...)
	if !complete {
		out = append(out, "",
			fmt.Sprintf("(lines %d–%d of %d — pass `offset`/`limit` for another range, or `codegraph_node <symbol>` for one symbol in full)",
				offset, shownEnd, total))
	}
	return textResultOf(strings.Join(out, "\n"))
}

// renderNodeSection mirrors renderNodeSection.
func (h *toolHandlers) renderNodeSection(node model.Node, includeCode bool) string {
	code := ""
	outline := ""
	if includeCode {
		if containerNodeKinds[node.Kind] {
			outline = h.buildContainerOutline(node)
		}
		if outline == "" {
			code, _ = h.backend.GetCode(node.ID)
		}
	}
	return formatNodeDetails(node, code, outline) + h.formatTrail(node)
}

// buildContainerOutline mirrors buildContainerOutline.
func (h *toolHandlers) buildContainerOutline(node model.Node) string {
	children, err := h.backend.GetChildren(node.ID)
	if err != nil {
		return ""
	}
	var filtered []model.Node
	for _, c := range children {
		if c.Kind != model.KindImport && c.Kind != model.KindExport {
			filtered = append(filtered, c)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].StartLine < filtered[j].StartLine })
	if len(filtered) == 0 {
		return ""
	}
	lines := []string{fmt.Sprintf("**Members (%d):**", len(filtered)), ""}
	for _, c := range filtered {
		loc := ""
		if c.StartLine != 0 {
			loc = fmt.Sprintf(":%d", c.StartLine)
		}
		sig := ""
		if c.Signature != "" {
			sig = fmt.Sprintf(" — `%s`", c.Signature)
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)%s%s", c.Name, c.Kind, loc, sig))
	}
	return strings.Join(lines, "\n")
}

// formatNodeDetails mirrors formatNodeDetails.
func formatNodeDetails(node model.Node, code, outline string) string {
	location := ""
	if node.StartLine != 0 {
		location = fmt.Sprintf(":%d", node.StartLine)
	}
	lines := []string{
		fmt.Sprintf("## %s (%s)", node.Name, node.Kind),
		"",
		fmt.Sprintf("**Location:** %s%s", node.FilePath, location),
	}
	if node.Signature != "" {
		lines = append(lines, fmt.Sprintf("**Signature:** `%s`", node.Signature))
	}
	if node.Docstring != "" && len(node.Docstring) < 200 {
		lines = append(lines, "", node.Docstring)
	}
	if outline != "" {
		lines = append(lines, "", outline, "",
			fmt.Sprintf("> Structural outline only. Read `%s` or call codegraph_node on a specific member for its body.", node.FilePath))
	} else if code != "" {
		numbered := code
		if node.StartLine != 0 {
			numbered = numberSourceLines(code, node.StartLine)
		}
		lines = append(lines, "", "```"+string(node.Language), numbered, "```")
	}
	return strings.Join(lines, "\n")
}

// formatTrail mirrors formatTrail.
func (h *toolHandlers) formatTrail(node model.Node) string {
	const trailCap = 12
	fmtEntry := func(e NodeEdge) string {
		base := fmt.Sprintf("%s (%s:%d)", e.Node.Name, e.Node.FilePath, e.Node.StartLine)
		if synth := h.synthEdgeNote(&e.Edge); synth != nil {
			return fmt.Sprintf("%s [%s]", base, synth.compact)
		}
		return base
	}
	collect := func(edges []NodeEdge) []NodeEdge {
		seen := map[string]struct{}{node.ID: {}}
		var out []NodeEdge
		for _, e := range edges {
			if _, ok := seen[e.Node.ID]; ok {
				continue
			}
			seen[e.Node.ID] = struct{}{}
			out = append(out, e)
		}
		return out
	}
	calleesRaw, _ := h.backend.GetCallees(node.ID)
	callersRaw, _ := h.backend.GetCallers(node.ID)
	callees := collect(calleesRaw)
	callers := collect(callersRaw)
	if len(callees) == 0 && len(callers) == 0 {
		return ""
	}
	lines := []string{"", "### Trail — codegraph_node any of these to follow it (no Read needed)"}
	if len(callees) > 0 {
		shown := callees
		if len(shown) > trailCap {
			shown = shown[:trailCap]
		}
		parts := make([]string, len(shown))
		for i, e := range shown {
			parts[i] = fmtEntry(e)
		}
		suffix := ""
		if len(callees) > trailCap {
			suffix = fmt.Sprintf(", +%d more", len(callees)-trailCap)
		}
		lines = append(lines, fmt.Sprintf("**Calls →** %s%s", strings.Join(parts, ", "), suffix))
	}
	if len(callers) > 0 {
		shown := callers
		if len(shown) > trailCap {
			shown = shown[:trailCap]
		}
		parts := make([]string, len(shown))
		for i, e := range shown {
			parts[i] = fmtEntry(e)
		}
		suffix := ""
		if len(callers) > trailCap {
			suffix = fmt.Sprintf(", +%d more", len(callers)-trailCap)
		}
		lines = append(lines, fmt.Sprintf("**Called by ←** %s%s", strings.Join(parts, ", "), suffix))
	}
	return strings.Join(lines, "\n")
}
