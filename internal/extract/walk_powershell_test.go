package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractPSRes(t *testing.T, name, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile(name, []byte(src), model.LangPowerShell)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func TestPSFileNodeEmitted(t *testing.T) {
	res := extractPSRes(t, "m.ps1", "$x = 1\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangPowerShell {
		t.Fatalf("file node language = %q, want powershell", res.Nodes[0].Language)
	}
}

func TestPSFunctionAndCall(t *testing.T) {
	res := extractPSRes(t, "m.ps1", `function Get-Greeting {
    param($name)
    Write-Hello $name
}
function Write-Hello($n) {
    "hi $n"
}
`)
	g := findNode(res, model.KindFunction, "Get-Greeting")
	w := findNode(res, model.KindFunction, "Write-Hello")
	if g == nil || w == nil {
		t.Fatalf("functions missing: Get-Greeting=%v Write-Hello=%v", g, w)
	}
	if !refFrom(res, g.ID, "Write-Hello", model.EdgeCalls) {
		t.Error("missing call Get-Greeting -> Write-Hello")
	}
}

func TestPSCommonCmdletNoCall(t *testing.T) {
	res := extractPSRes(t, "m.ps1", `function Go {
    Write-Host "hi"
    Get-ChildItem
}
`)
	if hasRef(res, "Write-Host", model.EdgeCalls) {
		t.Error("Write-Host should not produce a call ref")
	}
	if hasRef(res, "Get-ChildItem", model.EdgeCalls) {
		t.Error("Get-ChildItem should not produce a call ref")
	}
}

func TestPSClassMethodProperty(t *testing.T) {
	res := extractPSRes(t, "m.ps1", `class Animal {
    [string]$Name
    [string] Speak() {
        return "..."
    }
}
`)
	if findNode(res, model.KindClass, "Animal") == nil {
		t.Error("class Animal missing")
	}
	if findNode(res, model.KindProperty, "Name") == nil {
		t.Error("property Name missing")
	}
	if findNode(res, model.KindMethod, "Speak") == nil {
		t.Error("method Speak missing")
	}
}

func TestPSClassExtends(t *testing.T) {
	res := extractPSRes(t, "m.ps1", `class Animal {
    [string] Speak() {
        return "..."
    }
}
class Dog : Animal {
    [string] Speak() {
        return "woof"
    }
}
`)
	dog := findNode(res, model.KindClass, "Dog")
	if dog == nil {
		t.Fatal("class Dog missing")
	}
	if !refFrom(res, dog.ID, "Animal", model.EdgeExtends) {
		t.Error("missing extends Dog -> Animal")
	}
}

func TestPSEnum(t *testing.T) {
	res := extractPSRes(t, "m.ps1", `enum Color {
    Red
    Green
}
`)
	if findNode(res, model.KindEnum, "Color") == nil {
		t.Error("enum Color missing")
	}
	if findNode(res, model.KindEnumMember, "Red") == nil {
		t.Error("enum member Red missing")
	}
}

func TestPSDotSourceImport(t *testing.T) {
	res := extractPSRes(t, "main.ps1", ". ./lib.ps1\n")
	if findNode(res, model.KindImport, "lib") == nil {
		t.Error("dot-source import 'lib' missing")
	}
	fileID := res.Nodes[0].ID
	if !refFrom(res, fileID, "lib", model.EdgeImports) {
		t.Error("missing imports edge file -> lib")
	}
}

func TestPSImportModule(t *testing.T) {
	res := extractPSRes(t, "m.ps1", "Import-Module Foo\nusing module Bar\n")
	if findNode(res, model.KindImport, "Foo") == nil {
		t.Error("Import-Module Foo missing")
	}
	if findNode(res, model.KindImport, "Bar") == nil {
		t.Error("using module Bar missing")
	}
}

func TestPSInstantiate(t *testing.T) {
	res := extractPSRes(t, "m.ps1", `class Dog {
    [string] Speak() {
        return "woof"
    }
}
function Make {
    $d = [Dog]::new()
    $o = New-Object Dog
}
`)
	mk := findNode(res, model.KindFunction, "Make")
	if mk == nil {
		t.Fatal("function Make missing")
	}
	if !refFrom(res, mk.ID, "Dog", model.EdgeInstantiates) {
		t.Error("missing instantiates Make -> Dog ([Dog]::new())")
	}
}

func TestPSVariableAndConstant(t *testing.T) {
	res := extractPSRes(t, "m.ps1", "$y = 7\n$global:GThing = 5\n")
	if findNode(res, model.KindVariable, "y") == nil {
		t.Error("y should be a variable")
	}
	if findNode(res, model.KindConstant, "GThing") == nil {
		t.Error("$global:GThing should be a constant")
	}
}
