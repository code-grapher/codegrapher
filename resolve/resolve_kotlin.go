package resolve

import (
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// resolveKotlinRef resolves one Kotlin unresolved reference into an edge.
//
// Kotlin is statically typed on the JVM, so resolution delegates to the shared
// JVM helpers (resolveJVMRef with model.LangKotlin): package + import preference,
// resolve-through-import, and calls→instantiates promotion all work identically.
//
// The Kotlin extras the design calls for fall out of the JVM helpers without
// extra code:
//   - Top-level functions: emitted as KindFunction at file/namespace scope;
//     resolveJVMMethodByName already matches KindFunction for bare-name calls.
//   - Extension functions (`fun Foo.bar()`): emitted as ordinary functions;
//     a `recv.bar()` call resolves to bar by method name (best-effort,
//     deterministic pick — no receiver-type matching this pass, per the design).
//   - Companion/object members: emitted directly under their enclosing type as
//     members; a `Type.member()` call resolves to member by name via the same
//     dotted-call method-name lookup.
func resolveKotlinRef(ref model.UnresolvedReference, s *store.Store, cache map[string]*jvmFileContext) *model.Edge {
	return resolveJVMRef(ref, model.LangKotlin, s, cache)
}
