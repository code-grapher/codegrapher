// diffnodes: throwaway diagnostic — compare nodes extracted by port vs original DB.
// Usage: go run ./cmd/diffnodes <root-dir> <orig-db> [file-filter]
// Never committed as production code.
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/internal/extract"
	_ "modernc.org/sqlite"
)

type nodeKey struct {
	kind      string
	name      string
	startLine int
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: diffnodes <root-dir> <orig-db> [file-filter]")
		os.Exit(1)
	}
	rootDir := os.Args[1]
	origDB := os.Args[2]
	fileFilter := ""
	if len(os.Args) >= 4 {
		fileFilter = os.Args[3]
	}

	db, err := sql.Open("sqlite", origDB+"?mode=ro")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Get all non-yaml files from orig DB
	rows, err := db.Query(`
		SELECT DISTINCT file_path FROM nodes
		WHERE file_path NOT LIKE '%.yaml' AND file_path NOT LIKE '%.yml'
		ORDER BY file_path
	`)
	if err != nil {
		log.Fatal(err)
	}
	var files []string
	for rows.Next() {
		var fp string
		rows.Scan(&fp)
		if fileFilter == "" || strings.Contains(fp, fileFilter) {
			files = append(files, fp)
		}
	}
	rows.Close()

	type fileDiff struct {
		path    string
		missing []string
		extra   []string
	}

	var diffs []fileDiff
	totalMissing := 0
	totalExtra := 0

	// If file filter provided AND starts with "inspect:", dump port extraction for that file
	if fileFilter != "" && strings.HasPrefix(fileFilter, "inspect:") {
		target := strings.TrimPrefix(fileFilter, "inspect:")
		fullPath := filepath.Join(rootDir, target)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			log.Fatalf("cannot read %s: %v", fullPath, err)
		}
		lang := extract.DetectLanguage(target)
		res, _ := extract.ExtractFile(target, content, lang)
		fmt.Printf("PORT extracts %d nodes from %s:\n", len(res.Nodes), target)
		for _, n := range res.Nodes {
			fmt.Printf("  kind=%-12s name=%-40q line=%d\n", n.Kind, n.Name, n.StartLine)
		}
		return
	}

	for _, fp := range files {
		if strings.Contains(fp, "coverage_test.go") {
			continue
		}

		// Get orig nodes for this file
		origNodes := map[string]nodeKey{}
		rows2, err := db.Query(`SELECT id, kind, name, start_line FROM nodes WHERE file_path=?`, fp)
		if err != nil {
			continue
		}
		for rows2.Next() {
			var id, kind, name string
			var startLine int
			rows2.Scan(&id, &kind, &name, &startLine)
			origNodes[id] = nodeKey{kind, name, startLine}
		}
		rows2.Close()

		// Extract with port
		fullPath := filepath.Join(rootDir, fp)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			fmt.Printf("MISSING_FILE: %s\n", fp)
			continue
		}
		lang := extract.DetectLanguage(fp)
		res, _ := extract.ExtractFile(fp, content, lang)

		portNodes := map[string]nodeKey{}
		for _, n := range res.Nodes {
			portNodes[n.ID] = nodeKey{string(n.Kind), n.Name, n.StartLine}
		}

		// Diff
		var missing, extra []string
		for id, k := range origNodes {
			if _, ok := portNodes[id]; !ok {
				missing = append(missing, fmt.Sprintf("  MISSING id=%s kind=%s name=%q line=%d", id, k.kind, k.name, k.startLine))
			}
		}
		for id, k := range portNodes {
			if _, ok := origNodes[id]; !ok {
				extra = append(extra, fmt.Sprintf("  EXTRA   id=%s kind=%s name=%q line=%d", id, k.kind, k.name, k.startLine))
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)

		if len(missing) > 0 || len(extra) > 0 {
			diffs = append(diffs, fileDiff{fp, missing, extra})
			totalMissing += len(missing)
			totalExtra += len(extra)
		}
	}

	// Sort diffs by most impactful
	sort.Slice(diffs, func(i, j int) bool {
		di := len(diffs[i].missing) + len(diffs[i].extra)
		dj := len(diffs[j].missing) + len(diffs[j].extra)
		return di > dj
	})

	fmt.Printf("=== NODE DIFF SUMMARY ===\n")
	fmt.Printf("Files with diffs: %d\n", len(diffs))
	fmt.Printf("Total missing (in orig, not port): %d\n", totalMissing)
	fmt.Printf("Total extra (in port, not orig): %d\n", totalExtra)
	fmt.Println()

	for _, d := range diffs {
		fmt.Printf("FILE: %s (missing=%d extra=%d)\n", d.path, len(d.missing), len(d.extra))
		for _, m := range d.missing {
			fmt.Println(m)
		}
		for _, x := range d.extra {
			fmt.Println(x)
		}
	}
}
