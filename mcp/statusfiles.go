package mcp

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/specscore/codegrapher/model"
)

// -----------------------------------------------------------------------
// codegraph_status / codegraph_files — faithful ports of handleStatus and
// handleFiles. The worktree-mismatch and pending-files notices are direct-
// mode no-ops here: there is no daemon/watcher in this build (KNOWN-BUGS
// gap C-1), so both sources are always empty.
// -----------------------------------------------------------------------

func (h *toolHandlers) handleStatus(_ map[string]any) toolResult {
	stats, err := h.backend.GetStats()
	if err != nil {
		return errorResult(fmt.Sprintf("Tool execution failed: %s", err))
	}

	lines := []string{
		"## CodeGraph Status",
		"",
		fmt.Sprintf("**Files indexed:** %d", stats.FileCount),
		fmt.Sprintf("**Total nodes:** %d", stats.NodeCount),
		fmt.Sprintf("**Total edges:** %d", stats.EdgeCount),
		fmt.Sprintf("**Database size:** %s MB", toFixed2(float64(stats.DBSizeBytes)/1024/1024)),
		// Upstream reports its Node built-in SQLite backend; reproduced
		// verbatim for golden parity (the backend string is part of the
		// captured spec, like the CLI status payload's "node-sqlite").
		"**Backend:** node:sqlite (Node built-in) — full WAL + FTS5",
	}

	if stats.JournalMode == "wal" {
		lines = append(lines, "**Journal mode:** wal (concurrent reads safe)")
	} else {
		mode := stats.JournalMode
		if mode == "" {
			mode = "unknown"
		}
		lines = append(lines, fmt.Sprintf(
			"**Journal mode:** ⚠ %s — WAL not active, so reads can block on a concurrent write (WAL appears unsupported on this filesystem)", mode))
	}

	lines = append(lines, "", "### Nodes by Kind:")
	kinds := make([]string, 0, len(stats.NodesByKind))
	for k := range stats.NodesByKind {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds) // GROUP BY kind row order upstream — alphabetical
	for _, k := range kinds {
		if count := stats.NodesByKind[model.NodeKind(k)]; count > 0 {
			lines = append(lines, fmt.Sprintf("- %s: %d", k, count))
		}
	}

	lines = append(lines, "", "### Languages:")
	langs := make([]string, 0, len(stats.FilesByLanguage))
	for l := range stats.FilesByLanguage {
		langs = append(langs, string(l))
	}
	sort.Strings(langs)
	for _, l := range langs {
		if count := stats.FilesByLanguage[model.Language(l)]; count > 0 {
			lines = append(lines, fmt.Sprintf("- %s: %d", l, count))
		}
	}

	return textResultOf(strings.Join(lines, "\n"))
}

// toFixed2 mirrors JS Number.prototype.toFixed(2).
func toFixed2(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}

func (h *toolHandlers) handleFiles(args map[string]any) toolResult {
	pathFilter, _ := args["path"].(string)
	pattern, _ := args["pattern"].(string)
	format, _ := args["format"].(string)
	if format == "" {
		format = "tree"
	}
	includeMetadata := args["includeMetadata"] != false
	maxDepth := 0
	if n, ok := args["maxDepth"].(float64); ok {
		maxDepth = clamp(int(n), 1, 20)
	}

	allFiles, err := h.backend.GetFiles()
	if err != nil {
		return errorResult(fmt.Sprintf("Tool execution failed: %s", err))
	}
	if len(allFiles) == 0 {
		return textResultOf("No files indexed. Run `codegraph index` first.")
	}

	normalizedFilter := ""
	if pathFilter != "" {
		normalizedFilter = strings.ReplaceAll(pathFilter, "\\", "/")
		for strings.HasPrefix(normalizedFilter, "./") || strings.HasPrefix(normalizedFilter, "/") {
			normalizedFilter = strings.TrimPrefix(normalizedFilter, "./")
			normalizedFilter = strings.TrimPrefix(normalizedFilter, "/")
		}
		if normalizedFilter == "." {
			normalizedFilter = ""
		}
		normalizedFilter = strings.TrimRight(normalizedFilter, "/")
	}
	files := allFiles
	if normalizedFilter != "" {
		files = nil
		for _, f := range allFiles {
			if f.Path == normalizedFilter || strings.HasPrefix(f.Path, normalizedFilter+"/") {
				files = append(files, f)
			}
		}
	}

	if pattern != "" {
		re := globToRegex(pattern)
		var filtered []FileInfo
		for _, f := range files {
			if re.MatchString(f.Path) {
				filtered = append(filtered, f)
			}
		}
		files = filtered
	}

	if len(files) == 0 {
		return textResultOf("No files found matching the criteria.")
	}

	var output string
	switch format {
	case "flat":
		output = formatFilesFlat(files, includeMetadata)
	case "grouped":
		output = formatFilesGrouped(files, includeMetadata)
	default:
		output = formatFilesTree(files, includeMetadata, maxDepth)
	}
	return textResultOf(truncateOutput(output))
}

