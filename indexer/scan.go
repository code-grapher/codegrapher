package indexer

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/internal/extract"
	"github.com/specscore/codegrapher/model"
)

// MaxFileSize is the largest file (bytes) the indexer will parse. Generated
// bundles, minified JS, and vendored blobs above this produce no useful
// symbols (MAX_FILE_SIZE in src/extraction/index.ts).
const MaxFileSize = 1024 * 1024

// defaultIgnoreDirs mirrors DEFAULT_IGNORE_DIRS in src/extraction/index.ts:
// dependency, build, cache, and tooling-output directory names excluded by
// default — applied uniformly, git or not, tracked or not. The only opt-in is
// an explicit .gitignore negation (e.g. "!vendor/").
var defaultIgnoreDirs = map[string]bool{
	// JS / TS — dependency directories
	"node_modules": true, "bower_components": true, "jspm_packages": true, "web_modules": true,
	".yarn": true, ".pnpm-store": true,
	// JS / TS — framework & bundler build / cache / deploy output
	".next": true, ".nuxt": true, ".svelte-kit": true, ".turbo": true, ".vite": true,
	".parcel-cache": true, ".angular": true,
	".docusaurus": true, "storybook-static": true, ".vinxi": true, ".nitro": true, "out-tsc": true,
	".vercel": true, ".netlify": true, ".wrangler": true,
	// Build output (common across ecosystems)
	"dist": true, "build": true, "out": true, ".output": true,
	// Test / coverage
	"coverage": true, ".nyc_output": true,
	// Python
	"__pycache__": true, "__pypackages__": true, ".venv": true, "venv": true, ".pixi": true,
	".pdm-build":  true,
	".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true, ".tox": true, ".nox": true,
	".hypothesis":        true,
	".ipynb_checkpoints": true, ".eggs": true,
	// Rust / JVM (Maven, Gradle, Scala)
	"target": true, ".gradle": true,
	// .NET
	"obj": true,
	// Vendored deps (Go, PHP/Composer, Ruby/Bundler)
	"vendor": true,
	// Swift / iOS
	".build": true, "Pods": true, "Carthage": true, "DerivedData": true, ".swiftpm": true,
	// Dart / Flutter
	".dart_tool": true, ".pub-cache": true,
	// Native (Android NDK, C/C++ deps)
	".cxx": true, ".externalNativeBuild": true, "vcpkg_installed": true,
	// Scala tooling
	".bloop": true, ".metals": true,
	// Lua / Luau (LuaRocks)
	"lua_modules": true, ".luarocks": true,
	// Delphi / RAD Studio IDE backups
	"__history": true, "__recovery": true,
	// Generic cache
	".cache": true,
}

// IsSourceFile reports whether a project-relative path has a supported source
// extension (Go / TypeScript / TSX / JavaScript / JSX in this port).
func IsSourceFile(relPath string) bool {
	return extract.DetectLanguage(relPath) != model.LangUnknown
}

// --- minimal gitignore matcher ----------------------------------------------
//
// The original uses the `ignore` npm library with full gitignore semantics.
// This port implements the common subset: blank lines and comments, negation
// (!), dir-only patterns (trailing /), root-anchored patterns (containing /),
// and * / ** / ? globs. Last matching rule wins, like git. Built-in default
// ignores are evaluated first so a .gitignore negation can override them.

type ignoreRule struct {
	segment string         // non-empty: match this glob against any path segment
	segRe   *regexp.Regexp // compiled segment glob (nil for literal segment)
	re      *regexp.Regexp // anchored rule: full relative-path regexp
	dirOnly bool
	negate  bool
}

type ignoreMatcher struct {
	rules []ignoreRule
}

// globSegment translates a single-segment glob to a regexp.
func globSegment(pattern string) string {
	var b strings.Builder
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(`[^/]*`)
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}

