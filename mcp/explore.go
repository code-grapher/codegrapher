package mcp

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/query"
)

// -----------------------------------------------------------------------
// codegraph_explore — faithful port of handleExplore (src/mcp/tools.ts),
// including buildFlowFromNamedSymbols, buildBlastRadiusSection and the
// Random-Walk-with-Restart graph relevance ranking.
// -----------------------------------------------------------------------

// fileGroup is one file's gathered nodes plus its relevance score.
type fileGroup struct {
	nodes []model.Node
	score int
}

// fileEntry pairs a file path with its group for ordered iteration.
type fileEntry struct {
	path  string
	group *fileGroup
}

// exploreOutputBudget mirrors ExploreOutputBudget.
type exploreOutputBudget struct {
	maxOutputChars              int
	defaultMaxFiles             int
	maxCharsPerFile             int
	gapThreshold                int
	maxSymbolsInFileHeader      int
	maxEdgesPerRelationshipKind int
	includeRelationships        bool
	includeAdditionalFiles      bool
	includeCompletenessSignal   bool
	includeBudgetNote           bool
}

// getExploreOutputBudget mirrors getExploreOutputBudget tiers.
func getExploreOutputBudget(fileCount int) exploreOutputBudget {
	switch {
	case fileCount < 150:
		return exploreOutputBudget{
			maxOutputChars: 13000, defaultMaxFiles: 4, maxCharsPerFile: 3800,
			gapThreshold: 7, maxSymbolsInFileHeader: 5, maxEdgesPerRelationshipKind: 4,
		}
	case fileCount < 500:
		return exploreOutputBudget{
			maxOutputChars: 18000, defaultMaxFiles: 5, maxCharsPerFile: 3800,
			gapThreshold: 8, maxSymbolsInFileHeader: 6, maxEdgesPerRelationshipKind: 6,
		}
	case fileCount < 5000:
		return exploreOutputBudget{
			maxOutputChars: 24000, defaultMaxFiles: 8, maxCharsPerFile: 6500,
			gapThreshold: 12, maxSymbolsInFileHeader: 10, maxEdgesPerRelationshipKind: 10,
			includeRelationships: true, includeAdditionalFiles: true,
			includeCompletenessSignal: true, includeBudgetNote: true,
		}
	case fileCount < 15000:
		return exploreOutputBudget{
			maxOutputChars: 24000, defaultMaxFiles: 8, maxCharsPerFile: 7000,
			gapThreshold: 15, maxSymbolsInFileHeader: 15, maxEdgesPerRelationshipKind: 15,
			includeRelationships: true, includeAdditionalFiles: true,
			includeCompletenessSignal: true, includeBudgetNote: true,
		}
	default:
		return exploreOutputBudget{
			maxOutputChars: 24000, defaultMaxFiles: 8, maxCharsPerFile: 7000,
			gapThreshold: 15, maxSymbolsInFileHeader: 15, maxEdgesPerRelationshipKind: 15,
			includeRelationships: true, includeAdditionalFiles: true,
			includeCompletenessSignal: true, includeBudgetNote: true,
		}
	}
}

// getExploreBudget mirrors getExploreBudget (call-count recommendation).
func getExploreBudget(fileCount int) int {
	switch {
	case fileCount < 500:
		return 1
	case fileCount < 5000:
		return 2
	case fileCount < 15000:
		return 3
	case fileCount < 25000:
		return 4
	default:
		return 5
	}
}

// exploreLineNumbersEnabled mirrors the CODEGRAPH_EXPLORE_LINENUMS toggle.
func exploreLineNumbersEnabled() bool {
	return os.Getenv("CODEGRAPH_EXPLORE_LINENUMS") != "0"
}

// adaptiveExploreEnabled mirrors the CODEGRAPH_ADAPTIVE_EXPLORE toggle.
func adaptiveExploreEnabled() bool {
	v := os.Getenv("CODEGRAPH_ADAPTIVE_EXPLORE")
	return v != "0" && v != "false"
}

// rankEdgeKinds is the edge-kind set used by computeGraphRelevance.
var rankEdgeKinds = map[model.EdgeKind]bool{
	model.EdgeCalls: true, model.EdgeReferences: true, model.EdgeExtends: true,
	model.EdgeImplements: true, model.EdgeOverrides: true,
	model.EdgeInstantiates: true, model.EdgeReturns: true,
	model.EdgeTypeOf: true, model.EdgeImports: true,
}

// computeGraphRelevance is a faithful port of computeGraphRelevance:
// Random-Walk-with-Restart (personalized PageRank) over undirected adjacency,
// restart alpha 0.25 to the seeds, 25 power iterations.
func computeGraphRelevance(nodeIDs []string, edges []model.Edge, seedIDs map[string]struct{}) map[string]float64 {
	out := make(map[string]float64)
	n := len(nodeIDs)
	if n == 0 {
		return out
	}
	idx := make(map[string]int, n)
	for i, id := range nodeIDs {
		idx[id] = i
	}

	adj := make([][]int, n)
	for _, e := range edges {
		if !rankEdgeKinds[e.Kind] {
			continue
		}
		i, iok := idx[e.Source]
		j, jok := idx[e.Target]
		if !iok || !jok || i == j {
			continue
		}
		adj[i] = append(adj[i], j)
		adj[j] = append(adj[j], i)
	}

	r := make([]float64, n)
	rsum := 0.0
	for id := range seedIDs {
		if i, ok := idx[id]; ok {
			r[i] = 1
			rsum++
		}
	}
	if rsum == 0 {
		for i := range r {
			r[i] = 1
		}
		rsum = float64(n)
	}
	for i := range r {
		r[i] /= rsum
	}

	const alpha = 0.25
	s := append([]float64(nil), r...)
	for iter := 0; iter < 25; iter++ {
		next := make([]float64, n)
		for i := 0; i < n; i++ {
			si := s[i]
			if si == 0 {
				continue
			}
			d := len(adj[i])
			if d == 0 {
				next[i] += si
				continue
			}
			share := si / float64(d)
			for _, j := range adj[i] {
				next[j] += share
			}
		}
		for i := 0; i < n; i++ {
			s[i] = (1-alpha)*next[i] + alpha*r[i]
		}
	}
	for i, id := range nodeIDs {
		out[id] = s[i]
	}
	return out
}

// flowResult is buildFlowFromNamedSymbols' result.
type flowResult struct {
	text               string
	pathNodeIDs        map[string]struct{}
	namedNodeIDs       map[string]struct{}
	uniqueNamedNodeIDs map[string]struct{}
}

func emptyFlow() flowResult {
	return flowResult{
		pathNodeIDs:        map[string]struct{}{},
		namedNodeIDs:       map[string]struct{}{},
		uniqueNamedNodeIDs: map[string]struct{}{},
	}
}

