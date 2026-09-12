package indexer

import (
	"fmt"
	"strconv"
)

// indexGenerationKey changes only after a successful writer has completed all
// graph mutations. Readers use it as an optimistic snapshot fence: they may
// read without holding the exclusive writer lock, but must reject output if a
// writer completed while they were resolving paths or mapping stack frames.
const indexGenerationKey = "index_generation"

// IndexGeneration returns the single generation shared by every open scope.
// Mixed values mean a writer is completing a multi-scope update, which is not
// a safe snapshot for graph consumers.
func (idx *Indexer) IndexGeneration() (string, error) {
	stores := idx.Stores()
	if len(stores) == 0 {
		return "", fmt.Errorf("index has no scope stores")
	}
	generation := ""
	missing := false
	for _, s := range stores {
		value, err := s.GetMetadata(indexGenerationKey)
		if err != nil {
			return "", err
		}
		if value == "" {
			missing = true
			continue
		}
		if missing {
			return "", fmt.Errorf("index generation is changing")
		}
		if generation != "" && value != generation {
			return "", fmt.Errorf("index generation is changing")
		}
		generation = value
	}
	if generation == "" {
		return "0", nil
	}
	return generation, nil
}

func (idx *Indexer) bumpIndexGeneration() error {
	stores := idx.Stores()
	var highest uint64
	for _, s := range stores {
		value, err := s.GetMetadata(indexGenerationKey)
		if err != nil {
			return err
		}
		if value == "" {
			continue
		}
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return fmt.Errorf("parse index generation %q: %w", value, err)
		}
		if n > highest {
			highest = n
		}
	}
	next := strconv.FormatUint(highest+1, 10)
	for _, s := range stores {
		if err := s.SetMetadata(indexGenerationKey, next); err != nil {
			return err
		}
	}
	return nil
}
