package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
	"github.com/spf13/cobra"
)

// StackTraceResult maps runtime frames to the current, freshly-indexed source.
// It intentionally does not infer edges between frames: a stack is runtime
// evidence and may cross reflection, generated code, or framework callbacks.
type StackTraceResult struct {
	Status    string            `json:"status"`
	Freshness NodeFreshness     `json:"freshness"`
	Revision  string            `json:"revision,omitempty"`
	Frames    []StackTraceFrame `json:"frames"`
}

type StackTraceFrame struct {
	Index      int           `json:"index"`
	Raw        string        `json:"raw"`
	Function   string        `json:"function,omitempty"`
	FilePath   string        `json:"filePath,omitempty"`
	Line       int           `json:"line,omitempty"`
	Column     int           `json:"column,omitempty"`
	Status     string        `json:"status"`
	Hint       string        `json:"hint,omitempty"`
	Symbol     *BriefSymbol  `json:"symbol,omitempty"`
	Candidates []BriefSymbol `json:"candidates,omitempty"`
	Source     string        `json:"source,omitempty"`
}

type parsedStackFrame struct {
	index    int
	raw      string
	function string
	file     string
	line     int
	column   int
}

var (
	goFunctionRE = regexp.MustCompile(`^\s*([\w./~*()\-]+(?:\.[\w~*()\-]+)+)\([^\n]*\)$`)
	goLocationRE = regexp.MustCompile(`^\s*(.+\.(?:go|s)):(\d+)(?::(\d+))?(?:\s|$)`)
	v8RE         = regexp.MustCompile(`^\s*at\s+(?:(.*?)\s+\()?(.+?):(\d+)(?::(\d+))?\)?\s*$`)
	pythonRE     = regexp.MustCompile(`^\s*File\s+"(.+?)",\s+line\s+(\d+)(?:,\s+in\s+(.+))?`)
	dotnetRE     = regexp.MustCompile(`^\s*at\s+(.+?)(?:\([^)]*\))?\s+in\s+(.+?):line\s+(\d+)\s*$`)
	javaRE       = regexp.MustCompile(`^\s*at\s+(.+?)\(([^():]+):(\d+)\)\s*$`)
	rustRE       = regexp.MustCompile(`^\s*\d+:\s*(.*?)\s+at\s+(.+?):(\d+)(?::(\d+))?\s*$`)
	locationRE   = regexp.MustCompile(`(?:(?:\(|\[|\s)|^)([^\s()\[\]]+\.[A-Za-z0-9_+\-]+):(\d+)(?::(\d+))?`)
)

func newStacktraceCmd() *cobra.Command {
	var jsonOut bool
	var format, sourceMode, pathFlag, scope string
	cmd := &cobra.Command{
		Use:   "stacktrace [trace-file|-]",
		Short: "Map runtime stack frames to indexed symbols",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sourceMode != "" && sourceMode != "inline" && sourceMode != "footer" {
				return fmt.Errorf("--source must be inline or footer")
			}
			if sourceMode == "footer" && wantsJSON(format, jsonOut) {
				return errors.New("--source=footer is text-only; use --source=inline with --format json")
			}
			input, err := stacktraceInput(cmd.InOrStdin(), args)
			if err != nil {
				return err
			}
			root, err := nodeProjectPath(pathFlag)
			if err != nil {
				return err
			}
			idx, err := indexer.Open(root, indexer.Options{})
			if err != nil {
				return fmt.Errorf("open index: %w", err)
			}
			defer func() { _ = idx.Close() }()
			fresh, err := refreshNodeIndex(idx)
			if err != nil {
				return err
			}
			result, err := mapStacktrace(idx, splitCSV(scope), input, sourceMode != "", fresh)
			if err != nil {
				return err
			}
			if wantsJSON(format, jsonOut) {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			if sourceMode == "footer" {
				return printStacktraceFooter(cmd.OutOrStdout(), result)
			}
			return printStacktraceMarkdown(cmd.OutOrStdout(), result, sourceMode == "inline")
		},
	}
	addJSONOutputFlags(cmd, &format, &jsonOut)
	cmd.Flags().StringVar(&sourceMode, "source", "", "Include symbol source: footer (default when set) or inline")
	cmd.Flags().Lookup("source").NoOptDefVal = "footer"
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().StringVar(&scope, "scope", "", "Comma-separated scope keys to query (default: all scopes)")
	return cmd
}

