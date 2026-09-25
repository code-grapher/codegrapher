package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	covpkg "github.com/specscore/codegrapher/coverage"
	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
	"github.com/spf13/cobra"
)

// -----------------------------------------------------------------------
// context — one bounded, budget-capped bundle of everything a test writer
// needs about a set of symbols, built on top of node/callees/coverage's own
// queries (findNodeMatches, GetOutgoingEdges/GetIncomingEdges, readVerified
// IndexedNodeSource, FileCoverageFromStore) rather than duplicating them.
// -----------------------------------------------------------------------

// typeDeclKinds are the node kinds counted as "a type declaration" for
// section b (types touched) and section e (fakes).
var typeDeclKinds = map[model.NodeKind]bool{
	model.KindStruct:    true,
	model.KindInterface: true,
	model.KindClass:     true,
	model.KindTypeAlias: true,
	model.KindEnum:      true,
}

// testEntrypointRe matches Go test-entrypoint function names, which are
// excluded from the "helpers and fakes" section (e) — only non-entrypoint
// declarations in _test.go files are helpers.
var testEntrypointRe = regexp.MustCompile(`^(Test|Benchmark|Fuzz|Example)`)

// ContextResult is the JSON payload for `codegrapher context`.
type ContextResult struct {
	Requested       []string          `json:"requested"`
	Budget          int               `json:"budget"`
	Unresolved      []ContextNotFound `json:"unresolved,omitempty"`
	Sources         []ContextSource   `json:"sources,omitempty"`
	Types           []ContextType     `json:"types,omitempty"`
	Callees         []ContextCallee   `json:"callees,omitempty"`
	Tests           []ContextTestRef  `json:"tests,omitempty"`
	Helpers         []ContextType     `json:"helpers,omitempty"`
	Omitted         []string          `json:"omitted,omitempty"`
	TokensEstimated int               `json:"tokensEstimated"`
}

// ContextNotFound reports a requested symbol that did not resolve to
// exactly one node; it never blocks the other requested symbols.
type ContextNotFound struct {
	Requested  string        `json:"requested"`
	Status     string        `json:"status"` // not_found | ambiguous
	Hint       string        `json:"hint,omitempty"`
	Candidates []BriefSymbol `json:"candidates,omitempty"`
}

// ContextSource is section (a): one requested symbol's line-bounded source.
//
// ClosureOf/StartLine/EndLine are set when the requested --line fell inside
// a nested function literal (a closure with no name of its own — an
// anonymous RunE callback, a switch/loop body has none of these either, but
// those are not literals and stay unnarrowed) rather than directly in the
// resolved symbol's own statements: Source is then the literal's own
// line-bounded body, not the whole enclosing symbol, and ClosureOf names the
// enclosing function for orientation. See A1 (resolveByFileLine /
// narrowToEnclosingLiteral).
type ContextSource struct {
	Symbol    BriefSymbol `json:"symbol"`
	Source    string      `json:"source"`
	ClosureOf string      `json:"closureOf,omitempty"`
	StartLine int         `json:"startLine,omitempty"`
	EndLine   int         `json:"endLine,omitempty"`
}

// ContextType is section (b)/(e): a full type or constructor declaration.
//
// Roles are a mix of graph-verified facts (receiver, parameter/result,
// constructor — all real edges/lookups) and, for "field", a best-effort text
// scan (see fieldReadsOf/parseStructFields) that can miss or misfire on a
// shadowed receiver name, an unparsed embedded/generic field, etc.
// HeuristicRoles names the subset of Roles that came from that text scan
// rather than the graph, so a consumer can tell "the graph says so" from
// "a regex guessed" instead of trusting every role at the same confidence.
type ContextType struct {
	Roles          []string    `json:"roles"`
	HeuristicRoles []string    `json:"heuristicRoles,omitempty"` // subset of Roles that are text-scan-derived, not graph-verified
	For            string      `json:"for,omitempty"`            // constructor's target type name
	Symbol         BriefSymbol `json:"symbol"`
	Source         string      `json:"source"`
}

// ContextCallee is section (c): a direct callee, signature only.
//
// Seam is "interface" (a real graph fact: an incoming contains edge from a
// KindInterface owner) or "field"/"parameter" (a best-effort text scan over
// the caller's own source/signature — see seamKind). SeamSource makes that
// distinction explicit for consumers: "graph" for interface, "inferred" for
// field/parameter. It is set whenever Seam is non-empty.
type ContextCallee struct {
	Symbol     BriefSymbol `json:"symbol"`
	Seam       string      `json:"seam,omitempty"`       // interface | field | parameter
	SeamSource string      `json:"seamSource,omitempty"` // graph | inferred; set whenever Seam is non-empty
}

// ContextTestRef is section (d): an existing test that calls the symbol.
type ContextTestRef struct {
	Name      string `json:"name"`
	FilePath  string `json:"filePath"`
	StartLine int    `json:"startLine"`
	Hops      int    `json:"hops"` // 1 = direct, 2 = one hop
}

// contextTarget is one resolved-independently request: a symbol name (or, for
// a --symbols-file line-number entry, a "file:line" placeholder — see
// buildContextTargets) plus its own file/line disambiguation. --file/--line
// on the command line become every positional symbol's target.File/Line;
// --symbols-file lines carry their own per-target file, letting one
// invocation span several files' worth of symbols (D1) instead of the caller
// repeating the whole call, and paying for section e's helper bundle, once
// per file.
type contextTarget struct {
	Symbol string
	File   string
	Line   int
}

