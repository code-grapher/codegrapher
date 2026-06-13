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

	"github.com/specscore/codegrapher/gomod"
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

// versionPrefix strips a leading range operator from an npm semver spec.
var versionPrefix = regexp.MustCompile(`^[\s^~>=<v]+`)

// DetectVersion resolves the toolchain MAJOR version for filePath (absolute)
// within projectRoot, given its already-determined language. Graphs are grouped
// by major version, so e.g. Go 1.22 and 1.26.4 both map to "v1", and TypeScript
// 5.4.2 maps to "v5". It walks up to the nearest governing manifest (go.mod for
// Go, package.json for TS/JS) and returns fallbackVersion ("v0") when nothing is
// detectable.
func DetectVersion(projectRoot, filePath string, lang model.Language) string {
	var ver string
	switch lang {
	case model.LangGo:
		ver = detectGoVersion(projectRoot, filePath)
	case model.LangTypeScript, model.LangJavaScript, model.LangTSX, model.LangJSX:
		ver = detectNodeVersion(projectRoot, filePath)
	}
	return majorVersion(ver)
}

// majorVersion reduces a raw toolchain version (e.g. "1.22", "^5.4.2") to its
// major component prefixed with "v" (e.g. "v1", "v5"), or fallbackVersion when
// no leading numeric component is present.
func majorVersion(raw string) string {
	raw = versionPrefix.ReplaceAllString(strings.TrimSpace(raw), "")
	i := 0
	for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
		i++
	}
	if i == 0 {
		return fallbackVersion
	}
	return "v" + raw[:i]
}

func detectGoVersion(projectRoot, filePath string) string {
	data := readNearest(projectRoot, filePath, "go.mod")
	if data == nil {
		return ""
	}
	f, err := gomod.Parse("go.mod", data)
	if err != nil {
		return ""
	}
	if f.Toolchain != "" {
		return strings.TrimPrefix(f.Toolchain, "go") // "go1.26.4" -> "1.26.4"
	}
	return f.Go
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