func stacktraceInput(in io.Reader, args []string) (string, error) {
	if len(args) == 1 && args[0] != "-" {
		data, err := os.ReadFile(args[0])
		return string(data), err
	}
	data, err := io.ReadAll(in)
	return string(data), err
}

// parseStacktrace supports common location-bearing formats. It keeps the raw
// frame text so consumers can still reason about external/unmatched frames.
func parseStacktrace(input string) []parsedStackFrame {
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	out := make([]parsedStackFrame, 0)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if m := goFunctionRE.FindStringSubmatch(line); len(m) > 0 && i+1 < len(lines) {
			if loc := goLocationRE.FindStringSubmatch(lines[i+1]); len(loc) > 0 {
				out = append(out, makeParsedFrame(len(out), line+"\n"+lines[i+1], m[1], loc[1], loc[2], loc[3]))
				i++
				continue
			}
		}
		if m := pythonRE.FindStringSubmatch(line); len(m) > 0 {
			out = append(out, makeParsedFrame(len(out), line, m[3], m[1], m[2], ""))
			continue
		}
		if m := dotnetRE.FindStringSubmatch(line); len(m) > 0 {
			out = append(out, makeParsedFrame(len(out), line, m[1], m[2], m[3], ""))
			continue
		}
		if m := rustRE.FindStringSubmatch(line); len(m) > 0 {
			out = append(out, makeParsedFrame(len(out), line, m[1], m[2], m[3], m[4]))
			continue
		}
		if m := v8RE.FindStringSubmatch(line); len(m) > 0 {
			out = append(out, makeParsedFrame(len(out), line, strings.TrimSpace(m[1]), m[2], m[3], m[4]))
			continue
		}
		if m := javaRE.FindStringSubmatch(line); len(m) > 0 {
			out = append(out, makeParsedFrame(len(out), line, m[1], m[2], m[3], ""))
			continue
		}
		if m := locationRE.FindStringSubmatch(line); len(m) > 0 {
			out = append(out, makeParsedFrame(len(out), line, "", m[1], m[2], m[3]))
		}
	}
	return out
}

func makeParsedFrame(index int, raw, function, file, line, column string) parsedStackFrame {
	l, _ := strconv.Atoi(line)
	c, _ := strconv.Atoi(column)
	return parsedStackFrame{index: index, raw: raw, function: strings.TrimSpace(function), file: filepath.ToSlash(file), line: l, column: c}
}

func mapStacktrace(idx *indexer.Indexer, scopes []string, input string, wantSource bool, freshness NodeFreshness) (StackTraceResult, error) {
	result := StackTraceResult{Status: "ok", Freshness: freshness, Frames: []StackTraceFrame{}}
	stores := idx.StoresFiltered(scopes)
	for _, s := range stores {
		if revision, _ := s.GetMetadata("indexed_git_head"); revision != "" {
			result.Revision = revision
			break
		}
	}
	sources := map[string]string{}
	for _, parsed := range parseStacktrace(input) {
		frame := StackTraceFrame{Index: parsed.index, Raw: parsed.raw, Function: parsed.function, FilePath: parsed.file, Line: parsed.line, Column: parsed.column, Status: "unmatched", Candidates: []BriefSymbol{}}
		matches, err := matchStackFrame(stores, parsed)
		if err != nil {
			return result, err
		}
		if len(matches) == 0 {
			if parsed.file == "" {
				frame.Hint = "No source location was recognized in this frame."
			} else {
				frame.Hint = "No unique indexed callable contains this source location."
			}
			result.Frames = append(result.Frames, frame)
			continue
		}
		if len(matches) > 1 {
			frame.Status, frame.Hint, frame.Candidates = "ambiguous", "Multiple indexed callables match this frame; provide a trace with a more specific project path.", briefMatches(matches)
			result.Frames = append(result.Frames, frame)
			continue
		}
		brief := briefNode(matches[0].node)
		frame.Status, frame.Symbol = "matched", &brief
		if wantSource {
			if source, ok := sources[brief.ID]; ok {
				frame.Source = source
			} else {
				n, err := resolveNode(idx, scopes, brief.ID, "", 0, true, false, 0, result.Freshness)
				if err != nil {
					return result, err
				}
				frame.Source = n.Source
				sources[brief.ID] = n.Source
				result.Freshness = n.Freshness
			}
		}
		result.Frames = append(result.Frames, frame)
	}
	return result, nil
}

