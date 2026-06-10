package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// GenerateNodeID derives the deterministic ID for a symbol node:
//
//	"<kind>:" + hex(sha256("<filePath>:<kind>:<name>:<line>"))[:32]
//
// line is the 1-indexed start line. This formula is a cross-implementation
// contract with the original codegraph (tree-sitter-helpers.ts generateNodeId)
// and is pinned against golden output in id_test.go — do not change it.
func GenerateNodeID(filePath string, kind NodeKind, name string, line int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s:%s:%s:%d", filePath, kind, name, line))
	return string(kind) + ":" + hex.EncodeToString(sum[:])[:32]
}

// FileNodeID is the ID of a file node: the literal path, unhashed.
func FileNodeID(filePath string) string {
	return "file:" + filePath
}

// RouteNodeID is the ID of a route node: also unhashed.
func RouteNodeID(filePath string, line int, method, routePath string) string {
	return fmt.Sprintf("route:%s:%d:%s:%s", filePath, line, method, routePath)
}
