package indexer

import "fmt"

// WithConsistentRead holds the same cross-process lock used by index writers
// while fn resolves graph data and verifies source. It is intentionally an
// exclusive read lock for now: short CLI reads prefer a clear busy/retry over
// observing a partially applied multi-file index update.
//
// Call RefreshForRead before this method. Refresh may write and therefore
// cannot be called while this non-reentrant lock is held.
func (idx *Indexer) WithConsistentRead(fn func() error) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if err := idx.lock.Acquire(); err != nil {
		return fmt.Errorf("index is busy; retry when indexing completes: %w", err)
	}
	defer idx.lock.Release()
	return fn()
}
