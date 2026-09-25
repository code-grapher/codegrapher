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
// A --line target that falls inside a nested function literal (a closure
// with no name of its own — an anonymous RunE callback; a switch/loop body
// has none of these either, but those are not literals) resolves to the
// innermost NAMED enclosing function/method, per A1 (resolveByFileLine),
// and Source is that function's WHOLE body, not just the literal: a closure
// only makes sense read alongside the function that declares the variables
// it captures and wires it up (founder correction, 2026-09-25 — the earlier
// design narrowed to the literal's own few lines and lost exactly that
// context). Every line of a target literal carries a trailing "// TARGET"
// marker (see A1/resolveClosureNarrowing) so the reader can still find it
// inside the full function. StartLine/EndLine are always the enclosing
// node's own declared bounds, even when Narrowed is true.
//
// Narrowed is true only when the enclosing function is longer than
// --max-function-lines: Source then keeps just the target literal(s) whole,
// the lines declaring the free variables they capture (go/ast scope), and
// the statement that registers/calls each literal, joined by explicit
// "// … N lines elided" markers — never a silent drop.
type ContextSource struct {
	Symbol    BriefSymbol `json:"symbol"`
	Source    string      `json:"source"`
	Narrowed  bool        `json:"narrowed,omitempty"`
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
	var line, budget, testHops, helperBodies, maxFunctionLines, helperSignatures int
	var pathFlag string

	cmd := &cobra.Command{
		Use:   "context [symbol...]",
		Short: "Bounded test-writing context bundle for a set of symbols",
		Long: `Assemble everything a test writer needs about a set of symbols in one
bounded, budget-capped read: for each requested symbol, in order —
  a. its line-bounded source (--uncovered marks lines an ingested coverage
     profile reports as missed). --file/--line also resolve a symbol with no
     name of its own — an anonymous closure, a switch/loop body — to its
     innermost NAMED enclosing function/method and return that function's
     WHOLE source (a closure only makes sense alongside the function that
     captures for and wires it), marking every target literal's lines
     "// TARGET". Only when the enclosing function is longer than
     --max-function-lines (default 150) does it narrow: the target
     literal(s) whole, the lines declaring the free variables they capture,
     and the statement that registers/calls each one, joined by explicit
     "// … N lines elided" markers (--max-function-lines 0 always returns
     the whole function);
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
     ranked helpers' full source, not just their signature; --helper-signatures
     N, default 40, caps how many ranked helpers appear at all — signature or
     body — independent of remaining budget, and names how many were cut) and
     exported symbols of sibling *test/*fake* packages its tests import.
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
				scopes:           splitCSV(scopeFlag),
				forTest:          forFlag == "test",
				uncovered:        uncovered,
				budget:           budget,
				testHops:         testHops,
				helperBodies:     helperBodies,
				maxFunctionLines: maxFunctionLines,
				helperSignatures: helperSignatures,
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
	cmd.Flags().IntVar(&maxFunctionLines, "max-function-lines", 150, "Narrow a closure target's enclosing function to captures+literal when it is longer than N lines (0 = never narrow, always return the whole function)")
	cmd.Flags().IntVar(&helperSignatures, "helper-signatures", 40, "With --for test, cap section e's ranked helpers (signature or body) at N total, independent of budget (0 = no cap)")
	return cmd
}

type contextOptions struct {
	scopes           []string
	forTest          bool
	uncovered        bool
	budget           int
	testHops         int
	helperBodies     int
	maxFunctionLines int
	helperSignatures int
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
	targetLines := map[string][]int{} // node ID -> deduped requested lines, for A1 closure narrowing in section a
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
			lines := targetLines[m.node.ID]
			dup := false
			for _, l := range lines {
				if l == t.Line {
					dup = true
					break
				}
			}
			if !dup {
				targetLines[m.node.ID] = append(lines, t.Line)
			}
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

		displayStart, displayEnd := m.node.StartLine, m.node.EndLine
		kept := []keptRange{{start: m.node.StartLine, end: m.node.EndLine}}
		var targetRanges [][2]int
		narrowed := false
		if lines := targetLines[m.node.ID]; len(lines) > 0 {
			cn, err := resolveClosureNarrowing(root, m, lines, opts.maxFunctionLines)
			if err != nil {
				return nil, err
			}
			if cn != nil {
				targetRanges = cn.targetRanges
				narrowed = cn.narrowed
				kept = cn.kept
			}
		}
		var missed map[int]bool
		if opts.uncovered {
			missed, err = coverageMissedLines(m)
			if err != nil {
				return nil, err
			}
		}
		rendered := renderKeptRanges(src, m.node.StartLine, m.node.EndLine, m.node.QualifiedName, kept, targetRanges, missed)
		result.Sources = append(result.Sources, ContextSource{
			Symbol: briefNode(m.node), Source: rendered,
			Narrowed: narrowed, StartLine: displayStart, EndLine: displayEnd,
		})
		items = append(items, contextItem{
			section: "a", label: "source " + m.node.QualifiedName,
			text: renderSourceBlock(m.node.QualifiedName, m.node.FilePath, m.node.Language, displayStart, displayEnd, narrowed, rendered),
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
	var helperSignaturesCutNote string
	if opts.forTest {
		helperItems, helpers, cutCount, err := buildHelperSection(matches, testMatches, requestedNames, opts.helperBodies, opts.helperSignatures, root)
		if err != nil {
			return nil, err
		}
		result.Helpers = helpers
		items = append(items, helperItems...)
		if cutCount > 0 {
			helperSignaturesCutNote = fmt.Sprintf("e: %d more helper(s) not shown (--helper-signatures %d cap)", cutCount, opts.helperSignatures)
		}
	}

	included, omitted := fitBudget(items, opts.budget)
	result.TokensEstimated = included / 4
	applyBudgetCut(result, omitted)
	if helperSignaturesCutNote != "" {
		omitted = append(omitted, helperSignaturesCutNote)
	}
	result.Omitted = omitted
	return result, nil
}

// renderSourceBlock is the one place section (a)'s markdown/text rendering
// happens, shared by buildContext's budget-estimation item text and
// printContextMarkdown, so the two never drift on the A1 closure-marking
// (narrowed) note.
func renderSourceBlock(qualifiedName, filePath string, language model.Language, startLine, endLine int, narrowed bool, source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n### `%s` — %s:%d-%d\n", qualifiedName, filePath, startLine, endLine)
	if narrowed {
		fmt.Fprint(&b, "\n_(narrowed: long enclosing function, showing target literal(s) + captures only — see elided-lines markers below; rerun with --max-function-lines 0 for the whole function)_\n")
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
// its own) resolves to the same enclosing node here; resolveClosureNarrowing
// then marks (and, for a long function, narrows) what gets displayed for
// section (a).
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
			if !strings.HasSuffix(path, want) && !strings.Contains(path, want) {
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

// keptRange is one inclusive [start,end] source-line range rendered by
// renderKeptRanges — either the enclosing node's whole span (the default,
// unnarrowed render) or, in a narrowed render, one piece kept for a reason
// (the literal itself, its captured variables' declarations, or the
// statement that registers/calls it). Non-adjacent kept ranges get an
// explicit elision marker between them; nothing is ever silently dropped.
type keptRange struct{ start, end int }

// closureNarrowing is what resolveClosureNarrowing found for one enclosing
// node's requested target lines: every touched literal's own [start,end]
// (for "// TARGET" marking, regardless of narrowing) and the kept ranges to
// render — the whole node by default, or a narrowed set when the node is
// longer than maxFunctionLines.
type closureNarrowing struct {
	targetRanges [][2]int
	kept         []keptRange
	narrowed     bool
}

// resolveClosureNarrowing resolves targetLines against the raw Go AST of m's
// own file (A1): for every line that falls inside a nested anonymous
// function literal (a `RunE: func(...) {...}` callback, an error-group
// closure, ...), it records that literal's own line span for "// TARGET"
// marking. By default the whole enclosing node is still kept (a closure only
// makes sense read alongside the function that captures for and wires it up
// — founder correction, 2026-09-25: the previous design narrowed to the
// literal's own few lines and lost exactly that context). Only when m's own
// line count exceeds maxFunctionLines (0 = never narrow) does it narrow the
// kept ranges to each literal (whole), the lines declaring the free
// variables it captures from the enclosing function (resolved via a manual
// lexical scope walk — see capturedDeclRanges — rather than the deprecated
// go/ast.Object resolver), and the statement that registers/calls it. Returns nil
// when no requested line falls inside any nested literal (a switch/loop
// body, say — already fully covered by the node's own source) or the file
// can't be parsed; the caller then renders the node's full source unmarked,
// as before.
func resolveClosureNarrowing(root string, m matchedNode, targetLines []int, maxFunctionLines int) (*closureNarrowing, error) {
	if m.node.Language != model.LangGo {
		return nil, nil
	}
	abs := filepath.Join(root, filepath.FromSlash(m.node.FilePath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil //nolint:nilerr // best effort: fall back to the unmarked full source
	}
	fset := token.NewFileSet()
	// go/parser may return a partial AST even on error (ADR-003 fallback
	// convention elsewhere in this codebase) — use it anyway when non-nil.
	// SkipObjectResolution: capturedDeclRanges resolves captured identifiers
	// itself via a manual lexical scope walk, so the legacy (and deprecated)
	// go/ast.Object resolver's work is never used — skip it.
	astFile, _ := parser.ParseFile(fset, abs, data, parser.SkipObjectResolution)
	if astFile == nil {
		return nil, nil
	}

	type litHit struct {
		lit        *ast.FuncLit
		start, end int
	}
	var hits []litHit
	seenLit := map[*ast.FuncLit]bool{}
	for _, line := range targetLines {
		if line < m.node.StartLine || line > m.node.EndLine {
			continue
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
				best, bestSpan = lit, span
			}
			return true
		})
		if best != nil && !seenLit[best] {
			seenLit[best] = true
			hits = append(hits, litHit{best, fset.Position(best.Pos()).Line, fset.Position(best.End()).Line})
		}
	}
	if len(hits) == 0 {
		return nil, nil
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].start < hits[j].start })

	targetRanges := make([][2]int, len(hits))
	for i, h := range hits {
		targetRanges[i] = [2]int{h.start, h.end}
	}

	funcLines := m.node.EndLine - m.node.StartLine + 1
	if maxFunctionLines <= 0 || funcLines <= maxFunctionLines {
		return &closureNarrowing{
			targetRanges: targetRanges,
			kept:         []keptRange{{start: m.node.StartLine, end: m.node.EndLine}},
		}, nil
	}

	var kept []keptRange
	for _, h := range hits {
		kept = append(kept, keptRange{start: h.start, end: h.end})
		if stmt := smallestEnclosingStmt(astFile, h.lit); stmt != nil {
			kept = append(kept, keptRange{start: fset.Position(stmt.Pos()).Line, end: fset.Position(stmt.End()).Line})
		}
		for _, r := range capturedDeclRanges(fset, astFile, h.lit, m.node.StartLine, m.node.EndLine) {
			kept = append(kept, keptRange{start: r[0], end: r[1]})
		}
	}
	return &closureNarrowing{targetRanges: targetRanges, kept: mergeKeptRanges(kept), narrowed: true}, nil
}

// smallestEnclosingStmt returns the smallest ast.Stmt in file whose span
// fully contains lit — the statement that registers or calls the closure
// (an assignment, a `return`, a bare call, ...) — for a narrowed rendering.
// When the literal is itself nested in a larger composite literal (e.g.
// `return &cobra.Command{RunE: func(...){...}}`), that whole statement is
// what's kept: a known trade-off, not a further narrowing.
func smallestEnclosingStmt(file *ast.File, lit *ast.FuncLit) ast.Stmt {
	var best ast.Stmt
	bestSpan := token.Pos(-1)
	ast.Inspect(file, func(n ast.Node) bool {
		stmt, ok := n.(ast.Stmt)
		if !ok || stmt.Pos() > lit.Pos() || stmt.End() < lit.End() {
			return true
		}
		span := stmt.End() - stmt.Pos()
		if best == nil || span < bestSpan {
			best, bestSpan = stmt, span
		}
		return true
	})
	return best
}

// declScope is one lexical block's declarations, chained to its enclosing
// block — the manual replacement for go/ast's deprecated legacy object
// resolver (go/ast.Object / Ident.Obj, SA1019: "The relationship between
// Idents and Objects cannot be correctly computed without type
// information"). capturedDeclRanges below builds one chain of these for the
// scopes visible at a closure literal's position in its enclosing function
// (marked outer, since a name found there is a genuine capture) and a
// second chain, rooted at the first, for the literal's own params/results
// and nested blocks (marked local, not outer) — so a name the literal itself
// (re)declares shadows the outer one, exactly like ordinary Go scoping, and
// is correctly excluded from the capture set.
type declScope struct {
	parent *declScope
	outer  bool
	decls  map[string]ast.Node
}

func newDeclScope(parent *declScope, outer bool) *declScope {
	return &declScope{parent: parent, outer: outer, decls: map[string]ast.Node{}}
}

func (s *declScope) define(name string, decl ast.Node) {
	if s == nil || name == "" || name == "_" {
		return
	}
	s.decls[name] = decl
}

// resolve looks up name up the scope chain, returning the node that
// declares it and whether that declaration lives in an "outer" (enclosing-
// function) scope — i.e. is a capture rather than the literal's own.
func (s *declScope) resolve(name string) (decl ast.Node, outer, ok bool) {
	for sc := s; sc != nil; sc = sc.parent {
		if n, found := sc.decls[name]; found {
			return n, sc.outer, true
		}
	}
	return nil, false, false
}

func defineFieldListNames(s *declScope, fl *ast.FieldList) {
	if fl == nil {
		return
	}
	for _, f := range fl.List {
		for _, name := range f.Names {
			s.define(name.Name, f)
		}
	}
}

// defineSimpleDecl adds the name(s) a single non-block-opening statement
// declares (a `:=` assignment or a `var`/`const`/`type` decl) to s. Control
// statements that open their own nested scope (if/for/switch/...) are
// handled by their dedicated cases in scopeChainAt/walkIdentUses instead.
func defineSimpleDecl(s *declScope, stmt ast.Stmt) {
	switch st := stmt.(type) {
	case *ast.AssignStmt:
		if st.Tok != token.DEFINE {
			return
		}
		for _, lhs := range st.Lhs {
			if id, ok := lhs.(*ast.Ident); ok {
				s.define(id.Name, st)
			}
		}
	case *ast.DeclStmt:
		gd, ok := st.Decl.(*ast.GenDecl)
		if !ok {
			return
		}
		for _, spec := range gd.Specs {
			switch sp := spec.(type) {
			case *ast.ValueSpec:
				for _, id := range sp.Names {
					s.define(id.Name, sp)
				}
			case *ast.TypeSpec:
				s.define(sp.Name.Name, sp)
			}
		}
	}
}

// spanContains reports whether n's source span fully contains lit.
func spanContains(n ast.Node, lit *ast.FuncLit) bool {
	return n != nil && n.Pos() <= lit.Pos() && lit.End() <= n.End()
}

// enclosingFuncDecl returns the top-level function/method declaration whose
// body contains lit. Go disallows nested named-function declarations, so at
// most one *ast.FuncDecl in file can contain any given literal.
func enclosingFuncDecl(file *ast.File, lit *ast.FuncLit) *ast.FuncDecl {
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		if spanContains(fd.Body, lit) {
			return fd
		}
	}
	return nil
}

// scopeChainAt walks stmt — a statement or block known to span lit — and
// returns the declScope visible immediately before lit, chained to parent.
// It only descends into whichever nested block actually contains lit,
// accumulating declarations from each preceding sibling statement and from
// control-statement init clauses along the way, mirroring ordinary Go block
// scoping. Every scope it creates is marked with outer, so a single chain
// built from a function's body (outer=true) stays outer end to end.
func scopeChainAt(stmt ast.Node, parent *declScope, lit *ast.FuncLit, outer bool) *declScope {
	scope := newDeclScope(parent, outer)
	descendBlock := func(list []ast.Stmt) *declScope {
		for _, st := range list {
			if spanContains(st, lit) {
				return scopeChainAt(st, scope, lit, outer)
			}
			if st.End() <= lit.Pos() {
				defineSimpleDecl(scope, st)
			}
		}
		return scope
	}
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		return descendBlock(s.List)
	case *ast.IfStmt:
		if s.Init != nil {
			defineSimpleDecl(scope, s.Init)
		}
		if spanContains(s.Body, lit) {
			return scopeChainAt(s.Body, scope, lit, outer)
		}
		if s.Else != nil && spanContains(s.Else, lit) {
			return scopeChainAt(s.Else, scope, lit, outer)
		}
		return scope
	case *ast.ForStmt:
		if s.Init != nil {
			defineSimpleDecl(scope, s.Init)
		}
		if spanContains(s.Body, lit) {
			return scopeChainAt(s.Body, scope, lit, outer)
		}
		return scope
	case *ast.RangeStmt:
		if s.Tok == token.DEFINE {
			if id, ok := s.Key.(*ast.Ident); ok {
				scope.define(id.Name, s)
			}
			if id, ok := s.Value.(*ast.Ident); ok {
				scope.define(id.Name, s)
			}
		}
		if spanContains(s.Body, lit) {
			return scopeChainAt(s.Body, scope, lit, outer)
		}
		return scope
	case *ast.SwitchStmt:
		if s.Init != nil {
			defineSimpleDecl(scope, s.Init)
		}
		if spanContains(s.Body, lit) {
			return scopeChainAt(s.Body, scope, lit, outer)
		}
		return scope
	case *ast.TypeSwitchStmt:
		if s.Init != nil {
			defineSimpleDecl(scope, s.Init)
		}
		if spanContains(s.Body, lit) {
			return scopeChainAt(s.Body, scope, lit, outer)
		}
		return scope
	case *ast.SelectStmt:
		if spanContains(s.Body, lit) {
			return scopeChainAt(s.Body, scope, lit, outer)
		}
		return scope
	case *ast.CaseClause:
		return descendBlock(s.Body)
	case *ast.CommClause:
		return descendBlock(s.Body)
	case *ast.LabeledStmt:
		return scopeChainAt(s.Stmt, parent, lit, outer)
	default:
		return parent
	}
}

// enclosingScopeAtLit returns the scope chain visible in fd (parameters,
// receiver, named results, and every `:=`/var/type declared in a block
// enclosing lit) at the point lit appears — everything a closure literal at
// that position can capture.
func enclosingScopeAtLit(fd *ast.FuncDecl, lit *ast.FuncLit) *declScope {
	base := newDeclScope(nil, true)
	defineFieldListNames(base, fd.Recv)
	defineFieldListNames(base, fd.Type.Params)
	defineFieldListNames(base, fd.Type.Results)
	if fd.Body == nil {
		return base
	}
	return scopeChainAt(fd.Body, base, lit, true)
}

// walkIdentUses walks stmt within scope — which already reflects everything
// declared before stmt in its own block — resolving every identifier
// reference it finds (including inside any further-nested closure) and
// calling capture(declNode) for each one that resolves to an outer-scope
// declaration. It defines stmt's own declarations into scope exactly where
// Go does: visible to later siblings and nested blocks, never to stmt's own
// RHS/condition, so a name the literal redeclares shadows the outer one for
// every use that follows.
func walkIdentUses(stmt ast.Stmt, scope *declScope, capture func(ast.Node)) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		child := newDeclScope(scope, false)
		for _, st := range s.List {
			walkIdentUses(st, child, capture)
		}
	case *ast.IfStmt:
		child := newDeclScope(scope, false)
		if s.Init != nil {
			walkIdentUses(s.Init, child, capture)
		}
		walkExprUses(s.Cond, child, capture)
		walkIdentUses(s.Body, child, capture)
		if s.Else != nil {
			walkIdentUses(s.Else, child, capture)
		}
	case *ast.ForStmt:
		child := newDeclScope(scope, false)
		if s.Init != nil {
			walkIdentUses(s.Init, child, capture)
		}
		walkExprUses(s.Cond, child, capture)
		if s.Post != nil {
			walkIdentUses(s.Post, child, capture)
		}
		walkIdentUses(s.Body, child, capture)
	case *ast.RangeStmt:
		walkExprUses(s.X, scope, capture)
		child := newDeclScope(scope, false)
		if s.Tok == token.DEFINE {
			if id, ok := s.Key.(*ast.Ident); ok {
				child.define(id.Name, s)
			}
			if id, ok := s.Value.(*ast.Ident); ok {
				child.define(id.Name, s)
			}
		} else {
			walkExprUses(s.Key, child, capture)
			walkExprUses(s.Value, child, capture)
		}
		walkIdentUses(s.Body, child, capture)
	case *ast.SwitchStmt:
		child := newDeclScope(scope, false)
		if s.Init != nil {
			walkIdentUses(s.Init, child, capture)
		}
		walkExprUses(s.Tag, child, capture)
		walkIdentUses(s.Body, child, capture)
	case *ast.TypeSwitchStmt:
		child := newDeclScope(scope, false)
		if s.Init != nil {
			walkIdentUses(s.Init, child, capture)
		}
		walkIdentUses(s.Assign, child, capture)
		walkIdentUses(s.Body, child, capture)
	case *ast.CaseClause:
		child := newDeclScope(scope, false)
		for _, e := range s.List {
			walkExprUses(e, scope, capture)
		}
		for _, st := range s.Body {
			walkIdentUses(st, child, capture)
		}
	case *ast.SelectStmt:
		walkIdentUses(s.Body, scope, capture)
	case *ast.CommClause:
		child := newDeclScope(scope, false)
		if s.Comm != nil {
			walkIdentUses(s.Comm, child, capture)
		}
		for _, st := range s.Body {
			walkIdentUses(st, child, capture)
		}
	case *ast.LabeledStmt:
		walkIdentUses(s.Stmt, scope, capture)
	case *ast.AssignStmt:
		for _, e := range s.Rhs {
			walkExprUses(e, scope, capture)
		}
		for _, lhs := range s.Lhs {
			id, ok := lhs.(*ast.Ident)
			if ok && s.Tok == token.DEFINE {
				scope.define(id.Name, s)
				continue
			}
			walkExprUses(lhs, scope, capture)
		}
	case *ast.DeclStmt:
		gd, ok := s.Decl.(*ast.GenDecl)
		if !ok {
			return
		}
		for _, spec := range gd.Specs {
			switch sp := spec.(type) {
			case *ast.ValueSpec:
				for _, v := range sp.Values {
					walkExprUses(v, scope, capture)
				}
				walkExprUses(sp.Type, scope, capture)
				for _, id := range sp.Names {
					scope.define(id.Name, sp)
				}
			case *ast.TypeSpec:
				scope.define(sp.Name.Name, sp)
			}
		}
	case *ast.ExprStmt:
		walkExprUses(s.X, scope, capture)
	case *ast.ReturnStmt:
		for _, e := range s.Results {
			walkExprUses(e, scope, capture)
		}
	case *ast.GoStmt:
		walkExprUses(s.Call, scope, capture)
	case *ast.DeferStmt:
		walkExprUses(s.Call, scope, capture)
	case *ast.SendStmt:
		walkExprUses(s.Chan, scope, capture)
		walkExprUses(s.Value, scope, capture)
	case *ast.IncDecStmt:
		walkExprUses(s.X, scope, capture)
	case *ast.BranchStmt, *ast.EmptyStmt:
		// Labels aren't variable identifiers; nothing to resolve.
	default:
		// Best-effort fallback for any remaining statement kind: still find
		// identifier uses, just without scope-accurate declare-before-use
		// ordering (no such statement appears in this codebase's fixtures).
		ast.Inspect(s, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				resolveCapture(id, scope, capture)
			}
			return true
		})
	}
}

// walkExprUses resolves identifier uses within expr against scope,
// descending into any nested func literal with a fresh non-outer scope
// chained to scope — so a closure nested inside lit can itself capture from
// lit, while lit's own captures from the enclosing function stay reachable
// through the chain.
func walkExprUses(expr ast.Expr, scope *declScope, capture func(ast.Node)) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.Ident:
		resolveCapture(e, scope, capture)
	case *ast.FuncLit:
		child := newDeclScope(scope, false)
		defineFieldListNames(child, e.Type.Params)
		defineFieldListNames(child, e.Type.Results)
		walkIdentUses(e.Body, child, capture)
	case *ast.SelectorExpr:
		walkExprUses(e.X, scope, capture) // e.Sel is a field/method name, not a variable use
	case *ast.CallExpr:
		walkExprUses(e.Fun, scope, capture)
		for _, a := range e.Args {
			walkExprUses(a, scope, capture)
		}
	case *ast.BinaryExpr:
		walkExprUses(e.X, scope, capture)
		walkExprUses(e.Y, scope, capture)
	case *ast.UnaryExpr:
		walkExprUses(e.X, scope, capture)
	case *ast.ParenExpr:
		walkExprUses(e.X, scope, capture)
	case *ast.StarExpr:
		walkExprUses(e.X, scope, capture)
	case *ast.IndexExpr:
		walkExprUses(e.X, scope, capture)
		walkExprUses(e.Index, scope, capture)
	case *ast.IndexListExpr:
		walkExprUses(e.X, scope, capture)
		for _, idx := range e.Indices {
			walkExprUses(idx, scope, capture)
		}
	case *ast.SliceExpr:
		walkExprUses(e.X, scope, capture)
		walkExprUses(e.Low, scope, capture)
		walkExprUses(e.High, scope, capture)
		walkExprUses(e.Max, scope, capture)
	case *ast.TypeAssertExpr:
		walkExprUses(e.X, scope, capture)
	case *ast.KeyValueExpr:
		// Key may be a struct field name (not a var use) or a map key (a var
		// use); resolving it against scope is harmless either way, since a
		// field name won't match anything in the variable scope chain.
		walkExprUses(e.Key, scope, capture)
		walkExprUses(e.Value, scope, capture)
	case *ast.CompositeLit:
		walkExprUses(e.Type, scope, capture)
		for _, elt := range e.Elts {
			walkExprUses(elt, scope, capture)
		}
	}
}

func resolveCapture(id *ast.Ident, scope *declScope, capture func(ast.Node)) {
	if id.Name == "_" {
		return
	}
	decl, outer, ok := scope.resolve(id.Name)
	if ok && outer {
		capture(decl)
	}
}

// capturedDeclRanges returns the [start,end] line ranges declaring every
// identifier lit's body references that resolves — via the manual lexical
// scope walk above (declScope/enclosingScopeAtLit/walkIdentUses), not the
// deprecated go/ast.Object resolver — to a declaration inside the enclosing
// function [funcStart,funcEnd] but outside lit's own span: the free
// variables the closure captures from its enclosing function. A declaration
// outside that function (package/file scope, already visible without
// narrowing) or inside the literal itself (its own params/locals, or a name
// it redeclares that shadows an outer one — not a capture) is excluded.
func capturedDeclRanges(fset *token.FileSet, file *ast.File, lit *ast.FuncLit, funcStart, funcEnd int) [][2]int {
	fd := enclosingFuncDecl(file, lit)
	if fd == nil {
		return nil
	}
	outerScope := enclosingScopeAtLit(fd, lit)

	litScope := newDeclScope(outerScope, false)
	defineFieldListNames(litScope, lit.Type.Params)
	defineFieldListNames(litScope, lit.Type.Results)

	seen := map[ast.Node]bool{}
	var ranges [][2]int
	walkIdentUses(lit.Body, litScope, func(decl ast.Node) {
		if seen[decl] {
			return
		}
		seen[decl] = true
		dStart, dEnd := fset.Position(decl.Pos()).Line, fset.Position(decl.End()).Line
		if dStart < funcStart || dEnd > funcEnd {
			return // declared at package/file scope, not this function (defensive: outerScope is already bounded to fd)
		}
		ranges = append(ranges, [2]int{dStart, dEnd})
	})
	return ranges
}

// mergeKeptRanges sorts ranges by start line and merges any that overlap or
// are adjacent, so renderKeptRanges never emits a zero-line elision gap.
func mergeKeptRanges(ranges []keptRange) []keptRange {
	if len(ranges) == 0 {
		return nil
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	out := []keptRange{ranges[0]}
	for _, r := range ranges[1:] {
		last := &out[len(out)-1]
		if r.start <= last.end+1 {
			if r.end > last.end {
				last.end = r.end
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// renderKeptRanges renders fullSrc (m's whole node source, starting at
// nodeStart) across kept — the whole node by default, or a narrowed set of
// ranges from resolveClosureNarrowing. Every line inside targetRanges (a
// touched closure literal) carries a trailing "// TARGET"; every line missed
// reports as uncovered carries "// UNCOVERED" too, alongside when both
// apply. A gap before, between, or after kept ranges (relative to the node's
// own [nodeStart,nodeEnd]) becomes an explicit
// "// … N lines elided (enclosing function F is M lines; rerun with
// --max-function-lines 0 for all)" marker — never a silent drop.
func renderKeptRanges(fullSrc string, nodeStart, nodeEnd int, qualifiedName string, kept []keptRange, targetRanges [][2]int, missed map[int]bool) string {
	srcLines := strings.Split(fullSrc, "\n")
	lineText := func(abs int) (string, bool) {
		idx := abs - nodeStart
		if idx < 0 || idx >= len(srcLines) {
			return "", false
		}
		return srcLines[idx], true
	}
	target := map[int]bool{}
	for _, r := range targetRanges {
		for ln := r[0]; ln <= r[1]; ln++ {
			target[ln] = true
		}
	}
	funcTotalLines := nodeEnd - nodeStart + 1

	var b strings.Builder
	writeElision := func(gap int) {
		fmt.Fprintf(&b, "// … %d lines elided (enclosing function %s is %d lines; rerun with --max-function-lines 0 for all)\n", gap, qualifiedName, funcTotalLines)
	}
	prevEnd := nodeStart - 1
	for _, r := range kept {
		if r.start > prevEnd+1 {
			writeElision(r.start - prevEnd - 1)
		}
		for ln := r.start; ln <= r.end; ln++ {
			text, ok := lineText(ln)
			if !ok {
				continue
			}
			if target[ln] {
				text += " // TARGET"
			}
			if missed[ln] && strings.TrimSpace(text) != "" {
				text += " // UNCOVERED"
			}
			b.WriteString(text)
			b.WriteByte('\n')
		}
		prevEnd = r.end
	}
	if nodeEnd > prevEnd {
		writeElision(nodeEnd - prevEnd)
	}
	return strings.TrimSuffix(b.String(), "\n")
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
// symbols), caps the ranked list at helperSignatures total (signature or
// body — 0 means no cap) independent of the remaining --budget, and includes
// full source only for the top helperBodies-ranked entries of what's left
// (C1) — the rest keep their signature-only rendering. The third return
// value is how many ranked helpers the helperSignatures cap cut, for the
// caller to fold into the omitted list.
func buildHelperSection(matches []matchedNode, testMatches []matchedNode, requestedNames []string, helperBodies, helperSignatures int, root string) ([]contextItem, []ContextType, int, error) {
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
			return nil, nil, 0, err
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
				return nil, nil, 0, err
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
				return nil, nil, 0, err
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
		return nil, nil, 0, err
	}
	ranked := rankHelpers(helpers, referenced, requestedNames)

	// --helper-signatures caps the ranked list itself — signature or body —
	// independent of remaining --budget (0 = no cap); the cut count is
	// reported to the caller rather than naming each dropped helper, since
	// the cap exists precisely to avoid enumerating dozens of them.
	cutCount := 0
	if helperSignatures > 0 && len(ranked) > helperSignatures {
		cutCount = len(ranked) - helperSignatures
		ranked = ranked[:helperSignatures]
	}

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
	return items, ranked, cutCount, nil
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

// coverageMissedLines returns the set of m's own file lines the ingested
// coverage profile reports as missed (nil if nothing was ingested for that
// file), for renderKeptRanges to mark "// UNCOVERED" alongside any
// "// TARGET" closure marking.
func coverageMissedLines(m matchedNode) (map[int]bool, error) {
	row, err := m.store.GetCoverageByFile(m.node.FilePath)
	if err != nil {
		return nil, err
	}
	if row == nil || row.Ranges == "" {
		return nil, nil
	}
	var ranges []covpkg.Range
	if err := json.Unmarshal([]byte(row.Ranges), &ranges); err != nil {
		return nil, fmt.Errorf("context: decode coverage ranges for %s: %w", m.node.FilePath, err)
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
	return missed, nil
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
			if _, err := fmt.Fprint(w, renderSourceBlock(s.Symbol.QualifiedName, s.Symbol.FilePath, s.Symbol.Language, startLine, endLine, s.Narrowed, s.Source)); err != nil {
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
		// Not every entry here is a budget cut: a "--helper-signatures cap"
		// note (buildContext) fires independent of remaining budget too.
		if _, err := fmt.Fprintln(w, "\n## Omitted"); err != nil {
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
