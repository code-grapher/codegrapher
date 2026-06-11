package mcp

import (
	"path"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/query"
)

// -----------------------------------------------------------------------
// findRelevantContext — faithful port of ContextBuilder.findRelevantContext
// (src/context/index.ts). Hybrid search: exact symbol lookup, definition
// prefix search, per-term text search, multi-term re-ranking, CamelCase
// boundary matching, compound term matching — then type-hierarchy expansion,
// BFS traversal from entry points, and budget trimming.
// -----------------------------------------------------------------------

// highValueNodeKinds mirrors HIGH_VALUE_NODE_KINDS.
var highValueNodeKinds = []model.NodeKind{
	model.KindFunction, model.KindMethod, model.KindClass, model.KindInterface,
	model.KindTypeAlias, model.KindStruct, model.KindTrait,
	model.KindComponent, model.KindRoute, model.KindVariable,
	model.KindConstant, model.KindEnum, model.KindModule, model.KindNamespace,
}

// definitionNodeKinds is the class-like kind set used by the prefix and
// CamelCase search channels.
var definitionNodeKinds = []model.NodeKind{
	model.KindClass, model.KindInterface, model.KindStruct, model.KindTrait,
	model.KindProtocol, model.KindEnum, model.KindTypeAlias,
}

// textSearchKinds is the wide kind list used by the text channel when no
// explicit kind filter is set (imports excluded — they flood FTS results).
var textSearchKinds = []model.NodeKind{
	model.KindFile, model.KindModule, model.KindClass, model.KindStruct,
	model.KindInterface, model.KindTrait, model.KindProtocol,
	model.KindFunction, model.KindMethod, model.KindProperty, model.KindField,
	model.KindVariable, model.KindConstant, model.KindEnum,
	model.KindEnumMember, model.KindTypeAlias, model.KindNamespace,
	model.KindExport, model.KindRoute, model.KindComponent,
}

