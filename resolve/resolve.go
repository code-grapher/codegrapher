// Package resolve turns the store's unresolved_refs table into edges.
//
// Resolve reads all unresolved references, attempts to locate a matching node
// for each one, inserts the resulting edges, then clears the table.
package resolve

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// Stats reports how many references were resolved vs. left unresolved.
type Stats struct {
	Resolved   int
	Unresolved int
}

// Resolve processes all unresolved_refs in s, inserts edges for every ref that
// can be matched to a node, and then clears the table. projectRoot is used to
// read go.mod (if present) for cross-package Go module resolution.
func Resolve(s *store.Store, projectRoot string) (Stats, error) {
	refs, err := s.GetUnresolvedReferences()
	if err != nil {
		return Stats{}, err
	}

	goModulePath := loadGoModulePath(projectRoot)

	// Build a per-file import-mapping cache (populated lazily from the store).
	importCache := make(map[string][]importMapping) // filePath → mappings

	var edges []model.Edge
	stats := Stats{}

	for _, ref := range refs {
		edge := resolveRef(ref, s, projectRoot, goModulePath, importCache)
		if edge != nil {
			edges = append(edges, *edge)
			stats.Resolved++
		} else {
			stats.Unresolved++
		}
	}

	if err := s.InsertEdges(edges); err != nil {
		return stats, err
	}

	// Heuristic pass 1: Go implicit interface satisfaction (struct → interface).
	// Must be inserted before pass 2 so the interface-override pass can read them.
	implEdges, err := synthesizeGoImplementsEdges(s)
	if err != nil {
		return stats, err
	}
	if err := s.InsertEdges(implEdges); err != nil {
		return stats, err
	}

	// Heuristic pass 2: interface-method → concrete-method call edges.
	// Reads the implements edges inserted in pass 1.
	overrideEdges, err := synthesizeGoInterfaceOverrideEdges(s)
	if err != nil {
		return stats, err
	}
	if err := s.InsertEdges(overrideEdges); err != nil {
		return stats, err
	}

	if err := s.ClearUnresolvedReferences(); err != nil {
		return stats, err
	}

	return stats, nil
}

