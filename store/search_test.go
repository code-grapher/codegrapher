package store

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

// boundedEditDistance is a local copy used by search tests to avoid an
// import cycle (store ← query ← store).
func boundedEditDistance(a, b string, maxDist int) int {
	ar, br := []rune(a), []rune(b)
	al, bl := len(ar), len(br)
	diff := al - bl
	if diff < 0 {
		diff = -diff
	}
	if diff > maxDist {
		return maxDist + 1
	}
	if al == 0 {
		return bl
	}
	if bl == 0 {
		return al
	}
	prev := make([]int, bl+1)
	cur := make([]int, bl+1)
	for j := 0; j <= bl; j++ {
		prev[j] = j
	}
	for i := 1; i <= al; i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= bl; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			ins, del, sub := cur[j-1]+1, prev[j]+1, prev[j-1]+cost
			cur[j] = ins
			if del < cur[j] {
				cur[j] = del
			}
			if sub < cur[j] {
				cur[j] = sub
			}
			if cur[j] < rowMin {
				rowMin = cur[j]
			}
		}
		if rowMin > maxDist {
			return maxDist + 1
		}
		prev, cur = cur, prev
	}
	return prev[bl]
}

// seedSearchFixture inserts a small set of nodes into s for search testing.
func seedSearchFixture(t *testing.T, s *Store) {
	t.Helper()
	nodes := []model.Node{
		{
			ID: "fn:authenticate", Kind: model.KindFunction, Name: "authenticate",
			QualifiedName: "authenticate", FilePath: "auth/auth.go",
			Language: model.LangGo, StartLine: 10, EndLine: 20,
			UpdatedAt: fixedNow(),
		},
		{
			ID: "fn:authorize", Kind: model.KindFunction, Name: "authorize",
			QualifiedName: "authorize", FilePath: "auth/auth.go",
			Language: model.LangGo, StartLine: 25, EndLine: 35,
			UpdatedAt: fixedNow(),
		},
		{
			ID: "cls:AuthService", Kind: model.KindClass, Name: "AuthService",
			QualifiedName: "AuthService", FilePath: "auth/service.ts",
			Language: model.LangTypeScript, StartLine: 1, EndLine: 50,
			UpdatedAt: fixedNow(),
		},
		{
			ID: "fn:handleRequest", Kind: model.KindFunction, Name: "handleRequest",
			QualifiedName: "handleRequest", FilePath: "server/handler.go",
			Language: model.LangGo, StartLine: 5, EndLine: 15,
			UpdatedAt: fixedNow(),
		},
		{
			ID: "fn:processData", Kind: model.KindFunction, Name: "processData",
			QualifiedName: "processData", FilePath: "core/processor.go",
			Language: model.LangGo, StartLine: 1, EndLine: 30,
			Docstring: "Process incoming data records",
			UpdatedAt: fixedNow(),
		},
		{
			ID: "file:auth.go", Kind: model.KindFile, Name: "auth.go",
			QualifiedName: "auth/auth.go", FilePath: "auth/auth.go",
			Language: model.LangGo, StartLine: 1, EndLine: 100,
			UpdatedAt: fixedNow(),
		},
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
}

// TestSearchFTS_ReturnsMatchingNodes verifies that FTS returns nodes whose name
// contains the search term.
func TestSearchFTS_ReturnsMatchingNodes(t *testing.T) {
	s := newTestStore(t)
	seedSearchFixture(t, s)

	results, err := s.SearchFTS("authenticate", nil, nil, 10, 0)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result for 'authenticate', got none")
	}
	found := false
	for _, r := range results {
		if r.Node.Name == "authenticate" {
			found = true
			if r.Score <= 0 {
				t.Errorf("expected positive BM25 score, got %f", r.Score)
			}
		}
	}
	if !found {
		t.Errorf("authenticate not in FTS results: %v", results)
	}
}

// TestSearchFTS_PrefixMatch verifies prefix-based FTS matching (the "*" suffix).
func TestSearchFTS_PrefixMatch(t *testing.T) {
	s := newTestStore(t)
	seedSearchFixture(t, s)

	// "auth" should match both authenticate and authorize via prefix.
	results, err := s.SearchFTS("auth", nil, nil, 10, 0)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	nameSet := make(map[string]bool)
	for _, r := range results {
		nameSet[r.Node.Name] = true
	}
	if !nameSet["authenticate"] {
		t.Error("expected 'authenticate' in prefix results for 'auth'")
	}
	if !nameSet["authorize"] {
		t.Error("expected 'authorize' in prefix results for 'auth'")
	}
}

// TestSearchFTS_KindFilter verifies that kind filters restrict results.
func TestSearchFTS_KindFilter(t *testing.T) {
	s := newTestStore(t)
	seedSearchFixture(t, s)

	results, err := s.SearchFTS("auth", []model.NodeKind{model.KindFunction}, nil, 10, 0)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	for _, r := range results {
		if r.Node.Kind != model.KindFunction {
			t.Errorf("expected only function nodes, got %s (%s)", r.Node.Name, r.Node.Kind)
		}
	}
}

// TestSearchFTS_LangFilter verifies that language filters restrict results.
func TestSearchFTS_LangFilter(t *testing.T) {
	s := newTestStore(t)
	seedSearchFixture(t, s)

	results, err := s.SearchFTS("auth", nil, []model.Language{model.LangTypeScript}, 10, 0)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	for _, r := range results {
		if r.Node.Language != model.LangTypeScript {
			t.Errorf("expected only typescript nodes, got %s (%s)", r.Node.Name, r.Node.Language)
		}
	}
}

