package store

import (
	"database/sql"
	"fmt"

	"github.com/specscore/codegrapher/model"
)

// ReplaceSemantic atomically swaps the complete derived semantic graph. The
// caller holds the index writer lock and builds the graph before this call, so
// readers see either the prior or the new projection, never a partial rebuild.
func (s *Store) ReplaceSemantic(nodes []model.Node, edges []model.Edge) error {
	return s.Transaction(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM edges`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM nodes`); err != nil {
			return err
		}
		for _, n := range nodes {
			if n.ID == "" || n.Kind == "" || n.Name == "" || n.FilePath == "" || n.Language == "" {
				return fmt.Errorf("semantic node has missing required field: %q", n.ID)
			}
			if err := insertNode(tx, s.now, n); err != nil {
				return err
			}
		}
		for _, e := range edges {
			if err := insertEdge(tx, e); err != nil {
				return err
			}
		}
		return nil
	})
}