var (
	reFlowSplit   = regexp.MustCompile(`[\s,()\[\]]+`)
	reFileExt     = regexp.MustCompile(`(?i)\.(?:java|kt|kts|ts|tsx|js|jsx|mjs|cjs|cs|py|go|rb|php|swift|rs|cpp|cc|cxx|c|h|hpp|scala|lua|dart|vue|svelte)$`)
	reFlowIdent   = regexp.MustCompile(`^[A-Za-z_$][\w$]*(?:(?:::|\.)[\w$]+)*$`)
	reQualSep     = regexp.MustCompile(`::|\.`)
	flowCallable  = map[model.NodeKind]bool{model.KindMethod: true, model.KindFunction: true, model.KindComponent: true, "constructor": true}
	reFlowTestDir = regexp.MustCompile(`(?i)(^|/)(tests?|specs?|__tests__|testdata|mocks?|fixtures?)/`)
	reFlowTestExt = regexp.MustCompile(`(?i)\.(test|spec)\.[a-z]+$`)
)

// buildFlowFromNamedSymbols mirrors buildFlowFromNamedSymbols.
func (h *toolHandlers) buildFlowFromNamedSymbols(queryStr string) flowResult {
	empty := emptyFlow()

	var tokens []string
	tokenSeen := make(map[string]struct{})
	for _, t := range reFlowSplit.Split(queryStr, -1) {
		t = strings.TrimSpace(reFileExt.ReplaceAllString(t, ""))
		if len(t) < 3 || !reFlowIdent.MatchString(t) {
			continue
		}
		if _, ok := tokenSeen[t]; ok {
			continue
		}
		tokenSeen[t] = struct{}{}
		tokens = append(tokens, t)
		if len(tokens) >= 16 {
			break
		}
	}
	if len(tokens) < 2 {
		return empty
	}

	segPool := make(map[string]struct{})
	for _, t := range tokens {
		for _, s := range reQualSep.Split(strings.ToLower(t), -1) {
			if s != "" {
				segPool[s] = struct{}{}
			}
		}
	}

	named := NewSubgraph()
	uniqueNamedNodeIDs := make(map[string]struct{})
	for _, t := range tokens {
		all := h.findAllSymbols(t)
		var cands []model.Node
		for _, n := range all.nodes {
			if flowCallable[n.Kind] {
				cands = append(cands, n)
			}
		}
		specific := len(cands) <= 3
		picks := cands
		if !specific {
			picks = nil
			for _, n := range cands {
				var segs []string
				for _, s := range reQualSep.Split(strings.ToLower(n.QualifiedName), -1) {
					if s != "" {
						segs = append(segs, s)
					}
				}
				container := ""
				if len(segs) >= 2 {
					container = segs[len(segs)-2]
				}
				if container != "" {
					if _, ok := segPool[container]; ok {
						picks = append(picks, n)
					}
				}
			}
		}
		if len(picks) > 6 {
			picks = picks[:6]
		}
		for _, n := range picks {
			named.Set(n)
			if specific {
				uniqueNamedNodeIDs[n.ID] = struct{}{}
			}
		}
		if named.Len() > 40 {
			break
		}
	}
	if named.Len() < 2 {
		return empty
	}

	const maxHops = 7
	const maxBridge = 1
	type parentEntry struct {
		prev string
		edge *model.Edge
		node model.Node
	}
	type queueEntry struct {
		id     string
		depth  int
		streak int
	}

	var best []struct {
		node model.Node
		edge *model.Edge
	}
	seeds := named.Values()
	if len(seeds) > 8 {
		seeds = seeds[:8]
	}
	for _, seed := range seeds {
		parent := map[string]parentEntry{seed.ID: {node: seed}}
		q := []queueEntry{{id: seed.ID}}
		deep := ""
		deepDepth := 0
		for h2 := 0; h2 < len(q) && len(parent) < 1500; h2++ {
			cur := q[h2]
			if cur.id != seed.ID && named.Has(cur.id) && cur.depth > deepDepth {
				deep = cur.id
				deepDepth = cur.depth
			}
			if cur.depth >= maxHops-1 {
				continue
			}
			callees, err := h.backend.GetCallees(cur.id)
			if err != nil {
				continue
			}
			for _, c := range callees {
				if c.Edge.Kind != model.EdgeCalls {
					continue
				}
				if _, ok := parent[c.Node.ID]; ok {
					continue
				}
				newStreak := cur.streak + 1
				if named.Has(c.Node.ID) {
					newStreak = 0
				}
				if newStreak > maxBridge {
					continue
				}
				edge := c.Edge
				parent[c.Node.ID] = parentEntry{prev: cur.id, edge: &edge, node: c.Node}
				q = append(q, queueEntry{id: c.Node.ID, depth: cur.depth + 1, streak: newStreak})
			}
		}
		if deep == "" {
			continue
		}
		var chain []struct {
			node model.Node
			edge *model.Edge
		}
		cur := deep
		for cur != "" {
			p, ok := parent[cur]
			if !ok {
				break
			}
			chain = append(chain, struct {
				node model.Node
				edge *model.Edge
			}{p.node, p.edge})
			cur = p.prev
		}
		// reverse
		for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
			chain[i], chain[j] = chain[j], chain[i]
		}
		if best == nil || len(chain) > len(best) {
			best = chain
		}
	}

	hasMain := len(best) >= 3
	pathIDs := make(map[string]struct{}, len(best))
	for _, s := range best {
		pathIDs[s.node.ID] = struct{}{}
	}

	// Supplementary dynamic-dispatch links.
	var synthLines []string
	synthSeen := make(map[string]struct{})
	for _, n := range named.Values() {
		if len(synthLines) >= 6 {
			break
		}
		callers, _ := h.backend.GetCallers(n.ID)
		callees, _ := h.backend.GetCallees(n.ID)
		for _, pair := range append(callers, callees...) {
			if len(synthLines) >= 6 {
				break
			}
			other, edge := pair.Node, pair.Edge
			if edge.Provenance != "heuristic" || other.ID == n.ID {
				continue
			}
			if hasKey(pathIDs, edge.Source) && hasKey(pathIDs, edge.Target) {
				continue
			}
			src, tgt := n, other
			if edge.Source != n.ID {
				src, tgt = other, n
			}
			key := src.Name + ">" + tgt.Name
			if _, ok := synthSeen[key]; ok {
				continue
			}
			synthSeen[key] = struct{}{}
			label := string(edge.Kind)
			if note := h.synthEdgeNote(&edge); note != nil {
				label = note.compact
			}
			synthLines = append(synthLines, fmt.Sprintf("- %s → %s   [%s]", src.Name, tgt.Name, label))
		}
	}

	if !hasMain && len(synthLines) == 0 {
		return empty
	}

	var out []string
	if hasMain {
		out = append(out, "## Flow (call path among the symbols you queried)", "")
		for i, step := range best {
			if step.edge != nil {
				label := string(step.edge.Kind)
				if sy := h.synthEdgeNote(step.edge); sy != nil {
					label = sy.compact
				}
				out = append(out, fmt.Sprintf("   ↓ %s", label))
			}
			out = append(out, fmt.Sprintf("%d. %s (%s:%d)", i+1, step.node.Name, step.node.FilePath, step.node.StartLine))
		}
		out = append(out, "")
	}
	if len(synthLines) > 0 {
		out = append(out,
			"## Dynamic-dispatch links among your symbols",
			"(synthesized — the indirect hops grep/Read would reconstruct; the `@file:line` is the wiring site)",
			"")
		out = append(out, synthLines...)
		out = append(out, "")
	}
	out = append(out, "> Full source for these symbols is below — the call flow among them, followed by their bodies.", "")

	namedIDs := make(map[string]struct{})
	for _, id := range named.IDs() {
		namedIDs[id] = struct{}{}
	}
	return flowResult{
		text:               strings.Join(out, "\n"),
		pathNodeIDs:        pathIDs,
		namedNodeIDs:       namedIDs,
		uniqueNamedNodeIDs: uniqueNamedNodeIDs,
	}
}