// TestSearchFTS_EmptyQueryReturnsNil verifies that an empty text returns nil.
func TestSearchFTS_EmptyQueryReturnsNil(t *testing.T) {
	s := newTestStore(t)
	seedSearchFixture(t, s)

	results, err := s.SearchFTS("", nil, nil, 10, 0)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty text, got %v", results)
	}
}

// TestSearchLike_ReturnsSubstringMatches verifies LIKE fallback returns substring matches.
func TestSearchLike_ReturnsSubstringMatches(t *testing.T) {
	s := newTestStore(t)
	seedSearchFixture(t, s)

	results, err := s.SearchLike("handle", nil, nil, 10, 0)
	if err != nil {
		t.Fatalf("SearchLike: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Node.Name == "handleRequest" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'handleRequest' in SearchLike results for 'handle'")
	}
}

// TestSearchLike_ExactMatchScoresHighest verifies that exact-name match scores
// higher than prefix and contains matches.
func TestSearchLike_ExactMatchScoresHighest(t *testing.T) {
	s := newTestStore(t)
	// Insert three nodes: exact, prefix, contains.
	nodes := []model.Node{
		{ID: "fn:process", Kind: model.KindFunction, Name: "process",
			QualifiedName: "process", FilePath: "a.go", Language: model.LangGo,
			StartLine: 1, EndLine: 2, UpdatedAt: fixedNow()},
		{ID: "fn:processData", Kind: model.KindFunction, Name: "processData",
			QualifiedName: "processData", FilePath: "b.go", Language: model.LangGo,
			StartLine: 1, EndLine: 2, UpdatedAt: fixedNow()},
		{ID: "fn:preProcessData", Kind: model.KindFunction, Name: "preProcessData",
			QualifiedName: "preProcessData", FilePath: "c.go", Language: model.LangGo,
			StartLine: 1, EndLine: 2, UpdatedAt: fixedNow()},
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	results, err := s.SearchLike("process", nil, nil, 10, 0)
	if err != nil {
		t.Fatalf("SearchLike: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].Node.Name != "process" {
		t.Errorf("expected exact match 'process' first, got %q", results[0].Node.Name)
	}
}

// TestSearchFuzzy_FindsTypos verifies that the fuzzy search finds close matches.
func TestSearchFuzzy_FindsTypos(t *testing.T) {
	s := newTestStore(t)
	seedSearchFixture(t, s)

	// "athenticate" is one deletion away from "authenticate" (edit distance 1).
	results, err := s.SearchFuzzy("athenticate", nil, nil, 10, boundedEditDistance)
	if err != nil {
		t.Fatalf("SearchFuzzy: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Node.Name == "authenticate" {
			found = true
		}
	}
	if !found {
		names := make([]string, len(results))
		for i, r := range results {
			names[i] = r.Node.Name
		}
		t.Errorf("expected 'authenticate' in fuzzy results for 'athenticate', got: %v", names)
	}
}

// TestSearchFuzzy_RejectsDistantStrings verifies that fuzzy search doesn't
// match strings that are too different.
func TestSearchFuzzy_RejectsDistantStrings(t *testing.T) {
	s := newTestStore(t)
	// Insert a node with a clearly different name.
	nodes := []model.Node{
		{ID: "fn:xyzzy", Kind: model.KindFunction, Name: "xyzzy",
			QualifiedName: "xyzzy", FilePath: "a.go", Language: model.LangGo,
			StartLine: 1, EndLine: 2, UpdatedAt: fixedNow()},
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	results, err := s.SearchFuzzy("authenticate", nil, nil, 10, boundedEditDistance)
	if err != nil {
		t.Fatalf("SearchFuzzy: %v", err)
	}
	for _, r := range results {
		if r.Node.Name == "xyzzy" {
			t.Error("'xyzzy' should not match 'authenticate' fuzzy search")
		}
	}
}

// TestSearchAllByFilters_KindFilter verifies kind filtering without text.
func TestSearchAllByFilters_KindFilter(t *testing.T) {
	s := newTestStore(t)
	seedSearchFixture(t, s)

	results, err := s.SearchAllByFilters([]model.NodeKind{model.KindClass}, nil, 10)
	if err != nil {
		t.Fatalf("SearchAllByFilters: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected class results")
	}
	for _, r := range results {
		if r.Node.Kind != model.KindClass {
			t.Errorf("expected only class nodes, got %s", r.Node.Kind)
		}
		if r.Score != 1.0 {
			t.Errorf("expected score 1.0, got %f", r.Score)
		}
	}
}

// TestExactNameCaseInsensitive verifies case-insensitive exact name matching.
func TestExactNameCaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	seedSearchFixture(t, s)

	// "AUTHENTICATE" should match "authenticate" case-insensitively.
	nodes, err := s.ExactNameCaseInsensitive("AUTHENTICATE", nil, nil, 10)
	if err != nil {
		t.Fatalf("ExactNameCaseInsensitive: %v", err)
	}
	if len(nodes) == 0 {
		t.Error("expected case-insensitive match for AUTHENTICATE")
	}
	for _, n := range nodes {
		if n.Name != "authenticate" {
			t.Errorf("unexpected node: %s", n.Name)
		}
	}
}