// resolveRef attempts to resolve one unresolved reference into an edge.
// Returns nil when no matching node is found (the ref is silently dropped).
//
// When ref.Language or ref.FilePath is not set (the extractor leaves them
// blank on call-site refs), the from-node is looked up to fill them in.
func resolveRef(
	ref model.UnresolvedReference,
	s *store.Store,
	projectRoot string,
	goModulePath string,
	importCache map[string][]importMapping,
) *model.Edge {
	// Fill in missing FilePath / Language from the from-node.
	if ref.FilePath == "" || ref.Language == "" || ref.Language == model.LangUnknown {
		if n, err := s.GetNodeByID(ref.FromNodeID); err == nil && n != nil {
			if ref.FilePath == "" {
				ref.FilePath = n.FilePath
			}
			if ref.Language == "" || ref.Language == model.LangUnknown {
				ref.Language = n.Language
			}
		}
	}

	switch ref.Language {
	case model.LangGo:
		return resolveGoRef(ref, s, projectRoot, goModulePath, importCache)
	default:
		return resolveGenericRef(ref, s)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Go resolution
// ──────────────────────────────────────────────────────────────────────────────

// goBuiltIns is the set of Go built-in functions and identifiers that should
// never produce edges.
var goBuiltIns = map[string]bool{
	"make": true, "new": true, "len": true, "cap": true, "append": true,
	"copy": true, "delete": true, "close": true, "panic": true, "recover": true,
	"print": true, "println": true, "complex": true, "real": true, "imag": true,
	"error": true, "nil": true, "true": true, "false": true, "iota": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "float32": true, "float64": true, "complex64": true, "complex128": true,
	"string": true, "bool": true, "byte": true, "rune": true, "any": true,
	"clear": true,
}

// goStdlibPackages is the set of Go standard-library package names.  A dotted
// call whose package alias appears here will not be resolved.
var goStdlibPackages = map[string]bool{
	"fmt": true, "os": true, "io": true, "net": true, "http": true, "log": true,
	"math": true, "sort": true, "sync": true, "time": true, "path": true,
	"bytes": true, "strings": true, "strconv": true, "errors": true, "context": true,
	"json": true, "xml": true, "csv": true, "html": true, "template": true,
	"regexp": true, "reflect": true, "runtime": true, "testing": true, "flag": true,
	"bufio": true, "crypto": true, "encoding": true, "filepath": true, "hash": true,
	"mime": true, "rand": true, "signal": true, "sql": true, "syscall": true,
	"unicode": true, "unsafe": true, "atomic": true, "binary": true, "debug": true,
	"exec": true, "heap": true, "ring": true, "scanner": true, "tar": true,
	"zip": true, "gzip": true, "zlib": true, "tls": true, "url": true,
	"user": true, "pprof": true, "trace": true, "ast": true, "build": true,
	"parser": true, "printer": true, "token": true, "types": true, "cgo": true,
	"plugin": true, "race": true, "ioutil": true, "utf8": true, "utf16": true,
}

func resolveGoRef(
	ref model.UnresolvedReference,
	s *store.Store,
	projectRoot string,
	goModulePath string,
	importCache map[string][]importMapping,
) *model.Edge {
	switch ref.ReferenceKind {
	case model.EdgeImports:
		return resolveGoImportsRef(ref, s)
	case model.EdgeInstantiates:
		return resolveGoInstantiatesRef(ref, s, projectRoot, goModulePath, importCache)
	case model.EdgeReferences:
		return resolveGoReferencesRef(ref, s, projectRoot, goModulePath, importCache)
	case model.EdgeCalls:
		return resolveGoCallsRef(ref, s, projectRoot, goModulePath, importCache)
	default:
		return resolveGenericRef(ref, s)
	}
}

// resolveGoImportsRef resolves an `imports` ref from a file node to an import
// node in the same file with matching name.
func resolveGoImportsRef(ref model.UnresolvedReference, s *store.Store) *model.Edge {
	nodes, err := s.GetNodesByFile(ref.FilePath)
	if err != nil {
		return nil
	}
	for _, n := range nodes {
		if n.Kind == model.KindImport && n.Name == ref.ReferenceName {
			return &model.Edge{
				Source: ref.FromNodeID,
				Target: n.ID,
				Kind:   model.EdgeImports,
				Line:   ref.Line,
				Column: ref.Column,
			}
		}
	}
	return nil
}

// resolveGoInstantiatesRef resolves composite-literal instantiation refs.
// Handles both bare names ("SpecConfig") and qualified names ("projectdef.SpecConfig").
// For qualified names, resolves cross-package via the import map (upstream
// resolveGoCrossPackageReference in import-resolver.ts).
func resolveGoInstantiatesRef(
	ref model.UnresolvedReference,
	s *store.Store,
	projectRoot string,
	goModulePath string,
	importCache map[string][]importMapping,
) *model.Edge {
	name := ref.ReferenceName
	if goBuiltIns[name] {
		return nil
	}

	// Qualified name (e.g. "projectdef.SpecConfig"): resolve cross-package.
	if dotIdx := strings.Index(name, "."); dotIdx > 0 {
		pkgAlias := name[:dotIdx]
		typeName := name[dotIdx+1:]
		if goBuiltIns[pkgAlias] || goStdlibPackages[pkgAlias] || goBuiltIns[typeName] {
			return nil
		}
		mappings := getImportMappings(ref.FilePath, projectRoot, importCache)
		importPath := findImportByAlias(pkgAlias, mappings)
		if importPath == "" || isStdlibImport(importPath) {
			return nil
		}
		relDir := importPathToRelDir(importPath, goModulePath)
		if relDir == "" {
			return nil
		}
		candidates, err := s.GetNodesByName(typeName)
		if err != nil || len(candidates) == 0 {
			return nil
		}
		for i := range candidates {
			n := &candidates[i]
			if n.Language != model.LangGo {
				continue
			}
			nodeDir := filepath.Dir(filepath.ToSlash(n.FilePath))
			if nodeDir == relDir || strings.HasPrefix(nodeDir+"/", relDir+"/") {
				return &model.Edge{
					Source: ref.FromNodeID,
					Target: n.ID,
					Kind:   model.EdgeInstantiates,
					Line:   ref.Line,
					Column: ref.Column,
				}
			}
		}
		return nil
	}

	candidates, err := s.GetNodesByName(name)
	if err != nil || len(candidates) == 0 {
		return nil
	}
	target := pickBestGoType(candidates, ref.FilePath, model.LangGo)
	if target == nil {
		return nil
	}
	return &model.Edge{
		Source: ref.FromNodeID,
		Target: target.ID,
		Kind:   model.EdgeInstantiates,
		Line:   ref.Line,
		Column: ref.Column,
	}
}

// resolveGoReferencesRef resolves `references` refs.
// The referenceName may be:
//   - a bare type name from a type annotation (e.g., "Cache", "Store")
//   - a function name from a route/framework ref (e.g., "handleHealth")
//
// It looks for any matching node (type or function) in same package first.
func resolveGoReferencesRef(
	ref model.UnresolvedReference,
	s *store.Store,
	projectRoot string,
	goModulePath string,
	importCache map[string][]importMapping,
) *model.Edge {
	name := ref.ReferenceName
	if goBuiltIns[name] {
		return nil
	}
	candidates, err := s.GetNodesByName(name)
	if err != nil || len(candidates) == 0 {
		return nil
	}

	// Prefer type nodes (struct/interface/etc.) first; fall back to any Go node.
	target := pickBestGoType(candidates, ref.FilePath, model.LangGo)
	if target == nil {
		// Fall back: any Go node with this name in same package.
		target = pickBestGoNode(candidates, ref.FilePath)
	}
	if target == nil {
		return nil
	}
	return &model.Edge{
		Source: ref.FromNodeID,
		Target: target.ID,
		Kind:   model.EdgeReferences,
		Line:   ref.Line,
		Column: ref.Column,
	}
}

// resolveGoCallsRef resolves `calls` refs, handling:
//   - dotted cross-package calls:  store.NewCache  → NewCache in internal/store/
//   - dotted instance-method calls: cache.Warm     → Cache::Warm (single match)
//   - bare calls in same package:  normalize       → normalize in same dir
func resolveGoCallsRef(
	ref model.UnresolvedReference,
	s *store.Store,
	projectRoot string,
	goModulePath string,
	importCache map[string][]importMapping,
) *model.Edge {
	name := ref.ReferenceName
	if goBuiltIns[name] {
		return nil
	}

	// Check for dotted notation: "pkg.Name" or "obj.method"
	dotIdx := strings.Index(name, ".")
	if dotIdx > 0 {
		prefix := name[:dotIdx]
		symbol := name[dotIdx+1:]

		if goBuiltIns[prefix] || goStdlibPackages[prefix] {
			return nil
		}
		if goBuiltIns[symbol] {
			return nil
		}

		// Get import mappings for the calling file.
		mappings := getImportMappings(ref.FilePath, projectRoot, importCache)

		// Check if prefix is an imported package alias.
		importPath := findImportByAlias(prefix, mappings)
		if importPath != "" {
			// Cross-package resolution.
			if isStdlibImport(importPath) {
				return nil
			}
			edge := resolveCrossPackageGoCall(ref, s, symbol, importPath, goModulePath)
			if edge != nil {
				return edge
			}
		}

		// Prefix is not an import — treat as instance receiver.
		// Find by exact method name, preferring same language.
		return resolveGoMethodCall(ref, s, symbol, prefix)
	}

	// Bare name: try same package first, then global best-match.
	return resolveGoBareName(ref, s, name)
}

// resolveCrossPackageGoCall resolves a symbol in the package given by importPath.
// It strips the module prefix from importPath to get a relative directory, then
// finds a node with the given name whose file is under that directory.
func resolveCrossPackageGoCall(
	ref model.UnresolvedReference,
	s *store.Store,
	symbol string,
	importPath string,
	goModulePath string,
) *model.Edge {
	// Compute relative directory from import path.
	relDir := importPathToRelDir(importPath, goModulePath)
	if relDir == "" {
		return nil
	}

	candidates, err := s.GetNodesByName(symbol)
	if err != nil || len(candidates) == 0 {
		return nil
	}

	// Filter to Go nodes in the target package directory.
	var match *model.Node
	for i := range candidates {
		n := &candidates[i]
		if n.Language != model.LangGo {
			continue
		}
		nodeDir := filepath.Dir(filepath.ToSlash(n.FilePath))
		if nodeDir == relDir || strings.HasPrefix(nodeDir+"/", relDir+"/") {
			match = n
			break
		}
	}
	if match == nil {
		return nil
	}

	// Promote calls to instantiates if the target is a struct or class.
	kind := model.EdgeKind(ref.ReferenceKind)
	if kind == model.EdgeCalls && (match.Kind == model.KindStruct || match.Kind == model.KindClass) {
		kind = model.EdgeInstantiates
	}

	return &model.Edge{
		Source: ref.FromNodeID,
		Target: match.ID,
		Kind:   kind,
		Line:   ref.Line,
		Column: ref.Column,
	}
}

// resolveGoMethodCall resolves a dotted call where the prefix is an instance
// receiver (not an imported package). Mirrors upstream matchMethodCall Strategy 3:
//   - only considers kind=method (not function)
//   - single candidate → resolve directly
//   - multiple candidates → require word-overlap score ≥ 2 between receiver
//     words and the method's qualified-name words (splitCamelCase, length > 1),
//     plus a +1 language bonus; returns nil if no candidate reaches the threshold
func resolveGoMethodCall(ref model.UnresolvedReference, s *store.Store, methodName, receiverName string) *model.Edge {
	if goBuiltIns[methodName] {
		return nil
	}
	candidates, err := s.GetNodesByName(methodName)
	if err != nil || len(candidates) == 0 {
		return nil
	}

	// Filter to same-language method nodes only (upstream Strategy 3 uses kind='method').
	var goMethods []model.Node
	for _, n := range candidates {
		if n.Language == model.LangGo && n.Kind == model.KindMethod {
			goMethods = append(goMethods, n)
		}
	}
	if len(goMethods) == 0 {
		return nil
	}

	var target *model.Node
	if len(goMethods) == 1 {
		// Single candidate: resolve directly (upstream confidence 0.7).
		target = &goMethods[0]
	} else {
		// Multiple candidates: score by word overlap between receiver name and
		// method's qualified name (splitCamelCase filters words length ≤ 1).
		// Also add +1 language bonus (all are already Go here, so every candidate
		// gets +1 — effectively the threshold is 1 overlap word + 1 language = 2).
		receiverWords := splitCamelCase(receiverName)
		var bestMatch *model.Node
		bestScore := 0
		for i := range goMethods {
			m := &goMethods[i]
			classWords := splitCamelCase(m.QualifiedName)
			score := 0
			for _, rw := range receiverWords {
				for _, cw := range classWords {
					if strings.EqualFold(rw, cw) {
						score++
						break
					}
				}
			}
			score++ // language bonus (method is Go, matching ref.Language=Go)
			if score > bestScore {
				bestScore = score
				bestMatch = m
			}
		}
		if bestMatch == nil || bestScore < 2 {
			return nil
		}
		target = bestMatch
	}

	kind := model.EdgeKind(ref.ReferenceKind)
	if kind == model.EdgeCalls && (target.Kind == model.KindStruct || target.Kind == model.KindClass) {
		kind = model.EdgeInstantiates
	}

	return &model.Edge{
		Source: ref.FromNodeID,
		Target: target.ID,
		Kind:   kind,
		Line:   ref.Line,
		Column: ref.Column,
	}
}

// splitCamelCase splits a camelCase/PascalCase/qualified name into words,
// filtering out words of length ≤ 1. Mirrors upstream splitCamelCase in
// name-matcher.ts used for receiver-overlap scoring.
func splitCamelCase(s string) []string {
	// Insert spaces before uppercase letters that follow lowercase (camelCase split).
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := runes[i-1]
			if prev >= 'a' && prev <= 'z' {
				b.WriteRune(' ')
			} else if i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z' && prev >= 'A' && prev <= 'Z' {
				b.WriteRune(' ')
			}
		}
		b.WriteRune(r)
	}
	// Split on whitespace, dots, underscores, colons, slashes, backslashes.
	raw := strings.FieldsFunc(b.String(), func(r rune) bool {
		return r == ' ' || r == '.' || r == '_' || r == ':' || r == '/' || r == '\\'
	})
	var out []string
	for _, w := range raw {
		if len(w) > 1 {
			out = append(out, w)
		}
	}
	return out
}