// blastMeaningfulKinds mirrors MEANINGFUL in buildBlastRadiusSection.
var blastMeaningfulKinds = map[model.NodeKind]bool{
	model.KindFunction: true, model.KindMethod: true, model.KindClass: true,
	model.KindInterface: true, model.KindStruct: true, model.KindTrait: true,
	model.KindProtocol: true, model.KindEnum: true, model.KindTypeAlias: true,
	model.KindComponent: true, model.KindConstant: true, model.KindVariable: true,
	model.KindProperty: true, model.KindField: true,
}

// buildBlastRadiusSection mirrors buildBlastRadiusSection.
func (h *toolHandlers) buildBlastRadiusSection(sg *Subgraph) string {
	const rootCap = 5
	const fileCap = 4
	rel := func(p string) string { return strings.ReplaceAll(p, "\\", "/") }

	var roots []model.Node
	for _, id := range sg.Roots {
		if n, ok := sg.Get(id); ok && blastMeaningfulKinds[n.Kind] {
			roots = append(roots, n)
			if len(roots) >= rootCap {
				break
			}
		}
	}
	if len(roots) == 0 {
		return ""
	}

	var entries []string
	for _, root := range roots {
		callers, err := h.backend.GetCallers(root.ID)
		if err != nil {
			continue
		}
		seen := make(map[string]struct{})
		var uniq []model.Node
		for _, c := range callers {
			if _, ok := seen[c.Node.ID]; !ok {
				seen[c.Node.ID] = struct{}{}
				uniq = append(uniq, c.Node)
			}
		}
		if len(uniq) == 0 {
			continue
		}

		var callerFiles []string
		fileSeen := make(map[string]struct{})
		for _, n := range uniq {
			fp := rel(n.FilePath)
			if _, ok := fileSeen[fp]; !ok {
				fileSeen[fp] = struct{}{}
				callerFiles = append(callerFiles, fp)
			}
		}
		var testFiles, nonTest []string
		for _, f := range callerFiles {
			if query.IsTestFile(f) {
				testFiles = append(testFiles, f)
			} else {
				nonTest = append(nonTest, f)
			}
		}

		shownList := nonTest
		if len(shownList) > fileCap {
			shownList = shownList[:fileCap]
		}
		var shownParts []string
		for _, f := range shownList {
			shownParts = append(shownParts, "`"+f+"`")
		}
		shown := strings.Join(shownParts, ", ")
		more := ""
		if len(nonTest) > fileCap {
			more = fmt.Sprintf(" +%d more", len(nonTest)-fileCap)
		}
		where := ""
		if len(nonTest) > 0 {
			where = fmt.Sprintf(" in %s%s", shown, more)
		}
		tests := "; ⚠️ no covering tests found"
		if len(testFiles) > 0 {
			shownTests := testFiles
			if len(shownTests) > fileCap {
				shownTests = shownTests[:fileCap]
			}
			var tp []string
			for _, f := range shownTests {
				tp = append(tp, "`"+f+"`")
			}
			suffix := ""
			if len(testFiles) > fileCap {
				suffix = fmt.Sprintf(" +%d", len(testFiles)-fileCap)
			}
			tests = fmt.Sprintf("; tests: %s%s", strings.Join(tp, ", "), suffix)
		}

		plural := "s"
		if len(uniq) == 1 {
			plural = ""
		}
		entries = append(entries, fmt.Sprintf("- `%s` (%s:%d) — %d caller%s%s%s",
			root.Name, rel(root.FilePath), root.StartLine, len(uniq), plural, where, tests))
	}
	if len(entries) == 0 {
		return ""
	}

	parts := []string{"### Blast radius — what depends on these (update/verify before editing)", ""}
	parts = append(parts, entries...)
	parts = append(parts, "")
	return strings.Join(parts, "\n")
}

// isLowValueExplorePath mirrors handleExplore's isLowValue detector
// (tests/specs plus icon and i18n paths).
var lowValueExplorePatterns = []*regexp.Regexp{
	regexp.MustCompile(`/(tests?|__tests?__|spec)/`),
	regexp.MustCompile(`_test\.go$`),
	regexp.MustCompile(`(?:^|/)test_[^/]+\.py$`),
	regexp.MustCompile(`_test\.py$`),
	regexp.MustCompile(`_spec\.rb$`),
	regexp.MustCompile(`_test\.rb$`),
	regexp.MustCompile(`\.(test|spec)\.[jt]sx?$`),
	regexp.MustCompile(`(test|spec|tests)\.(java|kt|scala)$`),
	regexp.MustCompile(`(tests?|spec)\.cs$`),
	regexp.MustCompile(`tests?\.swift$`),
	regexp.MustCompile(`_test\.dart$`),
	regexp.MustCompile(`\bicons?\b`),
	regexp.MustCompile(`\bi18n\b`),
}

func isLowValueExplorePath(p string) bool {
	lp := strings.ToLower(p)
	for _, re := range lowValueExplorePatterns {
		if re.MatchString(lp) {
			return true
		}
	}
	return false
}

var reQueryMentionsTests = regexp.MustCompile(`(?i)\b(test|tests|testing|spec|verify|verifies)\b`)

// envelopeKinds mirrors ENVELOPE_KINDS.
var envelopeKinds = map[model.NodeKind]bool{
	model.KindFile: true, model.KindModule: true, model.KindClass: true,
	model.KindStruct: true, model.KindInterface: true, model.KindEnum: true,
	model.KindNamespace: true, model.KindProtocol: true, model.KindTrait: true,
	model.KindComponent: true,
}

var callableBodyKinds = map[model.NodeKind]bool{
	model.KindMethod: true, model.KindFunction: true, "constructor": true,
	model.KindComponent: true,
}

