package snapshot

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// ---------------------------------------------------------------------------
// inGitDB schema files (static, written on every export)
// ---------------------------------------------------------------------------

// rootCollectionsYAML maps collection IDs to their directory paths.
// "metadata" avoids the underscore restriction on collection IDs.
const rootCollectionsYAML = `nodes: nodes
edges: edges
files: files
metadata: project_metadata
`

// settingsYAML sets the default namespace for the database.
const settingsYAML = `default_namespace: codegrapher
`

// nodeCollectionDef is the .collection/definition.yaml for the nodes collection.
const nodeCollectionDef = `titles:
  en: Nodes
record_file:
  name: nodes.ingr
  format: ingr
  type: "[]map[string]any"
primary_key:
  - $ID
columns:
  kind:
    type: string
    titles:
      en: Kind
  name:
    type: string
    titles:
      en: Name
  qualified_name:
    type: string
    titles:
      en: Qualified Name
  file_path:
    type: string
    titles:
      en: File Path
  language:
    type: string
    titles:
      en: Language
  start_line:
    type: int
    titles:
      en: Start Line
  end_line:
    type: int
    titles:
      en: End Line
  start_column:
    type: int
    titles:
      en: Start Column
  end_column:
    type: int
    titles:
      en: End Column
  docstring:
    type: any
    titles:
      en: Docstring
  signature:
    type: any
    titles:
      en: Signature
  visibility:
    type: any
    titles:
      en: Visibility
  is_exported:
    type: bool
    titles:
      en: Is Exported
  is_async:
    type: bool
    titles:
      en: Is Async
  is_static:
    type: bool
    titles:
      en: Is Static
  is_abstract:
    type: bool
    titles:
      en: Is Abstract
  decorators:
    type: any
    titles:
      en: Decorators
  type_parameters:
    type: any
    titles:
      en: Type Parameters
  return_type:
    type: any
    titles:
      en: Return Type
columns_order:
  - kind
  - name
  - qualified_name
  - file_path
  - language
  - start_line
  - end_line
  - start_column
  - end_column
  - docstring
  - signature
  - visibility
  - is_exported
  - is_async
  - is_static
  - is_abstract
  - decorators
  - type_parameters
  - return_type
`

// edgeCollectionDef is the .collection/definition.yaml for the edges collection.
const edgeCollectionDef = `titles:
  en: Edges
record_file:
  name: edges.ingr
  format: ingr
  type: "[]map[string]any"
primary_key:
  - $ID
columns:
  source:
    type: string
    titles:
      en: Source
  target:
    type: string
    titles:
      en: Target
  kind:
    type: string
    titles:
      en: Kind
  metadata:
    type: any
    titles:
      en: Metadata
  line:
    type: int
    titles:
      en: Line
  col:
    type: int
    titles:
      en: Column
  provenance:
    type: any
    titles:
      en: Provenance
columns_order:
  - source
  - target
  - kind
  - metadata
  - line
  - col
  - provenance
`

// fileCollectionDef is the .collection/definition.yaml for the files collection.
const fileCollectionDef = `titles:
  en: Files
record_file:
  name: files.ingr
  format: ingr
  type: "[]map[string]any"
primary_key:
  - $ID
columns:
  content_hash:
    type: string
    titles:
      en: Content Hash
  language:
    type: string
    titles:
      en: Language
  size:
    type: int
    titles:
      en: Size
  node_count:
    type: int
    titles:
      en: Node Count
  errors:
    type: any
    titles:
      en: Errors
columns_order:
  - content_hash
  - language
  - size
  - node_count
  - errors
`

// metadataCollectionDef is the .collection/definition.yaml for the project_metadata collection.
const metadataCollectionDef = `titles:
  en: Project Metadata
record_file:
  name: project_metadata.ingr
  format: ingr
  type: "[]map[string]any"
primary_key:
  - $ID
columns:
  value:
    type: string
    titles:
      en: Value
columns_order:
  - value
`

func writeIngitdbConfig(outDir string) error {
	dir := filepath.Join(outDir, ".ingitdb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "root-collections.yaml"), []byte(rootCollectionsYAML), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "settings.yaml"), []byte(settingsYAML), 0o644)
}

func writeCollectionDef(dir, content string) error {
	schemaDir := filepath.Join(dir, ".collection")
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(schemaDir, "definition.yaml"), []byte(content), 0o644)
}

// ---------------------------------------------------------------------------
// README generation
// ---------------------------------------------------------------------------

