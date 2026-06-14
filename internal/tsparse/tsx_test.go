package tsparse

import "testing"

// TestTSXGrammarParsesJSX verifies the tsx grammar is wired up and parses JSX
// syntax without error nodes, whereas the plain typescript grammar cannot.
func TestTSXGrammarParsesJSX(t *testing.T) {
	const jsx = `const x = <div className="a">hi</div>;`

	tsxP, err := NewParser(LangTSX)
	if err != nil {
		t.Fatalf("NewParser(LangTSX): %v", err)
	}
	tsxTree, err := tsxP.Parse([]byte(jsx))
	if err != nil {
		t.Fatalf("tsx Parse: %v", err)
	}
	if tsxTree.RootNode().HasError() {
		t.Error("tsx grammar should parse JSX without error nodes")
	}

	// Control: the plain typescript grammar treats `<div ...>` as an invalid
	// type assertion and produces ERROR nodes on the same JSX input.
	tsP, err := NewParser(LangTypeScript)
	if err != nil {
		t.Fatalf("NewParser(LangTypeScript): %v", err)
	}
	tsTree, err := tsP.Parse([]byte(jsx))
	if err != nil {
		t.Fatalf("ts Parse: %v", err)
	}
	if !tsTree.RootNode().HasError() {
		t.Error("typescript grammar is expected to error on JSX (control assertion)")
	}
}
