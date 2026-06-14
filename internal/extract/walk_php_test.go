package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func findNodeQN(res model.ExtractionResult, kind model.NodeKind, qn string) *model.Node {
	for i := range res.Nodes {
		if res.Nodes[i].Kind == kind && res.Nodes[i].QualifiedName == qn {
			return &res.Nodes[i]
		}
	}
	return nil
}

func extractPhpRes(t *testing.T, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile("mod.php", []byte(src), model.LangPHP)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func TestPhpFileNodeEmitted(t *testing.T) {
	res := extractPhpRes(t, "<?php\n$x = 1;\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangPHP {
		t.Fatalf("file node language = %q, want php", res.Nodes[0].Language)
	}
}

// Namespace + class + interface + extends/implements + $this->prop field + method.
func TestPhpClassFull(t *testing.T) {
	res := extractPhpRes(t, `<?php
namespace App\Animals;

interface Speaker {
    public function speak(): string;
}

abstract class Animal implements Speaker {
    protected string $sound = "";
    public function __construct(string $name) {
        $this->name = $name;
    }
}

class Dog extends Animal {
    public function speak(): string {
        return "Woof";
    }
}
`)
	ns := findNode(res, model.KindNamespace, "App\\Animals")
	if ns == nil {
		t.Fatal("namespace App\\Animals not found")
	}
	iface := findNode(res, model.KindInterface, "Speaker")
	if iface == nil {
		t.Fatal("interface Speaker not found")
	}
	animal := findNode(res, model.KindClass, "Animal")
	if animal == nil {
		t.Fatal("class Animal not found")
	}
	if !animal.IsAbstract {
		t.Error("Animal should be abstract")
	}
	if !refFrom(res, animal.ID, "Speaker", model.EdgeImplements) {
		t.Error("missing implements Speaker")
	}
	// $this->name → field on Animal.
	field := findNode(res, model.KindField, "name")
	if field == nil || field.QualifiedName != "Animal::name" {
		t.Errorf("expected field Animal::name, got %v", field)
	}
	dog := findNode(res, model.KindClass, "Dog")
	if dog == nil {
		t.Fatal("class Dog not found")
	}
	if !refFrom(res, dog.ID, "Animal", model.EdgeExtends) {
		t.Error("missing extends Animal")
	}
	if findNodeQN(res, model.KindMethod, "Dog::speak") == nil {
		t.Error("expected method Dog::speak")
	}
	if findNodeQN(res, model.KindMethod, "Speaker::speak") == nil {
		t.Error("expected method Speaker::speak")
	}
}

// Trait declaration + trait-use inside a class → implements reference.
func TestPhpTrait(t *testing.T) {
	res := extractPhpRes(t, `<?php
trait Named {
    public string $name = "";
    public function getName(): string { return $this->name; }
}

class Dog {
    use Named;
}
`)
	tr := findNode(res, model.KindTrait, "Named")
	if tr == nil {
		t.Fatal("trait Named not found")
	}
	getName := findNode(res, model.KindMethod, "getName")
	if getName == nil || getName.QualifiedName != "Named::getName" {
		t.Errorf("expected Named::getName, got %v", getName)
	}
	dog := findNode(res, model.KindClass, "Dog")
	if dog == nil {
		t.Fatal("class Dog not found")
	}
	if !refFrom(res, dog.ID, "Named", model.EdgeImplements) {
		t.Error("missing trait-use implements Named")
	}
}

// Enum + cases.
func TestPhpEnum(t *testing.T) {
	res := extractPhpRes(t, `<?php
enum Color: string {
    case Red = "red";
    case Blue = "blue";
}
`)
	en := findNode(res, model.KindEnum, "Color")
	if en == nil {
		t.Fatal("enum Color not found")
	}
	red := findNode(res, model.KindEnumMember, "Red")
	if red == nil || red.QualifiedName != "Color::Red" {
		t.Errorf("expected enum member Color::Red, got %v", red)
	}
}

// Top-level function + const.
func TestPhpFunctionConst(t *testing.T) {
	res := extractPhpRes(t, `<?php
const VERSION = "1.0";
function helper(string $x): void {
}
`)
	fn := findNode(res, model.KindFunction, "helper")
	if fn == nil {
		t.Fatal("function helper not found")
	}
	if fn.Signature != "function helper(string $x): void" {
		t.Errorf("signature = %q", fn.Signature)
	}
	c := findNode(res, model.KindConstant, "VERSION")
	if c == nil {
		t.Fatal("const VERSION not found")
	}
}

// use import → KindImport node + imports reference.
func TestPhpUseImport(t *testing.T) {
	res := extractPhpRes(t, `<?php
namespace App;
use App\Animals\Dog;
use App\Util\Logger as Log;
`)
	dog := findNode(res, model.KindImport, "Dog")
	if dog == nil {
		t.Fatal("import Dog not found")
	}
	log := findNode(res, model.KindImport, "Log")
	if log == nil {
		t.Fatal("aliased import Log not found")
	}
}

// new T(...) → instantiates; $x = new T(); $x->m() → call recv carries inference.
func TestPhpNewAndCalls(t *testing.T) {
	res := extractPhpRes(t, `<?php
function run(): void {
    $d = new Dog("rex");
    $d->speak();
    helper();
}
`)
	run := findNode(res, model.KindFunction, "run")
	if run == nil {
		t.Fatal("function run not found")
	}
	if !refFrom(res, run.ID, "Dog", model.EdgeInstantiates) {
		t.Error("missing instantiates Dog")
	}
	if !refFrom(res, run.ID, "d.speak", model.EdgeCalls) {
		t.Error("missing call d.speak")
	}
	if !refFrom(res, run.ID, "helper", model.EdgeCalls) {
		t.Error("missing call helper")
	}
	// $d should be a variable carrying the constructor signature.
	v := findNode(res, model.KindVariable, "d")
	if v == nil || v.Signature != "= Dog(\"rex\")" {
		t.Errorf("expected var d signature '= Dog(\"rex\")', got %v", v)
	}
}

// PHP 8 attribute → decorator + decorates reference.
func TestPhpAttribute(t *testing.T) {
	res := extractPhpRes(t, `<?php
#[Route("/dog")]
class Dog {
}
`)
	dog := findNode(res, model.KindClass, "Dog")
	if dog == nil {
		t.Fatal("class Dog not found")
	}
	found := false
	for _, d := range dog.Decorators {
		if d == "Route" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Route decorator, got %v", dog.Decorators)
	}
	if !refFrom(res, dog.ID, "Route", model.EdgeDecorates) {
		t.Error("missing decorates Route")
	}
}
