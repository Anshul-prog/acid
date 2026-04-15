// #file: service.go
// #package: dbsearch
// #purpose: SearchService — the top-level object that owns metadata maps and
//           connection pools for ALL registered PostgreSQL data sources.
//
// Lifecycle:
//   1. NewSearchService(ctx, pools) → loads metadata from every pool in parallel
//      and fails fast if any source is unreachable.
//   2. Refresh(ctx) → re-loads all metadata without restarting the server.
//      Useful when new tables are added while the server is running.
//   3. Search(ctx, req) → the single public entry-point for all search modes.
//
// Thread safety: metadata maps are replaced atomically with a sync.RWMutex so
//   in-flight searches finish against the old snapshot while Refresh() writes.

package dbsearch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SearchService manages metadata and connection pools for every data source.
// #service: created once in main.go, injected into HTTP handlers.
type SearchService struct {
	mu    sync.RWMutex
	metas map[string]*DataSourceMeta // dataSourceID → schema snapshot
	pools map[string]*pgxpool.Pool   // dataSourceID → pgxpool (shared with app)

	// #background-refresh: tracks when metadata was last loaded
	loadedAt map[string]time.Time
}

// NewSearchService initialises the service by loading metadata from every pool.
// Metadata loading runs concurrently across all sources to minimise startup time.
//
// #init: called in cmd/api/main.go right after the DB pool is created.
//
// If any pool fails to yield metadata the error is returned immediately so the
// server fails fast rather than serving stale/empty search results.
func NewSearchService(ctx context.Context, pools map[string]*pgxpool.Pool) (*SearchService, error) {
	type result struct {
		id   string
		meta *DataSourceMeta
		err  error
	}

	ch := make(chan result, len(pools))

	for id, pool := range pools {
		id, pool := id, pool // capture loop vars
		go func() {
			cols, err := loadColumns(ctx, pool)
			if err != nil {
				ch <- result{id: id, err: fmt.Errorf("loadColumns(%s): %w", id, err)}
				return
			}
			pks, err := loadPKs(ctx, pool)
			if err != nil {
				ch <- result{id: id, err: fmt.Errorf("loadPKs(%s): %w", id, err)}
				return
			}
			ch <- result{id: id, meta: buildDataSourceMeta(cols, pks, id)}
		}()
	}

	metas := make(map[string]*DataSourceMeta, len(pools))
	loadedAt := make(map[string]time.Time, len(pools))

	for range pools {
		r := <-ch
		if r.err != nil {
			return nil, r.err
		}
		metas[r.id] = r.meta
		loadedAt[r.id] = time.Now()
	}

	return &SearchService{
		metas:    metas,
		pools:    pools,
		loadedAt: loadedAt,
	}, nil
}

// Refresh reloads metadata for every data source.
// #background-refresh: can be called by a background goroutine periodically
//   so the engine picks up newly created tables without a server restart.
func (s *SearchService) Refresh(ctx context.Context) error {
	type result struct {
		id   string
		meta *DataSourceMeta
		err  error
	}

	s.mu.RLock()
	pools := s.pools
	s.mu.RUnlock()

	ch := make(chan result, len(pools))
	for id, pool := range pools {
		id, pool := id, pool
		go func() {
			cols, err := loadColumns(ctx, pool)
			if err != nil {
				ch <- result{id: id, err: err}
				return
			}
			pks, err := loadPKs(ctx, pool)
			if err != nil {
				ch <- result{id: id, err: err}
				return
			}
			ch <- result{id: id, meta: buildDataSourceMeta(cols, pks, id)}
		}()
	}

	newMetas := make(map[string]*DataSourceMeta, len(pools))
	newAt := make(map[string]time.Time, len(pools))
	for range pools {
		r := <-ch
		if r.err != nil {
			return r.err
		}
		newMetas[r.id] = r.meta
		newAt[r.id] = time.Now()
	}

	s.mu.Lock()
	s.metas = newMetas
	s.loadedAt = newAt
	s.mu.Unlock()
	return nil
}

// DataSourceIDs returns a snapshot of all registered data-source IDs.
// #introspection: used by /api/admin/db-search/sources handler.
func (s *SearchService) DataSourceIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.metas))
	for id := range s.metas {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns a lightweight summary of all registered sources.
// #introspection: returned by /api/admin/db-search/sources.
func (s *SearchService) Stats() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]any, 0, len(s.metas))
	for id, m := range s.metas {
		textCols := 0
		for _, tm := range m.Tables {
			for _, cm := range tm.Columns {
				if cm.IsText {
					textCols++
				}
			}
		}
		out = append(out, map[string]any{
			"id":           id,
			"table_count":  len(m.Tables),
			"text_columns": textCols,
			"loaded_at":    s.loadedAt[id],
		})
	}
	return out
}

// getMeta returns the metadata for a data source (read-lock safe).
func (s *SearchService) getMeta(id string) (*DataSourceMeta, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.metas[id]
	return m, ok
}

// getPool returns the pool for a data source (read-lock safe).
func (s *SearchService) getPool(id string) (*pgxpool.Pool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.pools[id]
	return p, ok
}

// allSourceIDs returns a snapshot list of all source IDs.
func (s *SearchService) allSourceIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.metas))
	for id := range s.metas {
		ids = append(ids, id)
	}
	return ids
}
