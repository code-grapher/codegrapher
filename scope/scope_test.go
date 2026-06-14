package scope

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
)

func TestDetectVersionPython(t *testing.T) {
	dir := t.TempDir()
	got := DetectVersion(dir, filepath.Join(dir, "m.py"), model.LangPython)
	if got != "v3" {
		t.Fatalf("python default version = %q, want v3", got)
	}
}

func TestScopeKey(t *testing.T) {
	cases := []struct {
		lang model.Language
		ver  string
		want string
	}{
		{model.LangGo, "v1", "go-v1"},
		{model.LangTypeScript, "v5", "typescript-v5"},
		{model.LangYAML, "v0", "yaml-v0"},
	}
	for _, tc := range cases {
		got := Scope{Language: tc.lang, Version: tc.ver}.Key()
		if got != tc.want {
			t.Errorf("Key() = %q, want %q", got, tc.want)
		}
	}
}

// writeTree materializes a map of relative path -> content under a temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDetectVersion(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		file  string
		lang  model.Language
		want  string
	}{
		{
			name:  "go major from go directive",
			files: map[string]string{"go.mod": "module x\n\ngo 1.22\n"},
			file:  "main.go",
			lang:  model.LangGo,
			want:  "v1",
		},
		{
			name: "nearest package.json major wins over root",
			files: map[string]string{
				"package.json":     `{"devDependencies":{"typescript":"^4.9.5"}}`,
				"pkg/package.json": `{"devDependencies":{"typescript":"^5.4.2"}}`,
				"pkg/src/app.ts":   "",
			},
			file: "pkg/src/app.ts",
			lang: model.LangTypeScript,
			want: "v5",
		},
		{
			name:  "go patch version reduced to major",
			files: map[string]string{"go.mod": "module x\n\ngo 1.22.3\n"},
			file:  "main.go",
			lang:  model.LangGo,
			want:  "v1",
		},
		{
			name:  "go no go.mod falls back to v0",
			files: map[string]string{"main.go": "package main\n"},
			file:  "main.go",
			lang:  model.LangGo,
			want:  "v0",
		},
		{
			name:  "go.mod without go directive is v0",
			files: map[string]string{"go.mod": "module x\n"},
			file:  "main.go",
			lang:  model.LangGo,
			want:  "v0",
		},
		{
			name:  "typescript major from devDependencies",
			files: map[string]string{"package.json": `{"devDependencies":{"typescript":"^5.4.2"}}`},
			file:  "src/app.ts",
			lang:  model.LangTypeScript,
			want:  "v5",
		},
		{
			name:  "typescript major from dependencies (tsx)",
			files: map[string]string{"package.json": `{"dependencies":{"typescript":"~5.0.0"}}`},
			file:  "src/app.tsx",
			lang:  model.LangTSX,
			want:  "v5",
		},
		{
			name:  "javascript major from engines.node",
			files: map[string]string{"package.json": `{"engines":{"node":">=20.1.0"}}`},
			file:  "src/app.js",
			lang:  model.LangJavaScript,
			want:  "v20",
		},
		{
			name:  "package.json without version info is v0",
			files: map[string]string{"package.json": `{"name":"x"}`},
			file:  "src/app.ts",
			lang:  model.LangTypeScript,
			want:  "v0",
		},
		{
			name:  "no package.json is v0",
			files: map[string]string{"src/app.ts": ""},
			file:  "src/app.ts",
			lang:  model.LangTypeScript,
			want:  "v0",
		},
		{
			name:  "yaml is always v0",
			files: map[string]string{"go.mod": "module x\n\ngo 1.22\n", "config.yaml": ""},
			file:  "config.yaml",
			lang:  model.LangYAML,
			want:  "v0",
		},
		{
			name:  "malformed package.json is v0",
			files: map[string]string{"package.json": `{not json`},
			file:  "src/app.ts",
			lang:  model.LangTypeScript,
			want:  "v0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeTree(t, tc.files)
			got := DetectVersion(root, filepath.Join(root, tc.file), tc.lang)
			if got != tc.want {
				t.Errorf("DetectVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectGoVersionPrefersToolchain(t *testing.T) {
	dir := t.TempDir()
	gomodPath := filepath.Join(dir, "go.mod")
	content := "module x\n\ngo 1.22\n\ntoolchain go1.26.4\n"
	if err := os.WriteFile(gomodPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Both go 1.22 and toolchain go1.26.4 share major "v1"; assert the bucket.
	if got := DetectVersion(dir, src, model.LangGo); got != "v1" {
		t.Errorf("DetectVersion = %q, want v1", got)
	}
}

func TestDetectGoVersionToolchainDefaultFallsBackToGo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module x\n\ngo 1.22\n\ntoolchain default\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectVersion(dir, src, model.LangGo); got != "v1" {
		t.Errorf("DetectVersion = %q, want v1 (toolchain 'default' should fall back to the go directive)", got)
	}
}

// When projectRoot is not an ancestor of filePath, the upward walk must still
// terminate (at the filesystem root) and fall back to v0 rather than loop.
func TestDetectVersionRootNotAncestor(t *testing.T) {
	root := t.TempDir()
	got := DetectVersion(root, filepath.Join("nonexistent", "deep", "main.go"), model.LangGo)
	if got != "v0" {
		t.Errorf("DetectVersion() = %q, want %q", got, "v0")
	}
}
