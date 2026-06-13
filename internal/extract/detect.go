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
	// File-level-only languages: tracked in the files table with zero
	// symbol nodes, matching isFileLevelOnlyLanguage() in grammars.ts.
	case ".yml", ".yaml":
		return model.LangYAML
	default:
		return model.LangUnknown
	}
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
