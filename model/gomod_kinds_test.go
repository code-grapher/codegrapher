package model

import "testing"

func TestGoModKindsExist(t *testing.T) {
	if LangGoMod != "go.mod" {
		t.Errorf("LangGoMod = %q", LangGoMod)
	}
	for _, k := range []EdgeKind{EdgeRequires, EdgeReplaces, EdgeExcludes} {
		if k == "" {
			t.Errorf("edge kind is empty")
		}
	}
}
