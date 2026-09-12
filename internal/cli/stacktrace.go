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
	Hint      string            `json:"hint,omitempty"`
	Freshness NodeFreshness     `json:"freshness"`
	Revision  string            `json:"revision,omitempty"`
	MaxFrames int               `json:"maxFrames"`
	Truncated bool              `json:"truncated,omitempty"`
	Frames    []StackTraceFrame `json:"frames"`
}

type StackTraceFrame struct {
	Index          int           `json:"index"`
	Raw            string        `json:"raw"`
	Function       string        `json:"function,omitempty"`
	FilePath       string        `json:"filePath,omitempty"`
	Line           int           `json:"line,omitempty"`
	Column         int           `json:"column,omitempty"`
	Status         string        `json:"status"`
	Hint           string        `json:"hint,omitempty"`
	Symbol         *BriefSymbol  `json:"symbol,omitempty"`
	Candidates     []BriefSymbol `json:"candidates,omitempty"`
	PathCandidates []string      `json:"pathCandidates,omitempty"`
	Source         string        `json:"source,omitempty"`
}

const (
	defaultMaxStacktraceBytes  int64 = 1 << 20
	defaultMaxStacktraceFrames       = 256
)

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
	rustHeadRE   = regexp.MustCompile(`^\s*\d+:\s*(.+?)\s*$`)
	rustAtRE     = regexp.MustCompile(`^\s*at\s+(.+?):(\d+)(?::(\d+))?\s*$`)
	locationRE   = regexp.MustCompile(`(?:(?:\(|\[|\s)|^)([^\s()\[\]]+\.[A-Za-z0-9_+\-]+):(\d+)(?::(\d+))?`)
)