// globPath translates a multi-segment gitignore glob to a regexp source.
func globPath(pattern string) string {
	var b strings.Builder
	i := 0
	for i < len(pattern) {
		if strings.HasPrefix(pattern[i:], "**/") {
			b.WriteString(`(?:[^/]+/)*`)
			i += 3
			continue
		}
		if strings.HasPrefix(pattern[i:], "**") {
			b.WriteString(`.*`)
			i += 2
			continue
		}
		switch pattern[i] {
		case '*':
			b.WriteString(`[^/]*`)
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
		i++
	}
	return b.String()
}

// addPattern compiles one gitignore pattern line into the matcher.
func (m *ignoreMatcher) addPattern(line string) {
	line = strings.TrimRight(line, " \t")
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	negate := false
	if strings.HasPrefix(line, "!") {
		negate = true
		line = line[1:]
	}
	dirOnly := strings.HasSuffix(line, "/")
	line = strings.TrimSuffix(line, "/")
	if line == "" {
		return
	}
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")

	if !anchored {
		rule := ignoreRule{segment: line, dirOnly: dirOnly, negate: negate}
		if strings.ContainsAny(line, "*?") {
			rule.segRe = regexp.MustCompile("^" + globSegment(line) + "$")
		}
		m.rules = append(m.rules, rule)
		return
	}
	re, err := regexp.Compile("^" + globPath(line) + "$")
	if err != nil {
		return // drop uncompilable patterns, like readGitignorePatterns
	}
	m.rules = append(m.rules, ignoreRule{re: re, dirOnly: dirOnly, negate: negate})
}

func (r *ignoreRule) matchSegment(seg string) bool {
	if r.segRe != nil {
		return r.segRe.MatchString(seg)
	}
	return r.segment == seg
}

// matches reports whether the rule matches relPath (POSIX, no leading slash).
func (r *ignoreRule) matches(relPath string, isDir bool) bool {
	segs := strings.Split(relPath, "/")
	if r.segment != "" || r.segRe != nil {
		// Segment rule: match any segment; dir-only rules can't match the
		// final segment of a file path (only its ancestor directories).
		last := len(segs) - 1
		for i, seg := range segs {
			if !r.matchSegment(seg) {
				continue
			}
			if r.dirOnly && i == last && !isDir {
				continue
			}
			return true
		}
		return false
	}
	// Anchored rule: match the path itself or, for dir rules (and plain
	// prefixes), any ancestor directory.
	if r.re.MatchString(relPath) {
		return !r.dirOnly || isDir
	}
	for i := 1; i < len(segs); i++ {
		if r.re.MatchString(strings.Join(segs[:i], "/")) {
			return true // an ancestor dir matches → everything below is covered
		}
	}
	return false
}

// Ignored applies last-match-wins over all rules.
func (m *ignoreMatcher) Ignored(relPath string, isDir bool) bool {
	ignored := false
	for i := range m.rules {
		if m.rules[i].matches(relPath, isDir) {
			ignored = !m.rules[i].negate
		}
	}
	return ignored
}

// buildDefaultIgnore seeds a matcher with the built-in defaults merged with
// the project's root .gitignore, so a negation there (e.g. "!vendor/")
// overrides a default — mirroring buildDefaultIgnore in src/extraction/index.ts.
func buildDefaultIgnore(rootDir string) *ignoreMatcher {
	m := &ignoreMatcher{}
	for name := range defaultIgnoreDirs {
		m.addPattern(name + "/")
	}
	m.addPattern("*.egg-info/")
	m.addPattern("cmake-build-*/")
	m.addPattern("bazel-*/")
	if data, err := os.ReadFile(filepath.Join(rootDir, ".gitignore")); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			m.addPattern(strings.TrimSuffix(line, "\r"))
		}
	}
	return m
}

// --- scanning ----------------------------------------------------------------

// ScanDirectory enumerates the project's source files as project-relative
// POSIX paths. In git repos it uses `git ls-files` (which respects .gitignore
// at all levels); otherwise it walks the filesystem applying the built-in
// default ignores plus .gitignore files. Mirrors scanDirectory in
// src/extraction/index.ts. The result is sorted for determinism.
// ScanDirectory returns every non-gitignored file under rootDir. Admission is
// decoupled from language detection: every file that .gitignore filtering keeps
// is indexed. Recognized languages get full symbol extraction; unknown-language
// files (including SpecScore artifacts and binaries) become bare file-level
// nodes rather than vanishing.
func ScanDirectory(rootDir string) []string {
	files := gitVisibleFiles(rootDir)
	if files == nil {
		files = scanDirectoryWalk(rootDir)
	}
	sort.Strings(files)
	return files
}

