package query_test

import (
	"strings"
	"testing"

	"github.com/specscore/codegrapher/query"
)

// TestParseQuery ports __tests__/search-query-parser.test.ts parseQuery suite.
func TestParseQuery(t *testing.T) {
	t.Run("returns plain text for a query with no field prefixes", func(t *testing.T) {
		r := query.ParseQuery("authenticate user")
		if r.Text != "authenticate user" {
			t.Errorf("Text = %q, want %q", r.Text, "authenticate user")
		}
		if len(r.Kinds) != 0 {
			t.Errorf("Kinds = %v, want []", r.Kinds)
		}
		if len(r.Languages) != 0 {
			t.Errorf("Languages = %v, want []", r.Languages)
		}
		if len(r.PathFilters) != 0 {
			t.Errorf("PathFilters = %v, want []", r.PathFilters)
		}
		if len(r.NameFilters) != 0 {
			t.Errorf("NameFilters = %v, want []", r.NameFilters)
		}
	})

	t.Run("extracts kind: filter and removes it from text", func(t *testing.T) {
		r := query.ParseQuery("kind:function auth")
		if len(r.Kinds) != 1 || string(r.Kinds[0]) != "function" {
			t.Errorf("Kinds = %v, want [function]", r.Kinds)
		}
		if r.Text != "auth" {
			t.Errorf("Text = %q, want %q", r.Text, "auth")
		}
	})

	t.Run("extracts lang: and language: as the same filter family", func(t *testing.T) {
		a := query.ParseQuery("lang:typescript foo")
		b := query.ParseQuery("language:typescript foo")
		if len(a.Languages) != 1 || string(a.Languages[0]) != "typescript" {
			t.Errorf("lang: Languages = %v, want [typescript]", a.Languages)
		}
		if len(b.Languages) != 1 || string(b.Languages[0]) != "typescript" {
			t.Errorf("language: Languages = %v, want [typescript]", b.Languages)
		}
	})

	t.Run("recognizes language:go.mod as a language filter", func(t *testing.T) {
		r := query.ParseQuery("language:go.mod foo")
		if len(r.Languages) != 1 || string(r.Languages[0]) != "go.mod" {
			t.Errorf("Languages = %v, want [go.mod]", r.Languages)
		}
	})

	t.Run("recognizes language:package.json as a language filter", func(t *testing.T) {
		r := query.ParseQuery("language:package.json foo")
		if len(r.Languages) != 1 || string(r.Languages[0]) != "package.json" {
			t.Errorf("Languages = %v, want [package.json]", r.Languages)
		}
	})

	t.Run("handles multiple kind: filters as an OR set", func(t *testing.T) {
		r := query.ParseQuery("kind:function kind:method auth")
		if len(r.Kinds) != 2 {
			t.Fatalf("Kinds = %v, want 2 elements", r.Kinds)
		}
		kindSet := make(map[string]bool)
		for _, k := range r.Kinds {
			kindSet[string(k)] = true
		}
		if !kindSet["function"] || !kindSet["method"] {
			t.Errorf("Kinds = %v, want function and method", r.Kinds)
		}
	})

	t.Run("extracts path: and name: as substring filters (kept verbatim)", func(t *testing.T) {
		r := query.ParseQuery("path:src/api name:Handler")
		if len(r.PathFilters) != 1 || r.PathFilters[0] != "src/api" {
			t.Errorf("PathFilters = %v, want [src/api]", r.PathFilters)
		}
		if len(r.NameFilters) != 1 || r.NameFilters[0] != "Handler" {
			t.Errorf("NameFilters = %v, want [Handler]", r.NameFilters)
		}
	})

	t.Run("preserves quoted spans as a single token (whitespace in path:)", func(t *testing.T) {
		r := query.ParseQuery(`path:"my dir/file" foo`)
		if len(r.PathFilters) != 1 || r.PathFilters[0] != "my dir/file" {
			t.Errorf("PathFilters = %v, want [my dir/file]", r.PathFilters)
		}
		if r.Text != "foo" {
			t.Errorf("Text = %q, want %q", r.Text, "foo")
		}
	})

	t.Run("passes URL-like tokens through to text (does not match http: as a field)", func(t *testing.T) {
		r := query.ParseQuery("http://example.com")
		if r.Text != "http://example.com" {
			t.Errorf("Text = %q, want %q", r.Text, "http://example.com")
		}
		if len(r.Kinds) != 0 {
			t.Errorf("Kinds = %v, want []", r.Kinds)
		}
	})

	t.Run("passes empty-value tokens through as text (kind: → \"kind:\")", func(t *testing.T) {
		r := query.ParseQuery("kind: foo")
		if len(r.Kinds) != 0 {
			t.Errorf("Kinds = %v, want []", r.Kinds)
		}
		if !strings.Contains(r.Text, "kind:") {
			t.Errorf("Text = %q, want it to contain %q", r.Text, "kind:")
		}
	})

	t.Run("passes unknown field prefixes through as text (TODO: keeps the colon)", func(t *testing.T) {
		r := query.ParseQuery("TODO: needs review")
		if r.Text != "TODO: needs review" {
			t.Errorf("Text = %q, want %q", r.Text, "TODO: needs review")
		}
		if len(r.Kinds) != 0 {
			t.Errorf("Kinds = %v, want []", r.Kinds)
		}
	})

	t.Run("rejects unknown values for kind: (passes the whole token to text)", func(t *testing.T) {
		r := query.ParseQuery("kind:invalid foo")
		if len(r.Kinds) != 0 {
			t.Errorf("Kinds = %v, want []", r.Kinds)
		}
		if !strings.Contains(r.Text, "kind:invalid") {
			t.Errorf("Text = %q, want it to contain %q", r.Text, "kind:invalid")
		}
	})

	t.Run("handles all-filters-no-text query", func(t *testing.T) {
		r := query.ParseQuery("kind:function lang:typescript")
		if len(r.Kinds) != 1 || string(r.Kinds[0]) != "function" {
			t.Errorf("Kinds = %v, want [function]", r.Kinds)
		}
		if len(r.Languages) != 1 || string(r.Languages[0]) != "typescript" {
			t.Errorf("Languages = %v, want [typescript]", r.Languages)
		}
		if r.Text != "" {
			t.Errorf("Text = %q, want empty", r.Text)
		}
	})

	t.Run("survives empty input", func(t *testing.T) {
		r := query.ParseQuery("")
		if r.Text != "" {
			t.Errorf("Text = %q, want empty", r.Text)
		}
		if len(r.Kinds) != 0 {
			t.Errorf("Kinds = %v, want []", r.Kinds)
		}
	})

	t.Run("survives a very long input (no allocation explosion)", func(t *testing.T) {
		huge := strings.Repeat("foo ", 5000) // 20k chars
		r := query.ParseQuery(huge)
		if len(r.Text) == 0 {
			t.Error("Text should be non-empty for non-trivial input")
		}
	})
}