func newStacktraceCmd() *cobra.Command {
	var jsonOut bool
	var format, sourceMode, pathFlag, scope, expectedRevision string
	var maxBytes int64
	var maxFrames int
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
			input, err := stacktraceInputLimited(cmd.InOrStdin(), args, maxBytes)
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
			result, err := mapStacktraceWithLimit(idx, splitCSV(scope), input, maxFrames, sourceMode != "", fresh)
			if err != nil {
				return err
			}
			if expectedRevision != "" && result.Revision != expectedRevision {
				return fmt.Errorf("stack trace requires indexed revision %s, current index revision is %s", expectedRevision, result.Revision)
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
	cmd.Flags().Int64Var(&maxBytes, "max-bytes", defaultMaxStacktraceBytes, "Maximum stack trace input bytes")
	cmd.Flags().IntVar(&maxFrames, "max-frames", defaultMaxStacktraceFrames, "Maximum recognizable stack frames to map")
	cmd.Flags().StringVar(&expectedRevision, "revision", "", "Require this indexed Git revision before mapping")
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().StringVar(&scope, "scope", "", "Comma-separated scope keys to query (default: all scopes)")
	return cmd
}

func stacktraceInput(in io.Reader, args []string) (string, error) {
	return stacktraceInputLimited(in, args, defaultMaxStacktraceBytes)
}

func stacktraceInputLimited(in io.Reader, args []string, maxBytes int64) (string, error) {
	if maxBytes < 1 {
		return "", errors.New("--max-bytes must be positive")
	}
	var reader io.Reader = in
	if len(args) == 1 && args[0] != "-" {
		file, err := os.Open(args[0])
		if err != nil {
			return "", err
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes {
		return "", fmt.Errorf("stack trace exceeds --max-bytes (%d)", maxBytes)
	}
	return string(data), nil
}

// parseStacktrace supports common location-bearing formats. It keeps the raw
// frame text so consumers can still reason about external/unmatched frames.
func parseStacktrace(input string) []parsedStackFrame {
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	out := make([]parsedStackFrame, 0)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		// Rust's standard backtrace is two lines: "N: function" followed by
		// "at file:line". Preserve that order as one runtime frame.
		if m := rustHeadRE.FindStringSubmatch(line); len(m) > 0 && i+1 < len(lines) {
			if loc := rustAtRE.FindStringSubmatch(lines[i+1]); len(loc) > 0 {
				out = append(out, makeParsedFrame(len(out), line+"\n"+lines[i+1], m[1], loc[1], loc[2], loc[3]))
				i++
				continue
			}
		}
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
		// JVM frames otherwise look enough like V8 to be consumed by the
		// permissive V8 expression, so Java must be tested first.
		if m := javaRE.FindStringSubmatch(line); len(m) > 0 {
			out = append(out, makeParsedFrame(len(out), line, m[1], m[2], m[3], ""))
			continue
		}
		if m := v8RE.FindStringSubmatch(line); len(m) > 0 {
			out = append(out, makeParsedFrame(len(out), line, strings.TrimSpace(m[1]), m[2], m[3], m[4]))
			continue
		}
		if m := locationRE.FindStringSubmatch(line); len(m) > 0 {
			out = append(out, makeParsedFrame(len(out), line, "", m[1], m[2], m[3]))
			continue
		}
		// Keep recognizable native/unknown frames so the output never hides a
		// runtime boundary merely because it has no project source location.
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "at ") || strings.Contains(strings.ToLower(trimmed), "native") {
			out = append(out, parsedStackFrame{index: len(out), raw: line, function: strings.TrimSpace(strings.TrimPrefix(trimmed, "at "))})
		}
	}
	return out
}

func makeParsedFrame(index int, raw, function, file, line, column string) parsedStackFrame {
	l, _ := strconv.Atoi(line)
	c, _ := strconv.Atoi(column)
	file = strings.ReplaceAll(file, "\\", "/")
	return parsedStackFrame{index: index, raw: raw, function: strings.TrimSpace(function), file: filepath.ToSlash(file), line: l, column: c}
}

func mapStacktrace(idx *indexer.Indexer, scopes []string, input string, wantSource bool, freshness NodeFreshness) (StackTraceResult, error) {
	return mapStacktraceWithLimit(idx, scopes, input, defaultMaxStacktraceFrames, wantSource, freshness)
}

func mapStacktraceWithLimit(idx *indexer.Indexer, scopes []string, input string, maxFrames int, wantSource bool, freshness NodeFreshness) (StackTraceResult, error) {
	if maxFrames < 1 {
		return StackTraceResult{}, errors.New("--max-frames must be positive")
	}
	result := StackTraceResult{Status: "ok", Freshness: freshness, MaxFrames: maxFrames, Frames: []StackTraceFrame{}}
	stores := idx.StoresFiltered(scopes)
	for _, s := range stores {
		if revision, _ := s.GetMetadata("indexed_git_head"); revision != "" {
			result.Revision = revision
			break
		}
	}
	resolver, err := newStackResolver(stores)
	if err != nil {
		return result, err
	}
	sources := map[string]string{}
	parsedFrames := parseStacktrace(input)
	if len(parsedFrames) == 0 {
		result.Status, result.Hint = "no_frames", "No recognizable stack frames were found in the supplied input."
		return result, nil
	}
	if len(parsedFrames) > maxFrames {
		parsedFrames, result.Truncated = parsedFrames[:maxFrames], true
	}
	for _, parsed := range parsedFrames {
		frame := StackTraceFrame{Index: parsed.index, Raw: parsed.raw, Function: parsed.function, FilePath: parsed.file, Line: parsed.line, Column: parsed.column, Status: "unmatched", Candidates: []BriefSymbol{}}
		selection, err := resolver.resolve(parsed)
		if err != nil {
			return result, err
		}
		frame.Status, frame.Hint, frame.Candidates, frame.PathCandidates = selection.status, selection.hint, briefMatches(selection.candidates), selection.paths
		if selection.match == nil {
			result.Frames = append(result.Frames, frame)
			continue
		}
		brief := briefNode(selection.match.node)
		frame.Symbol = &brief
		if wantSource && (frame.Status == "exact" || frame.Status == "range" || frame.Status == "name_only") {
			if source, ok := sources[brief.ID]; ok {
				frame.Source = source
			} else {
				source, err := readVerifiedIndexedNodeSource(idx.Root(), *selection.match)
				if err != nil {
					return result, err
				}
				frame.Source, sources[brief.ID] = source, source
				result.Freshness.Verified = true
			}
		}
		result.Frames = append(result.Frames, frame)
	}
	return result, nil
}

type stackSelection struct {
	status, hint string
	match        *matchedNode
	candidates   []matchedNode
	paths        []string
}

// stackResolver scans compact file records once per command and loads a file's
// symbols at most once. It deliberately does not scan all nodes for every
// frame, which matters for production traces with hundreds of frames.
type stackResolver struct {
	stores []*store.Store
	files  []string
	nodes  map[string][]matchedNode
	memo   map[string]stackSelection
}

func newStackResolver(stores []*store.Store) (*stackResolver, error) {
	r := &stackResolver{stores: stores, nodes: map[string][]matchedNode{}, memo: map[string]stackSelection{}}
	paths := map[string]bool{}
	for _, s := range stores {
		files, err := s.GetAllFiles()
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			paths[f.Path] = true
		}
	}
	for path := range paths {
		r.files = append(r.files, path)
	}
	sort.Strings(r.files)
	return r, nil
}

