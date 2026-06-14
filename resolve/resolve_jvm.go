package resolve

import (
	"strings"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// ──────────────────────────────────────────────────────────────────────────────
// JVM (Java / Kotlin) static resolution
//
// These helpers are written on non-language-specific "JVM" names so the Kotlin
// resolver (sub-project 4) can reuse them. Java is statically typed and Go-like:
// a declared type resolves by name against the global symbol table, preferring
// same-package → explicit-import → wildcard-package → any. Imported types are
// resolved THROUGH the local import node to the real cross-file definition, with
// calls→instantiates promotion for classes/structs.
// ──────────────────────────────────────────────────────────────────────────────

// jvmFileContext is the per-file import/package resolution context. Built once
// per file (cached) so resolution is order-independent and deterministic.
type jvmFileContext struct {
	pkg          string              // declared package (namespace node name), "" if none
	explicit     map[string]struct{} // simple type names brought in by `import a.b.C;`
	wildcardPkgs map[string]struct{} // packages brought in by `import a.b.*;`
}

// implicitJVMPackages are wildcard packages every Java file imports implicitly.
var implicitJVMPackages = map[string]struct{}{
	"java.lang": {},
}

// getJVMContext returns the (cached) resolution context for a file.
func getJVMContext(filePath string, s *store.Store, cache map[string]*jvmFileContext) *jvmFileContext {
	if ctx, ok := cache[filePath]; ok {
		return ctx
	}
	ctx := buildJVMContext(filePath, s)
	cache[filePath] = ctx
	return ctx
}

// buildJVMContext reads a file's namespace + import nodes and derives its package
// name, explicit-import set, and wildcard-package set. Import FQNs come from the
// import node's signature ("import a.b.C;" / "import a.b.*;").
func buildJVMContext(filePath string, s *store.Store) *jvmFileContext {
	ctx := &jvmFileContext{
		explicit:     make(map[string]struct{}),
		wildcardPkgs: make(map[string]struct{}),
	}
	nodes, err := s.GetNodesByFile(filePath)
	if err != nil {
		return ctx
	}
	for i := range nodes {
		n := &nodes[i]
		if n.Language != model.LangJava {
			continue
		}
		switch n.Kind {
		case model.KindNamespace:
			if ctx.pkg == "" {
				ctx.pkg = n.Name
			}
		case model.KindImport:
			fq := jvmImportFQN(n.Signature, n.Name)
			if strings.HasSuffix(fq, ".*") {
				ctx.wildcardPkgs[strings.TrimSuffix(fq, ".*")] = struct{}{}
			} else if simple := jvmSimpleName(fq); simple != "" {
				ctx.explicit[simple] = struct{}{}
			}
		}
	}
	return ctx
}

// jvmImportFQN extracts the fully-qualified import target from an import node's
// signature ("import a.b.C;" → "a.b.C"; "import static a.b.C.m;" → "a.b.C.m").
// Falls back to the node name when the signature is not parseable.
func jvmImportFQN(sig, fallback string) string {
	s := strings.TrimSpace(sig)
	s = strings.TrimSuffix(s, ";")
	s = strings.TrimPrefix(s, "import")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "static")
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}

// jvmSimpleName returns the last dotted segment of a qualified name.
func jvmSimpleName(fq string) string {
	if idx := strings.LastIndex(fq, "."); idx >= 0 {
		return fq[idx+1:]
	}
	return fq
}

// jvmPackageOf returns the package (namespace) declared in the file that node n
// belongs to, or "" when none. Cached per file via the same context cache.
func jvmPackageOf(n *model.Node, s *store.Store, cache map[string]*jvmFileContext) string {
	return getJVMContext(n.FilePath, s, cache).pkg
}