// buildContextTargets assembles the request list from positional symbol
// arguments (each disambiguated by the shared --file/--line) and an optional
// --symbols-file of `path<TAB>symbol-or-line` lines (D1): a line whose second
// field parses as an integer is a bare source line (the A1 closure/line
// resolver is the only way to name it), anything else is a symbol name
// scoped to that file. Blank lines and lines starting with # are skipped.
func buildContextTargets(args []string, fileHint string, line int, symbolsFilePath string) ([]contextTarget, error) {
	var targets []contextTarget
	for _, a := range args {
		targets = append(targets, contextTarget{Symbol: a, File: fileHint, Line: line})
	}
	if symbolsFilePath != "" {
		data, err := os.ReadFile(symbolsFilePath)
		if err != nil {
			return nil, fmt.Errorf("read --symbols-file: %w", err)
		}
		for i, raw := range strings.Split(string(data), "\n") {
			raw = strings.TrimRight(raw, "\r")
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			parts := strings.SplitN(raw, "\t", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("--symbols-file line %d: want file<TAB>symbol-or-line, got %q", i+1, raw)
			}
			file := strings.TrimSpace(parts[0])
			rest := strings.TrimSpace(parts[1])
			t := contextTarget{File: file}
			if n, err := strconv.Atoi(rest); err == nil {
				t.Line = n
				t.Symbol = fmt.Sprintf("%s:%d", file, n)
			} else {
				t.Symbol = rest
			}
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("no symbols requested: pass symbol arguments or --symbols-file")
	}
	return targets, nil
}

func newContextCmd() *cobra.Command {
	var jsonOut, uncovered bool
	var format, fileHint, scopeFlag, forFlag, symbolsFile string
	var line, budget, testHops, helperBodies int
	var pathFlag string

	cmd := &cobra.Command{
		Use:   "context [symbol...]",
		Short: "Bounded test-writing context bundle for a set of symbols",
		Long: `Assemble everything a test writer needs about a set of symbols in one
bounded, budget-capped read: for each requested symbol, in order —
  a. its line-bounded source (--uncovered marks lines an ingested coverage
     profile reports as missed). --file/--line also resolve a symbol with no
     name of its own — an anonymous closure, a switch/loop body — to its
     innermost enclosing function or function literal;
  b. full declarations of the types it touches (receiver, parameter, result,
     and field types) plus constructors of its receiver type;
  c. its direct callees as signatures only, flagged when they are a seam. A
     requested function whose body is a single (optionally return'd) call is
     a thin wrapper: its callee's full source is surfaced too, as if it had
     been requested;
  d. existing tests that already call it, directly by default (--test-hops 2
     also includes one intermediate hop);
  e. with --for test, the package's own test helpers/fakes (ranked: helpers
     referenced by section d's tests first, then by name similarity to the
     requested symbols; --helper-bodies N, default 3, includes the top N
     ranked helpers' full source, not just their signature) and exported
     symbols of sibling *test/*fake* packages its tests import.
Deduplicated across every requested symbol, and section e once per package
even when several requested symbols/files share it. --symbols-file reads
file<TAB>symbol-or-line lines so one call can span several files.
--budget (default 20000, ~4 chars/token) fills sections in order and ends
with an omitted list naming what did not fit, instead of truncating
mid-item.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if forFlag != "" && forFlag != "test" {
				return errors.New(`--for must be "test"`)
			}
			if budget <= 0 {
				budget = 20000
			}
			if testHops <= 0 {
				testHops = 1
			}
			targets, err := buildContextTargets(args, fileHint, line, symbolsFile)
			if err != nil {
				return err
			}
			projectPath := ""
			startPath := ""
			if pathFlag != "" {
				startPath = resolveExactArg([]string{pathFlag})
				projectPath = resolveArg([]string{pathFlag})
			} else {
				cwd, _ := os.Getwd()
				startPath = cwd
				projectPath = findNearestOrReturn(cwd)
			}
			if mismatch := indexer.DetectWorktreeIndexMismatch(startPath, projectPath); mismatch != nil {
				return fmt.Errorf("cannot read a different git worktree's index:\n%s", indexer.WorktreeMismatchWarning(*mismatch))
			}
			if !indexer.IsInitialized(projectPath) {
				return fmt.Errorf("CodeGraph not initialized in %s", projectPath)
			}
			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				return fmt.Errorf("open index: %w", err)
			}
			defer func() { _ = idx.Close() }()

			if _, err := idx.RefreshForRead(indexer.Options{}); err != nil {
				return err
			}

			result, err := buildContext(idx, targets, contextOptions{
				scopes:       splitCSV(scopeFlag),
				forTest:      forFlag == "test",
				uncovered:    uncovered,
				budget:       budget,
				testHops:     testHops,
				helperBodies: helperBodies,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if wantsJSON(format, jsonOut) {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}
			return printContextMarkdown(out, result)
		},
	}

	addJSONOutputFlags(cmd, &format, &jsonOut)
	cmd.Flags().StringVar(&fileHint, "file", "", "Disambiguate every requested symbol by indexed file path")
	cmd.Flags().IntVar(&line, "line", 0, "Disambiguate every requested symbol by source line; also resolves an unnamed closure/block")
	cmd.Flags().StringVar(&forFlag, "for", "", `Widen the bundle for a purpose: "test" adds package test helpers/fakes`)
	cmd.Flags().BoolVar(&uncovered, "uncovered", false, "Mark source lines the ingested coverage profile reports as missed")
	cmd.Flags().IntVar(&budget, "budget", 20000, "Approximate output budget in tokens (~4 chars/token)")
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "Comma-separated scope keys to query (default: all scopes)")
	cmd.Flags().StringVar(&symbolsFile, "symbols-file", "", "Read additional file<TAB>symbol-or-line targets from this file, one per line")
	cmd.Flags().IntVar(&testHops, "test-hops", 1, "Existing-test hops to include in section d (1 = direct callers only, 2 = one intermediate hop too)")
	cmd.Flags().IntVar(&helperBodies, "helper-bodies", 3, "With --for test, include full source for the top N ranked helpers (0 disables bodies)")
	return cmd
}

type contextOptions struct {
	scopes       []string
	forTest      bool
	uncovered    bool
	budget       int
	testHops     int
	helperBodies int
}

// contextItem is one budget-fillable unit of output: a pre-rendered markdown
// chunk (used both to render text output and to estimate token cost) plus a
// short label used in the omitted list.
type contextItem struct {
	section string // a | b | c | d | e
	label   string
	text    string
}

func buildContext(idx *indexer.Indexer, targets []contextTarget, opts contextOptions) (*ContextResult, error) {
	requested := make([]string, len(targets))
	for i, t := range targets {
		requested[i] = t.Symbol
	}
	result := &ContextResult{Requested: requested, Budget: opts.budget}

	stores := idx.StoresFiltered(opts.scopes)

	var matches []matchedNode
	seenNode := map[string]bool{}
	targetLine := map[string]int{} // node ID -> requested line, for A1 closure narrowing in section a
	for _, t := range targets {
		found, err := findNodeMatches(stores, t.Symbol)
		if err != nil {
			return nil, err
		}
		found = narrowNodeMatches(found, t.File, t.Line)
		if len(found) == 0 && t.File != "" && t.Line > 0 {
			// A1: the requested "name" may be a placeholder for a symbol
			// with no name of its own (an anonymous closure, a switch/loop
			// body) — resolve directly to the innermost enclosing named
			// function/method at file:line instead of failing outright.
			lm, err := resolveByFileLine(stores, t.File, t.Line)
			if err != nil {
				return nil, err
			}
			if lm != nil {
				found = []matchedNode{*lm}
			}
		}
		if len(found) == 0 {
			result.Unresolved = append(result.Unresolved, ContextNotFound{
				Requested: t.Symbol, Status: "not_found",
				Hint: "Use query --brief to discover indexed symbols, or pass --file/--line to resolve an unnamed closure or block.",
			})
			continue
		}
		if len(found) > 1 {
			result.Unresolved = append(result.Unresolved, ContextNotFound{
				Requested: t.Symbol, Status: "ambiguous",
				Hint:       "Retry context with an exact candidate id, --file, or --line.",
				Candidates: briefMatches(found),
			})
			continue
		}
		m := found[0]
		if t.Line > 0 {
			targetLine[m.node.ID] = t.Line
		}
		if seenNode[m.node.ID] {
			continue
		}
		seenNode[m.node.ID] = true
		matches = append(matches, m)
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].node.FilePath != matches[j].node.FilePath {
			return matches[i].node.FilePath < matches[j].node.FilePath
		}
		return matches[i].node.StartLine < matches[j].node.StartLine
	})

	// Requested symbol names, captured before wrapper expansion (below) adds
	// surfaced callees, are the anchor section e's tier-2 similarity ranking
	// (B1) scores helpers against.
	requestedNames := make([]string, 0, len(matches))
	for _, m := range matches {
		requestedNames = append(requestedNames, m.node.Name)
	}

	var items []contextItem
	sourceCache := map[string]string{} // node ID -> verified full source
	root := idx.Root()

	// (a) source. A requested function whose body is a single (optionally
	// return'd) call — a thin wrapper — also surfaces its callee's full
	// source here, as if that callee had been requested too (E1): the loop
	// bound is re-read on each iteration so appended callees are processed.
	processed := map[string]bool{}
	for i := 0; i < len(matches); i++ {
		m := matches[i]
		if processed[m.node.ID] {
			continue
		}
		processed[m.node.ID] = true

		src, err := readVerifiedIndexedNodeSource(root, m)
		if err != nil {
			return nil, err
		}
		sourceCache[m.node.ID] = src

		rendered := src
		displayStart, displayEnd := m.node.StartLine, m.node.EndLine
		closureOf := ""
		if ln, ok := targetLine[m.node.ID]; ok {
			if litSrc, litStart, litEnd, orientation, ok := narrowToEnclosingLiteral(root, m, ln); ok {
				rendered = litSrc
				displayStart, displayEnd = litStart, litEnd
				closureOf = orientation
			}
		}
		if opts.uncovered {
			marked, err := markUncoveredSource(m, rendered, displayStart)
			if err != nil {
				return nil, err
			}
			rendered = marked
		}
		result.Sources = append(result.Sources, ContextSource{
			Symbol: briefNode(m.node), Source: rendered,
			ClosureOf: closureOf, StartLine: displayStart, EndLine: displayEnd,
		})
		items = append(items, contextItem{
			section: "a", label: "source " + m.node.QualifiedName,
			text: renderSourceBlock(m.node.QualifiedName, m.node.FilePath, m.node.Language, displayStart, displayEnd, closureOf, rendered),
		})

		if calleeName, ok := thinWrapperCallee(src); ok {
			if callee, err := resolveSoleCallee(m, calleeName); err != nil {
				return nil, err
			} else if callee != nil && !seenNode[callee.node.ID] {
				seenNode[callee.node.ID] = true
				matches = append(matches, *callee)
			}
		}
	}

	// (b) types touched + constructors
	typeItems, typeDecls, err := buildTypeSection(matches, sourceCache, root)
	if err != nil {
		return nil, err
	}
	result.Types = typeDecls
	items = append(items, typeItems...)

	// (c) direct callees, signatures only, seam-flagged
	calleeItems, callees, err := buildCalleeSection(matches, sourceCache, root)
	if err != nil {
		return nil, err
	}
	result.Callees = callees
	items = append(items, calleeItems...)

	// (d) existing tests calling the symbol directly (or, with
	// --test-hops 2, one intermediate hop too — F1 defaults to direct only)
	testItems, tests, testMatches, err := buildTestSection(matches, opts.testHops)
	if err != nil {
		return nil, err
	}
	result.Tests = tests
	items = append(items, testItems...)

	// (e) --for test: package helpers/fakes, ranked and capped (B1/C1/D1)
	if opts.forTest {
		helperItems, helpers, err := buildHelperSection(matches, testMatches, requestedNames, opts.helperBodies, root)
		if err != nil {
			return nil, err
		}
		result.Helpers = helpers
		items = append(items, helperItems...)
	}

	included, omitted := fitBudget(items, opts.budget)
	result.TokensEstimated = included / 4
	result.Omitted = omitted
	applyBudgetCut(result, omitted)
	return result, nil
}

// renderSourceBlock is the one place section (a)'s markdown/text rendering
// happens, shared by buildContext's budget-estimation item text and
// printContextMarkdown, so the two never drift on the A1 closure-orientation
// line.
func renderSourceBlock(qualifiedName, filePath string, language model.Language, startLine, endLine int, closureOf, source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n### `%s` — %s:%d-%d\n", qualifiedName, filePath, startLine, endLine)
	if closureOf != "" {
		fmt.Fprintf(&b, "\n_(closure; enclosing %s)_\n", closureOf)
	}
	fmt.Fprintf(&b, "\n```%s\n%s\n```\n", language, source)
	return b.String()
}

// fitBudget walks items in order, accumulating rendered length, and stops
// filling (recording every remaining item's label in omitted) the moment the
// next item would exceed the budget — never truncating an item mid-render.
// The very first item is always kept so a single oversized symbol still
// returns something rather than an empty bundle.
func fitBudget(items []contextItem, budgetTokens int) (includedChars int, omitted []string) {
	budgetChars := budgetTokens * 4
	for i, it := range items {
		next := includedChars + len(it.text)
		if next > budgetChars && i > 0 {
			for _, rest := range items[i:] {
				omitted = append(omitted, rest.section+": "+rest.label)
			}
			return includedChars, omitted
		}
		includedChars = next
	}
	return includedChars, omitted
}

// applyBudgetCut removes fields whose items were omitted, by label, from the
// structured result — keeping JSON and text output consistent about what was
// actually returned.
func applyBudgetCut(result *ContextResult, omitted []string) {
	if len(omitted) == 0 {
		return
	}
	cut := map[string]bool{}
	for _, o := range omitted {
		cut[o] = true
	}
	var sources []ContextSource
	for _, s := range result.Sources {
		if !cut["a: source "+s.Symbol.QualifiedName] {
			sources = append(sources, s)
		}
	}
	result.Sources = sources

	var types []ContextType
	for _, t := range result.Types {
		if !cut["b: "+typeLabel(t)] {
			types = append(types, t)
		}
	}
	result.Types = types

	var callees []ContextCallee
	for _, c := range result.Callees {
		if !cut["c: "+c.Symbol.QualifiedName] {
			callees = append(callees, c)
		}
	}
	result.Callees = callees

	var tests []ContextTestRef
	for _, t := range result.Tests {
		if !cut["d: "+t.Name+" "+t.FilePath] {
			tests = append(tests, t)
		}
	}
	result.Tests = tests

	var helpers []ContextType
	for _, h := range result.Helpers {
		if !cut["e: "+typeLabel(h)] {
			helpers = append(helpers, h)
		}
	}
	result.Helpers = helpers
}

func typeLabel(t ContextType) string {
	if t.For != "" {
		return "constructor " + t.Symbol.QualifiedName + " for " + t.For
	}
	return t.Symbol.QualifiedName
}

// -----------------------------------------------------------------------
// (a) line/closure resolution (A1) and thin-wrapper expansion (E1)
// -----------------------------------------------------------------------

// resolveByFileLine finds the innermost indexed Function/Method node in the
// file matching fileHint whose own [StartLine,EndLine] contains line. Used
// as a fallback when the requested "symbol" is a placeholder name that
// cannot resolve any other way — the caller knows a file:line (from a
// coverage profile or a worklist) but not a name, most often because the
// target is inside a switch/loop body with no name of its own. A target
// inside a nested function literal (a closure, which also has no name of
// its own) resolves to the same enclosing node here; narrowToEnclosingLiteral
// then narrows what gets displayed for section (a).
func resolveByFileLine(stores []*store.Store, fileHint string, line int) (*matchedNode, error) {
	want := strings.ToLower(strings.ReplaceAll(fileHint, "\\", "/"))
	var best *matchedNode
	bestSpan := -1
	for _, s := range stores {
		files, err := s.GetAllFiles()
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			path := strings.ToLower(f.Path)
			if !(strings.HasSuffix(path, want) || strings.Contains(path, want)) {
				continue
			}
			nodes, err := s.GetNodesByFile(f.Path)
			if err != nil {
				return nil, err
			}
			for _, n := range nodes {
				if n.Kind != model.KindFunction && n.Kind != model.KindMethod {
					continue
				}
				if n.StartLine <= line && line <= n.EndLine {
					span := n.EndLine - n.StartLine
					if best == nil || span < bestSpan {
						nCopy := n
						best = &matchedNode{node: nCopy, store: s}
						bestSpan = span
					}
				}
			}
		}
	}
	return best, nil
}

// narrowToEnclosingLiteral resolves a requested line against the raw Go AST
// of the matched node's own file (A1): when line falls inside an anonymous
// function literal nested in the node's body (a `RunE: func(...) {...}`
// callback, an error-group closure, ...), it returns just that literal's own
// line-bounded source plus a one-line signature of the enclosing named
// function for orientation, instead of the node's full — possibly
// hundreds-of-lines — body. Non-Go nodes, parse failures, or a line that is
// not inside any nested literal (a switch/loop body, say — already fully
// covered by the node's own source) report ok=false and leave the node's
// full source untouched.
func narrowToEnclosingLiteral(root string, m matchedNode, line int) (litSrc string, litStart, litEnd int, orientation string, ok bool) {
	if m.node.Language != model.LangGo {
		return "", 0, 0, "", false
	}
	if line < m.node.StartLine || line > m.node.EndLine {
		return "", 0, 0, "", false
	}
	abs := filepath.Join(root, filepath.FromSlash(m.node.FilePath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", 0, 0, "", false
	}
	fset := token.NewFileSet()
	// go/parser may return a partial AST even on error (ADR-003 fallback
	// convention elsewhere in this codebase) — use it anyway when non-nil.
	astFile, _ := parser.ParseFile(fset, abs, data, 0)
	if astFile == nil {
		return "", 0, 0, "", false
	}
	var best *ast.FuncLit
	bestSpan := -1
	ast.Inspect(astFile, func(n ast.Node) bool {
		lit, isLit := n.(*ast.FuncLit)
		if !isLit {
			return true
		}
		start := fset.Position(lit.Pos()).Line
		end := fset.Position(lit.End()).Line
		if start > line || line > end {
			return true
		}
		span := end - start
		if best == nil || span < bestSpan {
			best = lit
			bestSpan = span
		}
		return true
	})
	if best == nil {
		return "", 0, 0, "", false
	}
	start := fset.Position(best.Pos()).Line
	end := fset.Position(best.End()).Line
	litSrc = string(lineBoundedSource(data, start, end))
	label := m.node.QualifiedName
	if m.node.Signature != "" {
		label += m.node.Signature
	}
	orientation = fmt.Sprintf("`%s` — `%s:%d`", label, m.node.FilePath, m.node.StartLine)
	return litSrc, start, end, orientation, true
}

// reThinWrapperCall matches a lone statement that is a bare call or a
// `return`'d call, optionally through one package/receiver selector.
var reThinWrapperCall = regexp.MustCompile(`^(?:return\s+)?(?:[A-Za-z_]\w*\.)?([A-Za-z_]\w*)\(.*\)$`)

// thinWrapperCallee detects a function body consisting of exactly one
// statement — a bare call or `return call(...)` (E1) — and returns the
// called function's bare name so its full source can be surfaced too: a
// 2-line forwarding wrapper's own body tells a test writer nothing about the
// real logic living in the function it forwards to. Best-effort text scan,
// not a parser; a body with comments, multiple statements, or anything
// beyond one call is left alone.
func thinWrapperCallee(src string) (string, bool) {
	lines := strings.Split(src, "\n")
	var body []string
	started := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if !started {
			if i := strings.Index(line, "{"); i >= 0 {
				started = true
				after := strings.TrimSpace(line[i+1:])
				after = strings.TrimSuffix(after, "}")
				after = strings.TrimSpace(after)
				if after != "" {
					body = append(body, after)
				}
			}
			continue
		}
		if line == "" || line == "}" || strings.HasPrefix(line, "//") {
			continue
		}
		body = append(body, line)
	}
	if len(body) != 1 {
		return "", false
	}
	m := reThinWrapperCall.FindStringSubmatch(body[0])
	if m == nil {
		return "", false
	}
	return m[1], true
}

// resolveSoleCallee returns caller's one direct callee node named calleeName,
// when caller calls exactly one node with that name — the graph-verified
// counterpart to thinWrapperCallee's text match.
func resolveSoleCallee(caller matchedNode, calleeName string) (*matchedNode, error) {
	edges, err := caller.store.GetOutgoingEdges(caller.node.ID, []model.EdgeKind{model.EdgeCalls}, "")
	if err != nil {
		return nil, err
	}
	if len(edges) != 1 {
		return nil, nil
	}
	targets, err := caller.store.GetNodesByIDs([]string{edges[0].Target})
	if err != nil {
		return nil, err
	}
	tn, ok := targets[edges[0].Target]
	if !ok || !strings.EqualFold(tn.Name, calleeName) {
		return nil, nil
	}
	return &matchedNode{node: tn, store: caller.store}, nil
}

// -----------------------------------------------------------------------
// (b) types touched
// -----------------------------------------------------------------------

func buildTypeSection(matches []matchedNode, sourceCache map[string]string, root string) ([]contextItem, []ContextType, error) {
	var items []contextItem
	var decls []ContextType
	seen := map[string]int{} // node ID -> index into decls, for role merging

	add := func(role string, forType string, n model.Node, s *store.Store, heuristic bool) error {
		key := n.ID
		if idx, ok := seen[key]; ok {
			if !containsRole(decls[idx].Roles, role) {
				decls[idx].Roles = append(decls[idx].Roles, role)
				sort.Strings(decls[idx].Roles)
			}
			if heuristic && !containsRole(decls[idx].HeuristicRoles, role) {
				decls[idx].HeuristicRoles = append(decls[idx].HeuristicRoles, role)
				sort.Strings(decls[idx].HeuristicRoles)
			}
			return nil
		}
		src, err := readVerifiedIndexedNodeSource(root, matchedNode{node: n, store: s})
		if err != nil {
			return err
		}
		decl := ContextType{Roles: []string{role}, For: forType, Symbol: briefNode(n), Source: src}
		if heuristic {
			decl.HeuristicRoles = []string{role}
		}
		seen[key] = len(decls)
		decls = append(decls, decl)
		return nil
	}

	for _, m := range matches {
		if m.node.Kind != model.KindMethod && m.node.Kind != model.KindFunction {
			continue
		}
		s := m.store

		var receiverType string
		if m.node.Kind == model.KindMethod && strings.Contains(m.node.QualifiedName, "::") {
			receiverType = strings.SplitN(m.node.QualifiedName, "::", 2)[0]
		}

		if receiverType != "" {
			recvNodes, err := s.GetNodesByName(receiverType)
			if err != nil {
				return nil, nil, err
			}
			for _, rn := range recvNodes {
				if typeDeclKinds[rn.Kind] {
					if err := add("receiver", "", rn, s, false); err != nil {
						return nil, nil, err
					}
				}
			}
			// constructors of the receiver type.
			ctors, err := findConstructors(s, receiverType)
			if err != nil {
				return nil, nil, err
			}
			for _, c := range ctors {
				if err := add("constructor", receiverType, c, s, false); err != nil {
					return nil, nil, err
				}
			}
		}

		// parameter/result types: outgoing `references` edges from the
		// symbol itself, filtered to declared type kinds (same module/store).
		edges, err := s.GetOutgoingEdges(m.node.ID, []model.EdgeKind{model.EdgeReferences}, "")
		if err != nil {
			return nil, nil, err
		}
		if len(edges) > 0 {
			ids := make([]string, 0, len(edges))
			for _, e := range edges {
				ids = append(ids, e.Target)
			}
			targets, err := s.GetNodesByIDs(ids)
			if err != nil {
				return nil, nil, err
			}
			for _, tn := range targets {
				if typeDeclKinds[tn.Kind] {
					if err := add("parameter/result", "", tn, s, false); err != nil {
						return nil, nil, err
					}
				}
			}
		}

		// field types: best-effort text scan of the symbol's own source for
		// `receiverVar.Field` reads, matched against the receiver struct's
		// (textually parsed) field declarations — Go struct fields are not
		// indexed as first-class nodes, so this is the only route to them.
		if receiverType != "" {
			src := sourceCache[m.node.ID]
			receiverVar := receiverVarName(src)
			if receiverVar != "" {
				recvNodes, _ := s.GetNodesByName(receiverType)
				for _, rn := range recvNodes {
					if rn.Kind != model.KindStruct {
						continue
					}
					structSrc, err := readVerifiedIndexedNodeSource(root, matchedNode{node: rn, store: s})
					if err != nil {
						continue
					}
					fields := parseStructFields(structSrc)
					used := fieldReadsOf(src, receiverVar)
					for _, fname := range used {
						ftype, ok := fields[fname]
						if !ok {
							continue
						}
						base := bareTypeName(ftype)
						if base == "" || goBuiltinTypeName(base) {
							continue
						}
						typeNodes, err := s.GetNodesByName(base)
						if err != nil {
							continue
						}
						for _, tn := range typeNodes {
							if typeDeclKinds[tn.Kind] {
								if err := add("field", "", tn, s, true); err != nil {
									return nil, nil, err
								}
							}
						}
					}
				}
			}
		}
	}

	sort.SliceStable(decls, func(i, j int) bool {
		if decls[i].Symbol.FilePath != decls[j].Symbol.FilePath {
			return decls[i].Symbol.FilePath < decls[j].Symbol.FilePath
		}
		return decls[i].Symbol.StartLine < decls[j].Symbol.StartLine
	})
	for _, d := range decls {
		label := typeLabel(d)
		items = append(items, contextItem{
			section: "b", label: label,
			text: fmt.Sprintf("\n### Type — `%s` (%s, %s) — `%s:%d-%d`\n\n```%s\n%s\n```\n",
				d.Symbol.QualifiedName, d.Symbol.Kind, formatRoles(d.Roles, d.HeuristicRoles), d.Symbol.FilePath, d.Symbol.StartLine, d.Symbol.EndLine, d.Symbol.Language, d.Source),
		})
	}
	return items, decls, nil
}

// formatRoles renders a comma-joined role list, tagging each role that is
// also present in heuristicRoles with " (inferred)" so a text-scan-derived
// fact (currently only "field") is never visually indistinguishable from a
// graph-verified one (receiver, parameter/result, constructor).
func formatRoles(roles, heuristicRoles []string) string {
	heuristic := make(map[string]bool, len(heuristicRoles))
	for _, r := range heuristicRoles {
		heuristic[r] = true
	}
	parts := make([]string, len(roles))
	for i, r := range roles {
		if heuristic[r] {
			parts[i] = r + " (inferred)"
		} else {
			parts[i] = r
		}
	}
	return strings.Join(parts, ",")
}

func containsRole(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// findConstructors returns exported top-level functions in s named New<T>,
// Default<T>..., or returning T/*T.
func findConstructors(s *store.Store, typeName string) ([]model.Node, error) {
	var out []model.Node
	seen := map[string]bool{}
	err := s.IterateNodesByKind(model.KindFunction, func(n model.Node) error {
		if n.Name == "New"+typeName || strings.HasPrefix(n.Name, "New"+typeName) ||
			n.Name == "Default"+typeName || strings.HasPrefix(n.Name, "Default"+typeName) ||
			n.ReturnType == typeName || n.ReturnType == "*"+typeName {
			if !seen[n.ID] {
				seen[n.ID] = true
				out = append(out, n)
			}
		}
		return nil
	})
	return out, err
}

var reGoReceiverVar = regexp.MustCompile(`^func\s*\(\s*([A-Za-z_]\w*)\s+\*?\s*[A-Za-z_]\w*\s*\)`)

// receiverVarName extracts the receiver variable name from a method's own
// source (its first line), mirroring goReceiverType's grammar assumptions.
func receiverVarName(src string) string {
	firstLine := src
	if i := strings.IndexByte(src, '\n'); i >= 0 {
		firstLine = src[:i]
	}
	m := reGoReceiverVar.FindStringSubmatch(strings.TrimSpace(firstLine))
	if m == nil || m[1] == "_" {
		return ""
	}
	return m[1]
}

// reGoStructField matches simple Go struct field declarations: one or more
// comma-separated names, then a type (pointer/slice/map/qualified). Embedded
// fields (name-less) and complex generic types are not matched — best effort.
var reGoStructField = regexp.MustCompile(`(?m)^\s*([A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)*)\s+((?:\*|\[\]|map\[[^\]]*\])*[A-Za-z_][\w.]*)\s*(?:` + "`[^`]*`" + `)?\s*(?://.*)?$`)

// parseStructFields textually parses a struct's own source into a
// fieldName -> declared type map. Go structs are not indexed as first-class
// field nodes, so this is a best-effort text parse, not a graph query.
func parseStructFields(structSrc string) map[string]string {
	out := map[string]string{}
	for _, m := range reGoStructField.FindAllStringSubmatch(structSrc, -1) {
		names := strings.Split(m[1], ",")
		typ := strings.TrimSpace(m[2])
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n == "" || n == "struct" || n == "func" || n == "type" {
				continue
			}
			out[n] = typ
		}
	}
	return out
}

// fieldReadsOf finds `receiverVar.Field` occurrences in src (a best-effort
// text scan, since Go struct field access is not a graph edge).
func fieldReadsOf(src, receiverVar string) []string {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(receiverVar) + `\.([A-Za-z_]\w*)\b`)
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

func bareTypeName(t string) string {
	t = strings.TrimSpace(t)
	t = strings.TrimPrefix(t, "*")
	t = strings.TrimPrefix(t, "[]")
	if i := strings.LastIndex(t, "."); i >= 0 {
		t = t[i+1:]
	}
	return t
}

func goBuiltinTypeName(name string) bool {
	switch name {
	case "bool", "byte", "complex64", "complex128",
		"error", "float32", "float64",
		"int", "int8", "int16", "int32", "int64",
		"rune", "string",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"any", "comparable":
		return true
	}
	return false
}

// -----------------------------------------------------------------------
// (c) direct callees, signatures only, seam-flagged
// -----------------------------------------------------------------------

func buildCalleeSection(matches []matchedNode, sourceCache map[string]string, root string) ([]contextItem, []ContextCallee, error) {
	var callees []ContextCallee
	seen := map[string]bool{}
	for _, m := range matches {
		edges, err := m.store.GetOutgoingEdges(m.node.ID, []model.EdgeKind{model.EdgeCalls}, "")
		if err != nil {
			return nil, nil, err
		}
		if len(edges) == 0 {
			continue
		}
		ids := make([]string, 0, len(edges))
		for _, e := range edges {
			ids = append(ids, e.Target)
		}
		targets, err := m.store.GetNodesByIDs(ids)
		if err != nil {
			return nil, nil, err
		}
		// Stable order: iterate edges (insertion order), not the ID map.
		for _, e := range edges {
			tn, ok := targets[e.Target]
			if !ok || seen[tn.ID] {
				continue
			}
			seen[tn.ID] = true
			seam, err := seamKind(m, sourceCache[m.node.ID], tn, root)
			if err != nil {
				return nil, nil, err
			}
			callees = append(callees, ContextCallee{Symbol: briefNode(tn), Seam: seam, SeamSource: seamSourceFor(seam)})
		}
	}
	sort.SliceStable(callees, func(i, j int) bool {
		if callees[i].Symbol.FilePath != callees[j].Symbol.FilePath {
			return callees[i].Symbol.FilePath < callees[j].Symbol.FilePath
		}
		return callees[i].Symbol.StartLine < callees[j].Symbol.StartLine
	})
	var items []contextItem
	for _, c := range callees {
		items = append(items, contextItem{
			section: "c", label: c.Symbol.QualifiedName,
			text: fmt.Sprintf("- `%s`%s — `%s` — %s:%d\n", c.Symbol.QualifiedName, seamFlag(c.Seam, c.SeamSource), c.Symbol.Signature, c.Symbol.FilePath, c.Symbol.StartLine),
		})
	}
	return items, callees, nil
}

// seamSourceFor maps a seam value to the marking B1 requires: "interface" is
// a real graph fact ("graph"); "field"/"parameter" are best-effort text
// scans over the caller's own source ("inferred"). Empty seam (no flag)
// yields an empty source too.
func seamSourceFor(seam string) string {
	switch seam {
	case "interface":
		return "graph"
	case "field", "parameter":
		return "inferred"
	default:
		return ""
	}
}

// seamFlag renders the "[seam: ...]" markdown suffix, tagging a text-scan-
// derived seam with " (inferred)" so it is never visually indistinguishable
// from the graph-verified "interface" seam.
func seamFlag(seam, seamSource string) string {
	if seam == "" {
		return ""
	}
	if seamSource == "inferred" {
		return " [seam: " + seam + " (inferred)]"
	}
	return " [seam: " + seam + "]"
}

// seamKind flags a direct callee as a seam a test double could replace:
//   - "interface": the callee is a method declared on an interface (a real
//     graph fact: an incoming `contains` edge from a KindInterface node).
//   - "field": the callee's name matches a field of the caller's own
//     receiver struct whose declared type is a func or looks like an
//     interface type (best-effort text match — see parseStructFields).
//   - "parameter": the callee's name matches one of the caller's own
//     parameters declared with a func type (best-effort text match over the
//     caller's own signature).
func seamKind(caller matchedNode, callerSrc string, callee model.Node, root string) (string, error) {
	if callee.Kind == model.KindMethod {
		incoming, err := caller.store.GetIncomingEdges(callee.ID, []model.EdgeKind{model.EdgeContains})
		if err != nil {
			return "", err
		}
		if len(incoming) > 0 {
			ids := make([]string, 0, len(incoming))
			for _, e := range incoming {
				ids = append(ids, e.Source)
			}
			owners, err := caller.store.GetNodesByIDs(ids)
			if err != nil {
				return "", err
			}
			for _, o := range owners {
				if o.Kind == model.KindInterface {
					return "interface", nil
				}
			}
		}
	}

	if strings.Contains(caller.node.QualifiedName, "::") {
		receiverType := strings.SplitN(caller.node.QualifiedName, "::", 2)[0]
		receiverVar := receiverVarName(callerSrc)
		if receiverVar != "" {
			recvNodes, err := caller.store.GetNodesByName(receiverType)
			if err == nil {
				for _, rn := range recvNodes {
					if rn.Kind != model.KindStruct {
						continue
					}
					structSrc, err := readVerifiedIndexedNodeSource(root, matchedNode{node: rn, store: caller.store})
					if err != nil {
						continue
					}
					fields := parseStructFields(structSrc)
					if ftype, ok := fields[callee.Name]; ok && looksLikeSeamType(caller.store, ftype) {
						return "field", nil
					}
				}
			}
		}
	}

	if params := funcTypedParamNames(caller.node.Signature); params[callee.Name] {
		return "parameter", nil
	}
	return "", nil
}

func looksLikeSeamType(s *store.Store, ftype string) bool {
	ftype = strings.TrimSpace(ftype)
	if strings.HasPrefix(ftype, "func(") || strings.HasPrefix(ftype, "func (") {
		return true
	}
	base := bareTypeName(ftype)
	if base == "" {
		return false
	}
	nodes, err := s.GetNodesByName(base)
	if err != nil {
		return false
	}
	for _, n := range nodes {
		if n.Kind == model.KindInterface {
			return true
		}
	}
	return false
}

// reGoFuncParam matches one `name func(` style parameter in a signature's
// parameter list text (best effort, not a parser).
var reGoFuncParam = regexp.MustCompile(`([A-Za-z_]\w*)\s+func\s*\(`)

func funcTypedParamNames(signature string) map[string]bool {
	out := map[string]bool{}
	for _, m := range reGoFuncParam.FindAllStringSubmatch(signature, -1) {
		out[m[1]] = true
	}
	return out
}

// -----------------------------------------------------------------------
// (d) existing tests calling the symbol, directly or (opt-in) one hop (F1)
// -----------------------------------------------------------------------

// buildTestSection also returns the matched nodes behind each ContextTestRef
// (not just their name/file/line) so section (e)'s ranking (B1) can query
// their own outgoing calls without re-resolving them by name.
func buildTestSection(matches []matchedNode, testHops int) ([]contextItem, []ContextTestRef, []matchedNode, error) {
	if testHops < 1 {
		testHops = 1
	}
	var tests []ContextTestRef
	var testNodes []matchedNode
	seen := map[string]bool{}
	for _, m := range matches {
		found, err := testCallersOf(m.store, m.node.ID, testHops)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, tc := range found {
			if seen[tc.node.ID] {
				continue
			}
			seen[tc.node.ID] = true
			tests = append(tests, ContextTestRef{Name: tc.node.Name, FilePath: tc.node.FilePath, StartLine: tc.node.StartLine, Hops: tc.hops})
			testNodes = append(testNodes, matchedNode{node: tc.node, store: m.store})
		}
	}
	sort.SliceStable(tests, func(i, j int) bool {
		if tests[i].FilePath != tests[j].FilePath {
			return tests[i].FilePath < tests[j].FilePath
		}
		return tests[i].StartLine < tests[j].StartLine
	})
	var items []contextItem
	for _, t := range tests {
		items = append(items, contextItem{
			section: "d", label: t.Name + " " + t.FilePath,
			text: fmt.Sprintf("- `%s` — %s:%d (%d hop)\n", t.Name, t.FilePath, t.StartLine, t.Hops),
		})
	}
	return items, tests, testNodes, nil
}

type hoppedNode struct {
	node model.Node
	hops int
}

func isGoTestFile(path string) bool { return strings.HasSuffix(path, "_test.go") }

// testCallersOf returns _test.go functions/methods that call nodeID directly
// (1 hop) or, when maxHops >= 2, via one intermediate function (2 hops), via
// plain `calls` incoming edges (the same edge kind callers/callees already
// traverse). F1 defaults maxHops to 1: two-hop fuzzy matches dilute the
// useful direct entries far more than they add, so the caller opts in with
// --test-hops 2.
func testCallersOf(s *store.Store, nodeID string, maxHops int) ([]hoppedNode, error) {
	direct, err := s.GetIncomingEdges(nodeID, []model.EdgeKind{model.EdgeCalls})
	if err != nil {
		return nil, err
	}
	var out []hoppedNode
	var hop1NonTest []string
	if len(direct) > 0 {
		ids := make([]string, 0, len(direct))
		for _, e := range direct {
			ids = append(ids, e.Source)
		}
		nodes, err := s.GetNodesByIDs(ids)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			if isGoTestFile(n.FilePath) {
				out = append(out, hoppedNode{node: n, hops: 1})
			} else if maxHops >= 2 {
				hop1NonTest = append(hop1NonTest, n.ID)
			}
		}
	}
	for _, id := range hop1NonTest {
		indirect, err := s.GetIncomingEdges(id, []model.EdgeKind{model.EdgeCalls})
		if err != nil {
			return nil, err
		}
		if len(indirect) == 0 {
			continue
		}
		ids := make([]string, 0, len(indirect))
		for _, e := range indirect {
			ids = append(ids, e.Source)
		}
		nodes, err := s.GetNodesByIDs(ids)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			if isGoTestFile(n.FilePath) {
				out = append(out, hoppedNode{node: n, hops: 2})
			}
		}
	}
	return out, nil
}

// -----------------------------------------------------------------------
// (e) --for test: package helpers/fakes, ranked and capped (B1/C1/D1)
// -----------------------------------------------------------------------

var siblingTestFakeRe = regexp.MustCompile(`(?i)(test|fake)`)

// buildHelperSection collects the package's test helpers/fakes once per
// package (D1 — matches spanning several files in one call share a package's
// helper bundle instead of repeating it), ranks them (B1: helpers section d's
// tests reference first, then the rest by name similarity to the requested
// symbols), and includes full source only for the top helperBodies-ranked
// entries (C1) — the rest keep their signature-only rendering.
func buildHelperSection(matches []matchedNode, testMatches []matchedNode, requestedNames []string, helperBodies int, root string) ([]contextItem, []ContextType, error) {
	var helpers []ContextType
	seen := map[string]bool{}
	dirsDone := map[string]bool{}
	for _, m := range matches {
		dir := filepath.Dir(m.node.FilePath)
		key := m.store.Path() + "|" + dir
		if dirsDone[key] {
			continue
		}
		dirsDone[key] = true

		files, err := m.store.GetAllFiles()
		if err != nil {
			return nil, nil, err
		}
		var testFiles []string
		for _, f := range files {
			if filepath.Dir(f.Path) == dir && isGoTestFile(f.Path) {
				testFiles = append(testFiles, f.Path)
			}
		}
		sort.Strings(testFiles)

		for _, fp := range testFiles {
			nodes, err := m.store.GetNodesByFile(fp)
			if err != nil {
				return nil, nil, err
			}
			for _, n := range nodes {
				if !isHelperCandidate(n) || seen[n.ID] {
					continue
				}
				seen[n.ID] = true
				src, err := readVerifiedIndexedNodeSource(root, matchedNode{node: n, store: m.store})
				if err != nil {
					continue
				}
				helpers = append(helpers, ContextType{Roles: []string{"test-helper"}, Symbol: briefNode(n), Source: src})
			}

			// Sibling *test/*fake* packages this test file imports.
			siblingSyms, err := siblingHelperSymbols(m.store, fp, root)
			if err != nil {
				return nil, nil, err
			}
			for _, sym := range siblingSyms {
				if seen[sym.Symbol.ID] {
					continue
				}
				seen[sym.Symbol.ID] = true
				helpers = append(helpers, sym)
			}
		}
	}

	referenced, err := referencedHelperIDs(testMatches)
	if err != nil {
		return nil, nil, err
	}
	ranked := rankHelpers(helpers, referenced, requestedNames)
	if helperBodies < 0 {
		helperBodies = 0
	}
	for i := range ranked {
		if i >= helperBodies {
			ranked[i].Source = ""
		}
	}

	var items []contextItem
	for _, h := range ranked {
		if h.Source != "" {
			items = append(items, contextItem{
				section: "e", label: typeLabel(h),
				text: fmt.Sprintf("\n### Helper — `%s` (%s) — `%s:%d-%d`\n\n```%s\n%s\n```\n",
					h.Symbol.QualifiedName, h.Symbol.Kind, h.Symbol.FilePath, h.Symbol.StartLine, h.Symbol.EndLine, h.Symbol.Language, h.Source),
			})
			continue
		}
		items = append(items, contextItem{
			section: "e", label: typeLabel(h),
			text: fmt.Sprintf("- `%s` (%s) — `%s` — %s:%d\n", h.Symbol.QualifiedName, h.Symbol.Kind, h.Symbol.Signature, h.Symbol.FilePath, h.Symbol.StartLine),
		})
	}
	return items, ranked, nil
}

// referencedHelperIDs returns the node IDs every function/method declared in
// a section-d test's own _test.go file directly calls (B1's tier-1 set):
// not just the section-d test itself, but every test function in that same
// file — a helper another test in the file calls is still plausibly what a
// new test for the same package needs.
func referencedHelperIDs(testMatches []matchedNode) (map[string]bool, error) {
	referenced := map[string]bool{}
	doneFile := map[string]bool{}
	for _, tm := range testMatches {
		key := tm.store.Path() + "|" + tm.node.FilePath
		if doneFile[key] {
			continue
		}
		doneFile[key] = true
		nodes, err := tm.store.GetNodesByFile(tm.node.FilePath)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			if n.Kind != model.KindFunction && n.Kind != model.KindMethod {
				continue
			}
			edges, err := tm.store.GetOutgoingEdges(n.ID, []model.EdgeKind{model.EdgeCalls}, "")
			if err != nil {
				return nil, err
			}
			for _, e := range edges {
				referenced[e.Target] = true
			}
		}
	}
	return referenced, nil
}