// writeREADME generates a summary README.md in outDir.
func writeREADME(s *store.Store, outDir, projectRoot string) error {
	org, repo, isGitHub := detectRemote(projectRoot)
	stats, err := s.GetStats()
	if err != nil {
		return err
	}
	files, err := s.GetAllFiles()
	if err != nil {
		return err
	}

	var b strings.Builder

	// Title
	b.WriteString(fmt.Sprintf("# Code graph of %s/%s\n\n", org, repo))

	// Links
	b.WriteString(fmt.Sprintf("Indexed by [codegrapher](https://codegrapher.dev)"))
	if isGitHub {
		b.WriteString(fmt.Sprintf(" · [Browse online](https://codegrapher.dev/github.com/%s/%s)", org, repo))
	}
	b.WriteString("\n\n")

	// Stats table
	b.WriteString("## Stats\n\n")
	b.WriteString("| Metric | Count |\n")
	b.WriteString("|--------|-------|\n")

	// Files by language
	langOrder := sortedKeys(stats.FilesByLanguage, func(a, b model.Language) bool { return string(a) < string(b) })
	for _, lang := range langOrder {
		n := stats.FilesByLanguage[lang]
		if n > 0 {
			b.WriteString(fmt.Sprintf("| Files (%s) | %d |\n", lang, n))
		}
	}

	// Go packages (distinct dirs containing .go files)
	goPackages := countGoPackages(files)
	if goPackages > 0 {
		b.WriteString(fmt.Sprintf("| Go packages | %d |\n", goPackages))
	}

	// Nodes by kind (only nonzero, in a fixed display order)
	nodeKindOrder := []model.NodeKind{
		model.KindFunction, model.KindMethod, model.KindStruct, model.KindInterface,
		model.KindTypeAlias, model.KindConstant, model.KindVariable,
		model.KindImport, model.KindRoute,
	}
	for _, kind := range nodeKindOrder {
		n := stats.NodesByKind[kind]
		if n > 0 {
			b.WriteString(fmt.Sprintf("| Nodes (%s) | %d |\n", kind, n))
		}
	}
	// remaining kinds not in the fixed order
	for _, kind := range sortedNodeKinds(stats.NodesByKind) {
		if !inNodeKindOrder(kind, nodeKindOrder) && stats.NodesByKind[kind] > 0 {
			b.WriteString(fmt.Sprintf("| Nodes (%s) | %d |\n", kind, stats.NodesByKind[kind]))
		}
	}
	// Total nodes
	b.WriteString(fmt.Sprintf("| Nodes (total) | %d |\n", stats.NodeCount))

	// Edges by kind (nonzero, sorted)
	for _, kind := range sortedEdgeKinds(stats.EdgesByKind) {
		n := stats.EdgesByKind[kind]
		if n > 0 {
			b.WriteString(fmt.Sprintf("| Edges (%s) | %d |\n", kind, n))
		}
	}
	b.WriteString(fmt.Sprintf("| Edges (total) | %d |\n", stats.EdgeCount))

	b.WriteString("\n")
	b.WriteString("Regenerate: `codegrapher init && codegrapher export`\n\n")
	b.WriteString("This directory is an [inGitDB](https://ingitdb.com) database in [INGR](https://ingr.io) format.\n")

	return os.WriteFile(filepath.Join(outDir, "README.md"), []byte(b.String()), 0o644)
}

// detectRemote runs `git remote get-url origin` in projectRoot and parses the result.
// Returns org, repo, isGitHub. Falls back to ("unknown", dirName, false) on error.
func detectRemote(projectRoot string) (org, repo string, isGitHub bool) {
	fallbackRepo := filepath.Base(projectRoot)
	if fallbackRepo == "" || fallbackRepo == "." {
		fallbackRepo = "unknown"
	}

	if projectRoot == "" {
		return "unknown", fallbackRepo, false
	}

	out, err := exec.Command("git", "-C", projectRoot, "remote", "get-url", "origin").Output()
	if err != nil {
		return "unknown", fallbackRepo, false
	}

	url := strings.TrimSpace(string(out))
	return parseGitRemote(url, fallbackRepo)
}

// parseGitRemote parses a git remote URL into org/repo components.
// Handles https://github.com/org/repo, git@github.com:org/repo, and similar.
func parseGitRemote(url, fallbackRepo string) (org, repo string, isGitHub bool) {
	// Normalize: strip trailing .git
	url = strings.TrimSuffix(url, ".git")

	// HTTPS: https://github.com/org/repo
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
		url = strings.TrimPrefix(url, "https://")
		url = strings.TrimPrefix(url, "http://")
		parts := strings.SplitN(url, "/", 3)
		if len(parts) == 3 {
			host := parts[0]
			org := parts[1]
			repo := parts[2]
			return org, repo, host == "github.com"
		}
	}

	// SSH: git@github.com:org/repo
	if strings.HasPrefix(url, "git@") {
		url = strings.TrimPrefix(url, "git@")
		colonIdx := strings.Index(url, ":")
		if colonIdx >= 0 {
			host := url[:colonIdx]
			rest := url[colonIdx+1:]
			parts := strings.SplitN(rest, "/", 2)
			if len(parts) == 2 {
				return parts[0], parts[1], host == "github.com"
			}
		}
	}

	return "unknown", fallbackRepo, false
}

func countGoPackages(files []model.FileRecord) int {
	dirs := map[string]struct{}{}
	for _, f := range files {
		if f.Language == model.LangGo {
			dirs[filepath.Dir(f.Path)] = struct{}{}
		}
	}
	return len(dirs)
}

func sortedKeys[K ~string, V any](m map[K]V, less func(a, b K) bool) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return less(keys[i], keys[j]) })
	return keys
}

func sortedNodeKinds(m map[model.NodeKind]int) []model.NodeKind {
	return sortedKeys(m, func(a, b model.NodeKind) bool { return string(a) < string(b) })
}

func sortedEdgeKinds(m map[model.EdgeKind]int) []model.EdgeKind {
	return sortedKeys(m, func(a, b model.EdgeKind) bool { return string(a) < string(b) })
}

func inNodeKindOrder(kind model.NodeKind, order []model.NodeKind) bool {
	for _, k := range order {
		if k == kind {
			return true
		}
	}
	return false
}