// handleExplore is the faithful port of handleExplore.
func (h *toolHandlers) handleExplore(args map[string]any) toolResult {
	queryStr, errRes := h.validateString(args["query"], "query")
	if errRes != nil {
		return *errRes
	}

	stats, statsErr := h.backend.GetStats()
	var budget exploreOutputBudget
	if statsErr == nil {
		budget = getExploreOutputBudget(stats.FileCount)
	} else {
		budget = getExploreOutputBudget(int(^uint(0) >> 1))
	}
	maxFiles := clamp(intArg(args, "maxFiles", budget.defaultMaxFiles), 1, 20)

	subgraph, err := findRelevantContext(h.backend, queryStr, FindOptions{
		SearchLimit:    8,
		TraversalDepth: 3,
		MaxNodes:       200,
		MinScore:       0.2,
	})
	if err != nil {
		return errorResult(fmt.Sprintf("Tool execution failed: %s", err))
	}

	if subgraph.Len() == 0 {
		return textResultOf(fmt.Sprintf("No relevant code found for %q", queryStr))
	}

	// Graph-aware glue: callers/callees of roots living in already-surfaced files.
	glueNodeIDs := make(map[string]struct{})
	subgraphFiles := make(map[string]struct{})
	for _, n := range subgraph.Values() {
		subgraphFiles[n.FilePath] = struct{}{}
	}
	const glueNodeCap = 60
	for _, rootID := range subgraph.Roots {
		if len(glueNodeIDs) >= glueNodeCap {
			break
		}
		var neighbors []model.Node
		callers, err1 := h.backend.GetCallers(rootID)
		callees, err2 := h.backend.GetCallees(rootID)
		if err1 != nil && err2 != nil {
			continue
		}
		for _, c := range callers {
			neighbors = append(neighbors, c.Node)
		}
		for _, c := range callees {
			neighbors = append(neighbors, c.Node)
		}
		for _, nb := range neighbors {
			if len(glueNodeIDs) >= glueNodeCap {
				break
			}
			if subgraph.Has(nb.ID) {
				continue
			}
			if _, ok := subgraphFiles[nb.FilePath]; !ok {
				continue
			}
			subgraph.Set(nb)
			glueNodeIDs[nb.ID] = struct{}{}
		}
	}

	// Named-symbol seeding.
	namedSeedIDs := make(map[string]struct{})
	{
		var tokens []string
		tokenSeen := make(map[string]struct{})
		for _, t := range reFlowSplit.Split(queryStr, -1) {
			t = strings.TrimSpace(reFileExt.ReplaceAllString(t, ""))
			if len(t) < 3 || !reFlowIdent.MatchString(t) {
				continue
			}
			if _, ok := tokenSeen[t]; ok {
				continue
			}
			tokenSeen[t] = struct{}{}
			tokens = append(tokens, t)
			if len(tokens) >= 16 {
				break
			}
		}

		projectNameTokens := h.backend.GetProjectNameTokens()
		var typeTokens []string
		reTypeToken := regexp.MustCompile(`^[A-Z][A-Za-z0-9]{3,}`)
		for _, t := range tokens {
			if reTypeToken.MatchString(t) {
				if _, isProj := projectNameTokens[normalizeNameToken(t)]; !isProj {
					typeTokens = append(typeTokens, t)
				}
			}
		}
		inNamedContext := func(n model.Node) bool {
			for _, ct := range typeTokens {
				lc := strings.ToLower(ct)
				if strings.Contains(strings.ToLower(n.FilePath), lc) ||
					strings.Contains(strings.ToLower(n.QualifiedName), lc) {
					return true
				}
			}
			return false
		}
		isTestPath := func(p string) bool {
			return reFlowTestDir.MatchString(p) || reFlowTestExt.MatchString(p)
		}
		bodyLines := func(n model.Node) int {
			d := n.EndLine - n.StartLine
			if d < 0 {
				return 0
			}
			return d
		}

		for _, t := range tokens {
			isQual := regexp.MustCompile(`[./]|::`).MatchString(t)
			var raw []model.Node
			if isQual {
				raw = h.findAllSymbols(t).nodes
			} else {
				raw, _ = h.backend.GetNodesByName(t)
			}
			var cands []model.Node
			for _, n := range raw {
				if flowCallable[n.Kind] && !isTestPath(n.FilePath) {
					cands = append(cands, n)
				}
			}
			sort.SliceStable(cands, func(i, j int) bool {
				bi, bj := bodyLines(cands[i]), bodyLines(cands[j])
				si, sj := 0, 0
				if bi > 1 {
					si = 1
				}
				if bj > 1 {
					sj = 1
				}
				if si != sj {
					return si > sj
				}
				return bi > bj
			})
			var picks []model.Node
			if len(cands) <= 3 {
				picks = cands
			} else {
				var ctx []model.Node
				for _, n := range cands {
					if inNamedContext(n) {
						ctx = append(ctx, n)
					}
				}
				if len(ctx) > 0 {
					if len(ctx) > 4 {
						ctx = ctx[:4]
					}
					picks = ctx
				} else {
					picks = cands[:1]
				}
			}
			for _, n := range picks {
				if !subgraph.Has(n.ID) {
					subgraph.Set(n)
				}
				namedSeedIDs[n.ID] = struct{}{}
			}
		}
	}

	// Step 2: group nodes by file, score by relevance.
	var fileGroupOrder []string
	fileGroups := make(map[string]*fileGroup)
	entryNodeIDs := make(map[string]struct{})
	for _, id := range subgraph.Roots {
		entryNodeIDs[id] = struct{}{}
	}
	for id := range namedSeedIDs {
		entryNodeIDs[id] = struct{}{}
	}

	connectedToEntry := make(map[string]struct{})
	for _, edge := range subgraph.Edges {
		if hasKey(entryNodeIDs, edge.Source) {
			connectedToEntry[edge.Target] = struct{}{}
		}
		if hasKey(entryNodeIDs, edge.Target) {
			connectedToEntry[edge.Source] = struct{}{}
		}
	}

	for _, node := range subgraph.Values() {
		if node.Kind == model.KindImport || node.Kind == model.KindExport {
			continue
		}
		if isConfigLeafNode(node) {
			continue
		}
		group := fileGroups[node.FilePath]
		if group == nil {
			group = &fileGroup{}
			fileGroups[node.FilePath] = group
			fileGroupOrder = append(fileGroupOrder, node.FilePath)
		}
		group.nodes = append(group.nodes, node)
		switch {
		case hasKey(namedSeedIDs, node.ID):
			group.score += 50
		case hasKey(entryNodeIDs, node.ID):
			group.score += 10
		case hasKey(connectedToEntry, node.ID):
			group.score += 3
		default:
			group.score++
		}
	}

	var relevantFiles []fileEntry
	for _, fp := range fileGroupOrder {
		if fileGroups[fp].score >= 3 {
			relevantFiles = append(relevantFiles, fileEntry{fp, fileGroups[fp]})
		}
	}

	var queryTerms []string
	for _, t := range strings.Fields(strings.ToLower(queryStr)) {
		if len(t) >= 3 {
			queryTerms = append(queryTerms, t)
		}
	}

	// Hard-exclude test/spec files unless the query mentions tests.
	if !reQueryMentionsTests.MatchString(queryStr) {
		nonLow := relevantFiles[:0:0]
		for _, fe := range relevantFiles {
			if !isLowValueExplorePath(fe.path) {
				nonLow = append(nonLow, fe)
			}
		}
		if len(nonLow) >= 2 {
			relevantFiles = nonLow
		}
	}

	// Distinct-term hits per file.
	var uniqueQueryTerms []string
	uqSeen := make(map[string]struct{})
	for _, t := range queryTerms {
		if len(t) >= 3 {
			if _, ok := uqSeen[t]; !ok {
				uqSeen[t] = struct{}{}
				uniqueQueryTerms = append(uniqueQueryTerms, t)
			}
		}
	}
	fileTermHits := make(map[string]int)
	for _, fe := range relevantFiles {
		var nameParts []string
		for _, n := range fe.group.nodes {
			nameParts = append(nameParts, strings.ToLower(n.Name))
		}
		hay := strings.ToLower(fe.path) + " " + strings.Join(nameParts, " ")
		hits := 0
		for _, t := range uniqueQueryTerms {
			if strings.Contains(hay, t) {
				hits++
			}
		}
		fileTermHits[fe.path] = hits
	}

	// PRIMARY relevance: graph connectivity (RWR).
	nodeRwr := computeGraphRelevance(subgraph.IDs(), subgraph.Edges, entryNodeIDs)
	var fileGraphOrder []string
	fileGraphScore := make(map[string]float64)
	for _, node := range subgraph.Values() {
		if _, ok := fileGraphScore[node.FilePath]; !ok {
			fileGraphOrder = append(fileGraphOrder, node.FilePath)
		}
		fileGraphScore[node.FilePath] += nodeRwr[node.ID]
	}
	maxGraph := 0.0
	for _, g := range fileGraphScore {
		if g > maxGraph {
			maxGraph = g
		}
	}

	// Central files: top-2 graph-central files that also match a query term.
	centralFiles := make(map[string]struct{})
	{
		type centralEntry struct {
			fp string
			g  float64
		}
		var cands []centralEntry
		for _, fp := range fileGraphOrder {
			if fileGraphScore[fp] > 0 && fileTermHits[fp] >= 1 {
				cands = append(cands, centralEntry{fp, fileGraphScore[fp]})
			}
		}
		sort.SliceStable(cands, func(i, j int) bool {
			if cands[i].g != cands[j].g {
				return cands[i].g > cands[j].g
			}
			return fileTermHits[cands[i].fp] > fileTermHits[cands[j].fp]
		})
		if len(cands) > 2 {
			cands = cands[:2]
		}
		for _, c := range cands {
			centralFiles[c.fp] = struct{}{}
		}
	}

	// Files that define an entry symbol.
	entryFiles := make(map[string]struct{})
	for id := range entryNodeIDs {
		if n, ok := subgraph.Get(id); ok {
			entryFiles[n.FilePath] = struct{}{}
		}
	}

	// Relevance gate.
	if maxGraph > 0 {
		gated := relevantFiles[:0:0]
		for _, fe := range relevantFiles {
			if fileGraphScore[fe.path] >= maxGraph*0.06 ||
				hasKey(centralFiles, fe.path) ||
				hasKey(entryFiles, fe.path) ||
				fileTermHits[fe.path] >= 2 {
				gated = append(gated, fe)
			}
		}
		if len(gated) >= 2 {
			relevantFiles = gated
		}
	}

	// Named-seed files sort first.
	namedSeedFiles := make(map[string]struct{})
	for id := range namedSeedIDs {
		if n, ok := subgraph.Get(id); ok {
			namedSeedFiles[n.FilePath] = struct{}{}
		}
	}

	sortedFiles := append([]fileEntry(nil), relevantFiles...)
	sort.SliceStable(sortedFiles, func(i, j int) bool {
		a, bf := sortedFiles[i], sortedFiles[j]
		aNamed, bNamed := 0, 0
		if hasKey(namedSeedFiles, a.path) {
			aNamed = 1
		}
		if hasKey(namedSeedFiles, bf.path) {
			bNamed = 1
		}
		if aNamed != bNamed {
			return aNamed > bNamed
		}
		aG, bG := fileGraphScore[a.path], fileGraphScore[bf.path]
		diff := aG - bG
		if diff < 0 {
			diff = -diff
		}
		if diff > maxGraph*0.01 {
			return aG > bG
		}
		aHits, bHits := fileTermHits[a.path], fileTermHits[bf.path]
		if aHits != bHits {
			return aHits > bHits
		}
		aLow, bLow := isLowValueExplorePath(a.path), isLowValueExplorePath(bf.path)
		if aLow != bLow {
			return !aLow
		}
		aGen, bGen := query.IsGeneratedFile(a.path), query.IsGeneratedFile(bf.path)
		if aGen != bGen {
			return !aGen
		}
		if a.group.score != bf.group.score {
			return a.group.score > bf.group.score
		}
		return len(a.group.nodes) > len(bf.group.nodes)
	})

	lines := []string{
		fmt.Sprintf("## Exploration: %s", queryStr),
		"",
		fmt.Sprintf("Found %d symbols across %d files.", subgraph.Len(), len(fileGroups)),
		"",
	}

	if blastRadius := h.buildBlastRadiusSection(subgraph); blastRadius != "" {
		lines = append(lines, blastRadius)
	}

	// Relationships section (gated off for small tiers).
	if budget.includeRelationships {
		var significantEdges []model.Edge
		for _, e := range subgraph.Edges {
			if e.Kind != model.EdgeContains {
				significantEdges = append(significantEdges, e)
			}
		}
		if len(significantEdges) > 0 {
			lines = append(lines, "### Relationships", "")
			type rel struct{ source, target string }
			var kindOrder []model.EdgeKind
			byKind := make(map[model.EdgeKind][]rel)
			for _, edge := range significantEdges {
				sourceNode, ok1 := subgraph.Get(edge.Source)
				targetNode, ok2 := subgraph.Get(edge.Target)
				if !ok1 || !ok2 {
					continue
				}
				if _, ok := byKind[edge.Kind]; !ok {
					kindOrder = append(kindOrder, edge.Kind)
				}
				byKind[edge.Kind] = append(byKind[edge.Kind], rel{sourceNode.Name, targetNode.Name})
			}
			for _, kind := range kindOrder {
				edges := byKind[kind]
				shown := edges
				if len(shown) > budget.maxEdgesPerRelationshipKind {
					shown = shown[:budget.maxEdgesPerRelationshipKind]
				}
				lines = append(lines, fmt.Sprintf("**%s:**", kind))
				for _, e := range shown {
					lines = append(lines, fmt.Sprintf("- %s → %s", e.source, e.target))
				}
				if len(edges) > budget.maxEdgesPerRelationshipKind {
					lines = append(lines, fmt.Sprintf("- ... and %d more", len(edges)-budget.maxEdgesPerRelationshipKind))
				}
				lines = append(lines, "")
			}
		}
	}

	// Flow spine (also gates adaptive sizing).
	flow := h.buildFlowFromNamedSymbols(queryStr)

	// Polymorphic-sibling detection caches.
	const minSiblings = 3
	siblingSuper := make(map[string]bool)
	isPolymorphicSibling := func(nodes []model.Node) bool {
		for _, n := range nodes {
			edges, err := h.backend.GetOutgoingEdges(n.ID, nil)
			if err != nil {
				continue
			}
			for _, e := range edges {
				if e.Kind != model.EdgeImplements && e.Kind != model.EdgeExtends {
					continue
				}
				many, ok := siblingSuper[e.Target]
				if !ok {
					incoming, err := h.backend.GetIncomingEdges(e.Target, nil)
					count := 0
					if err == nil {
						for _, x := range incoming {
							if x.Kind == model.EdgeImplements || x.Kind == model.EdgeExtends {
								count++
							}
						}
					}
					many = count >= minSiblings
					siblingSuper[e.Target] = many
				}
				if many {
					return true
				}
			}
		}
		return false
	}

	superMany := make(map[string]bool)
	definesPolymorphicSupertype := func(nodes []model.Node) bool {
		for _, n := range nodes {
			switch n.Kind {
			case model.KindClass, model.KindInterface, model.KindStruct,
				model.KindTrait, model.KindProtocol, model.KindTypeAlias:
			default:
				continue
			}
			many, ok := superMany[n.ID]
			if !ok {
				incoming, err := h.backend.GetIncomingEdges(n.ID, nil)
				count := 0
				if err == nil {
					for _, x := range incoming {
						if x.Kind == model.EdgeImplements || x.Kind == model.EdgeExtends {
							count++
						}
					}
				}
				many = count >= minSiblings
				superMany[n.ID] = many
			}
			if many {
				return true
			}
		}
		return false
	}

	lines = append(lines, "### Source Code", "",
		"> The code below is the **verbatim, current on-disk source** of these files — re-read from disk on this call and line-numbered, byte-for-byte identical to what the Read tool returns. It is NOT a summary, outline, or stale cache. Treat each block as a Read you have already performed: do not Read a file shown here.",
		"")

	totalChars := len(strings.Join(lines, "\n"))
	filesIncluded := 0
	anyFileTrimmed := false
	projectRoot := h.backend.GetProjectRoot()
	withLineNumbers := exploreLineNumbersEnabled()

	for _, fe := range sortedFiles {
		filePath, group := fe.path, fe.group
		if filesIncluded >= maxFiles {
			break
		}
		fileNecessary := false
		for _, n := range group.nodes {
			if hasKey(entryNodeIDs, n.ID) || hasKey(flow.pathNodeIDs, n.ID) || hasKey(flow.uniqueNamedNodeIDs, n.ID) {
				fileNecessary = true
				break
			}
		}
		if !fileNecessary && float64(totalChars) > float64(budget.maxOutputChars)*0.9 {
			continue
		}

		absPath := validatePathWithinRoot(projectRoot, filePath)
		if absPath == "" {
			continue
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}
		fileContent := string(data)
		fileLines := strings.Split(fileContent, "\n")
		lang := ""
		if len(group.nodes) > 0 {
			lang = string(group.nodes[0].Language)
		}

		// Adaptive sizing: skeletonize off-spine polymorphic siblings and
		// on-spine god-files.
		spareNamed := false
		for _, n := range group.nodes {
			if hasKey(flow.uniqueNamedNodeIDs, n.ID) {
				spareNamed = true
				break
			}
		}
		fileDefinesSuper := definesPolymorphicSupertype(group.nodes)
		spared := spareNamed && !fileDefinesSuper
		hasSpineNode := false
		for _, n := range group.nodes {
			if hasKey(flow.pathNodeIDs, n.ID) {
				hasSpineNode = true
				break
			}
		}
		namedBodyChars := 0
		hasOffPathUniqueNamed := false
		for _, n := range group.nodes {
			if !callableBodyKinds[n.Kind] {
				continue
			}
			onSpine := hasKey(flow.pathNodeIDs, n.ID)
			uniqueNamed := hasKey(flow.uniqueNamedNodeIDs, n.ID)
			if onSpine || uniqueNamed {
				start, end := n.StartLine-1, n.EndLine
				if start < 0 {
					start = 0
				}
				if end > len(fileLines) {
					end = len(fileLines)
				}
				if end > start {
					namedBodyChars += len(strings.Join(fileLines[start:end], "\n"))
				}
			}
			if uniqueNamed && !onSpine {
				hasOffPathUniqueNamed = true
			}
		}
		onSpineGodFile := hasSpineNode && namedBodyChars > budget.maxCharsPerFile && hasOffPathUniqueNamed

		if adaptiveExploreEnabled() && len(flow.pathNodeIDs) > 0 &&
			(onSpineGodFile || (!hasSpineNode && isPolymorphicSibling(group.nodes) && !spared)) {
			if section, names, ok := h.renderSkeleton(group.nodes, fileLines, flow, budget, fileDefinesSuper, withLineNumbers); ok {
				tag := "skeleton (signatures only — codegraph_explore a name for its full body; do NOT Read)"
				if section.hasBodies {
					tag = "focused (the methods you named in full, the rest as signatures — codegraph_explore a signature by name for its body; do NOT Read)"
				}
				lines = append(lines,
					fmt.Sprintf("#### %s — %s · %s", filePath, names, tag), "",
					"```"+lang, section.text, "```", "")
				totalChars += len(section.text) + 120
				filesIncluded++
				continue
			}
		}

		// Whole-file rule.
		isCentralFile := hasKey(centralFiles, filePath)
		wholeFileMaxLines := 220
		if isCentralFile {
			wholeFileMaxLines = 280
		}
		var wholeFileMaxChars int
		if isCentralFile {
			rem := budget.maxOutputChars - totalChars - 200
			if rem < 0 {
				rem = 0
			}
			capped := int(float64(budget.maxCharsPerFile)*1.5 + 0.5)
			if rem < capped {
				wholeFileMaxChars = rem
			} else {
				wholeFileMaxChars = capped
			}
		} else {
			wholeFileMaxChars = budget.maxCharsPerFile * 3
		}
		if len(fileLines) <= wholeFileMaxLines && len(fileContent) <= wholeFileMaxChars {
			body := strings.TrimRight(fileContent, "\n")
			wholeSection := body
			if withLineNumbers {
				wholeSection = numberSourceLines(body, 1)
			}
			var uniqSymbols []string
			uniqSeen := make(map[string]struct{})
			for _, n := range group.nodes {
				if n.Kind == model.KindImport || n.Kind == model.KindExport {
					continue
				}
				s := fmt.Sprintf("%s(%s)", n.Name, n.Kind)
				if _, ok := uniqSeen[s]; !ok {
					uniqSeen[s] = struct{}{}
					uniqSymbols = append(uniqSymbols, s)
				}
			}
			headerNames := uniqSymbols
			if len(headerNames) > budget.maxSymbolsInFileHeader {
				headerNames = headerNames[:budget.maxSymbolsInFileHeader]
			}
			omitted := len(uniqSymbols) - len(headerNames)
			headerText := strings.Join(headerNames, ", ")
			if omitted > 0 {
				headerText = fmt.Sprintf("%s, +%d more", headerText, omitted)
			}
			wholeHeader := fmt.Sprintf("#### %s — %s", filePath, headerText)

			if !fileNecessary && totalChars+len(wholeSection)+200 > budget.maxOutputChars {
				anyFileTrimmed = true
				continue
			}
			lines = append(lines, wholeHeader, "", "```"+lang, wholeSection, "```", "")
			totalChars += len(wholeSection) + 200
			filesIncluded++
			continue
		}

		// Cluster nearby symbols.
		section, symbols, included := h.renderClusters(filePath, group, fileLines, subgraph, flow,
			entryNodeIDs, glueNodeIDs, connectedToEntry, budget, totalChars, withLineNumbers)
		if !included {
			continue
		}
		if section.trimmed {
			anyFileTrimmed = true
		}
		if !fileNecessary && totalChars+len(section.text)+200 > budget.maxOutputChars {
			anyFileTrimmed = true
			continue
		}
		lines = append(lines,
			fmt.Sprintf("#### %s — %s", filePath, symbols), "",
			"```"+lang, section.text, "```", "")
		totalChars += len(section.text) + 200
		filesIncluded++
	}

	// Remaining files list.
	if budget.includeAdditionalFiles {
		remaining := append([]fileEntry(nil), sortedFiles[minInt(filesIncluded, len(sortedFiles)):]...)
		for _, fp := range fileGroupOrder {
			if fileGroups[fp].score < 3 {
				remaining = append(remaining, fileEntry{fp, fileGroups[fp]})
			}
		}
		// peripheral files sorted by score desc — already appended after
		// remainingRelevant per upstream; sort just the peripheral tail.
		if len(remaining) > 0 {
			lines = append(lines, "### Not shown above — explore these names for their source", "")
			shown := remaining
			if len(shown) > 10 {
				shown = shown[:10]
			}
			for _, fe := range shown {
				var symbols []string
				for _, n := range fe.group.nodes {
					symbols = append(symbols, fmt.Sprintf("%s:%d", n.Name, n.StartLine))
				}
				lines = append(lines, fmt.Sprintf("- %s: %s", fe.path, strings.Join(symbols, ", ")))
			}
			if len(remaining) > 10 {
				lines = append(lines, fmt.Sprintf("- ... and %d more files", len(remaining)-10))
			}
		}
	}

	if budget.includeCompletenessSignal {
		lines = append(lines, "", "---",
			fmt.Sprintf("> **Complete source for %d files is included above — do NOT re-read them.** If your question also needs files/symbols listed under \"Not shown above\" (or any area this call didn't cover), make ANOTHER codegraph_explore targeting those names — it returns the same source with line numbers and is cheaper and more complete than reading. Reserve Read for a single specific line range explore can't surface.", filesIncluded))
	} else if anyFileTrimmed {
		lines = append(lines, "",
			"> Some file sections were trimmed for size. For a specific symbol you still need, run another `codegraph_explore` (or `codegraph_node`) with its exact name — line-numbered source, cheaper and more complete than Read.")
	}

	if budget.includeBudgetNote && statsErr == nil {
		callBudget := getExploreBudget(stats.FileCount)
		lines = append(lines, "",
			fmt.Sprintf("> **Explore budget: %d calls for this project (%s files indexed).** Each call covers ~6 files; if your question spans more, spend your remaining calls on the uncovered area BEFORE falling back to Read — another explore is cheaper and more complete than reading those files. Synthesize once you've used %d.",
				callBudget, localeString(stats.FileCount), callBudget))
	}

	output := flow.text + strings.Join(lines, "\n")
	hardCeiling := minInt(int(float64(budget.maxOutputChars)*1.5+0.5), 25000)
	if len(output) > hardCeiling {
		cut := output[:hardCeiling]
		lastSection := strings.LastIndex(cut, "\n#### ")
		boundary := strings.LastIndex(cut, "\n")
		if lastSection > hardCeiling/2 {
			boundary = lastSection
		}
		safe := cut
		if boundary > 0 {
			safe = cut[:boundary]
		}
		return textResultOf(safe + "\n\n... (output truncated to budget; the source above is complete and verbatim — treat it as already Read. For any area not covered, run another codegraph_explore with the specific names — do NOT Read these files.)")
	}
	return textResultOf(output)
}