// rankHelpers orders helpers into two tiers — referenced (by section d's
// tests or their file-mates) first, then the rest by name similarity to the
// requested symbols — each tier sorted by file path then line for
// determinism within it. This IS section e's final order (both for JSON and
// for the budget-fill/omitted items below), superseding the plain file/line
// sort every other section uses: an unranked ~580-line helper dump made only
// ~10 lines useful in a real trial (B1).
func rankHelpers(helpers []ContextType, referenced map[string]bool, requestedNames []string) []ContextType {
	var tier1, tier2 []ContextType
	for _, h := range helpers {
		if referenced[h.Symbol.ID] {
			tier1 = append(tier1, h)
		} else {
			tier2 = append(tier2, h)
		}
	}
	sort.SliceStable(tier1, func(i, j int) bool {
		if tier1[i].Symbol.FilePath != tier1[j].Symbol.FilePath {
			return tier1[i].Symbol.FilePath < tier1[j].Symbol.FilePath
		}
		return tier1[i].Symbol.StartLine < tier1[j].Symbol.StartLine
	})
	sort.SliceStable(tier2, func(i, j int) bool {
		si, sj := bestNameSimilarity(tier2[i].Symbol.Name, requestedNames), bestNameSimilarity(tier2[j].Symbol.Name, requestedNames)
		if si != sj {
			return si > sj
		}
		if tier2[i].Symbol.FilePath != tier2[j].Symbol.FilePath {
			return tier2[i].Symbol.FilePath < tier2[j].Symbol.FilePath
		}
		return tier2[i].Symbol.StartLine < tier2[j].Symbol.StartLine
	})
	return append(tier1, tier2...)
}