// TestBoundedEditDistance ports __tests__/search-query-parser.test.ts boundedEditDistance suite.
func TestBoundedEditDistance(t *testing.T) {
	t.Run("returns 0 for identical strings", func(t *testing.T) {
		if d := query.BoundedEditDistance("user", "user", 2); d != 0 {
			t.Errorf("got %d, want 0", d)
		}
	})

	t.Run("returns 1 for a single substitution", func(t *testing.T) {
		if d := query.BoundedEditDistance("user", "usar", 2); d != 1 {
			t.Errorf("got %d, want 1", d)
		}
	})

	t.Run("returns 1 for a single insertion", func(t *testing.T) {
		if d := query.BoundedEditDistance("user", "users", 2); d != 1 {
			t.Errorf("got %d, want 1", d)
		}
	})

	t.Run("returns 1 for a single deletion", func(t *testing.T) {
		if d := query.BoundedEditDistance("users", "user", 2); d != 1 {
			t.Errorf("got %d, want 1", d)
		}
	})

	t.Run("returns 2 for a transposition (two edits in basic Levenshtein)", func(t *testing.T) {
		if d := query.BoundedEditDistance("confg", "configX", 2); d != 2 {
			t.Errorf("got %d, want 2", d)
		}
	})

	t.Run("returns maxDist+1 when distance clearly exceeds budget", func(t *testing.T) {
		if d := query.BoundedEditDistance("foo", "completely-different", 2); d != 3 {
			t.Errorf("got %d, want 3", d)
		}
	})

	t.Run("respects length-difference shortcut", func(t *testing.T) {
		if d := query.BoundedEditDistance("a", "aaaaaaa", 2); d != 3 {
			t.Errorf("got %d, want 3", d)
		}
	})

	t.Run("handles empty inputs", func(t *testing.T) {
		if d := query.BoundedEditDistance("", "", 2); d != 0 {
			t.Errorf("(\"\",\"\") = %d, want 0", d)
		}
		if d := query.BoundedEditDistance("a", "", 2); d != 1 {
			t.Errorf("(\"a\",\"\") = %d, want 1", d)
		}
		if d := query.BoundedEditDistance("", "abc", 2); d != 3 {
			t.Errorf("(\"\",\"abc\") = %d, want 3", d)
		}
	})

	t.Run("is case-sensitive — caller must lowercase if case-insensitive match wanted", func(t *testing.T) {
		if d := query.BoundedEditDistance("Foo", "foo", 2); d != 1 {
			t.Errorf("got %d, want 1", d)
		}
	})

	t.Run("early-exits when row min exceeds budget (correctness, not just perf)", func(t *testing.T) {
		if d := query.BoundedEditDistance("aaaaa", "bbbbb", 2); d != 3 {
			t.Errorf("got %d, want 3", d)
		}
	})
}