// globToRegex mirrors ToolHandler.globToRegex.
func globToRegex(pattern string) *regexp.Regexp {
	escaped := regexp.MustCompile(`[.+^${}()|[\]\\]`).ReplaceAllString(pattern, `\$0`)
	escaped = strings.ReplaceAll(escaped, "**", "{{GLOBSTAR}}")
	escaped = strings.ReplaceAll(escaped, "*", "[^/]*")
	escaped = strings.ReplaceAll(escaped, "?", "[^/]")
	escaped = strings.ReplaceAll(escaped, "{{GLOBSTAR}}", ".*")
	re, err := regexp.Compile(escaped)
	if err != nil {
		return regexp.MustCompile(regexp.QuoteMeta(pattern))
	}
	return re
}

func formatFilesFlat(files []FileInfo, includeMetadata bool) string {
	lines := []string{fmt.Sprintf("## Files (%d)", len(files)), ""}
	sorted := append([]FileInfo(nil), files...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	for _, f := range sorted {
		if includeMetadata {
			lines = append(lines, fmt.Sprintf("- %s (%s, %d symbols)", f.Path, f.Language, f.NodeCount))
		} else {
			lines = append(lines, "- "+f.Path)
		}
	}
	return strings.Join(lines, "\n")
}

func formatFilesGrouped(files []FileInfo, includeMetadata bool) string {
	var langOrder []string
	byLang := make(map[string][]FileInfo)
	for _, f := range files {
		lang := string(f.Language)
		if _, ok := byLang[lang]; !ok {
			langOrder = append(langOrder, lang)
		}
		byLang[lang] = append(byLang[lang], f)
	}
	lines := []string{fmt.Sprintf("## Files by Language (%d total)", len(files)), ""}
	sort.SliceStable(langOrder, func(i, j int) bool {
		return len(byLang[langOrder[i]]) > len(byLang[langOrder[j]])
	})
	for _, lang := range langOrder {
		langFiles := append([]FileInfo(nil), byLang[lang]...)
		lines = append(lines, fmt.Sprintf("### %s (%d)", lang, len(langFiles)))
		sort.SliceStable(langFiles, func(i, j int) bool { return langFiles[i].Path < langFiles[j].Path })
		for _, f := range langFiles {
			if includeMetadata {
				lines = append(lines, fmt.Sprintf("- %s (%d symbols)", f.Path, f.NodeCount))
			} else {
				lines = append(lines, "- "+f.Path)
			}
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func formatFilesTree(files []FileInfo, includeMetadata bool, maxDepth int) string {
	type treeNode struct {
		name       string
		childOrder []string
		children   map[string]*treeNode
		file       *FileInfo
	}
	newNode := func(name string) *treeNode {
		return &treeNode{name: name, children: make(map[string]*treeNode)}
	}
	root := newNode("")

	for i := range files {
		f := files[i]
		parts := strings.Split(f.Path, "/")
		current := root
		for j, part := range parts {
			if part == "" {
				continue
			}
			child, ok := current.children[part]
			if !ok {
				child = newNode(part)
				current.children[part] = child
				current.childOrder = append(current.childOrder, part)
			}
			current = child
			if j == len(parts)-1 {
				ff := f
				current.file = &ff
			}
		}
	}

	lines := []string{fmt.Sprintf("## Project Structure (%d files)", len(files)), ""}

	var renderNode func(n *treeNode, prefix string, isLast bool, depth int)
	renderNode = func(n *treeNode, prefix string, isLast bool, depth int) {
		if maxDepth > 0 && depth > maxDepth {
			return
		}
		connector := "├── "
		childPrefix := "│   "
		if isLast {
			connector = "└── "
			childPrefix = "    "
		}
		if n.name != "" {
			line := prefix + connector + n.name
			if n.file != nil && includeMetadata {
				line += fmt.Sprintf(" (%s, %d symbols)", n.file.Language, n.file.NodeCount)
			}
			lines = append(lines, line)
		}

		children := make([]*treeNode, 0, len(n.children))
		for _, name := range n.childOrder {
			children = append(children, n.children[name])
		}
		// Directories first, then files, both alphabetically.
		sort.SliceStable(children, func(i, j int) bool {
			a, b := children[i], children[j]
			aIsDir := len(a.children) > 0 && a.file == nil
			bIsDir := len(b.children) > 0 && b.file == nil
			if aIsDir != bIsDir {
				return aIsDir
			}
			return a.name < b.name
		})
		for i, child := range children {
			nextPrefix := prefix
			if n.name != "" {
				nextPrefix = prefix + childPrefix
			}
			renderNode(child, nextPrefix, i == len(children)-1, depth+1)
		}
	}
	renderNode(root, "", true, 0)

	return strings.Join(lines, "\n")
}
