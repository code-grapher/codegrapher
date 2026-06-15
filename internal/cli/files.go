package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/indexer"
	"github.com/spf13/cobra"
)

func newFilesCmd() *cobra.Command {
	var jsonOut bool
	var pathFlag string
	var filterDir string
	var pattern string
	var format string
	var maxDepth int
	var noMetadata bool
	var scope string

	cmd := &cobra.Command{
		Use:   "files",
		Short: "Show project file structure from the index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var projectPath string
			if pathFlag != "" {
				projectPath = resolveArg([]string{pathFlag}, 0)
			} else {
				cwd, _ := os.Getwd()
				projectPath = findNearestOrReturn(cwd)
			}

			if !indexer.IsInitialized(projectPath) {
				printError(fmt.Sprintf("CodeGraph not initialized in %s", projectPath))
				os.Exit(1)
			}

			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				printError(fmt.Sprintf("Failed to open index: %s", err))
				os.Exit(1)
			}
			defer func() { _ = idx.Close() }()

			q := NewStoreQuerier(idx.StoresFiltered(splitCSV(scope))...)
			files, err := q.Files()
			if err != nil {
				printError(fmt.Sprintf("Failed to list files: %s", err))
				os.Exit(1)
			}

			if len(files) == 0 {
				printInfo("No files indexed. Run \"codegraph index\" first.")
				return nil
			}

			// Filter by directory prefix.
			if filterDir != "" {
				var filtered []FileInfo
				for _, f := range files {
					if strings.HasPrefix(f.Path, filterDir) || strings.HasPrefix(f.Path, "./"+filterDir) {
						filtered = append(filtered, f)
					}
				}
				files = filtered
			}

			// Filter by glob pattern.
			if pattern != "" {
				reStr := globToRegex(pattern)
				re, err := regexp.Compile(reStr)
				if err == nil {
					var filtered []FileInfo
					for _, f := range files {
						if re.MatchString(f.Path) {
							filtered = append(filtered, f)
						}
					}
					files = filtered
				}
			}

			if len(files) == 0 {
				printInfo("No files found matching the criteria.")
				return nil
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(files)
			}

			switch format {
			case "flat":
				fmt.Println(bold(fmt.Sprintf("\nFiles (%d):\n", len(files))))
				sorted := make([]FileInfo, len(files))
				copy(sorted, files)
				sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
				for _, f := range sorted {
					if !noMetadata {
						fmt.Printf("  %s %s\n", f.Path, dim(fmt.Sprintf("(%s, %d symbols)", f.Language, f.NodeCount)))
					} else {
						fmt.Printf("  %s\n", f.Path)
					}
				}
			case "grouped":
				byLang := map[string][]FileInfo{}
				for _, f := range files {
					byLang[string(f.Language)] = append(byLang[string(f.Language)], f)
				}
				fmt.Println(bold(fmt.Sprintf("\nFiles by Language (%d total):\n", len(files))))
				langs := make([]string, 0, len(byLang))
				for l := range byLang {
					langs = append(langs, l)
				}
				sort.Slice(langs, func(i, j int) bool { return len(byLang[langs[i]]) > len(byLang[langs[j]]) })
				for _, lang := range langs {
					langFiles := byLang[lang]
					fmt.Printf("%s (%d):\n", cyan(lang), len(langFiles))
					sort.Slice(langFiles, func(i, j int) bool { return langFiles[i].Path < langFiles[j].Path })
					for _, f := range langFiles {
						if !noMetadata {
							fmt.Printf("  %s %s\n", f.Path, dim(fmt.Sprintf("(%d symbols)", f.NodeCount)))
						} else {
							fmt.Printf("  %s\n", f.Path)
						}
					}
					fmt.Println()
				}
			default: // tree
				fmt.Println(bold(fmt.Sprintf("\nProject Structure (%d files):\n", len(files))))
				printFileTree(files, !noMetadata, maxDepth)
				fmt.Println()
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "Output as JSON")
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().StringVar(&filterDir, "filter", "", "Filter to files under this directory")
	cmd.Flags().StringVar(&pattern, "pattern", "", "Filter files matching this glob pattern")
	cmd.Flags().StringVar(&format, "format", "tree", "Output format (tree, flat, grouped)")
	cmd.Flags().IntVar(&maxDepth, "max-depth", 0, "Maximum directory depth for tree format (0 = unlimited)")
	cmd.Flags().BoolVar(&noMetadata, "no-metadata", false, "Hide file metadata")
	cmd.Flags().StringVar(&scope, "scope", "", "Comma-separated scope keys to query (default: all scopes)")
	return cmd
}

// printFileTree renders files as a directory tree.
func printFileTree(files []FileInfo, showMeta bool, maxDepth int) {
	type treeNode struct {
		name     string
		children map[string]*treeNode
		file     *FileInfo
	}
	root := &treeNode{children: map[string]*treeNode{}}
	for i := range files {
		f := &files[i]
		parts := strings.Split(f.Path, "/")
		cur := root
		for j, part := range parts {
			if part == "" {
				continue
			}
			if _, ok := cur.children[part]; !ok {
				cur.children[part] = &treeNode{name: part, children: map[string]*treeNode{}}
			}
			cur = cur.children[part]
			if j == len(parts)-1 {
				cur.file = f
			}
		}
	}

	var render func(node *treeNode, prefix string, isLast bool, depth int)
	render = func(node *treeNode, prefix string, isLast bool, depth int) {
		if maxDepth > 0 && depth > maxDepth {
			return
		}
		if node.name != "" {
			connector := "├── "
			childPrefix := "│   "
			if isLast {
				connector = "└── "
				childPrefix = "    "
			}
			line := prefix + connector + node.name
			if node.file != nil && showMeta {
				line += dim(fmt.Sprintf(" (%s, %d symbols)", node.file.Language, node.file.NodeCount))
			}
			fmt.Println(line)
			prefix += childPrefix
		}

		// Sort children: dirs first, then by name.
		type child struct {
			name string
			node *treeNode
		}
		var children []child
		for n, c := range node.children {
			children = append(children, child{n, c})
		}
		sort.Slice(children, func(i, j int) bool {
			iDir := len(children[i].node.children) > 0 && children[i].node.file == nil
			jDir := len(children[j].node.children) > 0 && children[j].node.file == nil
			if iDir != jDir {
				return iDir
			}
			return children[i].name < children[j].name
		})
		for i, c := range children {
			render(c.node, prefix, i == len(children)-1, depth+1)
		}
	}
	render(root, "", true, 0)
}
