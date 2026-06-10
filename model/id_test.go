package model

import "testing"

// These expected IDs are taken verbatim from testdata/golden (outputs of the
// ORIGINAL codegraph v0.9.9 against testdata/fixtures) — they pin the ID
// formula as a cross-implementation contract.
func TestGenerateNodeID_MatchesOriginal(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		kind     NodeKind
		symbol   string
		line     int
		want     string
	}{
		{
			name:     "go struct from golden query.json",
			filePath: "internal/store/store.go",
			kind:     KindStruct,
			symbol:   "Store",
			line:     13,
			want:     "struct:cb336db3e2962f3c7761dee74649bc38",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateNodeID(tt.filePath, tt.kind, tt.symbol, tt.line)
			if got != tt.want {
				t.Errorf("GenerateNodeID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFileNodeID(t *testing.T) {
	if got := FileNodeID("src/a.ts"); got != "file:src/a.ts" {
		t.Errorf("FileNodeID() = %q", got)
	}
}

func TestRouteNodeID(t *testing.T) {
	got := RouteNodeID("cmd/app/main.go", 16, "GET", "/greet")
	want := "route:cmd/app/main.go:16:GET:/greet"
	if got != want {
		t.Errorf("RouteNodeID() = %q, want %q", got, want)
	}
}
