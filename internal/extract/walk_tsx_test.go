package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

// TestExtractTSXComponent verifies that a .tsx file containing JSX is parsed
// with the tsx grammar (selected by ExtractFile for LangTSX) and its top-level
// symbols are extracted from a clean, error-free tree.
func TestExtractTSXComponent(t *testing.T) {
	const src = `export function App() {
  return (
    <div className="app">
      <Button label="go" />
    </div>
  );
}

export const Button = (props: { label: string }) => {
  return <button>{props.label}</button>;
};
`
	res, err := ExtractFile("App.tsx", []byte(src), model.LangTSX)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	names := map[string]bool{}
	for _, n := range res.Nodes {
		names[n.Name] = true
	}
	if !names["App"] {
		t.Errorf("expected App function symbol; got %v", keys(names))
	}
	if !names["Button"] {
		t.Errorf("expected Button symbol; got %v", keys(names))
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
