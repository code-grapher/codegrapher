package snapshot

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
coverage: coverage
nodecoverage: node_coverage
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

// coverageCollectionDef is the .collection/definition.yaml for the per-file
// coverage collection. Columns mirror coverage.EncodeFileCoverage's layout.
const coverageCollectionDef = `titles:
  en: Coverage
record_file:
  name: coverage.ingr
  format: ingr
  type: "[]map[string]any"
primary_key:
  - $ID
columns:
  content_hash:
    type: string
    titles:
      en: Content Hash
  mode:
    type: string
    titles:
      en: Mode
  ranges:
    type: string
    titles:
      en: Ranges
  lines_covered:
    type: int
    titles:
      en: Lines Covered
  lines_uncovered:
    type: int
    titles:
      en: Lines Uncovered
  run_at:
    type: int
    titles:
      en: Run At
columns_order:
  - content_hash
  - mode
  - ranges
  - lines_covered
  - lines_uncovered
  - run_at
`

// nodeCoverageCollectionDef is the .collection/definition.yaml for the
// per-function coverage collection. Columns mirror coverage.EncodeNodeCoverage.
const nodeCoverageCollectionDef = `titles:
  en: Node Coverage
record_file:
  name: node_coverage.ingr
  format: ingr
  type: "[]map[string]any"
primary_key:
  - $ID
columns:
  content_hash:
    type: string
    titles:
      en: Content Hash
  lines_covered:
    type: int
    titles:
      en: Lines Covered
  lines_uncovered:
    type: int
    titles:
      en: Lines Uncovered
  run_at:
    type: int
    titles:
      en: Run At
columns_order:
  - content_hash
  - lines_covered
  - lines_uncovered
  - run_at
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
func writeREADME(outDir, projectRoot string) error {
	org, repo, isGitHub := detectRemote(projectRoot)

	var b strings.Builder

	// Title
	b.WriteString(fmt.Sprintf("# Code graph of %s/%s\n\n", org, repo))

	// Links
	b.WriteString("Indexed by [codegrapher](https://codegrapher.dev)")
	if isGitHub {
		b.WriteString(fmt.Sprintf(" · [Browse online](https://codegrapher.dev/github.com/%s/%s)", org, repo))
	}
	b.WriteString("\n\n")

	// Directories table
	b.WriteString("## Contents\n\n")
	b.WriteString("| Directory | Contents |\n")
	b.WriteString("|-----------|----------|\n")
	b.WriteString("| `nodes/` | Symbol nodes (functions, methods, types, variables, …) |\n")
	b.WriteString("| `edges/` | Call and reference edges between nodes |\n")
	b.WriteString("| `files/` | Indexed source files with language and hash metadata |\n")
	b.WriteString("| `project_metadata/` | Project-level metadata (name, root path, index timestamp) |\n")
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
