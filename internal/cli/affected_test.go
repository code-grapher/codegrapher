package cli

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func TestFindAffectedTestsIncludesCoLocatedGoTests(t *testing.T) {
	s := newMemStore(t)
	for _, path := range []string{
		"internal/githubobserver/observer.go",
		"internal/githubobserver/observer_test.go",
		"internal/other/other_test.go",
	} {
		if err := s.UpsertFile(model.FileRecord{Path: path, Language: model.LangGo}); err != nil {
			t.Fatal(err)
		}
	}

	got, _ := findAffectedTests(s, []string{"internal/githubobserver/observer.go"}, 5, "")
	if !got["internal/githubobserver/observer_test.go"] {
		t.Fatalf("same-package observer test missing: %v", got)
	}
	if got["internal/other/other_test.go"] {
		t.Fatalf("unrelated Go test included: %v", got)
	}
}
