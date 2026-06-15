package main_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/internal/paritytest"
)

// binaryPath is set by TestMain after building the binary.
var binaryPath string

func TestMain(m *testing.M) {
	bin, err := buildBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "parity: skipping — binary build failed: %v\n", err)
		os.Exit(0)
	}
	binaryPath = bin
	defer func() { _ = os.Remove(bin) }()
	os.Exit(m.Run())
}

func buildBinary() (string, error) {
	tmp, err := os.CreateTemp("", "codegrapher-parity-*")
	if err != nil {
		return "", err
	}
	_ = tmp.Close()
	path := tmp.Name()
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", path, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Dir = filepath.Join(repoRoot(), "cmd", "codegrapher")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build failed: %s\n%s", err, out)
	}
	return path, nil
}

func repoRoot() string {
	// cmd/codegrapher/parity_test.go → repo root is two levels up
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// fixture describes a parity fixture.
type fixture struct {
	name    string
	query   string
	symbols []string
}

var fixtures = []fixture{
	{
		name:    "go-small",
		query:   "store",
		symbols: []string{"Get", "Set", "Lookup", "normalize", "handleGreet", "Store::Get"},
	},
	{
		name:    "ts-small",
		query:   "store",
		symbols: []string{"get", "set", "lookup", "normalize", "describe", "Cache::lookup"},
	},
	{
		name:    "py-small",
		query:   "dog",
		symbols: []string{"speak", "describe", "make_dog", "Dog", "label", "Dog::speak"},
	},
	{
		name:    "cs-small",
		query:   "dog",
		symbols: []string{"Speak", "Describe", "MakeDog", "Dog", "Label", "Dog::Speak"},
	},
	{
		name:    "java-small",
		query:   "circle",
		symbols: []string{"area", "label", "run", "Circle", "Shape", "Circle::area"},
	},
	{
		name:    "kt-small",
		query:   "circle",
		symbols: []string{"area", "label", "run", "Circle", "Shape", "Circle::area"},
	},
	{
		name:    "rb-small",
		query:   "dog",
		symbols: []string{"speak", "describe", "make_dog", "Dog", "breed", "Dog::speak"},
	},
	{
		name:    "rs-small",
		query:   "circle",
		symbols: []string{"area", "label", "run", "Circle", "Shape", "Circle::area"},
	},
	{
		name:    "php-small",
		query:   "dog",
		symbols: []string{"speak", "describe", "make_dog", "Dog", "walk", "Dog::speak"},
	},
	{
		name:    "c-small",
		query:   "shape",
		symbols: []string{"area", "label", "run", "Shape", "Kind", "PI"},
	},
	{
		name:    "scala-small",
		query:   "circle",
		symbols: []string{"area", "label", "run", "Circle", "Shape", "Circle::area"},
	},
	{
		name:    "swift-small",
		query:   "circle",
		symbols: []string{"area", "label", "run", "Circle", "Shape", "Point::area"},
	},
	{
		name:    "cpp-small",
		query:   "shape",
		symbols: []string{"area", "distanceTo", "run", "Circle", "Shape", "Point"},
	},
	{
		name:    "dart-small",
		query:   "circle",
		symbols: []string{"area", "label", "run", "Circle", "Shape", "Circle::area"},
	},
	{
		name:    "lua-small",
		query:   "shape",
		symbols: []string{"area", "label", "run", "Shape", "new", "Shape::area"},
	},
	{
		name:    "elixir-small",
		query:   "circle",
		symbols: []string{"area", "label", "run", "Circle", "Shape", "Circle::area"},
	},
	{
		name:    "haskell-small",
		query:   "circle",
		symbols: []string{"area", "label", "run", "Circle", "Shape"},
	},
	{
		name:    "objc-small",
		query:   "circle",
		symbols: []string{"area", "label", "run", "Circle", "Shape", "Drawable"},
	},
	{
		name:    "perl-small",
		query:   "dog",
		symbols: []string{"speak", "new", "Dog", "Animal", "Dog::speak", "Animal::new"},
	},
	{
		name:    "erlang-small",
		query:   "shape",
		symbols: []string{"area", "run", "shape", "app", "circle"},
	},
	{
		name:    "julia-small",
		query:   "circle",
		symbols: []string{"area", "describe", "Circle", "Rectangle", "Shape", "Shapes::area"},
	},
	{
		name:    "fsharp-small",
		query:   "circle",
		symbols: []string{"Radius", "run", "Shapes", "App", "Circle", "Shapes::area"},
	},
	{
		name:    "r-small",
		query:   "area",
		symbols: []string{"area", "twice", "run", "util", "RADIUS", "stats"},
	},
	{
		name:    "bash-small",
		query:   "greet",
		symbols: []string{"greet", "helper", "MAX_RETRIES", "name"},
	},
	{
		name:    "powershell-small",
		query:   "speak",
		symbols: []string{"Get-Area", "Animal", "Dog", "Invoke-Main", "Speak", "Name"},
	},
	{
		name:    "sql-small",
		query:   "users",
		symbols: []string{"users", "orders", "user_orders", "id", "name", "user_id"},
	},
}

// TestParityGoldens runs the binary against all goldens and asserts full-value parity.
func TestParityGoldens(t *testing.T) {
	if binaryPath == "" {
		t.Skip("binary not available")
	}

	root := repoRoot()
	fixtureBase := filepath.Join(root, "testdata", "fixtures")
	goldenBase := filepath.Join(root, "testdata", "golden")

	for _, fix := range fixtures {
		fix := fix
		t.Run(fix.name, func(t *testing.T) {
			// Copy fixture to a temp dir so init doesn't pollute the source tree.
			tmpDir := t.TempDir()
			srcFixture := filepath.Join(fixtureBase, fix.name)
			if err := copyDir(srcFixture, tmpDir); err != nil {
				t.Fatalf("copy fixture: %v", err)
			}

			// Init the index.
			env := append(os.Environ(), "CODEGRAPH_NO_WATCH=1")
			initOut, err := runBinary(env, tmpDir, "init")
			if err != nil {
				t.Fatalf("init failed: %v\n%s", err, initOut)
			}

			goldenDir := filepath.Join(goldenBase, fix.name)

			// --- status ---
			t.Run("status", func(t *testing.T) {
				got, err := runBinary(env, tmpDir, "status", "--json")
				if err != nil {
					t.Fatalf("status: %v\n%s", err, got)
				}
				assertGolden(t, filepath.Join(goldenDir, "status.json"), got, false)
			})

			// --- files ---
			t.Run("files", func(t *testing.T) {
				got, err := runBinary(env, tmpDir, "files", "--json")
				if err != nil {
					t.Fatalf("files: %v\n%s", err, got)
				}
				assertGolden(t, filepath.Join(goldenDir, "files.json"), got, true)
			})

			// --- query ---
			t.Run("query", func(t *testing.T) {
				got, err := runBinary(env, tmpDir, "query", fix.query, "--json", "-l", "20")
				if err != nil {
					t.Fatalf("query: %v\n%s", err, got)
				}
				assertGolden(t, filepath.Join(goldenDir, "query.json"), got, false)
			})

			// --- callers / callees / impact per symbol ---
			for _, sym := range fix.symbols {
				sym := sym
				t.Run("callers-"+sym, func(t *testing.T) {
					got, err := runBinary(env, tmpDir, "callers", sym, "--json")
					if err != nil {
						t.Fatalf("callers %s: %v\n%s", sym, err, got)
					}
					assertGolden(t, filepath.Join(goldenDir, "callers-"+sym+".json"), got, false)
				})
				t.Run("callees-"+sym, func(t *testing.T) {
					got, err := runBinary(env, tmpDir, "callees", sym, "--json")
					if err != nil {
						t.Fatalf("callees %s: %v\n%s", sym, err, got)
					}
					assertGolden(t, filepath.Join(goldenDir, "callees-"+sym+".json"), got, false)
				})
				t.Run("impact-"+sym, func(t *testing.T) {
					got, err := runBinary(env, tmpDir, "impact", sym, "--json")
					if err != nil {
						t.Fatalf("impact %s: %v\n%s", sym, err, got)
					}
					assertGolden(t, filepath.Join(goldenDir, "impact-"+sym+".json"), got, false)
				})
			}
		})
	}
}

func assertGolden(t *testing.T, goldenPath string, got []byte, topLevelUnordered bool) {
	t.Helper()
	// Strip trailing newline from binary output.
	got = bytes.TrimRight(got, "\n")
	diff, err := paritytest.Diff(goldenPath, got, topLevelUnordered)
	if err != nil {
		t.Fatalf("parity diff error: %v", err)
	}
	if diff != "" {
		t.Errorf("parity mismatch:\n%s", diff)
	}
}

func runBinary(env []string, dir string, args ...string) ([]byte, error) {
	cmd := exec.Command(binaryPath, args...)
	cmd.Env = env
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		return append(out, stderr...), err
	}
	return out, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		// Skip .codegraph directories if they happen to exist in the fixture.
		if strings.Contains(rel, ".codegraph") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, fi.Mode())
	})
}
