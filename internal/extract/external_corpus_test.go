package extract_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/internal/extract"
	"github.com/specscore/codegrapher/model"
)

// py3MaxParseErrorRate is a generous ceiling for the Python 3 corpus. The
// extractor should parse real-world Py3 source with very few hard failures;
// exceeding this signals a regression worth investigating.
const py3MaxParseErrorRate = 0.10

type externalRepo struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA    string `json:"sha"`
	Lang   string `json:"lang"`
	Python int    `json:"python"`
}

// repoLang returns the extractor language this repo exercises, defaulting to
// Python for legacy manifest entries without a `lang` field.
func (r externalRepo) repoLang() model.Language {
	switch r.Lang {
	case "java":
		return model.LangJava
	case "kotlin":
		return model.LangKotlin
	case "csharp":
		return model.LangCSharp
	case "ruby":
		return model.LangRuby
	case "rust":
		return model.LangRust
	case "php":
		return model.LangPHP
	case "c":
		return model.LangC
	case "scala":
		return model.LangScala
	case "swift":
		return model.LangSwift
	case "cpp":
		return model.LangCPP
	case "dart":
		return model.LangDart
	case "lua":
		return model.LangLua
	case "elixir":
		return model.LangElixir
	case "haskell":
		return model.LangHaskell
	case "objc":
		return model.LangObjC
	case "perl":
		return model.LangPerl
	case "erlang":
		return model.LangErlang
	case "julia":
		return model.LangJulia
	case "fsharp":
		return model.LangFSharp
	case "r":
		return model.LangR
	case "bash":
		return model.LangBash
	default:
		return model.LangPython
	}
}

// sourceExts returns the file extensions to collect for a repo's language.
func (r externalRepo) sourceExts() []string {
	switch r.repoLang() {
	case model.LangJava:
		return []string{".java"}
	case model.LangKotlin:
		return []string{".kt", ".kts"}
	case model.LangCSharp:
		return []string{".cs"}
	case model.LangRuby:
		return []string{".rb"}
	case model.LangRust:
		return []string{".rs"}
	case model.LangPHP:
		return []string{".php"}
	case model.LangC:
		return []string{".c", ".h"}
	case model.LangScala:
		return []string{".scala", ".sc"}
	case model.LangSwift:
		return []string{".swift"}
	case model.LangCPP:
		return []string{".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx"}
	case model.LangDart:
		return []string{".dart"}
	case model.LangLua:
		return []string{".lua"}
	case model.LangElixir:
		return []string{".ex", ".exs"}
	case model.LangHaskell:
		return []string{".hs"}
	case model.LangObjC:
		return []string{".m", ".h"}
	case model.LangPerl:
		return []string{".pl", ".pm"}
	case model.LangErlang:
		return []string{".erl", ".hrl"}
	case model.LangJulia:
		return []string{".jl"}
	case model.LangFSharp:
		return []string{".fs", ".fsi", ".fsx"}
	case model.LangR:
		return []string{".R", ".r"}
	case model.LangBash:
		return []string{".sh", ".bash"}
	default:
		return []string{".py", ".pyi"}
	}
}

type externalManifest struct {
	Repos []externalRepo `json:"repos"`
}

// TestExternalCorpus is an OPT-IN, informational parse/robustness corpus. It is
// NOT a golden and requires network access, so it is skipped unless
// CODEGRAPH_EXTERNAL_CORPUS=1. It shallow-clones each manifest repo at a pinned
// commit sha and runs every source file (per the repo's language) through the
// extractor, asserting no panics and (for the Python 3 repo) a low parse-error
// rate. Java repos are informational (no-panic) only.
func TestExternalCorpus(t *testing.T) {
	if os.Getenv("CODEGRAPH_EXTERNAL_CORPUS") != "1" {
		t.Skip("external corpus is opt-in and needs network; set CODEGRAPH_EXTERNAL_CORPUS=1 to run")
	}

	manifestPath := filepath.Join(repoRoot, "testdata", "external", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest externalManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(manifest.Repos) == 0 {
		t.Fatalf("manifest has no repos")
	}

	cacheDir := filepath.Join(repoRoot, "testdata", "external", "cache")

	for _, repo := range manifest.Repos {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dest := filepath.Join(cacheDir, repo.Name)
			if err := ensureClone(t, dest, repo.URL, repo.SHA); err != nil {
				t.Logf("WARNING: clone/fetch %s failed (offline?): %v", repo.Name, err)
				t.Skipf("skipping %s: could not obtain repo at %s", repo.Name, repo.SHA)
			}

			wantLang := repo.repoLang()
			files, err := collectSourceFiles(dest, repo.sourceExts())
			if err != nil {
				t.Fatalf("walk clone %s: %v", dest, err)
			}
			sort.Strings(files)

			var fileCount, errorFiles, totalNodes int
			for _, absPath := range files {
				content, err := os.ReadFile(absPath)
				if err != nil {
					t.Fatalf("read %s: %v", absPath, err)
				}
				lang := extract.DetectLanguage(absPath)
				if lang != wantLang {
					continue
				}
				relPath, err := filepath.Rel(dest, absPath)
				if err != nil {
					t.Fatalf("rel path: %v", err)
				}
				relPath = filepath.ToSlash(relPath)

				// A panic here fails the test, satisfying the no-panic assertion.
				result, err := extract.ExtractFile(relPath, content, lang)
				if err != nil {
					// A hard extractor error is itself a parse failure for this file.
					errorFiles++
					fileCount++
					continue
				}
				fileCount++
				totalNodes += len(result.Nodes)
				if len(result.Errors) > 0 {
					errorFiles++
				}
			}

			var rate float64
			if fileCount > 0 {
				rate = float64(errorFiles) / float64(fileCount)
			}
			t.Logf("%s (%s): files=%d nodes=%d parse-error-files=%d parse-error-rate=%.4f",
				repo.Name, wantLang, fileCount, totalNodes, errorFiles, rate)

			if wantLang == model.LangPython && repo.Python == 3 {
				t.Logf("%s: enforcing parse-error-rate <= %.2f (threshold)", repo.Name, py3MaxParseErrorRate)
				if rate > py3MaxParseErrorRate {
					t.Errorf("%s: parse-error rate %.4f exceeds threshold %.2f", repo.Name, rate, py3MaxParseErrorRate)
				}
			}
		})
	}
}

// ensureClone shallow-fetches url at sha into dest if not already populated.
func ensureClone(t *testing.T, dest, url, sha string) error {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		return nil // already cloned
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	steps := [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", url},
		{"fetch", "--depth", "1", "origin", sha},
		{"checkout", "-q", "FETCH_HEAD"},
	}
	for _, args := range steps {
		cmd := exec.Command("git", args...)
		cmd.Dir = dest
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmtCmdErr(args, out, err)
		}
	}
	return nil
}

func fmtCmdErr(args []string, out []byte, err error) error {
	return &cmdError{cmd: "git " + strings.Join(args, " "), out: string(out), err: err}
}

type cmdError struct {
	cmd string
	out string
	err error
}

func (e *cmdError) Error() string {
	return e.cmd + ": " + e.err.Error() + "\n" + e.out
}

// collectSourceFiles returns absolute paths of all files under root whose
// extension is in exts (lower-cased comparison).
func collectSourceFiles(root string, exts []string) ([]string, error) {
	want := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		want[e] = struct{}{}
	}
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := want[strings.ToLower(filepath.Ext(path))]; ok {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
