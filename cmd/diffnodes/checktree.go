//go:build checktree
// +build checktree

// Run: go run -tags checktree ./cmd/diffnodes/checktree.go <filepath>
package main

import (
	"fmt"
	"os"

	"github.com/specscore/codegrapher/internal/tsparse"
)

func main() {
	content, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	p, _ := tsparse.NewParser(tsparse.LangGo)
	tree, err := p.Parse(content)
	if err != nil {
		fmt.Println("parse error:", err)
		return
	}
	root := tree.RootNode()
	fmt.Printf("root kind=%s hasError=%v namedChildren=%d\n", root.Kind(), root.HasError(), root.NamedChildCount())
	for i := 0; i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		if child == nil {
			continue
		}
		fmt.Printf("  child[%d] kind=%-30s line=%d hasError=%v\n", i, child.Kind(), child.StartPoint().Row+1, child.HasError())
	}
}