// resolveGoBareName resolves a bare name (no dot notation) by finding a
// function, method, or constant with that name in the same package first,
// then globally within the same language.
func resolveGoBareName(ref model.UnresolvedReference, s *store.Store, name string) *model.Edge {
	candidates, err := s.GetNodesByName(name)
	if err != nil || len(candidates) == 0 {
		return nil
	}

	// Filter to Go nodes, preferring same directory (package).
	refDir := filepath.Dir(ref.FilePath)
	var samePkg, otherPkg []model.Node
	for _, n := range candidates {
		if n.Language != model.LangGo {
			continue
		}
		if n.Kind == model.KindImport || n.Kind == model.KindFile {
			continue
		}
		nodeDir := filepath.Dir(n.FilePath)
		if nodeDir == refDir {
			samePkg = append(samePkg, n)
		} else {
			otherPkg = append(otherPkg, n)
		}
	}

	pool := samePkg
	if len(pool) == 0 {
		pool = otherPkg
	}
	if len(pool) == 0 {
		return nil
	}

	target := pickBestNode(pool, ref.FilePath)
	if target == nil {
		return nil
	}

	kind := model.EdgeKind(ref.ReferenceKind)
	if kind == model.EdgeCalls && (target.Kind == model.KindStruct || target.Kind == model.KindClass) {
		kind = model.EdgeInstantiates
	}

	return &model.Edge{
		Source: ref.FromNodeID,
		Target: target.ID,
		Kind:   kind,
		Line:   ref.Line,
		Column: ref.Column,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Generic (non-Go) resolution
// ──────────────────────────────────────────────────────────────────────────────

// resolveGenericRef performs a simple name-based lookup for non-Go refs.
// Handles dotted calls like "cache.warm" by finding methods named "warm" in
// same-language same-file context, mirroring upstream's matchMethodCall strategy.
func resolveGenericRef(ref model.UnresolvedReference, s *store.Store) *model.Edge {
	name := ref.ReferenceName

	// Dotted call: "obj.method" or "ns.Symbol" — try method resolution first.
	if dotIdx := strings.Index(name, "."); dotIdx > 0 {
		symbol := name[dotIdx+1:]
		if symbol != "" && !strings.ContainsAny(symbol, "./") {
			edge := resolveDottedRef(ref, s, symbol)
			if edge != nil {
				return edge
			}
		}
		// For qualified names that couldn't be resolved by method lookup, bail.
		// Don't fall through to bare-name lookup with the full dotted name.
		return nil
	}

	candidates, err := s.GetNodesByName(name)
	if err != nil || len(candidates) == 0 {
		return nil
	}
	target := pickBestNode(candidates, ref.FilePath)
	if target == nil {
		return nil
	}
	kind := ref.ReferenceKind
	// Promote calls to instantiates if target is a class/struct.
	if kind == model.EdgeCalls && (target.Kind == model.KindClass || target.Kind == model.KindStruct) {
		kind = model.EdgeInstantiates
	}
	return &model.Edge{
		Source: ref.FromNodeID,
		Target: target.ID,
		Kind:   kind,
		Line:   ref.Line,
		Column: ref.Column,
	}
}

// resolveDottedRef resolves a dotted "receiver.method" call by finding methods
// named `symbol` that belong to a class matching the capitalized receiver name
// or any same-language method with that exact name (if unique). Mirrors upstream
// matchMethodCall strategy 2 and 3.
func resolveDottedRef(ref model.UnresolvedReference, s *store.Store, symbol string) *model.Edge {
	candidates, err := s.GetNodesByName(symbol)
	if err != nil || len(candidates) == 0 {
		return nil
	}

	// Filter to same-language method/function candidates.
	var langMatches []model.Node
	for _, n := range candidates {
		if n.Language == ref.Language && (n.Kind == model.KindMethod || n.Kind == model.KindFunction) {
			langMatches = append(langMatches, n)
		}
	}
	if len(langMatches) == 0 {
		// Fallback: any same-language node.
		for _, n := range candidates {
			if n.Language == ref.Language {
				langMatches = append(langMatches, n)
			}
		}
	}
	if len(langMatches) == 0 {
		return nil
	}

	// If only one candidate, use it (high confidence).
	if len(langMatches) == 1 {
		return &model.Edge{
			Source: ref.FromNodeID,
			Target: langMatches[0].ID,
			Kind:   ref.ReferenceKind,
			Line:   ref.Line,
			Column: ref.Column,
		}
	}

	// Multiple candidates: pick best by proximity.
	target := pickBestNode(langMatches, ref.FilePath)
	if target == nil {
		return nil
	}
	return &model.Edge{
		Source: ref.FromNodeID,
		Target: target.ID,
		Kind:   ref.ReferenceKind,
		Line:   ref.Line,
		Column: ref.Column,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Selection helpers
// ──────────────────────────────────────────────────────────────────────────────

// pickBestGoType selects the best struct/class/interface/type-alias candidate,
// preferring same directory, then same language, then first in insertion order.
func pickBestGoType(candidates []model.Node, refFilePath string, lang model.Language) *model.Node {
	refDir := filepath.Dir(refFilePath)
	var samePkg, otherPkg []model.Node
	for _, n := range candidates {
		if n.Language != lang {
			continue
		}
		switch n.Kind {
		case model.KindStruct, model.KindClass, model.KindInterface,
			model.KindTypeAlias, model.KindEnum:
		default:
			continue
		}
		if filepath.Dir(n.FilePath) == refDir {
			samePkg = append(samePkg, n)
		} else {
			otherPkg = append(otherPkg, n)
		}
	}
	pool := samePkg
	if len(pool) == 0 {
		pool = otherPkg
	}
	if len(pool) == 0 {
		return nil
	}
	return &pool[0]
}

// pickBestGoNode selects the best Go node (any kind except import/file) from
// the candidates, preferring same file > same directory > other.
func pickBestGoNode(candidates []model.Node, refFilePath string) *model.Node {
	refDir := filepath.Dir(refFilePath)
	var samePkg, other []model.Node
	for _, n := range candidates {
		if n.Language != model.LangGo {
			continue
		}
		if n.Kind == model.KindImport || n.Kind == model.KindFile {
			continue
		}
		if filepath.Dir(n.FilePath) == refDir || n.FilePath == refFilePath {
			samePkg = append(samePkg, n)
		} else {
			other = append(other, n)
		}
	}
	pool := samePkg
	if len(pool) == 0 {
		pool = other
	}
	if len(pool) == 0 {
		return nil
	}
	return &pool[0]
}

// pickBestNode selects the best node from a candidate list using a simple
// scoring heuristic: same-file > same-directory > other.  When scores tie, the
// first candidate (in DB insertion order) is returned.
func pickBestNode(candidates []model.Node, refFilePath string) *model.Node {
	if len(candidates) == 0 {
		return nil
	}
	refDir := filepath.Dir(refFilePath)

	bestScore := -1
	var best *model.Node
	for i := range candidates {
		n := &candidates[i]
		score := 0
		if n.FilePath == refFilePath {
			score = 100
		} else if filepath.Dir(n.FilePath) == refDir {
			score = 50
		}
		if score > bestScore {
			bestScore = score
			best = n
		}
	}
	return best
}

// ──────────────────────────────────────────────────────────────────────────────
// Import helpers
// ──────────────────────────────────────────────────────────────────────────────

// importMapping maps a local alias to its full import path.
type importMapping struct {
	localAlias string // alias or last segment of path
	importPath string
}

// getImportMappings returns (cached) import mappings for a Go source file,
// reading the file from projectRoot if not already cached.
func getImportMappings(filePath, projectRoot string, cache map[string][]importMapping) []importMapping {
	if v, ok := cache[filePath]; ok {
		return v
	}
	fullPath := filepath.Join(projectRoot, filepath.FromSlash(filePath))
	content, err := os.ReadFile(fullPath)
	if err != nil {
		cache[filePath] = nil
		return nil
	}
	mappings := parseGoImports(string(content))
	cache[filePath] = mappings
	return mappings
}

// parseGoImports parses Go import declarations from source text and returns
// a slice of importMappings. Handles both single-line and grouped imports,
// as well as aliased imports (alias "path").
func parseGoImports(src string) []importMapping {
	var result []importMapping
	inBlock := false

	lines := strings.Split(src, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "import (") {
			inBlock = true
			continue
		}
		if inBlock && line == ")" {
			inBlock = false
			continue
		}

		var path, alias string
		if inBlock {
			path, alias = parseImportSpec(line)
		} else if strings.HasPrefix(line, `import "`) || strings.HasPrefix(line, "import `") {
			// Single import: import "path"
			trimmed := strings.TrimPrefix(line, "import ")
			path = strings.Trim(trimmed, `"`+"`")
		} else if strings.HasPrefix(line, "import ") {
			// Aliased single import: import alias "path"
			path, alias = parseImportSpec(strings.TrimPrefix(line, "import "))
		}

		if path == "" {
			continue
		}
		local := alias
		if local == "" || local == "." || local == "_" {
			// Default: last segment of the import path.
			parts := strings.Split(path, "/")
			local = parts[len(parts)-1]
		}
		result = append(result, importMapping{localAlias: local, importPath: path})
	}
	return result
}

// parseImportSpec parses one import spec line (inside a parenthesised block or
// after "import "). Returns (importPath, alias); alias is empty when absent.
func parseImportSpec(line string) (path, alias string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "//") {
		return "", ""
	}
	// Find the quoted path.
	qStart := strings.IndexAny(line, `"`+"`")
	if qStart < 0 {
		return "", ""
	}
	quote := line[qStart]
	qEnd := strings.IndexByte(line[qStart+1:], quote)
	if qEnd < 0 {
		return "", ""
	}
	path = line[qStart+1 : qStart+1+qEnd]
	alias = strings.TrimSpace(line[:qStart])
	// Drop inline comments.
	if idx := strings.Index(alias, "//"); idx >= 0 {
		alias = strings.TrimSpace(alias[:idx])
	}
	return path, alias
}

// findImportByAlias returns the import path whose local alias equals alias.
func findImportByAlias(alias string, mappings []importMapping) string {
	for _, m := range mappings {
		if m.localAlias == alias {
			return m.importPath
		}
	}
	return ""
}

// isStdlibImport reports whether importPath names a Go standard library package.
// It checks both the exact last segment (for flat stdlib packages) and the full
// path (for multi-segment stdlib paths like "net/http").
func isStdlibImport(importPath string) bool {
	// Full-path check for well-known multi-segment stdlib packages.
	if goStdlibPackages[importPath] {
		return true
	}
	// Last-segment check.
	parts := strings.Split(importPath, "/")
	last := parts[len(parts)-1]
	if goStdlibPackages[last] {
		return true
	}
	// Heuristic: stdlib paths have no dots in the first segment.
	if len(parts) > 0 && !strings.Contains(parts[0], ".") {
		return true
	}
	return false
}

// importPathToRelDir converts a Go import path to a relative directory by
// stripping the module path prefix. Returns "" when the import path is not
// within the module (e.g., an external dependency).
func importPathToRelDir(importPath, modulePath string) string {
	if modulePath == "" {
		return ""
	}
	if !strings.HasPrefix(importPath, modulePath) {
		return ""
	}
	rel := strings.TrimPrefix(importPath, modulePath)
	rel = strings.TrimPrefix(rel, "/")
	return filepath.ToSlash(rel)
}

// ──────────────────────────────────────────────────────────────────────────────
// go.mod loader
// ──────────────────────────────────────────────────────────────────────────────

// loadGoModulePath reads the module declaration from go.mod at projectRoot.
// Returns "" when go.mod is absent or unreadable.
func loadGoModulePath(projectRoot string) string {
	gomodPath := filepath.Join(projectRoot, "go.mod")
	f, err := os.Open(gomodPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// ──────────────────────────────────────────────────────────────────────────────
// Heuristic synthesis passes (ported from callback-synthesizer.ts)
// ──────────────────────────────────────────────────────────────────────────────

// goMethodNameSet returns the set of method names directly contained by nodeID
// (i.e. via "contains" edges whose target is a method node).
func goMethodNameSet(s *store.Store, nodeID string) (map[string][]model.Node, error) {
	containsEdges, err := s.GetOutgoingEdges(nodeID, []model.EdgeKind{model.EdgeContains}, "")
	if err != nil {
		return nil, err
	}
	byName := make(map[string][]model.Node)
	for _, e := range containsEdges {
		n, err := s.GetNodeByID(e.Target)
		if err != nil || n == nil || n.Kind != model.KindMethod {
			continue
		}
		byName[n.Name] = append(byName[n.Name], *n)
	}
	return byName, nil
}

// collectNodesByKind collects all nodes of a given kind into a slice.
// This avoids holding a sql.Rows cursor open while issuing nested queries.
func collectNodesByKind(s *store.Store, kind model.NodeKind) ([]model.Node, error) {
	var nodes []model.Node
	err := s.IterateNodesByKind(kind, func(n model.Node) error {
		nodes = append(nodes, n)
		return nil
	})
	return nodes, err
}

// synthesizeGoImplementsEdges synthesizes heuristic "implements" edges for Go:
// a struct implicitly satisfies an interface when its method-name set covers the
// interface's method-name set (structural typing). Ported from goImplementsEdges
// in callback-synthesizer.ts. Line is set to struct.StartLine, col is 0 (null).
func synthesizeGoImplementsEdges(s *store.Store) ([]model.Edge, error) {
	// Collect nodes first so we never issue nested queries while a cursor is open.
	allInterfaces, err := collectNodesByKind(s, model.KindInterface)
	if err != nil {
		return nil, err
	}
	allStructs, err := collectNodesByKind(s, model.KindStruct)
	if err != nil {
		return nil, err
	}

	// Build method-name sets for all Go interfaces.
	type ifaceInfo struct {
		node    model.Node
		methods map[string]struct{}
	}
	var ifaces []ifaceInfo
	for _, n := range allInterfaces {
		if n.Language != model.LangGo {
			continue
		}
		byName, err := goMethodNameSet(s, n.ID)
		if err != nil {
			return nil, err
		}
		if len(byName) == 0 {
			continue // empty interface (e.g. `any`) — would match everything
		}
		names := make(map[string]struct{}, len(byName))
		for name := range byName {
			names[name] = struct{}{}
		}
		ifaces = append(ifaces, ifaceInfo{node: n, methods: names})
	}
	if len(ifaces) == 0 {
		return nil, nil
	}

	var edges []model.Edge
	seen := make(map[string]struct{})

	for _, structNode := range allStructs {
		if structNode.Language != model.LangGo {
			continue
		}
		structMethods, err := goMethodNameSet(s, structNode.ID)
		if err != nil {
			return nil, err
		}
		for _, iface := range ifaces {
			// Struct must have all methods the interface requires.
			if len(structMethods) < len(iface.methods) {
				continue
			}
			all := true
			for m := range iface.methods {
				if _, ok := structMethods[m]; !ok {
					all = false
					break
				}
			}
			if !all {
				continue
			}
			key := structNode.ID + ">" + iface.node.ID
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			edges = append(edges, model.Edge{
				Source:     structNode.ID,
				Target:     iface.node.ID,
				Kind:       model.EdgeImplements,
				Line:       structNode.StartLine,
				Provenance: "heuristic",
			})
		}
	}
	return edges, nil
}

// synthesizeGoInterfaceOverrideEdges synthesizes heuristic "calls" edges from
// each interface method to the matching concrete method on implementing structs.
// Requires synthesizeGoImplementsEdges to have been run first (reads "implements"
// edges from the store). Ported from interfaceOverrideEdges in
// callback-synthesizer.ts. Line is set to the interface method's StartLine.
func synthesizeGoInterfaceOverrideEdges(s *store.Store) ([]model.Edge, error) {
	// Collect all Go structs first (avoid nested cursors).
	allStructs, err := collectNodesByKind(s, model.KindStruct)
	if err != nil {
		return nil, err
	}

	var edges []model.Edge
	seen := make(map[string]struct{})

	for _, structNode := range allStructs {
		if structNode.Language != model.LangGo {
			continue
		}
		// Find implements edges for this struct (inserted by pass 1).
		implEdges, err := s.GetOutgoingEdges(structNode.ID, []model.EdgeKind{model.EdgeImplements}, "")
		if err != nil {
			return nil, err
		}
		if len(implEdges) == 0 {
			continue
		}

		// Build a name→methods map for struct's own methods.
		structMethodsByName, err := goMethodNameSet(s, structNode.ID)
		if err != nil {
			return nil, err
		}

		for _, implEdge := range implEdges {
			iface, err := s.GetNodeByID(implEdge.Target)
			if err != nil || iface == nil || iface.Language != model.LangGo {
				continue
			}
			// For each method declared on the interface, link to the matching struct method.
			ifaceContains, err := s.GetOutgoingEdges(iface.ID, []model.EdgeKind{model.EdgeContains}, "")
			if err != nil {
				return nil, err
			}
			for _, ce := range ifaceContains {
				ifaceMethod, err := s.GetNodeByID(ce.Target)
				if err != nil || ifaceMethod == nil || ifaceMethod.Kind != model.KindMethod {
					continue
				}
				concreteMethods, ok := structMethodsByName[ifaceMethod.Name]
				if !ok {
					continue
				}
				for _, cm := range concreteMethods {
					if ifaceMethod.ID == cm.ID {
						continue
					}
					key := ifaceMethod.ID + ">" + cm.ID
					if _, dup := seen[key]; dup {
						continue
					}
					seen[key] = struct{}{}
					edges = append(edges, model.Edge{
						Source:     ifaceMethod.ID,
						Target:     cm.ID,
						Kind:       model.EdgeCalls,
						Line:       ifaceMethod.StartLine,
						Provenance: "heuristic",
					})
				}
			}
		}
	}
	return edges, nil
}