// typeHierarchyKinds gates the hierarchy expansion step.
var typeHierarchyKinds = map[model.NodeKind]bool{
	model.KindClass: true, model.KindInterface: true, model.KindStruct: true,
	model.KindTrait: true, model.KindProtocol: true,
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

func ceilFloat(f float64) int {
	n := int(f)
	if float64(n) < f {
		n++
	}
	return n
}

// findRelevantContext builds the relevance subgraph for a query.
func findRelevantContext(b GraphBackend, queryStr string, opts FindOptions) (*Subgraph, error) {
	// DEFAULT_FIND_OPTIONS
	if opts.SearchLimit == 0 {
		opts.SearchLimit = 3
	}
	if opts.TraversalDepth == 0 {
		opts.TraversalDepth = 1
	}
	if opts.MaxNodes == 0 {
		opts.MaxNodes = 20
	}
	if opts.MinScore == 0 {
		opts.MinScore = 0.3
	}
	if opts.NodeKinds == nil {
		opts.NodeKinds = highValueNodeKinds
	}

	sg := NewSubgraph()
	sg.Confidence = "high"

	if strings.TrimSpace(queryStr) == "" {
		return sg, nil
	}

	// === HYBRID SEARCH ===

	// Step 1: Extract potential symbol names from query.
	symbolsFromQuery := extractSymbolsFromQuery(queryStr)

	// Step 2: Exact matches for extracted symbols, with co-location boosting.
	var exactMatches []model.SearchResult
	if len(symbolsFromQuery) > 0 {
		var kinds []model.NodeKind
		if len(opts.NodeKinds) > 0 {
			kinds = opts.NodeKinds
		}
		em, err := b.FindNodesByExactName(symbolsFromQuery, kinds, ceilFloat(float64(opts.SearchLimit)*5))
		if err == nil {
			exactMatches = em
			if len(exactMatches) > 1 {
				fileSymbolCounts := make(map[string]map[string]struct{})
				for _, r := range exactMatches {
					names := fileSymbolCounts[r.Node.FilePath]
					if names == nil {
						names = make(map[string]struct{})
						fileSymbolCounts[r.Node.FilePath] = names
					}
					names[strings.ToLower(r.Node.Name)] = struct{}{}
				}
				for i := range exactMatches {
					symbolCount := len(fileSymbolCounts[exactMatches[i].Node.FilePath])
					if symbolCount > 1 {
						exactMatches[i].Score += float64(symbolCount-1) * 20
					}
				}
				sort.SliceStable(exactMatches, func(i, j int) bool {
					return exactMatches[i].Score > exactMatches[j].Score
				})
			}
			if cap2 := ceilFloat(float64(opts.SearchLimit) * 2); len(exactMatches) > cap2 {
				exactMatches = exactMatches[:cap2]
			}
		}
	}

	// Step 2b: definition (class/interface) prefix search with stem variants.
	if len(symbolsFromQuery) > 0 {
		expanded := append([]string(nil), symbolsFromQuery...)
		expandedSeen := make(map[string]struct{}, len(expanded))
		for _, s := range expanded {
			expandedSeen[s] = struct{}{}
		}
		for _, sym := range symbolsFromQuery {
			for _, variant := range getStemVariants(sym) {
				if _, ok := expandedSeen[variant]; !ok {
					expandedSeen[variant] = struct{}{}
					expanded = append(expanded, variant)
				}
			}
		}
		for _, sym := range expanded {
			titleCased := titleCase(sym)
			if titleCased == sym {
				continue // already title-case — handled by exact match
			}
			prefixResults, err := b.SearchNodes(titleCased, definitionNodeKinds, 30)
			if err != nil {
				continue
			}
			var matched []model.SearchResult
			for _, r := range prefixResults {
				if strings.HasPrefix(strings.ToLower(r.Node.Name), strings.ToLower(titleCased)) {
					brevityBonus := 10 - float64(len(r.Node.Name)-len(titleCased))/3
					if brevityBonus < 0 {
						brevityBonus = 0
					}
					matched = append(matched, model.SearchResult{Node: r.Node, Score: r.Score + 15 + brevityBonus})
				}
			}
			sort.SliceStable(matched, func(i, j int) bool { return matched[i].Score > matched[j].Score })
			limitN := ceilFloat(float64(opts.SearchLimit))
			if len(matched) > limitN {
				matched = matched[:limitN]
			}
			for _, r := range matched {
				exists := false
				for _, e := range exactMatches {
					if e.Node.ID == r.Node.ID {
						exists = true
						break
					}
				}
				if !exists {
					exactMatches = append(exactMatches, r)
				}
			}
		}
		sort.SliceStable(exactMatches, func(i, j int) bool { return exactMatches[i].Score > exactMatches[j].Score })
		if cap3 := ceilFloat(float64(opts.SearchLimit) * 3); len(exactMatches) > cap3 {
			exactMatches = exactMatches[:cap3]
		}
	}

	// Step 3: per-term text search with multi-term boosting.
	var textResults []model.SearchResult
	{
		searchTerms := extractSearchTerms(queryStr, true)
		if len(searchTerms) > 0 {
			searchKinds := textSearchKinds
			if len(opts.NodeKinds) > 0 {
				searchKinds = opts.NodeKinds
			}
			type termEntry struct {
				result   model.SearchResult
				termHits int
			}
			var termOrder []string
			termMap := make(map[string]*termEntry)
			for _, term := range searchTerms {
				termResults, err := b.SearchNodes(term, searchKinds, opts.SearchLimit*2)
				if err != nil {
					continue
				}
				for _, r := range termResults {
					if existing, ok := termMap[r.Node.ID]; ok {
						existing.termHits++
						if r.Score > existing.result.Score {
							existing.result.Score = r.Score
						}
					} else {
						termMap[r.Node.ID] = &termEntry{result: r, termHits: 1}
						termOrder = append(termOrder, r.Node.ID)
					}
				}
			}
			for _, id := range termOrder {
				e := termMap[id]
				textResults = append(textResults, model.SearchResult{
					Node:  e.result.Node,
					Score: e.result.Score + float64(e.termHits-1)*5,
				})
			}
			sort.SliceStable(textResults, func(i, j int) bool { return textResults[i].Score > textResults[j].Score })
			if cap2 := opts.SearchLimit * 2; len(textResults) > cap2 {
				textResults = textResults[:cap2]
			}
		}
	}

	// Step 4: merge channels, max score on duplicates.
	resultIdx := make(map[string]int)
	var searchResults []model.SearchResult
	for _, r := range exactMatches {
		if i, ok := resultIdx[r.Node.ID]; ok {
			if r.Score > searchResults[i].Score {
				searchResults[i].Score = r.Score
			}
		} else {
			resultIdx[r.Node.ID] = len(searchResults)
			searchResults = append(searchResults, r)
		}
	}
	for _, r := range textResults {
		if i, ok := resultIdx[r.Node.ID]; ok {
			if r.Score > searchResults[i].Score {
				searchResults[i].Score = r.Score
			}
		} else {
			resultIdx[r.Node.ID] = len(searchResults)
			searchResults = append(searchResults, r)
		}
	}

	queryLower := strings.ToLower(queryStr)
	isTestQuery := strings.Contains(queryLower, "test") || strings.Contains(queryLower, "spec")

	// Deprioritize test files early.
	if !isTestQuery {
		for i := range searchResults {
			if query.IsTestFile(searchResults[i].Node.FilePath) {
				searchResults[i].Score *= 0.3
			}
		}
	}

	// Core-directory boost.
	if dominant, err := b.GetDominantFile(); err == nil && dominant != nil &&
		dominant.EdgeCount >= 3*dominant.NextEdgeCount {
		if slash := strings.LastIndex(dominant.FilePath, "/"); slash > 0 {
			coreDir := dominant.FilePath[:slash+1]
			for i := range searchResults {
				if strings.HasPrefix(searchResults[i].Node.FilePath, coreDir) {
					searchResults[i].Score += 25
				}
			}
		}
	}

	// Step 5a: multi-term co-occurrence re-ranking.
	queryTermsForBoost := extractSearchTerms(queryStr, true)
	if len(queryTermsForBoost) >= 2 {
		// Group stem variants of the same root word.
		sorted := append([]string(nil), queryTermsForBoost...)
		sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
		assigned := make(map[string]struct{})
		var termGroups [][]string
		for _, term := range sorted {
			if _, ok := assigned[term]; ok {
				continue
			}
			group := []string{term}
			assigned[term] = struct{}{}
			for _, other := range sorted {
				if _, ok := assigned[other]; ok {
					continue
				}
				if strings.Contains(term, other) || strings.Contains(other, term) {
					group = append(group, other)
					assigned[other] = struct{}{}
				}
			}
			termGroups = append(termGroups, group)
		}

		exactMatchIDs := make(map[string]struct{}, len(exactMatches))
		for _, r := range exactMatches {
			exactMatchIDs[r.Node.ID] = struct{}{}
		}
		distinctiveTokens := make(map[string]struct{})
		for _, s := range symbolsFromQuery {
			if isDistinctiveIdentifier(s) {
				distinctiveTokens[strings.ToLower(s)] = struct{}{}
			}
		}
		distinctiveExactMatchIDs := make(map[string]struct{})
		for _, r := range exactMatches {
			if _, ok := distinctiveTokens[strings.ToLower(r.Node.Name)]; ok {
				distinctiveExactMatchIDs[r.Node.ID] = struct{}{}
			}
		}

		for i := range searchResults {
			nameLower := strings.ToLower(searchResults[i].Node.Name)
			dirSegments := strings.Split(strings.ToLower(path.Dir(searchResults[i].Node.FilePath)), "/")
			matchCount := 0
			for _, group := range termGroups {
				groupMatches := false
				for _, term := range group {
					inName := strings.Contains(nameLower, term)
					inDir := false
					for _, seg := range dirSegments {
						if seg == term {
							inDir = true
							break
						}
					}
					if inName || inDir {
						groupMatches = true
						break
					}
				}
				if groupMatches {
					matchCount++
				}
			}
			id := searchResults[i].Node.ID
			switch {
			case matchCount >= 2:
				searchResults[i].Score *= 1 + float64(matchCount)*0.5
			case hasKey(distinctiveExactMatchIDs, id):
				// keep full score
			case hasKey(exactMatchIDs, id):
				searchResults[i].Score *= 0.3
			default:
				searchResults[i].Score *= 0.6
			}
		}
		sort.SliceStable(searchResults, func(i, j int) bool { return searchResults[i].Score > searchResults[j].Score })
	}

	// Step 5b: CamelCase-boundary matching via LIKE.
	if len(symbolsFromQuery) > 0 {
		camelSearched := make(map[string]struct{})
		searchIDSet := make(map[string]struct{}, len(searchResults))
		for _, r := range searchResults {
			searchIDSet[r.Node.ID] = struct{}{}
		}
		type camelEntry struct {
			result    model.SearchResult
			termCount int
		}
		var camelOrder []string
		camelNodeTerms := make(map[string]*camelEntry)
		maxCamelPerTerm := ceilDiv(opts.SearchLimit, 2)

		for _, sym := range symbolsFromQuery {
			titleCased := titleCase(sym)
			if len(titleCased) < 3 {
				continue
			}
			termKey := strings.ToLower(titleCased)
			if _, ok := camelSearched[termKey]; ok {
				continue
			}
			camelSearched[termKey] = struct{}{}

			likeResults, err := b.FindNodesByNameSubstring(titleCased, definitionNodeKinds, 200, true)
			if err != nil {
				continue
			}
			var termCandidates []model.SearchResult
			for _, r := range likeResults {
				name := r.Node.Name
				idx := strings.Index(name, titleCased)
				if idx <= 0 {
					continue
				}
				prev := name[idx-1]
				if !((prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z')) {
					continue
				}
				if _, ok := searchIDSet[r.Node.ID]; ok {
					continue
				}
				if query.IsTestFile(r.Node.FilePath) && !isTestQuery {
					continue
				}
				pathScore := query.ScorePathRelevance(r.Node.FilePath, queryStr, nil)
				brevityBonus := 6 - float64(len(name)-len(titleCased))/4
				if brevityBonus < 0 {
					brevityBonus = 0
				}
				termCandidates = append(termCandidates, model.SearchResult{Node: r.Node, Score: 8 + brevityBonus + pathScore})
			}
			sort.SliceStable(termCandidates, func(i, j int) bool { return termCandidates[i].Score > termCandidates[j].Score })

			accumPerTerm := maxCamelPerTerm * 4
			if len(termCandidates) > accumPerTerm {
				termCandidates = termCandidates[:accumPerTerm]
			}
			for _, r := range termCandidates {
				if existing, ok := camelNodeTerms[r.Node.ID]; ok {
					existing.termCount++
				} else {
					camelNodeTerms[r.Node.ID] = &camelEntry{result: r, termCount: 1}
					camelOrder = append(camelOrder, r.Node.ID)
				}
			}
		}

		var camelResults []model.SearchResult
		for _, id := range camelOrder {
			info := camelNodeTerms[id]
			score := info.result.Score*float64(1+info.termCount) + float64(info.termCount-1)*30
			camelResults = append(camelResults, model.SearchResult{Node: info.result.Node, Score: score})
		}
		sort.SliceStable(camelResults, func(i, j int) bool { return camelResults[i].Score > camelResults[j].Score })
		maxCamelTotal := opts.SearchLimit
		if len(camelResults) > maxCamelTotal {
			camelResults = camelResults[:maxCamelTotal]
		}
		for _, r := range camelResults {
			searchResults = append(searchResults, r)
			searchIDSet[r.Node.ID] = struct{}{}
		}

		// Step 5c: compound term matching — 2+ query terms at any position.
		if len(symbolsFromQuery) >= 2 {
			type compoundEntry struct {
				node  model.Node
				terms map[string]struct{}
			}
			var compoundOrder []string
			compoundTermMap := make(map[string]*compoundEntry)
			for _, sym := range symbolsFromQuery {
				titleCased := titleCase(sym)
				if len(titleCased) < 3 {
					continue
				}
				likeResults, err := b.FindNodesByNameSubstring(titleCased, definitionNodeKinds, 200, false)
				if err != nil {
					continue
				}
				for _, r := range likeResults {
					if _, ok := searchIDSet[r.Node.ID]; ok {
						continue
					}
					if query.IsTestFile(r.Node.FilePath) && !isTestQuery {
						continue
					}
					if entry, ok := compoundTermMap[r.Node.ID]; ok {
						entry.terms[titleCased] = struct{}{}
					} else {
						compoundTermMap[r.Node.ID] = &compoundEntry{
							node:  r.Node,
							terms: map[string]struct{}{titleCased: {}},
						}
						compoundOrder = append(compoundOrder, r.Node.ID)
					}
				}
			}
			var compoundResults []model.SearchResult
			for _, id := range compoundOrder {
				entry := compoundTermMap[id]
				if len(entry.terms) >= 2 {
					pathScore := query.ScorePathRelevance(entry.node.FilePath, queryStr, nil)
					brevityBonus := 6 - float64(len(entry.node.Name))/8
					if brevityBonus < 0 {
						brevityBonus = 0
					}
					compoundResults = append(compoundResults, model.SearchResult{
						Node:  entry.node,
						Score: 10 + float64(len(entry.terms)-1)*20 + pathScore + brevityBonus,
					})
				}
			}
			sort.SliceStable(compoundResults, func(i, j int) bool { return compoundResults[i].Score > compoundResults[j].Score })
			maxCompound := ceilDiv(opts.SearchLimit, 2)
			if len(compoundResults) > maxCompound {
				compoundResults = compoundResults[:maxCompound]
			}
			for _, r := range compoundResults {
				searchResults = append(searchResults, r)
				searchIDSet[r.Node.ID] = struct{}{}
			}
		}
	}

	// Final sort and truncation.
	sort.SliceStable(searchResults, func(i, j int) bool { return searchResults[i].Score > searchResults[j].Score })
	if cap3 := opts.SearchLimit * 3; len(searchResults) > cap3 {
		searchResults = searchResults[:cap3]
	}

	// Filter by minimum score.
	filteredResults := searchResults[:0:0]
	for _, r := range searchResults {
		if r.Score >= opts.MinScore {
			filteredResults = append(filteredResults, r)
		}
	}

	// Resolve imports/exports to their definitions.
	filteredResults = resolveImportsToDefinitions(b, filteredResults)

	// Cap entry points.
	if len(filteredResults) > opts.SearchLimit {
		filteredResults = filteredResults[:opts.SearchLimit]
	}

	// Confidence signal.
	confidence := "high"
	{
		var confTerms []string
		for _, t := range extractSearchTerms(queryStr, false) {
			if len(t) >= 3 {
				confTerms = append(confTerms, t)
			}
		}
		if len(confTerms) >= 2 && len(filteredResults) > 0 {
			distinctive := make(map[string]struct{})
			for _, s := range symbolsFromQuery {
				if isDistinctiveIdentifier(s) {
					distinctive[strings.ToLower(s)] = struct{}{}
				}
			}
			anyStrong := false
			for _, r := range filteredResults {
				if _, ok := distinctive[strings.ToLower(r.Node.Name)]; ok {
					anyStrong = true
					break
				}
				nameLower := strings.ToLower(r.Node.Name)
				dirSegs := strings.Split(strings.ToLower(path.Dir(r.Node.FilePath)), "/")
				hits := 0
				for _, t := range confTerms {
					inDir := false
					for _, seg := range dirSegs {
						if seg == t {
							inDir = true
							break
						}
					}
					if strings.Contains(nameLower, t) || inDir {
						hits++
						if hits >= 2 {
							break
						}
					}
				}
				if hits >= 2 {
					anyStrong = true
					break
				}
			}
			if !anyStrong {
				confidence = "low"
			}
		}
	}
	sg.Confidence = confidence

	// Add entry points to subgraph.
	for _, r := range filteredResults {
		sg.Set(r.Node)
		sg.Roots = append(sg.Roots, r.Node.ID)
	}

	// Expand type hierarchy for class/interface entry points.
	maxHierarchyNodes := ceilDiv(opts.MaxNodes, 4)
	hierarchyNodesAdded := 0
	for _, r := range filteredResults {
		if hierarchyNodesAdded >= maxHierarchyNodes {
			break
		}
		if !typeHierarchyKinds[r.Node.Kind] {
			continue
		}
		hierarchy, err := b.GetTypeHierarchy(r.Node.ID)
		if err != nil {
			continue
		}
		for _, n := range hierarchy.Values() {
			if !sg.Has(n.ID) {
				sg.Set(n)
				hierarchyNodesAdded++
			}
		}
		for _, edge := range hierarchy.Edges {
			if !edgeExists(sg.Edges, edge) {
				sg.Edges = append(sg.Edges, edge)
			}
		}
	}

	// Pass 2: expand hierarchy of newly-discovered parent types.
	if hierarchyNodesAdded > 0 {
		rootSet := make(map[string]struct{}, len(sg.Roots))
		for _, id := range sg.Roots {
			rootSet[id] = struct{}{}
		}
		var pass2 []model.Node
		for _, n := range sg.Values() {
			if typeHierarchyKinds[n.Kind] {
				if _, isRoot := rootSet[n.ID]; !isRoot {
					pass2 = append(pass2, n)
				}
			}
		}
		for _, candidate := range pass2 {
			if hierarchyNodesAdded >= maxHierarchyNodes {
				break
			}
			siblingHierarchy, err := b.GetTypeHierarchy(candidate.ID)
			if err != nil {
				continue
			}
			for _, n := range siblingHierarchy.Values() {
				if !sg.Has(n.ID) && hierarchyNodesAdded < maxHierarchyNodes {
					sg.Set(n)
					hierarchyNodesAdded++
				}
			}
			for _, edge := range siblingHierarchy.Edges {
				if sg.Has(edge.Source) && sg.Has(edge.Target) && !edgeExists(sg.Edges, edge) {
					sg.Edges = append(sg.Edges, edge)
				}
			}
		}
	}

	// Traverse from each entry point.
	for _, r := range filteredResults {
		limit := ceilDiv(opts.MaxNodes, maxInt(1, len(filteredResults)))
		traversal, err := b.TraverseBFS(r.Node.ID, TraversalOptions{
			MaxDepth:  opts.TraversalDepth,
			EdgeKinds: opts.EdgeKinds,
			NodeKinds: opts.NodeKinds,
			Direction: "both",
			Limit:     limit,
		})
		if err != nil {
			return nil, err
		}
		for _, n := range traversal.Values() {
			if !sg.Has(n.ID) {
				sg.Set(n)
			}
		}
		for _, edge := range traversal.Edges {
			if !edgeExists(sg.Edges, edge) {
				sg.Edges = append(sg.Edges, edge)
			}
		}
	}

	// Trim to max nodes: prioritize entry points and direct neighbors.
	if sg.Len() > opts.MaxNodes {
		priority := make(map[string]struct{}, len(sg.Roots))
		var priorityOrder []string
		addPriority := func(id string) {
			if _, ok := priority[id]; !ok {
				priority[id] = struct{}{}
				priorityOrder = append(priorityOrder, id)
			}
		}
		for _, id := range sg.Roots {
			addPriority(id)
		}
		for _, edge := range sg.Edges {
			if _, ok := priority[edge.Source]; ok {
				addPriority(edge.Target)
			}
			if _, ok := priority[edge.Target]; ok {
				addPriority(edge.Source)
			}
		}

		trimmed := NewSubgraph()
		trimmed.Roots = sg.Roots
		trimmed.Confidence = sg.Confidence
		for _, id := range priorityOrder {
			if n, ok := sg.Get(id); ok && trimmed.Len() < opts.MaxNodes {
				trimmed.Set(n)
			}
		}
		for _, n := range sg.Values() {
			if trimmed.Len() >= opts.MaxNodes {
				break
			}
			if !trimmed.Has(n.ID) {
				trimmed.Set(n)
			}
		}
		for _, e := range sg.Edges {
			if trimmed.Has(e.Source) && trimmed.Has(e.Target) {
				trimmed.Edges = append(trimmed.Edges, e)
			}
		}
		sg = trimmed
	}

	// Per-file diversity cap.
	maxPerFile := maxInt(5, ceilFloat(float64(opts.MaxNodes)*0.2))
	{
		var fileOrder []string
		fileCounts := make(map[string][]string)
		for _, n := range sg.Values() {
			if _, ok := fileCounts[n.FilePath]; !ok {
				fileOrder = append(fileOrder, n.FilePath)
			}
			fileCounts[n.FilePath] = append(fileCounts[n.FilePath], n.ID)
		}
		rootSet := make(map[string]struct{}, len(sg.Roots))
		for _, id := range sg.Roots {
			rootSet[id] = struct{}{}
		}
		kindPriority := map[model.NodeKind]int{
			model.KindClass: 3, model.KindInterface: 3, model.KindStruct: 3,
			model.KindTrait: 3, model.KindProtocol: 3, model.KindEnum: 3,
			model.KindMethod: 1, model.KindFunction: 1,
			model.KindProperty: 0, model.KindField: 0, model.KindVariable: 0,
		}
		for _, fp := range fileOrder {
			nodeIDs := fileCounts[fp]
			if len(nodeIDs) <= maxPerFile {
				continue
			}
			score := func(id string) int {
				s := 0
				if _, ok := rootSet[id]; ok {
					s += 10
				}
				if n, ok := sg.Get(id); ok {
					s += kindPriority[n.Kind]
				}
				return s
			}
			sort.SliceStable(nodeIDs, func(i, j int) bool { return score(nodeIDs[i]) > score(nodeIDs[j]) })
			for _, id := range nodeIDs[maxPerFile:] {
				sg.Delete(id)
			}
		}
	}

	// Non-production node cap.
	if !isTestQuery {
		maxNonProd := maxInt(3, ceilFloat(float64(opts.MaxNodes)*0.15))
		var nonProdIDs []string
		for _, n := range sg.Values() {
			if query.IsTestFile(n.FilePath) {
				nonProdIDs = append(nonProdIDs, n.ID)
			}
		}
		if len(nonProdIDs) > maxNonProd {
			for _, id := range nonProdIDs[maxNonProd:] {
				sg.Delete(id)
				for i, rootID := range sg.Roots {
					if rootID == id {
						sg.Roots = append(sg.Roots[:i], sg.Roots[i+1:]...)
						break
					}
				}
			}
		}
	}

	// Re-filter edges after caps.
	kept := sg.Edges[:0:0]
	for _, e := range sg.Edges {
		if sg.Has(e.Source) && sg.Has(e.Target) {
			kept = append(kept, e)
		}
	}
	sg.Edges = kept

	// Edge recovery between selected nodes.
	recoveryKinds := []model.EdgeKind{
		model.EdgeCalls, model.EdgeExtends, model.EdgeImplements,
		model.EdgeReferences, model.EdgeOverrides,
	}
	recovered, err := b.FindEdgesBetweenNodes(sg.IDs(), recoveryKinds)
	if err == nil {
		existing := make(map[string]struct{}, len(sg.Edges))
		for _, e := range sg.Edges {
			existing[edgeKey(e)] = struct{}{}
		}
		for _, e := range recovered {
			k := edgeKey(e)
			if _, ok := existing[k]; !ok {
				sg.Edges = append(sg.Edges, e)
				existing[k] = struct{}{}
			}
		}
	}

	return sg, nil
}

// resolveImportsToDefinitions mirrors ContextBuilder.resolveImportsToDefinitions.
func resolveImportsToDefinitions(b GraphBackend, results []model.SearchResult) []model.SearchResult {
	var resolved []model.SearchResult
	seenIDs := make(map[string]struct{})

	for _, r := range results {
		n := r.Node
		if n.Kind != model.KindImport && n.Kind != model.KindExport {
			if _, ok := seenIDs[n.ID]; !ok {
				seenIDs[n.ID] = struct{}{}
				resolved = append(resolved, r)
			}
			continue
		}

		edgeKind := model.EdgeImports
		if n.Kind == model.KindExport {
			edgeKind = model.EdgeExports
		}
		edges, err := b.GetOutgoingEdges(n.ID, []model.EdgeKind{edgeKind})
		if err != nil {
			continue
		}
		for _, edge := range edges {
			target, err := b.GetNodeByID(edge.Target)
			if err != nil || target == nil {
				continue
			}
			if _, ok := seenIDs[target.ID]; !ok {
				seenIDs[target.ID] = struct{}{}
				resolved = append(resolved, model.SearchResult{Node: *target, Score: r.Score})
			}
		}
	}
	return resolved
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

func edgeKey(e model.Edge) string {
	return e.Source + ":" + e.Target + ":" + string(e.Kind)
}

func edgeExists(edges []model.Edge, e model.Edge) bool {
	for _, x := range edges {
		if x.Source == e.Source && x.Target == e.Target && x.Kind == e.Kind {
			return true
		}
	}
	return false
}

func hasKey(m map[string]struct{}, k string) bool {
	_, ok := m[k]
	return ok
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