func matchStackFrame(stores []*store.Store, frame parsedStackFrame) ([]matchedNode, error) {
	if frame.file == "" {
		if frame.function == "" {
			return nil, nil
		}
		return matchStackFunction(stores, frame.function)
	}
	paths, err := indexedPathMatches(stores, frame.file)
	if err != nil {
		return nil, err
	}
	if len(paths) != 1 {
		return nil, nil
	} // never guess an ambiguous basename.
	var matches []matchedNode
	for _, s := range stores {
		nodes, err := s.GetNodesByFile(paths[0])
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			if callableNode(n) && n.StartLine <= frame.line && n.EndLine >= frame.line {
				matches = append(matches, matchedNode{node: n, store: s})
			}
		}
	}
	if len(matches) == 0 && frame.function != "" {
		return matchStackFunction(stores, frame.function)
	}
	// A nested callable has the smallest range. Ties stay ambiguous.
	sort.SliceStable(matches, func(i, j int) bool {
		ai, aj := matches[i].node.EndLine-matches[i].node.StartLine, matches[j].node.EndLine-matches[j].node.StartLine
		return ai < aj
	})
	if len(matches) > 1 && (matches[0].node.EndLine-matches[0].node.StartLine) == (matches[1].node.EndLine-matches[1].node.StartLine) {
		return matches, nil
	}
	return matches[:1], nil
}

func indexedPathMatches(stores []*store.Store, stackPath string) ([]string, error) {
	want := strings.ToLower(strings.TrimPrefix(filepath.ToSlash(stackPath), "./"))
	set := map[string]bool{}
	for _, s := range stores {
		nodes, err := s.AllNodes()
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			path := strings.ToLower(n.FilePath)
			if path == want || strings.HasSuffix(want, "/"+path) || strings.HasSuffix(path, "/"+want) {
				set[n.FilePath] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for path := range set {
		out = append(out, path)
	}
	sort.Strings(out)
	return out, nil
}

func matchStackFunction(stores []*store.Store, name string) ([]matchedNode, error) {
	name = strings.TrimSpace(name)
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	if colon := strings.LastIndex(name, "::"); colon >= 0 {
		name = name[colon+2:]
	}
	matches, err := findNodeMatches(stores, name)
	if err != nil {
		return nil, err
	}
	out := matches[:0]
	for _, m := range matches {
		if callableNode(m.node) {
			out = append(out, m)
		}
	}
	return out, nil
}

func callableNode(n model.Node) bool {
	return n.Kind == model.KindFunction || n.Kind == model.KindMethod || n.Kind == model.KindComponent
}

func printStacktraceMarkdown(w io.Writer, result StackTraceResult, inline bool) error {
	if _, err := fmt.Fprintf(w, "## Runtime stack: %d frame(s)\n", len(result.Frames)); err != nil {
		return err
	}
	if result.Revision != "" {
		if _, err := fmt.Fprintf(w, "- Indexed revision: `%s`\n", result.Revision); err != nil {
			return err
		}
	}
	for _, frame := range result.Frames {
		if frame.Symbol == nil {
			if _, err := fmt.Fprintf(w, "\n#%d %s — %s\n", frame.Index, frame.Status, frame.Hint); err != nil {
				return err
			}
			continue
		}
		s := *frame.Symbol
		if _, err := fmt.Fprintf(w, "\n#%d `%s` (%s) — %s:%d\n", frame.Index, s.QualifiedName, s.Kind, s.FilePath, s.StartLine); err != nil {
			return err
		}
		if s.Signature != "" {
			if _, err := fmt.Fprintf(w, "`%s`\n", s.Signature); err != nil {
				return err
			}
		}
		if inline {
			if err := printStackFrameSource(w, frame); err != nil {
				return err
			}
		}
	}
	return nil
}

func printStackFrameSource(w io.Writer, frame StackTraceFrame) error {
	if frame.Source == "" || frame.Symbol == nil {
		return nil
	}
	fence := codeFence(frame.Source)
	_, err := fmt.Fprintf(w, "\n%s%s\n%s\n%s\n", fence, frame.Symbol.Language, frame.Source, fence)
	return err
}

func printStacktraceFooter(w io.Writer, result StackTraceResult) error {
	metadata := result
	metadata.Frames = append([]StackTraceFrame(nil), result.Frames...)
	for i := range metadata.Frames {
		metadata.Frames[i].Source = ""
	}
	b, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "```json\n%s\n```\n\n## Sources\n", b); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, frame := range result.Frames {
		if frame.Symbol != nil && !seen[frame.Symbol.ID] {
			seen[frame.Symbol.ID] = true
			if err := printStackFrameSource(w, frame); err != nil {
				return err
			}
		}
	}
	return nil
}
