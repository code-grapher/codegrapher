package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func TestDetectLanguageGoMod(t *testing.T) {
	cases := map[string]model.Language{
		"go.mod":         model.LangGoMod,
		"sub/dir/go.mod": model.LangGoMod,
		"main.go":        model.LangGo,
		"go.sum":         model.LangUnknown,
		"gomod":          model.LangUnknown,
	}
	for path, want := range cases {
		if got := DetectLanguage(path); got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestDetectPython(t *testing.T) {
	cases := map[string]model.Language{
		"a/b/foo.py":  model.LangPython,
		"stubs/x.pyi": model.LangPython,
		"foo.pyc":     model.LangUnknown,
	}
	for path, want := range cases {
		if got := DetectLanguage(path); got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestDetectObjC(t *testing.T) {
	cases := map[string]model.Language{
		"a/b/foo.m":  model.LangObjC,
		"a/b/foo.mm": model.LangObjC,
	}
	for path, want := range cases {
		if got := DetectLanguage(path); got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestDetectLanguageContentHeader exercises the `.h` content sniff: Objective-C
// markers win first, then C++ markers, else C. C/C++ `.h` handling must be
// preserved.
func TestDetectLanguageContentHeader(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    model.Language
	}{
		{"objc-interface", "@interface Foo : NSObject\n@end\n", model.LangObjC},
		{"objc-protocol", "@protocol Drawable\n@end\n", model.LangObjC},
		{"objc-import", "#import <Foundation/Foundation.h>\n", model.LangObjC},
		{"cpp-class", "class Foo { public: int x; };\n", model.LangCPP},
		{"cpp-namespace", "namespace ns { int f(); }\n", model.LangCPP},
		{"plain-c", "int add(int a, int b);\n", model.LangC},
	}
	for _, c := range cases {
		if got := DetectLanguageContent("foo.h", []byte(c.content)); got != c.want {
			t.Errorf("%s: DetectLanguageContent(.h) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDetectLanguagePackageJSON(t *testing.T) {
	cases := map[string]model.Language{
		"package.json":         model.LangPackageJSON,
		"sub/pkg/package.json": model.LangPackageJSON,
		"package-lock.json":    model.LangUnknown,
		"tsconfig.json":        model.LangUnknown,
	}
	for path, want := range cases {
		if got := DetectLanguage(path); got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}
