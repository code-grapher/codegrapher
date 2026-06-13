package pkgjson

import "testing"

const sample = `{
  "name": "@acme/widget",
  "version": "1.2.3",
  "engines": { "node": ">=18" },
  "dependencies": { "left-pad": "^1.3.0", "react": "^18.2.0" },
  "devDependencies": { "vitest": "^1.0.0", "left-pad": "^1.3.0" },
  "peerDependencies": { "react": ">=18" },
  "optionalDependencies": { "fsevents": "^2.3.0" }
}`

func TestParse(t *testing.T) {
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Name != "@acme/widget" {
		t.Errorf("Name = %q", f.Name)
	}
	if f.Version != "1.2.3" {
		t.Errorf("Version = %q", f.Version)
	}
	if f.Engines["node"] != ">=18" {
		t.Errorf("Engines = %v", f.Engines)
	}
	if f.Dependencies["left-pad"] != "^1.3.0" || f.Dependencies["react"] != "^18.2.0" {
		t.Errorf("Dependencies = %v", f.Dependencies)
	}
	if f.DevDependencies["vitest"] != "^1.0.0" {
		t.Errorf("DevDependencies = %v", f.DevDependencies)
	}
	if f.PeerDependencies["react"] != ">=18" {
		t.Errorf("PeerDependencies = %v", f.PeerDependencies)
	}
	if f.OptionalDependencies["fsevents"] != "^2.3.0" {
		t.Errorf("OptionalDependencies = %v", f.OptionalDependencies)
	}
}

func TestParseMinimal(t *testing.T) {
	f, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Name != "" || len(f.Dependencies) != 0 {
		t.Errorf("expected empty File, got %+v", f)
	}
}

func TestParseMalformed(t *testing.T) {
	if _, err := Parse([]byte(`{ not json`)); err == nil {
		t.Fatal("expected error for malformed package.json")
	}
}