func (r *stackResolver) pathMatches(stackPath string) []string {
	want := strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(filepath.ToSlash(stackPath), "\\", "/"), "./"))
	var out []string
	for _, path := range r.files {
		candidate := strings.ToLower(path)
		if candidate == want || strings.HasSuffix(want, "/"+candidate) || strings.HasSuffix(candidate, "/"+want) {
			out = append(out, path)
		}
	}
	return out
}

func (r *stackResolver) nodesInFile(path string) ([]matchedNode, error) {
	if nodes, ok := r.nodes[path]; ok {
		return nodes, nil
	}
	var out []matchedNode
	for _, s := range r.stores {
		nodes, err := s.GetNodesByFile(path)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			if callableNode(n) {
				out = append(out, matchedNode{node: n, store: s})
			}
		}
	}
	r.nodes[path] = out
	return out, nil
}

func (r *stackResolver) resolve(frame parsedStackFrame) (stackSelection, error) {
	key := strings.Join([]string{frame.file, strconv.Itoa(frame.line), strconv.Itoa(frame.column), frame.function}, "\x00")
	if selection, ok := r.memo[key]; ok {
		return selection, nil
	}
	selection, err := r.resolveUncached(frame)
	if err == nil {
		r.memo[key] = selection
	}
	return selection, err
}

func (r *stackResolver) resolveUncached(frame parsedStackFrame) (stackSelection, error) {
	if frame.file == "" {
		if frame.function == "" {
			return stackSelection{status: "external", hint: "Runtime frame has no project source location."}, nil
		}
		matches, err := matchStackFunction(r.stores, frame.function)
		if err != nil {
			return stackSelection{}, err
		}
		if len(matches) == 1 {
			return stackSelection{status: "name_only", hint: "Matched by runtime function name only; no source location was present.", match: &matches[0]}, nil
		}
		if len(matches) > 1 {
			return stackSelection{status: "ambiguous", hint: "Runtime function name matches multiple indexed callables.", candidates: matches}, nil
		}
		return stackSelection{status: "unmatched", hint: "No indexed callable matches this runtime function name."}, nil
	}
	paths := r.pathMatches(frame.file)
	if len(paths) != 1 {
		if len(paths) > 1 {
			return stackSelection{status: "ambiguous", hint: "Stack file path matches multiple indexed files; no basename was guessed.", paths: paths}, nil
		}
		return stackSelection{status: "external", hint: "Stack file is not indexed in this project."}, nil
	}
	nodes, err := r.nodesInFile(paths[0])
	if err != nil {
		return stackSelection{}, err
	}
	var ranges []matchedNode
	for _, n := range nodes {
		if frame.line > 0 && n.node.StartLine <= frame.line && n.node.EndLine >= frame.line {
			ranges = append(ranges, n)
		}
	}
	sort.SliceStable(ranges, func(i, j int) bool { return nodeSpan(ranges[i].node) < nodeSpan(ranges[j].node) })
	if len(ranges) > 0 {
		if len(ranges) > 1 && nodeSpan(ranges[0].node) == nodeSpan(ranges[1].node) {
			return stackSelection{status: "ambiguous", hint: "Multiple equally specific callables contain this source location.", candidates: ranges}, nil
		}
		selected := ranges[0]
		if runtimeName := stackFunctionName(frame.function); runtimeName != "" && !strings.EqualFold(runtimeName, selected.node.Name) {
			return stackSelection{status: "mismatch", hint: "Runtime function name contradicts the callable at this source location.", match: &selected, candidates: []matchedNode{selected}}, nil
		}
		status := "range"
		hint := "Matched by indexed source range."
		if frame.function != "" {
			status, hint = "exact", "Matched by runtime name and indexed source range."
		}
		return stackSelection{status: status, hint: hint, match: &selected}, nil
	}
	if name := stackFunctionName(frame.function); name != "" {
		var named []matchedNode
		for _, n := range nodes {
			if strings.EqualFold(n.node.Name, name) {
				named = append(named, n)
			}
		}
		if len(named) == 1 {
			return stackSelection{status: "stale", hint: "Runtime function name exists in the resolved file but the trace line is outside its indexed range; source is withheld.", match: &named[0]}, nil
		}
		if len(named) > 1 {
			return stackSelection{status: "ambiguous", hint: "Runtime function name has multiple callables in the resolved file.", candidates: named}, nil
		}
	}
	return stackSelection{status: "unmatched", hint: "No indexed callable contains this source line in the resolved file."}, nil
}

