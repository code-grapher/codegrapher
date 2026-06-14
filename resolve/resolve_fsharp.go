package resolve

import (
	"strings"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// resolveFSharpRef resolves one F# unresolved reference into an edge.
//
//   - imports (`open A.B`): resolve through to the real module/namespace
//     definition named like the opened path's last segment; else fall back to
//     the local import node (resolveGenericRef).
//   - extends (`inherit Base()`): resolve Base to its class/struct definition.
//   - implements (`interface I with`): resolve I to its interface definition.
//   - instantiates (`T(...)` / `new T`): resolve T to its class/struct/record
//     definition by name.
//   - calls: a qualified `A.B.func` resolves func preferring the opened/same
//     module; a bare `func` resolves to a same-module or opened function,
//     resolving THROUGH a local open import. F# core builtins are skipped.
func resolveFSharpRef(ref model.UnresolvedReference, s *store.Store) *model.Edge {
	name := ref.ReferenceName

	switch ref.ReferenceKind {
	case model.EdgeImports:
		if target := resolveFSharpModuleByName(fsLastSeg(name), ref.FilePath, s); target != nil {
			return &model.Edge{Source: ref.FromNodeID, Target: target.ID, Kind: model.EdgeImports, Line: ref.Line, Column: ref.Column}
		}
		return resolveGenericRef(ref, s)

	case model.EdgeExtends:
		target := resolveFSharpTypeByName(fsLastSeg(name), ref.FilePath, s, model.KindClass)
		if target == nil {
			target = resolveFSharpTypeByName(fsLastSeg(name), ref.FilePath, s, model.KindStruct)
		}
		if target == nil {
			return nil
		}
		return &model.Edge{Source: ref.FromNodeID, Target: target.ID, Kind: model.EdgeExtends, Line: ref.Line, Column: ref.Column}

	case model.EdgeImplements:
		target := resolveFSharpTypeByName(fsLastSeg(name), ref.FilePath, s, model.KindInterface)
		if target == nil {
			return nil
		}
		return &model.Edge{Source: ref.FromNodeID, Target: target.ID, Kind: model.EdgeImplements, Line: ref.Line, Column: ref.Column}

	case model.EdgeInstantiates:
		simple := fsLastSeg(name)
		target := resolveFSharpTypeByName(simple, ref.FilePath, s, model.KindClass)
		if target == nil {
			target = resolveFSharpTypeByName(simple, ref.FilePath, s, model.KindStruct)
		}
		if target == nil {
			target = resolveFSharpTypeByName(simple, ref.FilePath, s, model.KindEnum)
		}
		if target == nil {
			return nil
		}
		return &model.Edge{Source: ref.FromNodeID, Target: target.ID, Kind: model.EdgeInstantiates, Line: ref.Line, Column: ref.Column}

	case model.EdgeCalls:
		if fsIsBuiltin(name) {
			return nil
		}
		if dotIdx := strings.LastIndex(name, "."); dotIdx > 0 {
			fn := name[dotIdx+1:]
			if fn == "" {
				return nil
			}
			target := resolveFSharpDefinitionByName(fn, ref.FilePath, s)
			if target == nil {
				return nil
			}
			return &model.Edge{Source: ref.FromNodeID, Target: target.ID, Kind: model.EdgeCalls, Line: ref.Line, Column: ref.Column}
		}
		target := resolveFSharpDefinitionByName(name, ref.FilePath, s)
		if target == nil {
			return nil
		}
		return &model.Edge{Source: ref.FromNodeID, Target: target.ID, Kind: model.EdgeCalls, Line: ref.Line, Column: ref.Column}
	}

	return resolveGenericRef(ref, s)
}

// resolveFSharpModuleByName returns the best F# module/namespace named name,
// preferring same-file/same-dir.
func resolveFSharpModuleByName(name, refFilePath string, s *store.Store) *model.Node {
	candidates, err := s.GetNodesByName(name)
	if err != nil || len(candidates) == 0 {
		return nil
	}
	var defs []model.Node
	for _, n := range candidates {
		if n.Language == model.LangFSharp && (n.Kind == model.KindModule || n.Kind == model.KindNamespace) {
			defs = append(defs, n)
		}
	}
	if len(defs) == 0 {
		return nil
	}
	return pickBestNode(defs, refFilePath)
}

// resolveFSharpTypeByName returns the best F# node named name of the given kind,
// preferring same-file/same-dir.
func resolveFSharpTypeByName(name, refFilePath string, s *store.Store, kind model.NodeKind) *model.Node {
	candidates, err := s.GetNodesByName(name)
	if err != nil || len(candidates) == 0 {
		return nil
	}
	var defs []model.Node
	for _, n := range candidates {
		if n.Language == model.LangFSharp && n.Kind == kind {
			defs = append(defs, n)
		}
	}
	if len(defs) == 0 {
		return nil
	}
	return pickBestNode(defs, refFilePath)
}

// resolveFSharpDefinitionByName returns the best real (non-import, non-file) F#
// definition named name, preferring same-file/same-dir.
func resolveFSharpDefinitionByName(name, refFilePath string, s *store.Store) *model.Node {
	candidates, err := s.GetNodesByName(name)
	if err != nil || len(candidates) == 0 {
		return nil
	}
	var defs []model.Node
	for _, n := range candidates {
		if n.Language != model.LangFSharp {
			continue
		}
		if n.Kind == model.KindImport || n.Kind == model.KindFile {
			continue
		}
		defs = append(defs, n)
	}
	if len(defs) == 0 {
		return nil
	}
	return pickBestNode(defs, refFilePath)
}

// fsLastSeg returns the final dotted segment of a name (A.B.C → "C").
func fsLastSeg(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// fsBuiltins is a small skip set of F# core functions/modules whose calls are
// not resolved to user definitions.
var fsBuiltins = map[string]bool{
	"printfn": true, "printf": true, "sprintf": true, "eprintfn": true,
	"failwith": true, "failwithf": true, "invalidArg": true, "raise": true,
	"id": true, "ignore": true, "not": true, "box": true, "unbox": true,
	"ref": true, "fst": true, "snd": true, "string": true, "int": true,
	"float": true, "bool": true, "char": true, "byte": true, "abs": true,
	"max": true, "min": true, "compare": true, "hash": true, "typeof": true,
	"async": true, "seq": true, "lazy": true,
}

// fsBuiltinModules are F# core module prefixes whose member calls are skipped.
var fsBuiltinModules = []string{
	"List.", "Seq.", "Array.", "Map.", "Set.", "Option.", "Result.",
	"Async.", "String.", "Math.", "Console.", "Operators.",
}

// fsIsBuiltin reports whether a call name targets an F# core builtin.
func fsIsBuiltin(name string) bool {
	if fsBuiltins[name] {
		return true
	}
	for _, m := range fsBuiltinModules {
		if strings.HasPrefix(name, m) {
			return true
		}
	}
	return false
}