type skeletonSection struct {
	text      string
	hasBodies bool
}

// renderSkeleton implements the adaptive (skeletonizing) per-symbol render.
func (h *toolHandlers) renderSkeleton(groupNodes []model.Node, fileLines []string, flow flowResult,
	budget exploreOutputBudget, fileDefinesSuper, withLineNumbers bool) (skeletonSection, string, bool) {

	var syms []model.Node
	for _, n := range groupNodes {
		if n.Kind != model.KindImport && n.Kind != model.KindExport && n.StartLine > 0 {
			syms = append(syms, n)
		}
	}
	sort.SliceStable(syms, func(i, j int) bool { return syms[i].StartLine < syms[j].StartLine })

	prio := func(n model.Node) int {
		if !callableBodyKinds[n.Kind] {
			return 99
		}
		if hasKey(flow.pathNodeIDs, n.ID) {
			return 0
		}
		if hasKey(flow.uniqueNamedNodeIDs, n.ID) {
			return 1
		}
		if fileDefinesSuper && hasKey(flow.namedNodeIDs, n.ID) {
			return 2
		}
		return 99
	}

	bodyCap := int(float64(budget.maxCharsPerFile)*1.5 + 0.5)
	bodyIDs := make(map[string]struct{})
	bodyChars := 0
	var bodyCands []model.Node
	for _, n := range syms {
		if prio(n) < 99 && n.EndLine >= n.StartLine {
			bodyCands = append(bodyCands, n)
		}
	}
	sort.SliceStable(bodyCands, func(i, j int) bool { return prio(bodyCands[i]) < prio(bodyCands[j]) })
	for _, n := range bodyCands {
		start, end := n.StartLine-1, n.EndLine
		if start < 0 {
			start = 0
		}
		if end > len(fileLines) {
			end = len(fileLines)
		}
		sz := len(strings.Join(fileLines[start:end], "\n"))
		if bodyChars+sz > bodyCap && len(bodyIDs) > 0 {
			continue
		}
		bodyIDs[n.ID] = struct{}{}
		bodyChars += sz
	}

	var skel []string
	coveredUntil := 0
	sigCount, sigDropped := 0, 0
	sigMax := maxInt(12, budget.maxSymbolsInFileHeader*2)
	for _, n := range syms {
		if n.StartLine <= coveredUntil {
			continue
		}
		if _, ok := bodyIDs[n.ID]; ok {
			end := n.EndLine
			if end > len(fileLines) {
				end = len(fileLines)
			}
			body := strings.Join(fileLines[n.StartLine-1:end], "\n")
			if withLineNumbers {
				skel = append(skel, numberSourceLines(body, n.StartLine))
			} else {
				skel = append(skel, body)
			}
			coveredUntil = end
		} else {
			lineNo := n.StartLine
			for k := 0; k < 4; k++ {
				idx := n.StartLine - 1 + k
				if idx < len(fileLines) && strings.Contains(fileLines[idx], n.Name) {
					lineNo = n.StartLine + k
					break
				}
			}
			if lineNo <= coveredUntil {
				continue
			}
			if sigCount >= sigMax {
				sigDropped++
				continue
			}
			sig := ""
			if lineNo-1 < len(fileLines) {
				sig = strings.TrimSpace(fileLines[lineNo-1])
			}
			if sig != "" {
				if withLineNumbers {
					skel = append(skel, fmt.Sprintf("%d\t%s", lineNo, sig))
				} else {
					skel = append(skel, sig)
				}
				sigCount++
			}
		}
	}
	if sigDropped > 0 {
		skel = append(skel, fmt.Sprintf("… +%d more (signatures elided)", sigDropped))
	}
	if len(skel) == 0 {
		return skeletonSection{}, "", false
	}

	var names []string
	nameSeen := make(map[string]struct{})
	for _, n := range groupNodes {
		if n.Kind == model.KindImport || n.Kind == model.KindExport {
			continue
		}
		if _, ok := nameSeen[n.Name]; !ok {
			nameSeen[n.Name] = struct{}{}
			names = append(names, n.Name)
		}
	}
	if len(names) > budget.maxSymbolsInFileHeader {
		names = names[:budget.maxSymbolsInFileHeader]
	}
	return skeletonSection{text: strings.Join(skel, "\n"), hasBodies: len(bodyIDs) > 0},
		strings.Join(names, ", "), true
}