func bestNameSimilarity(name string, requestedNames []string) int {
	best := 0
	for _, r := range requestedNames {
		if s := nameSimilarity(name, r); s > best {
			best = s
		}
	}
	return best
}

// nameSimilarity is a cheap, best-effort relevance score for B1's tier-2
// ranking: exact match scores highest, a substring relationship next, then
// the longest common substring length — good enough to put e.g.
// "fakeSSHClient" ahead of "unrelatedHelper" when the requested symbol is
// "runAgentRemote" without requiring a real fuzzy-matching library.
func nameSimilarity(a, b string) int {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1000
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return 500
	}
	return longestCommonSubstringLen(a, b)
}

func longestCommonSubstringLen(a, b string) int {
	prevRow := make([]int, len(b)+1)
	best := 0
	for i := 1; i <= len(a); i++ {
		curRow := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				curRow[j] = prevRow[j-1] + 1
				if curRow[j] > best {
					best = curRow[j]
				}
			}
		}
		prevRow = curRow
	}
	return best
}

func isHelperCandidate(n model.Node) bool {
	switch n.Kind {
	case model.KindFunction, model.KindMethod:
		return !testEntrypointRe.MatchString(n.Name) && n.Name != "init"
	case model.KindStruct, model.KindInterface, model.KindTypeAlias:
		return true
	}
	return false
}

