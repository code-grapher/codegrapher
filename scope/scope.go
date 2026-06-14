// Package scope identifies the (language, toolchain-version) partition a source
// file belongs to. Graph data is stored and served per scope, so every file is
// mapped to exactly one scope at index time.
package scope

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/specscore/codegrapher/gomod"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/pkgjson"
)

// fallbackVersion is used when no toolchain version can be detected for a file.
const fallbackVersion = "v0"

// Scope is a (language, version) partition. Version is a detected toolchain
// version string (e.g. "1.22", "5.4.2") or fallbackVersion ("v0").
type Scope struct {
	Language model.Language
	Version  string
}

// Key is the stable identifier used in DB filenames, manifest entries, URL path
// segments, and CLI --scope values: "{language}-{version}".
func (s Scope) Key() string {
	return string(s.Language) + "-" + s.Version
}

// versionPrefix strips a leading range operator from an npm semver spec.
var versionPrefix = regexp.MustCompile(`^[\s^~>=<v]+`)

// DetectVersion resolves the toolchain MAJOR version for filePath (absolute)
// within projectRoot, given its already-determined language. Graphs are grouped
// by major version, so e.g. Go 1.22 and 1.26.4 both map to "v1", and TypeScript
// 5.4.2 maps to "v5". It walks up to the nearest governing manifest (go.mod for
// Go, package.json for TS/JS) and returns fallbackVersion ("v0") when nothing is
// detectable.
func DetectVersion(projectRoot, filePath string, lang model.Language) string {
	var ver string
	switch lang {
	case model.LangGo, model.LangGoMod:
		ver = detectGoVersion(projectRoot, filePath)
	case model.LangTypeScript, model.LangJavaScript, model.LangTSX, model.LangJSX, model.LangPackageJSON:
		ver = detectNodeVersion(projectRoot, filePath)
	case model.LangPython:
		// Python graphs are grouped under a single major; no manifest parsing
		// in this pass (PEP 621 pyproject support is a later enhancement).
		ver = "3"
	case model.LangCSharp:
		// C# graphs use fallback version this pass (no .csproj LangVersion
		// parsing yet — see the C# extraction design, Out of scope).
		ver = ""
	case model.LangJava:
		// Java scope uses the fallback version this pass: no pom.xml /
		// build.gradle parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangKotlin:
		// Kotlin scope uses the fallback version this pass: no build.gradle(.kts)
		// parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangRuby:
		// Ruby scope uses the fallback version this pass: no Gemfile /
		// .ruby-version parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangRust:
		// Rust scope uses the fallback version this pass: no Cargo.toml edition
		// parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangPHP:
		// PHP scope uses the fallback version this pass: no composer.json
		// parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangC, model.LangCPP:
		// C / C++ scope uses the fallback version this pass: no build-system
		// (CMake/Make/compile_commands) parsing. majorVersion("") → "v0".
		ver = ""
	case model.LangScala:
		// Scala scope uses the fallback version this pass: no build.sbt
		// parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangSwift:
		// Swift scope uses the fallback version this pass: no SwiftPM/
		// Package.swift parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangDart:
		// Dart scope uses the fallback version this pass: no pubspec.yaml
		// parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangLua:
		// Lua scope uses the fallback version this pass: no rockspec/.luarc
		// parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangElixir:
		// Elixir scope uses the fallback version this pass: no mix.exs
		// parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangHaskell:
		// Haskell scope uses the fallback version this pass: no cabal/stack
		// parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangObjC:
		// Objective-C scope uses the fallback version this pass: no build-system
		// (Xcode/CocoaPods/SwiftPM) parsing. majorVersion("") → "v0".
		ver = ""
	case model.LangPerl:
		// Perl scope uses the fallback version this pass: no cpanfile/
		// Makefile.PL parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangErlang:
		// Erlang scope uses the fallback version this pass: no rebar.config/
		// .app.src parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangJulia:
		// Julia scope uses the fallback version this pass: no Project.toml
		// parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangFSharp:
		// F# scope uses the fallback version this pass: no .fsproj/paket
		// parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	case model.LangR:
		// R scope uses the fallback version this pass: no DESCRIPTION/renv
		// parsing yet. majorVersion("") → fallbackVersion ("v0").
		ver = ""
	}
	return majorVersion(ver)
}

// majorVersion reduces a raw toolchain version (e.g. "1.22", "^5.4.2") to its
// major component prefixed with "v" (e.g. "v1", "v5"), or fallbackVersion when
// no leading numeric component is present.
func majorVersion(raw string) string {
	raw = versionPrefix.ReplaceAllString(strings.TrimSpace(raw), "")
	i := 0
	for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
		i++
	}
	if i == 0 {
		return fallbackVersion
	}
	return "v" + raw[:i]
}

func detectGoVersion(projectRoot, filePath string) string {
	data := readNearest(projectRoot, filePath, "go.mod")
	if data == nil {
		return ""
	}
	f, err := gomod.Parse("go.mod", data)
	if err != nil {
		return ""
	}
	if strings.HasPrefix(f.Toolchain, "go") {
		return strings.TrimPrefix(f.Toolchain, "go") // "go1.26.4" -> "1.26.4"
	}
	return f.Go
}

func detectNodeVersion(projectRoot, filePath string) string {
	data := readNearest(projectRoot, filePath, "package.json")
	if data == nil {
		return ""
	}
	f, err := pkgjson.Parse(data)
	if err != nil {
		return ""
	}
	if v := f.DevDependencies["typescript"]; v != "" {
		return v
	}
	if v := f.Dependencies["typescript"]; v != "" {
		return v
	}
	return f.Engines["node"]
}

// readNearest walks up from filePath's directory to projectRoot, returning the
// contents of the first directory containing name, or nil if none is found.
func readNearest(projectRoot, filePath, name string) []byte {
	root := filepath.Clean(projectRoot)
	dir := filepath.Dir(filepath.Clean(filePath))
	for {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return data
		}
		if dir == root {
			return nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}
