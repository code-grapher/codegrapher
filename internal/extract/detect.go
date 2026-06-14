// Package extract provides AST-based symbol extraction for Go and TypeScript/JavaScript files.
// It is a port of src/extraction/ from github.com/colbymchenry/codegraph (MIT).
package extract

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/specscore/codegrapher/model"
)

// DetectLanguage returns the model.Language for the given file path,
// based solely on the file extension. Mirrors EXTENSION_MAP in
// src/extraction/grammars.ts. File-level-only languages (yaml, twig,
// properties) return their own Language constant so the indexer records a
// file row with zero nodes, matching the original's behaviour.
func DetectLanguage(filePath string) model.Language {
	if filepath.Base(filePath) == "go.mod" {
		return model.LangGoMod
	}
	if filepath.Base(filePath) == "package.json" {
		return model.LangPackageJSON
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return model.LangGo
	case ".ts", ".mts", ".cts":
		return model.LangTypeScript
	case ".tsx":
		return model.LangTSX
	case ".js", ".mjs", ".cjs":
		return model.LangJavaScript
	case ".jsx":
		return model.LangJSX
	case ".py", ".pyi":
		return model.LangPython
	case ".cs":
		return model.LangCSharp
	case ".java":
		return model.LangJava
	case ".kt", ".kts":
		return model.LangKotlin
	case ".rb":
		return model.LangRuby
	case ".rs":
		return model.LangRust
	case ".php":
		return model.LangPHP
	case ".c":
		return model.LangC
	// Path-only callers (scan filtering) treat .h as C for back-compat; the
	// content-aware DetectLanguageContent sniffs .h for C++ markers.
	case ".h":
		return model.LangC
	case ".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx":
		return model.LangCPP
	case ".scala", ".sc":
		return model.LangScala
	case ".swift":
		return model.LangSwift
	case ".dart":
		return model.LangDart
	case ".lua":
		return model.LangLua
	case ".ex", ".exs":
		return model.LangElixir
	case ".hs":
		return model.LangHaskell
	case ".m", ".mm":
		return model.LangObjC
	// File-level-only languages: tracked in the files table with zero
	// symbol nodes, matching isFileLevelOnlyLanguage() in grammars.ts.
	case ".yml", ".yaml":
		return model.LangYAML
	default:
		return model.LangUnknown
	}
}

// cppHeaderMarkers matches C++-only constructs used to disambiguate a `.h`
// header (which DetectLanguage defaults to C) as actually C++.
var cppHeaderMarkers = regexp.MustCompile(`\bclass\b|\bnamespace\b|\btemplate\b|::|\bpublic:|\bprivate:|\bprotected:`)

// objcHeaderMarkers matches Objective-C-only constructs used to disambiguate a
// `.h` header as Objective-C rather than C/C++ (Obj-C `@`-directives and
// `#import`, which plain C/C++ headers do not use).
var objcHeaderMarkers = regexp.MustCompile(`@interface\b|@protocol\b|@implementation\b|#import\b`)

// DetectLanguageContent returns the model.Language for path, using content to
// disambiguate ambiguous extensions. For unambiguous extensions it delegates to
// the path-only DetectLanguage. For `.h` (which DetectLanguage defaults to C for
// back-compat) it sniffs content: Objective-C markers (`@interface`/`@protocol`/
// `@implementation`/`#import`) win first → LangObjC; else C++ markers → LangCPP;
// else LangC. Callers that have the file content available (the indexer extract
// path, the parity test) should prefer this over DetectLanguage.
func DetectLanguageContent(path string, content []byte) model.Language {
	if strings.ToLower(filepath.Ext(path)) == ".h" {
		if objcHeaderMarkers.Match(content) {
			return model.LangObjC
		}
		if cppHeaderMarkers.Match(content) {
			return model.LangCPP
		}
		return model.LangC
	}
	return DetectLanguage(path)
}

// IsFileLevelOnly reports whether lang is tracked at the file-record level
// only — stored in the files table with zero symbol nodes. Mirrors
// isFileLevelOnlyLanguage() in src/extraction/grammars.ts.
func IsFileLevelOnly(lang model.Language) bool {
	return lang == model.LangYAML
}

// generatedPatterns is ported from src/extraction/generated-detection.ts.
var generatedPatterns = []*regexp.Regexp{
	// Go — protobuf / gRPC / pulsar
	regexp.MustCompile(`\.pb\.go$`),
	regexp.MustCompile(`\.pulsar\.go$`),
	regexp.MustCompile(`_grpc\.pb\.go$`),
	// Go — mockgen
	regexp.MustCompile(`_mock\.go$`),
	regexp.MustCompile(`_mocks\.go$`),
	regexp.MustCompile(`^mock_[^/]+\.go$`),
	// TypeScript / JavaScript — codegen suffixes
	regexp.MustCompile(`\.generated\.[jt]sx?$`),
	regexp.MustCompile(`\.gen\.[jt]sx?$`),
	regexp.MustCompile(`\.pb\.[jt]s$`),
	regexp.MustCompile(`_pb\.[jt]s$`),
	regexp.MustCompile(`_grpc_pb\.[jt]s$`),
	// Python — protobuf / gRPC
	regexp.MustCompile(`_pb2(_grpc)?\.py$`),
	regexp.MustCompile(`_pb2\.pyi$`),
	// C++ — protobuf
	regexp.MustCompile(`\.pb\.(cc|h)$`),
	// C# — protobuf / gRPC
	regexp.MustCompile(`\.g\.cs$`),
	regexp.MustCompile(`Grpc\.cs$`),
	// Java — protobuf / gRPC
	regexp.MustCompile(`OuterClass\.java$`),
	regexp.MustCompile(`Grpc\.java$`),
	// Swift — protobuf
	regexp.MustCompile(`\.pb\.swift$`),
	// Dart — build_runner / freezed / json_serializable / chopper
	regexp.MustCompile(`\.g\.dart$`),
	regexp.MustCompile(`\.freezed\.dart$`),
	regexp.MustCompile(`\.pb\.dart$`),
	regexp.MustCompile(`\.pbgrpc\.dart$`),
	regexp.MustCompile(`\.chopper\.dart$`),
	// Rust — in-tree generated files
	regexp.MustCompile(`\.generated\.rs$`),
}

// IsGeneratedFile reports whether filePath looks like a tool-generated source
// file, based on its filename. Path-only — does not read content.
func IsGeneratedFile(filePath string) bool {
	for _, p := range generatedPatterns {
		if p.MatchString(filePath) {
			return true
		}
	}
	return false
}
