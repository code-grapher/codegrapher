package indexer

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"sync"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/scope"
	"github.com/specscore/codegrapher/store"
)

const (
	dbPrefix = "codegraph-"
	dbSuffix = ".db"
)

// ScopedDatabasePath returns the database file path for a scope within a
// project: .codegraph/codegraph-{lang}-{version}.db.
func ScopedDatabasePath(projectRoot string, sc scope.Scope) string {
	return filepath.Join(GetCodeGraphDir(projectRoot), dbPrefix+sc.Key()+dbSuffix)
}

// parseScopeFromDBName extracts a scope from a scoped database filename, or
// reports false if the name is not a scoped DB. The scope key is
// "{language}-{version}"; language never contains a dash, so the first dash
// separates language from version.
func parseScopeFromDBName(name string) (scope.Scope, bool) {
	if !strings.HasPrefix(name, dbPrefix) || !strings.HasSuffix(name, dbSuffix) {
		return scope.Scope{}, false
	}
	key := name[len(dbPrefix) : len(name)-len(dbSuffix)]
	dash := strings.IndexByte(key, '-')
	if dash <= 0 || dash == len(key)-1 {
		return scope.Scope{}, false
	}
	return scope.Scope{
		Language: model.Language(key[:dash]),
		Version:  key[dash+1:],
	}, true
}

// Registry manages the per-scope SQLite stores of a single project. Stores are
// opened lazily and cached; existing scope databases on disk are discovered at
// open time.
type Registry struct {
	root string
	opts []store.Option

	// readOnly registries open every scope database with store.OpenReadOnly
	// and never create one. allowStale tolerates old-schema databases (and
	// skips legacy WAL ones) so diagnostics can still report; the skipped and
	// stale databases are recorded in upgrade.
	readOnly   bool
	allowStale bool
	upgrade    []error

	mu     sync.Mutex
	stores map[scope.Scope]*store.Store
}

// OpenRegistry creates a registry for projectRoot and discovers (but does not
// open) the scope databases already present in its .codegraph directory.
func OpenRegistry(projectRoot string, opts ...store.Option) (*Registry, error) {
	return openRegistry(projectRoot, false, false, opts)
}

// OpenRegistryReadOnly is OpenRegistry for read-only consumers: every scope
// database is opened read-only (no migration, no journal conversion) and no
// database is ever created. A stale-schema or legacy-WAL database fails with
// a *store.NeedsUpgradeError, unless allowStale is set, in which case
// stale-schema databases are opened anyway, WAL ones are skipped, and both
// are reported by UpgradeNeeded.
func OpenRegistryReadOnly(projectRoot string, allowStale bool, opts ...store.Option) (*Registry, error) {
	return openRegistry(projectRoot, true, allowStale, opts)
}

func openRegistry(projectRoot string, readOnly, allowStale bool, opts []store.Option) (*Registry, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	if !readOnly {
		// Write registries (init, sync, watch, daemon, coverage, export) bulk-write
		// the derived index: skip fsync and keep the rollback journal in memory.
		// See store.WithFastWrites for the crash-safety trade-off.
		opts = append(append([]store.Option{}, opts...), store.WithFastWrites())
	}
	r := &Registry{root: root, opts: opts, readOnly: readOnly, allowStale: allowStale, stores: map[scope.Scope]*store.Store{}}

	matches, err := filepath.Glob(filepath.Join(GetCodeGraphDir(root), dbPrefix+"*"+dbSuffix))
	if err != nil {
		return nil, err
	}
	for _, m := range matches {
		sc, ok := parseScopeFromDBName(filepath.Base(m))
		if !ok {
			continue
		}
		s, err := r.openExisting(m)
		if err != nil {
			var nu *store.NeedsUpgradeError
			if r.allowStale && errors.As(err, &nu) {
				r.upgrade = append(r.upgrade, err)
				continue
			}
			_ = r.Close()
			return nil, err
		}
		if reason := s.UpgradeReason(); reason != "" {
			r.upgrade = append(r.upgrade, &store.NeedsUpgradeError{Path: m, Reason: reason})
		}
		r.stores[sc] = s
	}
	return r, nil
}

func (r *Registry) openExisting(path string) (*store.Store, error) {
	if !r.readOnly {
		return store.Open(path, r.opts...)
	}
	opts := r.opts
	if r.allowStale {
		opts = append(append([]store.Option{}, opts...), store.WithAllowStale())
	}
	return store.OpenReadOnly(path, opts...)
}

// ReadOnly reports whether the registry was opened with OpenRegistryReadOnly.
func (r *Registry) ReadOnly() bool { return r.readOnly }

// UpgradeNeeded returns a *store.NeedsUpgradeError (the first one, when
// several databases need it) for a registry opened with allowStale that found
// a stale-schema or legacy-WAL database, else nil.
func (r *Registry) UpgradeNeeded() error {
	if len(r.upgrade) == 0 {
		return nil
	}
	return r.upgrade[0]
}

// Store returns the store for a scope, creating (and initializing) its database
// on first request.
func (r *Registry) Store(sc scope.Scope) (*store.Store, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s, ok := r.stores[sc]; ok {
		return s, nil
	}
	if r.readOnly {
		return nil, fmt.Errorf("registry: scope %s has no database and the index is open read-only", sc.Key())
	}
	// OpenRegistry pre-loads every existing scope DB, so a cache miss means the
	// scope is new: initialize its database.
	s, err := store.Initialize(ScopedDatabasePath(r.root, sc), r.opts...)
	if err != nil {
		return nil, fmt.Errorf("registry: create scope %s: %w", sc.Key(), err)
	}
	r.stores[sc] = s
	return s, nil
}

// Scopes returns the scopes the registry currently knows about.
func (r *Registry) Scopes() []scope.Scope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]scope.Scope, 0, len(r.stores))
	for sc := range r.stores {
		out = append(out, sc)
	}
	return out
}

// Stores returns every open store keyed by scope.
func (r *Registry) Stores() map[scope.Scope]*store.Store {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[scope.Scope]*store.Store, len(r.stores))
	maps.Copy(out, r.stores)
	return out
}

// Close closes every open store, returning the first error encountered.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for sc, s := range r.stores {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(r.stores, sc)
	}
	return firstErr
}
