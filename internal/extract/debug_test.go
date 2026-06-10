//go:build debug

package extract

import (
	"fmt"
	"testing"

	"github.com/specscore/codegrapher/internal/tsparse"
)

func TestDebugTree(t *testing.T) {
	p, _ := tsparse.NewParser(tsparse.LangGo)
	src := []byte(`// Store is a key-value store.
type Store struct {
	items map[string]string
}
`)
	tree, _ := p.Parse(src)
	root := tree.RootNode()
	tsparse.Walk(root, func(n *tsparse.Node) {
		text := n.Text()
		if len(text) > 40 {
			text = text[:40]
		}
		fmt.Printf("kind=%s start=(%d,%d) end=(%d,%d) text=%q\n",
			n.Kind(),
			n.StartPoint().Row, n.StartPoint().Column,
			n.EndPoint().Row, n.EndPoint().Column,
			text)
	})
}