func siblingHelperSymbols(s *store.Store, testFilePath, root string) ([]ContextType, error) {
	fileNodes, err := s.GetNodesByFile(testFilePath)
	if err != nil {
		return nil, err
	}
	var fileNodeID string
	for _, n := range fileNodes {
		if n.Kind == model.KindFile {
			fileNodeID = n.ID
			break
		}
	}
	if fileNodeID == "" {
		return nil, nil
	}
	imports, err := s.GetOutgoingEdges(fileNodeID, []model.EdgeKind{model.EdgeImports}, "")
	if err != nil {
		return nil, err
	}
	if len(imports) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(imports))
	for _, e := range imports {
		ids = append(ids, e.Target)
	}
	targets, err := s.GetNodesByIDs(ids)
	if err != nil {
		return nil, err
	}
	var out []ContextType
	for _, imp := range targets {
		base := path_Base(imp.Name)
		if !siblingTestFakeRe.MatchString(base) {
			continue
		}
		files, err := s.GetAllFiles()
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if filepath.Base(filepath.Dir(f.Path)) != base {
				continue
			}
			nodes, err := s.GetNodesByFile(f.Path)
			if err != nil {
				return nil, err
			}
			for _, n := range nodes {
				if !n.IsExported || !typeDeclKindsOrFunc(n.Kind) {
					continue
				}
				src, err := readVerifiedIndexedNodeSource(root, matchedNode{node: n, store: s})
				if err != nil {
					continue
				}
				out = append(out, ContextType{Roles: []string{"sibling-package:" + base}, Symbol: briefNode(n), Source: src})
			}
		}
	}
	return out, nil
}

