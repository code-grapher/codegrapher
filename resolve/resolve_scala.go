package resolve

import (
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// resolveScalaRef resolves one Scala unresolved reference into an edge.
//
// Scala is statically typed on the JVM, so resolution delegates to the shared
// JVM helpers (resolveJVMRef with model.LangScala): package + import preference,
// resolve-through-import, and calls→instantiates promotion all work identically
// to Java/Kotlin. The Scala-specific import forms (`import a.b._` wildcard and
// `import a.b.{x => y}` rename) are normalized by the extractor into the same
// import-node shape the JVM context builder already understands (a wildcard
// import carries a ".*"-suffixed signature; a rename binds the renamed simple
// name), so no extra resolution code is needed here.
//
// Scala extras that fall out of the JVM helpers without extra code:
//   - Top-level `def`/`val` (package scope): emitted as KindFunction/KindConstant;
//     resolveJVMMethodByName already matches KindFunction for bare-name calls.
//   - `object` members (companion / singleton): emitted directly under the object
//     as static members; `Obj.member()` resolves via the dotted-call method lookup.
//   - Companion `apply` construction `T(...)`: a bare call to a class name promotes
//     to instantiates in resolveJVMRef; a call to the companion's `apply` resolves
//     to that method by name.
func resolveScalaRef(ref model.UnresolvedReference, s *store.Store, cache map[string]*jvmFileContext) *model.Edge {
	return resolveJVMRef(ref, model.LangScala, s, cache)
}
