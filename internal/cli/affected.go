package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
	"github.com/spf13/cobra"
)

func newAffectedCmd() *cobra.Command {
	var jsonOut bool
	var quiet bool
	var pathFlag string
	var stdin bool
	var depth int
	var filterGlob string
	var scope string

	cmd := &cobra.Command{
		Use:   "affected [files...]",
		Short: "Find test files affected by changed source files",
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

			changedFiles := make([]string, len(args))
			copy(changedFiles, args)

			if stdin {
				sc := bufio.NewScanner(os.Stdin)
				for sc.Scan() {
					line := sc.Text()
					if line != "" {
						changedFiles = append(changedFiles, line)
					}
				}
			}

			if len(changedFiles) == 0 {
				if !quiet {
					printInfo("No files provided. Use file arguments or --stdin.")
				}
				return nil
			}

			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				printError(fmt.Sprintf("Failed to open index: %s", err))
				os.Exit(1)
			}
			defer idx.Close()

			affectedTests, totalDependents := findAffectedTestsAcross(
				idx.StoresFiltered(splitCSV(scope)), changedFiles, depth, filterGlob)
			sortedTests := make([]string, 0, len(affectedTests))
			for t := range affectedTests {
				sortedTests = append(sortedTests, t)
			}
			sort.Strings(sortedTests)

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"changedFiles":             changedFiles,
					"affectedTests":            sortedTests,
					"totalDependentsTraversed": totalDependents,
				})
			}
			if quiet {
				for _, t := range sortedTests {
					fmt.Println(t)
				}
				return nil
			}
			if len(sortedTests) == 0 {
				printInfo("No test files affected by the changed files.")
			} else {
				fmt.Println(bold(fmt.Sprintf("\nAffected test files (%d):\n", len(sortedTests))))
				for _, t := range sortedTests {
					fmt.Println("  " + cyan(t))
				}
				fmt.Println()
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "Output as JSON")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only output file paths")
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().BoolVar(&stdin, "stdin", false, "Read file list from stdin (one per line)")
	cmd.Flags().IntVarP(&depth, "depth", "d", 5, "Max dependency traversal depth")
	cmd.Flags().StringVarP(&filterGlob, "filter", "f", "", "Custom glob filter for test files")
	cmd.Flags().StringVar(&scope, "scope", "", "Comma-separated scope keys to query (default: all scopes)")
	return cmd
}

// findAffectedTestsAcross fans findAffectedTests out across every scope store
// and merges the affected-test sets and dependent counts. A source file's
// import graph lives entirely within one scope store, so the union across
// stores reconstructs the whole-repo result.
func findAffectedTestsAcross(stores []*store.Store, changedFiles []string, maxDepth int, filterGlob string) (map[string]bool, int) {
	affectedTests := map[string]bool{}
	total := 0
	for _, s := range stores {
		tests, dependents := findAffectedTests(s, changedFiles, maxDepth, filterGlob)
		for t := range tests {
			affectedTests[t] = true
		}
		total += dependents
	}
	return affectedTests, total
}

// findAffectedTests performs BFS through file import/dependency edges to find
// test files transitively affected by the given changed files.
func findAffectedTests(s *store.Store, changedFiles []string, maxDepth int, filterGlob string) (map[string]bool, int) {
	var customFilter *regexp.Regexp
	if filterGlob != "" {
		reStr := globToRegex(filterGlob)
		customFilter, _ = regexp.Compile(reStr)
	}

	isTest := func(path string) bool {
		if customFilter != nil {
			return customFilter.MatchString(path)
		}
		// Default patterns.
		for _, pattern := range []string{".spec.", ".test.", "_test.go", "/__tests__/", "/tests/", "/test/", "/e2e/", "/spec/"} {
			if containsStr(path, pattern) {
				return true
			}
		}
		return false
	}

	affectedTests := map[string]bool{}
	allDependents := map[string]bool{}

	for _, file := range changedFiles {
		if isTest(file) {
			affectedTests[file] = true
			continue
		}
		// BFS through files that import this file.
		type item struct {
			file  string
			depth int
		}
		queue := []item{{file, 0}}
		visited := map[string]bool{file: true}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if cur.depth >= maxDepth {
				continue
			}
			// Find file node for cur.file.
			fileNodes, err := s.GetNodesByQualifiedNameExact(cur.file)
			if err != nil || len(fileNodes) == 0 {
				continue
			}
			for _, fn := range fileNodes {
				if fn.Kind != model.KindFile {
					continue
				}
				// Find nodes that import this file.
				incomingEdges, err := s.GetIncomingEdges(fn.ID, []model.EdgeKind{model.EdgeImports})
				if err != nil {
					continue
				}
				for _, e := range incomingEdges {
					depNode, err := s.GetNodeByID(e.Source)
					if err != nil || depNode == nil {
						continue
					}
					depPath := depNode.FilePath
					if visited[depPath] {
						continue
					}
					visited[depPath] = true
					allDependents[depPath] = true
					if isTest(depPath) {
						affectedTests[depPath] = true
					} else {
						queue = append(queue, item{depPath, cur.depth + 1})
					}
				}
			}
		}
	}
	return affectedTests, len(allDependents)
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