func typeDeclKindsOrFunc(k model.NodeKind) bool {
	return typeDeclKinds[k] || k == model.KindFunction || k == model.KindMethod
}

// path_Base is a tiny alias so this file needs no second import named "path"
// alongside "path/filepath" — import paths use forward slashes regardless of
// OS, so a plain last-segment split is enough here.
func path_Base(importPath string) string {
	if i := strings.LastIndex(importPath, "/"); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}

// -----------------------------------------------------------------------
// coverage marking
// -----------------------------------------------------------------------

// markUncoveredSource appends " // UNCOVERED" to every line of src (starting
// at startLine, which is m.node.StartLine for a whole-symbol render or a
// narrowed closure literal's own start line — see narrowToEnclosingLiteral)
// that the ingested coverage profile reports as missed.
func markUncoveredSource(m matchedNode, src string, startLine int) (string, error) {
	row, err := m.store.GetCoverageByFile(m.node.FilePath)
	if err != nil {
		return "", err
	}
	if row == nil || row.Ranges == "" {
		return src, nil
	}
	var ranges []covpkg.Range
	if err := json.Unmarshal([]byte(row.Ranges), &ranges); err != nil {
		return "", fmt.Errorf("context: decode coverage ranges for %s: %w", m.node.FilePath, err)
	}
	missed := map[int]bool{}
	for _, r := range ranges {
		if r.Kind != covpkg.KindMiss {
			continue
		}
		for line := r.Start; line <= r.End; line++ {
			missed[line] = true
		}
	}
	lines := strings.Split(src, "\n")
	for i := range lines {
		lineNo := startLine + i
		if missed[lineNo] && lines[i] != "" {
			lines[i] += " // UNCOVERED"
		}
	}
	return strings.Join(lines, "\n"), nil
}

