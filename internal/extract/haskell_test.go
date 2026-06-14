package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func TestExtractHaskellSymbols(t *testing.T) {
	src := `module Shape where

import Data.List (sort)
import qualified Data.Map as Map

class Shape a where
  area :: a -> Double

data Circle = Circle Double

data Point = Point { px :: Double, py :: Double }

type Radius = Double

instance Shape Circle where
  area (Circle r) = pi * r * r

scale :: Double -> Double
scale x = x * 2

scale2 :: Double -> Double
scale2 0 = 0
scale2 x = scale x
`
	res, err := ExtractFile("/p/Shape.hs", []byte(src), model.LangHaskell)
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[model.NodeKind][]string{}
	for _, n := range res.Nodes {
		kinds[n.Kind] = append(kinds[n.Kind], n.Name)
	}
	want := map[model.NodeKind]int{
		model.KindModule:     2, // Shape module + Shape.Circle instance scope
		model.KindInterface:  1, // class Shape
		model.KindMethod:     2, // area (class sig) + area (instance impl)
		model.KindStruct:     2, // Circle, Point
		model.KindEnumMember: 2, // Circle ctor, Point ctor
		model.KindField:      2, // px, py
		model.KindTypeAlias:  1, // Radius
		model.KindImport:     2, // Data.List, Data.Map(as Map)
		model.KindFunction:   2, // scale, scale2 (deduped equations)
	}
	for k, n := range want {
		if len(kinds[k]) != n {
			t.Errorf("kind %s: got %d %v, want %d", k, len(kinds[k]), kinds[k], n)
		}
	}

	// implements edge ref T@C
	var foundImpl bool
	for _, r := range res.UnresolvedReferences {
		if r.ReferenceKind == model.EdgeImplements && r.ReferenceName == "Circle@Shape" {
			foundImpl = true
		}
	}
	if !foundImpl {
		t.Errorf("missing implements ref Circle@Shape; refs=%v", refNames(res.UnresolvedReferences, model.EdgeImplements))
	}

	// scale2 should call scale (a bare-name call), not duplicate the function node.
	var callsScale bool
	for _, r := range res.UnresolvedReferences {
		if r.ReferenceKind == model.EdgeCalls && r.ReferenceName == "scale" {
			callsScale = true
		}
	}
	if !callsScale {
		t.Errorf("expected calls ref to scale; refs=%v", refNames(res.UnresolvedReferences, model.EdgeCalls))
	}
}

func refNames(refs []model.UnresolvedReference, k model.EdgeKind) []string {
	var out []string
	for _, r := range refs {
		if r.ReferenceKind == k {
			out = append(out, r.ReferenceName)
		}
	}
	return out
}
