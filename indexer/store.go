package indexer

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// HashContent returns the SHA-256 hex digest of file content, matching
// hashContent in src/extraction/index.ts.
func HashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// storeExtractionResult persists one file's extraction output: replaces any
// prior data for the file, inserts validated nodes, edges between inserted
// nodes, unresolved references with denormalized file context, and the file
// record. Ported from ExtractionOrchestrator.storeExtractionResult.
func storeExtractionResult(
	s *store.Store,
	filePath string,
	content []byte,
	lang model.Language,
	size int64,
	mtimeMs int64,
	result model.ExtractionResult,
	now func() int64,
) error {
	contentHash := HashContent(content)

	existing, err := s.GetFileByPath(filePath)
	if err != nil {
		return err
	}
	if existing != nil && existing.ContentHash == contentHash {
		return nil // no changes
	}
	if existing != nil {
		if err := s.DeleteFile(filePath); err != nil {
			return err
		}
	}

	// Filter out nodes with missing required fields before insertion, so
	// edges never reference nodes that insertNode would silently skip.
	validNodes := result.Nodes[:0:0]
	for _, n := range result.Nodes {
		if n.ID != "" && n.Kind != "" && n.Name != "" && n.FilePath != "" && n.Language != "" {
			validNodes = append(validNodes, n)
		}
	}
	if len(validNodes) > 0 {
		if err := s.InsertNodes(validNodes); err != nil {
			return err
		}
	}

	insertedIDs := make(map[string]bool, len(validNodes))
	for _, n := range validNodes {
		insertedIDs[n.ID] = true
	}

	if len(result.Edges) > 0 {
		validEdges := result.Edges[:0:0]
		for _, e := range result.Edges {
			if insertedIDs[e.Source] && insertedIDs[e.Target] {
				validEdges = append(validEdges, e)
			}
		}
		if len(validEdges) > 0 {
			if err := s.InsertEdges(validEdges); err != nil {
				return err
			}
		}
	}

	if len(result.UnresolvedReferences) > 0 {
		refs := result.UnresolvedReferences[:0:0]
		for _, r := range result.UnresolvedReferences {
			if !insertedIDs[r.FromNodeID] {
				continue
			}
			if r.FilePath == "" {
				r.FilePath = filePath
			}
			if r.Language == "" {
				r.Language = lang
			}
			refs = append(refs, r)
		}
		if len(refs) > 0 {
			if err := s.InsertUnresolvedRefs(refs); err != nil {
				return err
			}
		}
	}

	rec := model.FileRecord{
		Path:        filePath,
		ContentHash: contentHash,
		Language:    lang,
		Size:        size,
		ModifiedAt:  mtimeMs,
		IndexedAt:   now(),
		NodeCount:   len(result.Nodes),
	}
	if len(result.Errors) > 0 {
		rec.Errors = result.Errors
	}
	return s.UpsertFile(rec)
}