// -----------------------------------------------------------------------
// text rendering
// -----------------------------------------------------------------------

func printContextMarkdown(w io.Writer, result *ContextResult) error {
	if _, err := fmt.Fprintf(w, "# Context — %s\n\n", strings.Join(result.Requested, ", ")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Budget: %d tokens (est. %d used)\n", result.Budget, result.TokensEstimated); err != nil {
		return err
	}
	for _, u := range result.Unresolved {
		if _, err := fmt.Fprintf(w, "\n## %s symbol `%s`\n\n%s\n", u.Status, u.Requested, u.Hint); err != nil {
			return err
		}
	}
	if len(result.Sources) > 0 {
		if _, err := fmt.Fprintln(w, "\n## a. Source"); err != nil {
			return err
		}
		for _, s := range result.Sources {
			startLine, endLine := s.StartLine, s.EndLine
			if startLine == 0 {
				startLine, endLine = s.Symbol.StartLine, s.Symbol.EndLine
			}
			if _, err := fmt.Fprint(w, renderSourceBlock(s.Symbol.QualifiedName, s.Symbol.FilePath, s.Symbol.Language, startLine, endLine, s.ClosureOf, s.Source)); err != nil {
				return err
			}
		}
	}
	if len(result.Types) > 0 {
		if _, err := fmt.Fprintln(w, "\n## b. Types touched"); err != nil {
			return err
		}
		for _, t := range result.Types {
			if _, err := fmt.Fprintf(w, "\n### `%s` (%s, %s)\n\n```%s\n%s\n```\n", t.Symbol.QualifiedName, t.Symbol.Kind, formatRoles(t.Roles, t.HeuristicRoles), t.Symbol.Language, t.Source); err != nil {
				return err
			}
		}
	}
	if len(result.Callees) > 0 {
		if _, err := fmt.Fprintln(w, "\n## c. Direct callees"); err != nil {
			return err
		}
		for _, c := range result.Callees {
			if _, err := fmt.Fprintf(w, "- `%s`%s — `%s` — %s:%d\n", c.Symbol.QualifiedName, seamFlag(c.Seam, c.SeamSource), c.Symbol.Signature, c.Symbol.FilePath, c.Symbol.StartLine); err != nil {
				return err
			}
		}
	}
	if len(result.Tests) > 0 {
		if _, err := fmt.Fprintln(w, "\n## d. Existing tests"); err != nil {
			return err
		}
		for _, t := range result.Tests {
			if _, err := fmt.Fprintf(w, "- `%s` — %s:%d (%d hop)\n", t.Name, t.FilePath, t.StartLine, t.Hops); err != nil {
				return err
			}
		}
	}
	if len(result.Helpers) > 0 {
		if _, err := fmt.Fprintln(w, "\n## e. Test helpers/fakes"); err != nil {
			return err
		}
		for _, h := range result.Helpers {
			if h.Source != "" {
				if _, err := fmt.Fprintf(w, "\n### `%s` (%s) — `%s:%d-%d`\n\n```%s\n%s\n```\n", h.Symbol.QualifiedName, h.Symbol.Kind, h.Symbol.FilePath, h.Symbol.StartLine, h.Symbol.EndLine, h.Symbol.Language, h.Source); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(w, "- `%s` (%s) — `%s` — %s:%d\n", h.Symbol.QualifiedName, h.Symbol.Kind, h.Symbol.Signature, h.Symbol.FilePath, h.Symbol.StartLine); err != nil {
				return err
			}
		}
	}
	if len(result.Omitted) > 0 {
		if _, err := fmt.Fprintln(w, "\n## Omitted (over budget)"); err != nil {
			return err
		}
		for _, o := range result.Omitted {
			if _, err := fmt.Fprintf(w, "- %s\n", o); err != nil {
				return err
			}
		}
	}
	return nil
}
