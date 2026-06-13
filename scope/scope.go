// Package scope identifies the (language, toolchain-version) partition a source
// file belongs to. Graph data is stored and served per scope, so every file is
// mapped to exactly one scope at index time.
package scope

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/specscore/codegrapher/model"
)

// fallbackVersion is used when no toolchain version can be detected for a file.
const fallbackVersion = "v0"

// Scope is a (language, version) partition. Version is a detected toolchain
// version string (e.g. "1.22", "5.4.2") or fallbackVersion ("v0").
type Scope struct {
	Language model.Language
	Version  string
}

// Key is the stable identifier used in DB filenames, manifest entries, URL path
// segments, and CLI --scope values: "{language}-{version}".
func (s Scope) Key() string {
	return string(s.Language) + "-" + s.Version
}

var (
	// goDirective matches the `go 1.22` / `go 1.22.3` line in a go.mod.
	goDirective = regexp.MustCompile(`(?m)^\s*go\s+(\d+\.\d+(?:\.\d+)?)`)
	// versionPrefix strips a leading range operator from an npm semver spec.
	versionPrefix = regexp.MustCompile(`^[\s^~>=<v]+`)
	// safeVersion is the allowed character set for a version path segment.
	safeVersion = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// DetectVersion resolves the toolchain version for filePath (absolute) within
// projectRoot, given its already-determined language. It walks up to the
// nearest governing manifest (go.mod for Go, package.json for TS/JS) and
// returns fallbackVersion when nothing is detectable.
func DetectVersion(projectRoot, filePath string, lang model.Language) string {
	var ver string
	switch lang {
	case model.LangGo:
		ver = detectGoVersion(projectRoot, filePath)
	case model.LangTypeScript, model.LangJavaScript, model.LangTSX, model.LangJSX:
		ver = detectNodeVersion(projectRoot, filePath)
	}
	ver = sanitizeVersion(ver)
	if ver == "" {
		return fallbackVersion
	}
	return ver
}

func sanitizeVersion(v string) string {
	v = versionPrefix.ReplaceAllString(strings.TrimSpace(v), "")
	if v == "" || !safeVersion.MatchString(v) {
		return ""
	}
	return v
}

func detectGoVersion(projectRoot, filePath string) string {
	data := readNearest(projectRoot, filePath, "go.mod")
	if data == nil {
		return ""
	}
	m := goDirective.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return string(m[1])
}

func detectNodeVersion(projectRoot, filePath string) string {
	data := readNearest(projectRoot, filePath, "package.json")
	if data == nil {
		return ""
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Engines         map[string]string `json:"engines"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	if v := pkg.DevDependencies["typescript"]; v != "" {
		return v
	}
	if v := pkg.Dependencies["typescript"]; v != "" {
		return v
	}
	return pkg.Engines["node"]
}

// readNearest walks up from filePath's directory to projectRoot, returning the
// contents of the first directory containing name, or nil if none is found.
func readNearest(projectRoot, filePath, name string) []byte {
	root := filepath.Clean(projectRoot)
	dir := filepath.Dir(filepath.Clean(filePath))
	for {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return data
		}
		if dir == root {
			return nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}
