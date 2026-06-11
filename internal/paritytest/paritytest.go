// Package paritytest compares codegrapher output against golden outputs
// captured from the original codegraph CLI (testdata/golden), implementing
// the normalization rules documented in tools/parity/README.md:
//
//   - machine-specific fields are replaced with "<NORM>"
//   - arrays whose order is not functionally meaningful are sorted
//   - object keys compare order-insensitively (canonical JSON)
//   - query[] result order IS meaningful (descending score) and is preserved
package paritytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// normalizedKeys are machine- or time-specific fields replaced before diffing.
var normalizedKeys = map[string]bool{
	"projectPath": true,
	"updatedAt":   true,
	"dbSizeBytes": true,
	"lastIndexed": true,
	"version":     true,
	"indexPath":   true,
}

// unorderedArrayKeys name object fields whose array order is incidental
// (JS Map/object iteration order upstream); they are sorted before diffing.
var unorderedArrayKeys = map[string]bool{
	"callers":  true,
	"callees":  true,
	"affected": true,
}

// Canonicalize parses raw JSON and returns its normalized, deterministically
// ordered re-encoding. topLevelUnordered marks the whole top-level array as
// order-insensitive (e.g. the files verb); query results keep their order.
func Canonicalize(raw []byte, topLevelUnordered bool) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("paritytest: parse: %w", err)
	}
	v = normalize(v, "", topLevelUnordered)
	return json.Marshal(v) // Go marshals map keys sorted — canonical by construction
}

func normalize(v any, key string, topLevelUnordered bool) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if normalizedKeys[k] {
				t[k] = "<NORM>"
				continue
			}
			t[k] = normalize(val, k, false)
		}
		return t
	case []any:
		for i, item := range t {
			t[i] = normalize(item, "", false)
		}
		if unorderedArrayKeys[key] || topLevelUnordered {
			sortArray(t)
		}
		return t
	default:
		return v
	}
}

// sortArray orders array elements by their canonical JSON encoding — stable,
// total, and implementation-independent. Keys are paired with their items
// before sorting (sorting items while indexing a detached key slice compares
// stale positions once elements move).
func sortArray(items []any) {
	type keyed struct {
		key  string
		item any
	}
	ks := make([]keyed, len(items))
	for i, item := range items {
		b, err := json.Marshal(item)
		if err != nil {
			b = nil
		}
		ks[i] = keyed{key: string(b), item: item}
	}
	sort.SliceStable(ks, func(i, j int) bool { return ks[i].key < ks[j].key })
	for i, k := range ks {
		items[i] = k.item
	}
}

// Diff compares got against the golden file at goldenPath after canonicalizing
// both sides. It returns "" when equivalent, else a human-readable mismatch.
func Diff(goldenPath string, got []byte, topLevelUnordered bool) (string, error) {
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		return "", fmt.Errorf("paritytest: read golden: %w", err)
	}
	cw, err := Canonicalize(want, topLevelUnordered)
	if err != nil {
		return "", fmt.Errorf("paritytest: golden %s: %w", goldenPath, err)
	}
	cg, err := Canonicalize(got, topLevelUnordered)
	if err != nil {
		return "", fmt.Errorf("paritytest: got: %w", err)
	}
	if bytes.Equal(cw, cg) {
		return "", nil
	}
	return fmt.Sprintf("parity mismatch vs %s\n--- golden (canonical)\n%s\n--- got (canonical)\n%s",
		goldenPath, cw, cg), nil
}
