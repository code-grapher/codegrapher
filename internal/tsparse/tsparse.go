// Package tsparse wraps the gotreesitter pure-Go tree-sitter runtime,
// exposing a thin Parser type and a Node type that the rest of the codebase
// can walk without importing gotreesitter directly.
//
// See docs/adr/001-parser-wazero.md (STATUS section) for the rationale for
// using gotreesitter instead of raw wazero + WASM grammars.
package tsparse

import (
	"fmt"
	"os"
	"strconv"
	"time"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Language identifies which grammar to use.
type Language int

const (
	LangGo Language = iota
	LangTypeScript
	// LangTSX selects the tree-sitter `tsx` grammar, a superset of the
	// typescript grammar that also parses JSX. Used for .tsx/.jsx files.
	LangTSX
	// LangPython selects the tree-sitter `python` grammar (Python 3).
	LangPython
	// LangCSharp selects the tree-sitter `c-sharp` grammar.
	LangCSharp
	// LangJava selects the tree-sitter `java` grammar.
	LangJava
	// LangKotlin selects the tree-sitter `kotlin` grammar.
	LangKotlin
	// LangRuby selects the tree-sitter `ruby` grammar.
	LangRuby
	// LangRust selects the tree-sitter `rust` grammar.
	LangRust
	// LangPHP selects the tree-sitter `php` grammar.
	LangPHP
	// LangC selects the tree-sitter `c` grammar.
	LangC
	// LangScala selects the tree-sitter `scala` grammar.
	LangScala
)

// Point is a (row, column) position in source text (0-indexed).
type Point struct {
	Row    uint32
	Column uint32
}

// Node is a syntax-tree node. The zero value is not valid.
type Node struct {
	inner *gts.Node
	lang  *gts.Language
	src   []byte
}

// Kind returns the grammar node type (e.g. "function_declaration").
func (n *Node) Kind() string { return n.inner.Type(n.lang) }

// StartPoint returns the 0-based (row, column) of the first byte.
func (n *Node) StartPoint() Point {
	p := n.inner.StartPoint()
	return Point{Row: p.Row, Column: p.Column}
}

// EndPoint returns the 0-based (row, column) one past the last byte.
func (n *Node) EndPoint() Point {
	p := n.inner.EndPoint()
	return Point{Row: p.Row, Column: p.Column}
}

// Text returns the source bytes this node spans.
func (n *Node) Text() string { return n.inner.Text(n.src) }

// ChildCount returns the total number of children (named + anonymous).
func (n *Node) ChildCount() int { return n.inner.ChildCount() }

// Child returns the i-th child (0-indexed).
func (n *Node) Child(i int) *Node {
	c := n.inner.Child(i)
	if c == nil {
		return nil
	}
	return &Node{inner: c, lang: n.lang, src: n.src}
}

// NamedChildCount returns the number of named (non-anonymous) children.
func (n *Node) NamedChildCount() int { return n.inner.NamedChildCount() }

// NamedChild returns the i-th named child (0-indexed).
func (n *Node) NamedChild(i int) *Node {
	c := n.inner.NamedChild(i)
	if c == nil {
		return nil
	}
	return &Node{inner: c, lang: n.lang, src: n.src}
}

// ChildByFieldName returns the child with the given field name, or nil.
func (n *Node) ChildByFieldName(name string) *Node {
	c := n.inner.ChildByFieldName(name, n.lang)
	if c == nil {
		return nil
	}
	return &Node{inner: c, lang: n.lang, src: n.src}
}

// FieldNameForChild returns the field name (if any) for the i-th child.
func (n *Node) FieldNameForChild(i int) string {
	return n.inner.FieldNameForChild(i, n.lang)
}

// IsNamed reports whether this node is named (not an anonymous token).
func (n *Node) IsNamed() bool { return n.inner.IsNamed() }

// HasError reports whether this node or any descendant is an ERROR node.
func (n *Node) HasError() bool { return n.inner.HasError() }

// Tree is a parsed syntax tree.
type Tree struct {
	root *Node
}

// RootNode returns the root of the syntax tree.
func (t *Tree) RootNode() *Node { return t.root }

// Walk calls fn on every node in the tree in pre-order depth-first order.
func Walk(n *Node, fn func(*Node)) {
	if n == nil {
		return
	}
	fn(n)
	for i := 0; i < n.ChildCount(); i++ {
		Walk(n.Child(i), fn)
	}
}

// Parser parses source files for a fixed language.
type Parser struct {
	lang *gts.Language
}

// NewParser returns a Parser for the given Language.
func NewParser(lang Language) (*Parser, error) {
	switch lang {
	case LangGo:
		return &Parser{lang: grammars.GoLanguage()}, nil
	case LangTypeScript:
		return &Parser{lang: grammars.TypescriptLanguage()}, nil
	case LangTSX:
		return &Parser{lang: grammars.TsxLanguage()}, nil
	case LangPython:
		return &Parser{lang: grammars.PythonLanguage()}, nil
	case LangCSharp:
		return &Parser{lang: grammars.CSharpLanguage()}, nil
	case LangJava:
		return &Parser{lang: grammars.JavaLanguage()}, nil
	case LangKotlin:
		return &Parser{lang: grammars.KotlinLanguage()}, nil
	case LangRuby:
		return &Parser{lang: grammars.RubyLanguage()}, nil
	case LangRust:
		return &Parser{lang: grammars.RustLanguage()}, nil
	case LangPHP:
		return &Parser{lang: grammars.PhpLanguage()}, nil
	case LangC:
		return &Parser{lang: grammars.CLanguage()}, nil
	case LangScala:
		return &Parser{lang: grammars.ScalaLanguage()}, nil
	default:
		return nil, fmt.Errorf("tsparse: unknown language %d", lang)
	}
}

// parseTimeout bounds a single file's parse. gotreesitter has pathological
// blow-ups on rare literal-heavy files (its issue #110: minutes of CPU and
// gigabytes of heap on a file the C tree-sitter parses instantly) — without a
// budget one such file hangs indexing and can OOM the machine. On expiry the
// file degrades to a per-file parse error (logged; index stays usable), a
// deliberate divergence documented in KNOWN-BUGS.md D-2.
var parseTimeout = func() time.Duration {
	if v := os.Getenv("CODEGRAPH_PARSE_TIMEOUT_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms >= 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 30 * time.Second
}()

// Parse parses src and returns the syntax tree.
func (p *Parser) Parse(src []byte) (*Tree, error) {
	inner := gts.NewParser(p.lang)
	if parseTimeout > 0 {
		inner.SetTimeoutMicros(uint64(parseTimeout / time.Microsecond))
	}
	tree, err := inner.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("tsparse: parse: %w", err)
	}
	root := &Node{inner: tree.RootNode(), lang: p.lang, src: src}
	return &Tree{root: root}, nil
}