func nodeSpan(n model.Node) int { return n.EndLine - n.StartLine }

func matchStackFunction(stores []*store.Store, name string) ([]matchedNode, error) {
	name = stackFunctionName(name)
	if name == "" {
		return nil, nil
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

func stackFunctionName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, "(...)")
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	if colon := strings.LastIndex(name, "::"); colon >= 0 {
		name = name[colon+2:]
	}
	return strings.Trim(name, "() ")
}

func callableNode(n model.Node) bool {
	return n.Kind == model.KindFunction || n.Kind == model.KindMethod || n.Kind == model.KindComponent
}

func printStacktraceMarkdown(w io.Writer, result StackTraceResult, inline bool) error {
	if result.Status != "ok" {
		_, err := fmt.Fprintf(w, "## Runtime stack: %s\n\n%s\n", result.Status, result.Hint)
		return err
	}
	if _, err := fmt.Fprintf(w, "## Runtime stack: %d frame(s)\n", len(result.Frames)); err != nil {
		return err
	}
	if result.Revision != "" {
		if _, err := fmt.Fprintf(w, "- Indexed revision: `%s`\n", result.Revision); err != nil {
			return err
		}
	}
	for _, frame := range result.Frames {
		if frame.Symbol == nil || (frame.Status != "exact" && frame.Status != "range" && frame.Status != "name_only") {
			if err := printUnmatchedStackFrame(w, frame); err != nil {
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

func printUnmatchedStackFrame(w io.Writer, frame StackTraceFrame) error {
	location := frame.FilePath
	if frame.Line > 0 {
		location = fmt.Sprintf("%s:%d", location, frame.Line)
	}
	if _, err := fmt.Fprintf(w, "\n#%d %s", frame.Index, frame.Status); err != nil {
		return err
	}
	if frame.Function != "" {
		if _, err := fmt.Fprintf(w, " `%s`", frame.Function); err != nil {
			return err
		}
	}
	if location != "" {
		if _, err := fmt.Fprintf(w, " — %s", location); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", frame.Hint); err != nil {
		return err
	}
	if frame.Raw != "" {
		if _, err := fmt.Fprintf(w, "- Raw: `%s`\n", strings.ReplaceAll(frame.Raw, "\n", " | ")); err != nil {
			return err
		}
	}
	if frame.Symbol != nil {
		if _, err := fmt.Fprintf(w, "- Indexed callable: `%s` — %s:%d\n", frame.Symbol.QualifiedName, frame.Symbol.FilePath, frame.Symbol.StartLine); err != nil {
			return err
		}
	}
	for _, path := range frame.PathCandidates {
		if _, err := fmt.Fprintf(w, "- Candidate file: `%s`\n", path); err != nil {
			return err
		}
	}
	for _, candidate := range frame.Candidates {
		if _, err := fmt.Fprintf(w, "- Candidate: `%s` — %s:%d\n", candidate.ID, candidate.FilePath, candidate.StartLine); err != nil {
			return err
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