type clusterSection struct {
	text    string
	trimmed bool
}

// renderClusters implements the cluster-based render for large files.
func (h *toolHandlers) renderClusters(filePath string, group *fileGroup, fileLines []string, subgraph *Subgraph, flow flowResult,
	entryNodeIDs, glueNodeIDs, connectedToEntry map[string]struct{},
	budget exploreOutputBudget, totalChars int, withLineNumbers bool) (clusterSection, string, bool) {

	type rng struct {
		start, end int
		name       string
		kind       string
		importance int
	}

	rangeNodes := NewSubgraph()
	for _, n := range group.nodes {
		if n.StartLine > 0 && n.EndLine > 0 {
			rangeNodes.Set(n)
		}
	}
	for id := range flow.namedNodeIDs {
		if rangeNodes.Has(id) {
			continue
		}
		n, err := h.backend.GetNodeByID(id)
		if err != nil || n == nil {
			continue
		}
		if n.FilePath == filePath && n.StartLine > 0 && n.EndLine > 0 {
			rangeNodes.Set(*n)
		}
	}

	var ranges []rng
	for _, n := range rangeNodes.Values() {
		if envelopeKinds[n.Kind] && (n.EndLine-n.StartLine+1) > len(fileLines)/2 {
			continue
		}
		importance := 1
		switch {
		case hasKey(entryNodeIDs, n.ID):
			importance = 10
		case hasKey(flow.namedNodeIDs, n.ID):
			importance = 9
		case hasKey(glueNodeIDs, n.ID):
			importance = 6
		case hasKey(connectedToEntry, n.ID):
			importance = 3
		}
		ranges = append(ranges, rng{n.StartLine, n.EndLine, n.Name, string(n.Kind), importance})
	}

	edgeLines := make(map[string]struct{})
	for _, node := range group.nodes {
		outgoing, err := h.backend.GetOutgoingEdges(node.ID, nil)
		if err != nil {
			continue
		}
		for _, edge := range outgoing {
			if edge.Line <= 0 || edge.Kind == model.EdgeContains {
				continue
			}
			key := fmt.Sprintf("%d:%s", edge.Line, edge.Target)
			if _, ok := edgeLines[key]; ok {
				continue
			}
			edgeLines[key] = struct{}{}
			targetName := string(edge.Kind)
			if tn, ok := subgraph.Get(edge.Target); ok {
				targetName = tn.Name
			}
			ranges = append(ranges, rng{edge.Line, edge.Line, targetName, string(edge.Kind), 2})
		}
	}

	sort.SliceStable(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	if len(ranges) == 0 {
		return clusterSection{}, "", false
	}

	type cluster struct {
		start, end    int
		symbols       []string
		score         int
		maxImportance int
	}
	var clusters []cluster
	current := cluster{ranges[0].start, ranges[0].end,
		[]string{fmt.Sprintf("%s(%s)", ranges[0].name, ranges[0].kind)},
		ranges[0].importance, ranges[0].importance}
	for _, r := range ranges[1:] {
		if r.start <= current.end+budget.gapThreshold {
			if r.end > current.end {
				current.end = r.end
			}
			current.symbols = append(current.symbols, fmt.Sprintf("%s(%s)", r.name, r.kind))
			current.score += r.importance
			if r.importance > current.maxImportance {
				current.maxImportance = r.importance
			}
		} else {
			clusters = append(clusters, current)
			current = cluster{r.start, r.end,
				[]string{fmt.Sprintf("%s(%s)", r.name, r.kind)},
				r.importance, r.importance}
		}
	}
	clusters = append(clusters, current)

	const contextPadding = 3
	buildSection := func(c cluster) string {
		startIdx := c.start - 1 - contextPadding
		if startIdx < 0 {
			startIdx = 0
		}
		endIdx := c.end + contextPadding
		if endIdx > len(fileLines) {
			endIdx = len(fileLines)
		}
		slice := strings.Join(fileLines[startIdx:endIdx], "\n")
		if withLineNumbers {
			return numberSourceLines(slice, startIdx+1)
		}
		return slice
	}
	const gapMarker = "\n\n... (gap) ...\n\n"

	type rankedCluster struct {
		idx  int
		span int
		c    cluster
	}
	ranked := make([]rankedCluster, len(clusters))
	for i, c := range clusters {
		ranked[i] = rankedCluster{i, c.end - c.start + 1, c}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, bf := ranked[i], ranked[j]
		if a.c.maxImportance != bf.c.maxImportance {
			return a.c.maxImportance > bf.c.maxImportance
		}
		da := float64(a.c.score) / float64(a.span)
		db := float64(bf.c.score) / float64(bf.span)
		if da != db {
			return da > db
		}
		if a.c.score != bf.c.score {
			return a.c.score > bf.c.score
		}
		return a.span < bf.span
	})

	fileBudget := budget.maxCharsPerFile
	if rem := budget.maxOutputChars - totalChars - 200; rem < fileBudget {
		if rem < 0 {
			rem = 0
		}
		fileBudget = rem
	}
	chosen := make(map[int]struct{})
	projectedChars := 0
	for _, rc := range ranked {
		sectionLen := len(buildSection(rc.c))
		if len(chosen) > 0 {
			sectionLen += len(gapMarker)
		}
		if len(chosen) == 0 {
			chosen[rc.idx] = struct{}{}
			projectedChars += sectionLen
			continue
		}
		if projectedChars+sectionLen > fileBudget {
			continue
		}
		chosen[rc.idx] = struct{}{}
		projectedChars += sectionLen
	}

	var fileSection strings.Builder
	var allSymbols []string
	for i, c := range clusters {
		if _, ok := chosen[i]; !ok {
			continue
		}
		if fileSection.Len() > 0 {
			fileSection.WriteString(gapMarker)
		}
		fileSection.WriteString(buildSection(c))
		allSymbols = append(allSymbols, c.symbols...)
	}

	trimmed := len(chosen) < len(clusters)

	symbolCounts := make(map[string]int)
	var symbolOrder []string
	for _, s := range allSymbols {
		if _, ok := symbolCounts[s]; !ok {
			symbolOrder = append(symbolOrder, s)
		}
		symbolCounts[s]++
	}
	sort.SliceStable(symbolOrder, func(i, j int) bool {
		return symbolCounts[symbolOrder[i]] > symbolCounts[symbolOrder[j]]
	})
	headerSymbols := symbolOrder
	if len(headerSymbols) > budget.maxSymbolsInFileHeader {
		headerSymbols = headerSymbols[:budget.maxSymbolsInFileHeader]
	}
	omitted := len(symbolOrder) - len(headerSymbols)
	headerSuffix := strings.Join(headerSymbols, ", ")
	if omitted > 0 {
		headerSuffix = fmt.Sprintf("%s, +%d more", headerSuffix, omitted)
	}

	return clusterSection{text: fileSection.String(), trimmed: trimmed}, headerSuffix, true
}

// localeString mirrors Number.prototype.toLocaleString for en-US integers
// (comma thousands separators).
func localeString(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
