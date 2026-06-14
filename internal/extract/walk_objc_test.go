package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractObjCRes(t *testing.T, name, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile(name, []byte(src), model.LangObjC)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func objcHasRef(res model.ExtractionResult, kind model.EdgeKind, name string) bool {
	for _, r := range res.UnresolvedReferences {
		if r.ReferenceKind == kind && r.ReferenceName == name {
			return true
		}
	}
	return false
}

func objcNode(res model.ExtractionResult, kind model.NodeKind, name string) *model.Node {
	for i := range res.Nodes {
		if res.Nodes[i].Kind == kind && res.Nodes[i].Name == name {
			return &res.Nodes[i]
		}
	}
	return nil
}

func TestObjCFileNode(t *testing.T) {
	res := extractObjCRes(t, "a.m", "int x;\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangObjC {
		t.Fatalf("file node lang = %q, want objc", res.Nodes[0].Language)
	}
}

// Protocol → interface; interface with superclass + adopted protocol →
// class with extends + implements; property → property; ivar → field;
// class/instance methods with keyword selectors.
func TestObjCInterfaceFull(t *testing.T) {
	res := extractObjCRes(t, "shapes.h", `
#import <Foundation/Foundation.h>

@protocol Drawable <NSObject>
- (NSString *)label;
@end

@interface Shape : NSObject {
    NSString *_name;
}
@property (nonatomic, copy) NSString *name;
- (double)area;
@end

@interface Circle : Shape <Drawable>
+ (instancetype)circleWithRadius:(double)radius;
- (NSString *)label;
@end
`)
	if n := objcNode(res, model.KindInterface, "Drawable"); n == nil {
		t.Error("missing protocol → interface Drawable")
	}
	if n := objcNode(res, model.KindClass, "Shape"); n == nil {
		t.Error("missing class Shape")
	}
	if n := objcNode(res, model.KindClass, "Circle"); n == nil {
		t.Error("missing class Circle")
	}
	if n := objcNode(res, model.KindProperty, "name"); n == nil {
		t.Error("missing property name")
	}
	if n := objcNode(res, model.KindField, "_name"); n == nil {
		t.Error("missing ivar field _name")
	}
	if n := objcNode(res, model.KindMethod, "area"); n == nil {
		t.Error("missing method area")
	}
	// Keyword selector keeps the colon.
	cm := objcNode(res, model.KindMethod, "circleWithRadius:")
	if cm == nil {
		t.Fatal("missing keyword-selector method circleWithRadius:")
	}
	if !cm.IsStatic {
		t.Error("circleWithRadius: (+) should be a class method (isStatic)")
	}
	// extends + implements references.
	if !objcHasRef(res, model.EdgeExtends, "Shape") {
		t.Error("missing extends Shape")
	}
	if !objcHasRef(res, model.EdgeImplements, "Drawable") {
		t.Error("missing implements Drawable")
	}
	// #import → import node + imports ref.
	if n := objcNode(res, model.KindImport, "Foundation/Foundation.h"); n == nil {
		t.Error("missing #import node")
	}
	if !objcHasRef(res, model.EdgeImports, "Foundation/Foundation.h") {
		t.Error("missing imports ref for Foundation")
	}
}

// Message-send → calls the selector; [Class alloc]/[[Class alloc] init] →
// instantiates; self/super receivers stripped; multi-part keyword selector.
func TestObjCMessageSends(t *testing.T) {
	res := extractObjCRes(t, "shapes.m", `
#import "shapes.h"

@implementation Circle
- (NSString *)label {
    return [self name];
}
- (void)configure {
    [self doThing:1 with:2];
    [super configure];
    Circle *c = [[Circle alloc] init];
    id d = [Circle alloc];
}
@end
`)
	if !objcHasRef(res, model.EdgeCalls, "name") {
		t.Error("missing calls name (from [self name])")
	}
	if !objcHasRef(res, model.EdgeCalls, "doThing:with:") {
		t.Error("missing calls doThing:with: (keyword selector)")
	}
	if !objcHasRef(res, model.EdgeCalls, "configure") {
		t.Error("missing calls configure (from [super configure])")
	}
	if !objcHasRef(res, model.EdgeInstantiates, "Circle") {
		t.Error("missing instantiates Circle (alloc/init)")
	}
}

// A category @interface X (Cat) attaches methods to X by qualified name; no
// separate category node is created.
func TestObjCCategory(t *testing.T) {
	res := extractObjCRes(t, "extra.h", `
@interface Shape (Extra)
- (void)extraMethod;
@end
`)
	m := objcNode(res, model.KindMethod, "extraMethod")
	if m == nil {
		t.Fatal("missing category method extraMethod")
	}
	if m.QualifiedName != "Shape::extraMethod" {
		t.Errorf("category method qualified name = %q, want Shape::extraMethod", m.QualifiedName)
	}
	if objcNode(res, model.KindClass, "Extra") != nil {
		t.Error("category name should not become a class node")
	}
}