// gitVisibleFiles returns all files visible to git (tracked + untracked, not
// ignored), filtered through the built-in default ignores. Returns nil when
// git is unavailable, the dir isn't a repo, or the project dir is gitignored
// by a parent repo (callers fall back to a filesystem walk).
func gitVisibleFiles(rootDir string) []string {
	gitRoot, err := gitOutput(rootDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil
	}
	absRoot, _ := filepath.Abs(rootDir)
	if rp, err := filepath.EvalSymlinks(gitRoot); err == nil {
		gitRoot = rp
	}
	if rp, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = rp
	}
	if gitRoot != absRoot {
		// rootDir is nested inside a parent repo. If the parent ignores it,
		// git ls-files would return nothing — fall back to a walk.
		if exec.Command("git", "-C", rootDir, "check-ignore", "-q", absRoot).Run() == nil {
			return nil
		}
	}

	seen := map[string]bool{}
	if !collectGitFiles(rootDir, "", seen) {
		return nil
	}
	ig := buildDefaultIgnore(rootDir)
	var files []string
	for f := range seen {
		if !ig.Ignored(f, false) {
			files = append(files, f)
		}
	}
	return files
}

// collectGitFiles gathers tracked + untracked files from the repo at repoDir,
// prefixing each with prefix. Recurses into embedded (non-submodule) git
// repos, which surface in untracked output as opaque "subdir/" entries.
func collectGitFiles(repoDir, prefix string, files map[string]bool) bool {
	tracked, err := gitOutput(repoDir, "ls-files", "-z", "-c", "--recurse-submodules")
	if err != nil {
		return false
	}
	for rel := range strings.SplitSeq(tracked, "\x00") {
		if rel != "" {
			files[prefix+filepath.ToSlash(rel)] = true
		}
	}
	untracked, err := gitOutput(repoDir, "ls-files", "-z", "-o", "--exclude-standard")
	if err != nil {
		return false
	}
	for rel := range strings.SplitSeq(untracked, "\x00") {
		if rel == "" {
			continue
		}
		if strings.HasSuffix(rel, "/") {
			childDir := filepath.Join(repoDir, rel)
			if _, err := os.Stat(filepath.Join(childDir, ".git")); err == nil {
				collectGitFiles(childDir, prefix+rel, files)
			}
			continue
		}
		files[prefix+filepath.ToSlash(rel)] = true
	}
	return true
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// scanDirectoryWalk is the filesystem-walk fallback for non-git projects.
// Nested .gitignore files layer per-directory, mirroring scanDirectoryWalk in
// src/extraction/index.ts.
func scanDirectoryWalk(rootDir string) []string {
	var files []string
	visited := map[string]bool{}

	type scoped struct {
		dir string
		m   *ignoreMatcher
	}

	ignored := func(fullPath string, isDir bool, matchers []scoped) bool {
		for _, sm := range matchers {
			rel, err := filepath.Rel(sm.dir, fullPath)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if rel == "" || strings.HasPrefix(rel, "..") {
				continue
			}
			if sm.m.Ignored(rel, isDir) {
				return true
			}
		}
		return false
	}

	var walk func(dir string, matchers []scoped)
	walk = func(dir string, matchers []scoped) {
		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return
		}
		if visited[realDir] {
			return // symlink cycle
		}
		visited[realDir] = true

		// This directory's own .gitignore applies to everything below it.
		// The root's is already merged into the seeded base matcher.
		active := matchers
		if dir != rootDir {
			if data, err := os.ReadFile(filepath.Join(dir, ".gitignore")); err == nil {
				m := &ignoreMatcher{}
				for line := range strings.SplitSeq(string(data), "\n") {
					m.addPattern(strings.TrimSuffix(line, "\r"))
				}
				active = append(append([]scoped{}, matchers...), scoped{dir: dir, m: m})
			}
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if name == ".git" || IsCodeGraphDataDir(name) {
				continue
			}
			fullPath := filepath.Join(dir, name)
			relPath, err := filepath.Rel(rootDir, fullPath)
			if err != nil {
				continue
			}
			relPath = filepath.ToSlash(relPath)

			isDir := entry.IsDir()
			if entry.Type()&os.ModeSymlink != 0 {
				fi, err := os.Stat(fullPath)
				if err != nil {
					continue // broken symlink
				}
				isDir = fi.IsDir()
			}

			if isDir {
				if !ignored(fullPath, true, active) {
					walk(fullPath, active)
				}
			} else if !ignored(fullPath, false, active) {
				files = append(files, relPath)
			}
		}
	}

	walk(rootDir, []scoped{{dir: rootDir, m: buildDefaultIgnore(rootDir)}})
	return files
}
