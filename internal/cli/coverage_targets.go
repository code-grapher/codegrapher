package cli

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/specscore/codegrapher/coverage"
	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
	"github.com/spf13/cobra"
)

type coverageTarget struct {
	QualifiedName     string  `json:"qualifiedName"`
	FilePath          string  `json:"filePath"`
	Line              int     `json:"line"`
	StatementsCovered int     `json:"statementsCovered"`
	StatementsTotal   int     `json:"statementsTotal"`
	Percent           float64 `json:"percent"`
	Freshness         string  `json:"freshness"`
	Ref               string  `json:"ref,omitempty"`
}

type coverageTargetsResult struct {
	Targets    []coverageTarget `json:"targets"`
	StaleFiles []string         `json:"staleFiles,omitempty"`
}

func staleCoverageFiles(stores []*store.Store) ([]string, error) {
	seen := map[string]bool{}
	for _, s := range stores {
		rows, err := s.GetAllCoverage()
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			f, err := s.GetFileByPath(r.FilePath)
			if err != nil {
				return nil, err
			}
			if f == nil || f.ContentHash != r.ContentHash {
				seen[r.FilePath] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out, nil
}

func coverageTargets(stores []*store.Store) ([]coverageTarget, error) {
	var out []coverageTarget
	for _, s := range stores {
		files, err := s.GetAllFiles()
		if err != nil {
			return nil, err
		}
		rows, err := s.GetAllNodeCoverage()
		if err != nil {
			return nil, err
		}
		byID := map[string]store.NodeCoverageRow{}
		for _, r := range rows {
			byID[r.NodeID] = r
		}
		for _, f := range files {
			nodes, err := s.GetNodesByFile(f.Path)
			if err != nil {
				return nil, err
			}
			for _, n := range nodes {
				if n.Kind != model.KindFunction && n.Kind != model.KindMethod {
					continue
				}
				r, ok := byID[n.ID]
				if !ok {
					continue
				}
				total := r.StatementsCovered + r.StatementsUncovered
				if total == 0 || r.StatementsUncovered == 0 {
					continue
				}
				fresh := "current"
				if r.ContentHash != f.ContentHash {
					fresh = "stale"
				}
				out = append(out, coverageTarget{QualifiedName: n.QualifiedName, FilePath: f.Path, Line: n.StartLine, StatementsCovered: r.StatementsCovered, StatementsTotal: total, Percent: coverage.Pct(r.StatementsCovered, r.StatementsUncovered), Freshness: fresh, Ref: r.Ref})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Freshness != out[j].Freshness {
			return out[i].Freshness == "current"
		}
		ui, uj := out[i].StatementsTotal-out[i].StatementsCovered, out[j].StatementsTotal-out[j].StatementsCovered
		if ui != uj {
			return ui > uj
		}
		if out[i].FilePath != out[j].FilePath {
			return out[i].FilePath < out[j].FilePath
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].QualifiedName < out[j].QualifiedName
	})
	return out, nil
}

func newCoverageTargetsCmd() *cobra.Command {
	var root, format string
	var jsonOut bool
	cmd := &cobra.Command{Use: "targets", Short: "Rank zero and partial functions by uncovered Go statements", Long: "List functions with missed statements, largest gap first. Stale rows are labeled and follow current rows; regenerate profiles after source changes.", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if format != "text" && format != "json" {
			return fmt.Errorf("coverage targets: --format must be text or json")
		}
		projectPath := root
		if projectPath == "" {
			projectPath = resolveArg(nil)
		} else {
			projectPath = resolveArg([]string{projectPath})
		}
		if !indexer.IsInitialized(projectPath) {
			return fmt.Errorf("no codegraph index found at %s", projectPath)
		}
		idx, err := indexer.Open(projectPath, indexer.Options{})
		if err != nil {
			return err
		}
		defer func() { _ = idx.Close() }()
		if _, err := idx.RefreshForRead(indexer.Options{}); err != nil {
			return err
		}
		targets, err := coverageTargets(idx.Stores())
		if err != nil {
			return err
		}
		if targets == nil {
			targets = []coverageTarget{}
		}
		staleFiles, err := staleCoverageFiles(idx.Stores())
		if err != nil {
			return err
		}
		if wantsJSON(format, jsonOut) {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(coverageTargetsResult{Targets: targets, StaleFiles: staleFiles})
		}
		for _, t := range targets {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%5d missed  %5d/%-5d  %5.1f%%  %-7s  %s:%d  %s\n", t.StatementsTotal-t.StatementsCovered, t.StatementsCovered, t.StatementsTotal, t.Percent, t.Freshness, t.FilePath, t.Line, t.QualifiedName); err != nil {
				return err
			}
		}
		if len(targets) == 0 && len(staleFiles) == 0 {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "No uncovered measured functions. Ingest a Go profile to populate targets."); err != nil {
				return err
			}
		}
		for _, path := range staleFiles {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "stale coverage: %s (regenerate profiles after source changed)\n", path); err != nil {
				return err
			}
		}
		return nil
	}}
	cmd.Flags().StringVar(&root, "root", "", "Repository root (default: current project)")
	addJSONOutputFlags(cmd, &format, &jsonOut)
	return cmd
}
