package indexer

import (
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/scope"
)

func TestStoresFiltered(t *testing.T) {
	root := t.TempDir()
	if err := CreateDirectory(root); err != nil {
		t.Fatal(err)
	}
	reg, err := OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	goSc := scope.Scope{Language: model.LangGo, Version: "1.22"}
	tsSc := scope.Scope{Language: model.LangTypeScript, Version: "5.4"}
	for _, sc := range []scope.Scope{goSc, tsSc} {
		if _, err := reg.Store(sc); err != nil {
			t.Fatal(err)
		}
	}
	idx := newIndexer(root, reg)
	defer idx.Close()

	// Empty filter returns all stores (== Stores()).
	if got := idx.StoresFiltered(nil); len(got) != 2 {
		t.Fatalf("StoresFiltered(nil) returned %d, want 2", len(got))
	}

	// Single key filters to one store.
	got := idx.StoresFiltered([]string{goSc.Key()})
	if len(got) != 1 {
		t.Fatalf("StoresFiltered([go]) returned %d, want 1", len(got))
	}
	if got[0] != reg.Stores()[goSc] {
		t.Fatalf("StoresFiltered([go]) returned wrong store")
	}

	// Unknown keys are ignored; result is deterministic (sorted by key).
	multi := idx.StoresFiltered([]string{tsSc.Key(), "nope-1.0", goSc.Key()})
	if len(multi) != 2 {
		t.Fatalf("StoresFiltered(known+unknown) returned %d, want 2", len(multi))
	}
	// Ordering: "go-1.22" < "typescript-5.4".
	stores := reg.Stores()
	if multi[0] != stores[goSc] || multi[1] != stores[tsSc] {
		t.Fatalf("StoresFiltered ordering not deterministic by key")
	}
}
