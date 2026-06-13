package gomod

import "testing"

const sample = `module github.com/example/proj

go 1.22.3

toolchain go1.26.4

require (
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.9.0 // indirect
)

require golang.org/x/mod v0.33.0

replace github.com/spf13/cobra => ../forked-cobra

replace github.com/old/dep v1.0.0 => github.com/new/dep v2.0.0

exclude github.com/bad/dep v0.1.0

retract (
	v1.0.0
	[v1.1.0, v1.2.0]
)
`

func TestParse(t *testing.T) {
	f, err := Parse("go.mod", []byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Module != "github.com/example/proj" {
		t.Errorf("Module = %q", f.Module)
	}
	if f.Go != "1.22.3" {
		t.Errorf("Go = %q", f.Go)
	}
	if f.Toolchain != "go1.26.4" {
		t.Errorf("Toolchain = %q", f.Toolchain)
	}
	if len(f.Requires) != 3 {
		t.Fatalf("Requires = %d, want 3", len(f.Requires))
	}
	if !f.Requires[1].Indirect {
		t.Errorf("testify should be indirect")
	}
	if f.Requires[0].Path != "github.com/spf13/cobra" || f.Requires[0].Version != "v1.10.2" {
		t.Errorf("Requires[0] = %+v", f.Requires[0])
	}
	if f.Requires[0].Line != 8 {
		t.Errorf("Requires[0].Line = %d, want 8", f.Requires[0].Line)
	}
	if len(f.Replaces) != 2 {
		t.Fatalf("Replaces = %d, want 2", len(f.Replaces))
	}
	if f.Replaces[0].NewPath != "../forked-cobra" || f.Replaces[0].NewVersion != "" {
		t.Errorf("local replace = %+v", f.Replaces[0])
	}
	if f.Replaces[0].Line != 14 {
		t.Errorf("Replaces[0].Line = %d, want 14", f.Replaces[0].Line)
	}
	if f.Replaces[1].NewPath != "github.com/new/dep" || f.Replaces[1].NewVersion != "v2.0.0" {
		t.Errorf("module replace = %+v", f.Replaces[1])
	}
	if len(f.Excludes) != 1 || f.Excludes[0].Path != "github.com/bad/dep" {
		t.Errorf("Excludes = %+v", f.Excludes)
	}
	if f.Excludes[0].Line != 18 {
		t.Errorf("Excludes[0].Line = %d, want 18", f.Excludes[0].Line)
	}
	if len(f.Retracts) != 2 {
		t.Fatalf("Retracts = %d, want 2", len(f.Retracts))
	}
	if f.Retracts[0].Low != "v1.0.0" || f.Retracts[0].High != "v1.0.0" {
		t.Errorf("single retract = %+v", f.Retracts[0])
	}
	if f.Retracts[0].Line != 21 {
		t.Errorf("Retracts[0].Line = %d, want 21", f.Retracts[0].Line)
	}
	if f.Retracts[1].Low != "v1.1.0" || f.Retracts[1].High != "v1.2.0" {
		t.Errorf("range retract = %+v", f.Retracts[1])
	}
}

func TestParseMalformed(t *testing.T) {
	if _, err := Parse("go.mod", []byte("this is not a go.mod {{{")); err == nil {
		t.Fatal("expected error for malformed go.mod")
	}
}