// resolveJVMRef resolves one Java unresolved reference into an edge using static,
// Go-like resolution. Shared with Kotlin (sub-project 4).
func resolveJVMRef(ref model.UnresolvedReference, s *store.Store, cache map[string]*jvmFileContext) *model.Edge {
	name := ref.ReferenceName

	// Dotted calls "recv.method": resolve by the method name, conservatively.
	if ref.ReferenceKind == model.EdgeCalls {
		if dotIdx := strings.Index(name, "."); dotIdx > 0 {
			attr := name[dotIdx+1:]
			if attr == "" || strings.ContainsAny(attr, "./") {
				return nil
			}
			return resolveJVMMethodByName(ref, s, attr)
		}
	}

	// `imports` refs: target the real definition in the source package when one
	// exists; otherwise fall back to the local import node (generic behavior).
	if ref.ReferenceKind == model.EdgeImports {
		if edge := resolveJVMImportsRef(ref, s, cache); edge != nil {
			return edge
		}
		return resolveGenericRef(ref, s)
	}

	// Type-bearing refs (calls / instantiates / extends / implements / references):
	// resolve a bare type/symbol name with package preference, through imports.
	if !strings.Contains(name, ".") {
		if target := resolveJVMTypeByName(name, ref.FilePath, s, cache); target != nil {
			kind := ref.ReferenceKind
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
	}

	// Bare calls that didn't resolve as a type: try a same-name method/function.
	if ref.ReferenceKind == model.EdgeCalls && !strings.Contains(name, ".") {
		return resolveJVMMethodByName(ref, s, name)
	}

	return nil
}

// resolveJVMImportsRef resolves an `imports` ref to the real definition (class /
// interface / enum) named by the import when one exists, rather than the local
// import shim node. Returns nil when no in-repo definition exists.
func resolveJVMImportsRef(ref model.UnresolvedReference, s *store.Store, cache map[string]*jvmFileContext) *model.Edge {
	target := resolveJVMTypeByName(ref.ReferenceName, ref.FilePath, s, cache)
	if target == nil {
		return nil
	}
	return &model.Edge{
		Source: ref.FromNodeID,
		Target: target.ID,
		Kind:   model.EdgeImports,
		Line:   ref.Line,
		Column: ref.Column,
	}
}

// resolveJVMTypeByName finds the best real (non-import, non-file) JVM type/symbol
// definition named name, applying package preference:
//
//	same-package → explicitly imported → wildcard-package → any.
//
// Within a preference tier, same-file beats same-dir beats other (pickBestNode).
// This is the JVM analogue of Go's import-map resolution and reuses the
// "resolve through the local import node" pattern (an imported type's
// constructor/call lands on the real cross-file definition).
func resolveJVMTypeByName(name, refFilePath string, s *store.Store, cache map[string]*jvmFileContext) *model.Node {
	candidates, err := s.GetNodesByName(name)
	if err != nil || len(candidates) == 0 {
		return nil
	}

	var defs []model.Node
	for i := range candidates {
		n := &candidates[i]
		if n.Language != model.LangJava {
			continue
		}
		if n.Kind == model.KindImport || n.Kind == model.KindFile {
			continue
		}
		defs = append(defs, *n)
	}
	if len(defs) == 0 {
		return nil
	}

	ctx := getJVMContext(refFilePath, s, cache)

	var samePkg, imported, wildcard, other []model.Node
	for i := range defs {
		n := &defs[i]
		pkg := jvmPackageOf(n, s, cache)
		switch {
		case ctx.pkg != "" && pkg == ctx.pkg:
			samePkg = append(samePkg, *n)
		case isExplicitlyImported(name, ctx):
			imported = append(imported, *n)
		case isWildcardImported(pkg, ctx):
			wildcard = append(wildcard, *n)
		default:
			other = append(other, *n)
		}
	}

	for _, pool := range [][]model.Node{samePkg, imported, wildcard, other} {
		if len(pool) > 0 {
			return pickBestNode(pool, refFilePath)
		}
	}
	return nil
}

// isExplicitlyImported reports whether the simple type name is brought in by an
// explicit `import a.b.Name;` in the referencing file.
func isExplicitlyImported(name string, ctx *jvmFileContext) bool {
	_, ok := ctx.explicit[name]
	return ok
}

// isWildcardImported reports whether a definition's package is brought in by a
// wildcard `import pkg.*;` (or is an implicit package like java.lang).
func isWildcardImported(pkg string, ctx *jvmFileContext) bool {
	if pkg == "" {
		return false
	}
	if _, ok := ctx.wildcardPkgs[pkg]; ok {
		return true
	}
	_, ok := implicitJVMPackages[pkg]
	return ok
}

// resolveJVMMethodByName resolves a (dotted or bare) call to a method/function
// named methodName, conservatively: a single same-language method/function match
// resolves; ambiguity stays unresolved. No signature matching this pass.
func resolveJVMMethodByName(ref model.UnresolvedReference, s *store.Store, methodName string) *model.Edge {
	candidates, err := s.GetNodesByName(methodName)
	if err != nil || len(candidates) == 0 {
		return nil
	}
	var matches []model.Node
	for i := range candidates {
		n := &candidates[i]
		if n.Language == model.LangJava && (n.Kind == model.KindMethod || n.Kind == model.KindFunction) {
			matches = append(matches, *n)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	var target *model.Node
	if len(matches) == 1 {
		target = &matches[0]
	} else {
		// Multiple: deterministic same-file/dir preference (documented; no
		// signature-based overload resolution this pass).
		target = pickBestNode(matches, ref.FilePath)
	}
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
