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
