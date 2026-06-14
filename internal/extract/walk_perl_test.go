package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractPerlRes(t *testing.T, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile("mod.pm", []byte(src), model.LangPerl)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func TestPerlFileNodeEmitted(t *testing.T) {
	res := extractPerlRes(t, "my $x = 1;\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangPerl {
		t.Fatalf("file node language = %q, want perl", res.Nodes[0].Language)
	}
}

// 1. package → module; subs inside → methods qualified by package
func TestPerlPackageAndSub(t *testing.T) {
	res := extractPerlRes(t, `package Animal;
sub new {
}
sub speak {
}
1;
`)
	mn := findNode(res, model.KindModule, "Animal")
	if mn == nil {
		t.Fatal("package Animal not found")
	}
	m := findNode(res, model.KindMethod, "speak")
	if m == nil || m.QualifiedName != "Animal::speak" {
		t.Errorf("method speak qualified name = %v", m)
	}
	n := findNode(res, model.KindMethod, "new")
	if n == nil || n.QualifiedName != "Animal::new" {
		t.Errorf("method new qualified name = %v", n)
	}
}

// 2. Top-level sub (no package) is a function
func TestPerlTopLevelSub(t *testing.T) {
	res := extractPerlRes(t, `sub helper {
}
`)
	fn := findNode(res, model.KindFunction, "helper")
	if fn == nil {
		t.Fatal("top-level sub should be a function")
	}
	if fn.Signature != "sub helper" {
		t.Errorf("signature = %q", fn.Signature)
	}
}

// 3. Flat packages: two packages in one file, subs attribute to the right one
func TestPerlFlatPackages(t *testing.T) {
	res := extractPerlRes(t, `package A;
sub one { }
package B;
sub two { }
`)
	o := findNode(res, model.KindMethod, "one")
	if o == nil || o.QualifiedName != "A::one" {
		t.Errorf("one qualified name = %v", o)
	}
	tw := findNode(res, model.KindMethod, "two")
	if tw == nil || tw.QualifiedName != "B::two" {
		t.Errorf("two qualified name = %v", tw)
	}
}

// 4. use parent / use base / @ISA → extends
func TestPerlInheritance(t *testing.T) {
	for _, src := range []string{
		"package Dog;\nuse parent 'Animal';\n",
		"package Dog;\nuse base qw(Animal);\n",
		"package Dog;\nour @ISA = ('Animal');\n",
	} {
		res := extractPerlRes(t, src)
		cn := findNode(res, model.KindModule, "Dog")
		if cn == nil {
			t.Fatalf("package Dog not found for %q", src)
		}
		if !refFrom(res, cn.ID, "Animal", model.EdgeExtends) {
			t.Errorf("missing extends Animal for %q", src)
		}
	}
}

// 5. use Module → import + imports ref
func TestPerlUseImport(t *testing.T) {
	res := extractPerlRes(t, `use Dog::Breed;
`)
	if findNode(res, model.KindImport, "Breed") == nil {
		t.Error("use Dog::Breed should yield import node 'Breed'")
	}
	if !hasRef(res, "Breed", model.EdgeImports) {
		t.Error("imports Breed ref missing")
	}
}

// 6. use constant → constant
func TestPerlUseConstant(t *testing.T) {
	res := extractPerlRes(t, `use constant PI => 3.14;
`)
	if findNode(res, model.KindConstant, "PI") == nil {
		t.Error("use constant PI should be a constant")
	}
}

// 7. my/our variables and ALL-CAPS constants
func TestPerlVarsAndConstants(t *testing.T) {
	res := extractPerlRes(t, `my $count = 0;
our $GREETING = "hi";
my @items = ();
`)
	if findNode(res, model.KindVariable, "count") == nil {
		t.Error("count should be a variable")
	}
	if findNode(res, model.KindConstant, "GREETING") == nil {
		t.Error("GREETING should be a constant")
	}
	if findNode(res, model.KindVariable, "items") == nil {
		t.Error("items should be a variable")
	}
}

// 8. calls: bare, self-stripped, package->new, $var->method, Foo::bar
func TestPerlCalls(t *testing.T) {
	res := extractPerlRes(t, `sub f {
    my $self = shift;
    foo(1);
    $self->helper;
    Dog->new;
    $obj->method;
    Foo::bar();
}
`)
	fn := findNode(res, model.KindFunction, "f")
	if fn == nil {
		t.Fatal("f not found")
	}
	if !refFrom(res, fn.ID, "foo", model.EdgeCalls) {
		t.Error("missing call foo")
	}
	if !refFrom(res, fn.ID, "helper", model.EdgeCalls) {
		t.Error("$self->helper should strip receiver → helper")
	}
	if !refFrom(res, fn.ID, "Dog.new", model.EdgeCalls) {
		t.Error("missing call Dog.new")
	}
	if !refFrom(res, fn.ID, "obj.method", model.EdgeCalls) {
		t.Error("missing call obj.method")
	}
	if !refFrom(res, fn.ID, "Foo::bar", model.EdgeCalls) {
		t.Error("missing call Foo::bar")
	}
}

// 9. builtins skipped
func TestPerlBuiltinsSkipped(t *testing.T) {
	res := extractPerlRes(t, `sub f {
    print "x";
    my @s = sort @items;
    push @items, 1;
}
`)
	fn := findNode(res, model.KindFunction, "f")
	if fn == nil {
		t.Fatal("f not found")
	}
	for _, b := range []string{"print", "sort", "push"} {
		if refFrom(res, fn.ID, b, model.EdgeCalls) {
			t.Errorf("builtin %q should be skipped", b)
		}
	}
}

// 10. var type inference signature
func TestPerlVarTypeInferenceSignature(t *testing.T) {
	res := extractPerlRes(t, `my $d = Dog->new(name => 'rex');
`)
	v := findNode(res, model.KindVariable, "d")
	if v == nil {
		t.Fatal("d should be a variable")
	}
	if v.Signature != "= Dog->new(name => 'rex')" {
		t.Errorf("signature = %q", v.Signature)
	}
}
