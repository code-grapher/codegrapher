package model

import "testing"

func TestPackageJSONLanguagesExist(t *testing.T) {
	if LangPackageJSON != "package.json" {
		t.Errorf("LangPackageJSON = %q", LangPackageJSON)
	}
	if LangNode != "node" {
		t.Errorf("LangNode = %q", LangNode)
	}
}
