package database

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type MultiDBManager struct {
	pools      map[string]*pgxpool.Pool
	primaryDB string
	mu        sync.RWMutex
	configs   map[string]*MultiDBConfig
}

type MultiDBConfig struct {
	Name            string
	URL             string
	MaxConns         int32
	MinConns         int32
	MaxConnLifetime  time.Duration
	MaxConnIdleTime time.Duration
	Enabled        bool
	TableCount      int
	TotalRecords   int64
}

type DBMeta struct {
	Name         string   `json:"name"`
	TableCount  int      `json:"table_count"`
	TotalRows  int64     `json:"total_rows"`
	LastSync   time.Time `json:"last_sync"`
	Status    string   `json:"status"`
	IsReplica bool     `json:"is_replica"`
	RefDBs    []string `json:"ref_dbs,omitempty"`
}

func NewMultiDBManager() *MultiDBManager {
	return &MultiDBManager{
		pools:    make(map[string]*pgxpool.Pool),
		configs: make(map[string]*MultiDBConfig),
	}
}

func (m *MultiDBManager) AddDatabase(ctx context.Context, name, url string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.pools[name]; exists {
		return fmt.Errorf("database %s already exists", name)
	}

	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return fmt.Errorf("failed to parse database URL: %w", err)
	}

	config.MaxConns = 25
	config.MinConns = 5
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	config.ConnConfig.RuntimeParams["statement_timeout"] = "10000"

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("failed to ping database: %w", err)
	}

	m.pools[name] = pool
	m.configs[name] = &MultiDBConfig{
		Name:            name,
		URL:             url,
		MaxConns:         25,
		MinConns:         5,
		MaxConnLifetime:  time.Hour,
		MaxConnIdleTime:  30 * time.Minute,
		Enabled:        true,
	}

	log.Printf("✅ Added database: %s (max_conns=%d)", name, config.MaxConns)
	return nil
}

func (m *MultiDBManager) GetPool(name string) *pgxpool.Pool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pools[name]
}

func (m *MultiDBManager) GetPrimaryPool() *pgxpool.Pool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.primaryDB == "" {
		return nil
	}
	return m.pools[m.primaryDB]
}

func (m *MultiDBManager) SetPrimaryDB(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.pools[name]; !exists {
		return fmt.Errorf("database %s not found", name)
	}
	m.primaryDB = name
	return nil
}

func (m *MultiDBManager) GetDatabases() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.pools))
	for name := range m.pools {
		names = append(names, name)
	}
	return names
}

func (m *MultiDBManager) GetDatabaseMeta(ctx context.Context) ([]DBMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make([]DBMeta, 0, len(m.pools))
	for name, pool := range m.pools {
		var tableCount int
		var totalRows int64

		err := pool.QueryRow(ctx, `
			SELECT count(*), COALESCE(sum(pg_total_relation_size(relid)), 0::bigint)
			FROM pg_stat_user_tables
		`).Scan(&tableCount, &totalRows)

		refDBs := m.findReferencedDBs(ctx, pool, name)

		results = append(results, DBMeta{
			Name:         name,
			TableCount:   tableCount,
			TotalRows:   totalRows,
			LastSync:    time.Now(),
			Status:     "active",
			IsReplica:  false,
			RefDBs:     refDBs,
		})
	}

	return results, nil
}

func (m *MultiDBManager) findReferencedDBs(ctx context.Context, pool *pgxpool.Pool, dbName string) []string {
	rows, err := pool.Query(ctx, `
		SELECT table_name 
		FROM information_schema.tables 
	WHERE table_schema = 'public'
	AND table_name LIKE '%_ref' OR table_name LIKE '%_link' OR table_name LIKE '%_rel'
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var refs []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			refs = append(refs, t)
		}
	}
	return refs
}

func (m *MultiDBManager) FindEntityAcrossDBs(ctx context.Context, entityValue string, columnName string) ([]DBMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []DBMeta

	for name, pool := range m.pools {
		var count int

		query := fmt.Sprintf(`
			SELECT count(*) FROM (
				SELECT 1 FROM %s WHERE %s::text ILIKE $1
				UNION ALL
				SELECT 1 FROM %s_ref WHERE %s::text ILIKE $1
				UNION ALL
				SELECT 1 FROM %s_link WHERE %s::text ILIKE $1
			) matches
		`, name, columnName, name, columnName, name, columnName)

		err := pool.QueryRow(ctx, query, "%"+entityValue+"%").Scan(&count)
		if err != nil || count == 0 {
			continue
		}

		results = append(results, DBMeta{
			Name:    name,
			Status:  fmt.Sprintf("found_%d", count),
			RefDBs: []string{fmt.Sprintf("match_in_%s", columnName)},
		})
	}

	return results, nil
}

func (m *MultiDBManager) DiscoverTables(ctx context.Context, dbName string) ([]string, error) {
	m.mu.RLock()
	pool := m.pools[dbName]
	m.mu.RUnlock()

	if pool == nil {
		return nil, fmt.Errorf("database %s not found", dbName)
	}

	rows, err := pool.Query(ctx, `
		SELECT table_name 
		FROM information_schema.tables 
		WHERE table_schema = 'public' 
		AND table_type = 'BASE TABLE'
		ORDER BY table_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tables = append(tables, name)
		}
	}
	return tables, rows.Err()
}

func (m *MultiDBManager) GenerateCrossDBReport(ctx context.Context, dbName, tableName string, searchTerm string) ([]map[string]interface{}, error) {
	m.mu.RLock()
	pool := m.pools[dbName]
	m.mu.RUnlock()

	if pool == nil {
		return nil, fmt.Errorf("database %s not found", dbName)
	}

	searchPattern := "%" + searchTerm + "%"

	query := `
		SELECT 
			t.table_name as source_table,
			c.column_name,
			c.data_type,
			'EXACT' as match_type
		FROM information_schema.tables t
		JOIN information_schema.columns c 
			ON t.table_name = c.table_name AND t.table_schema = c.table_schema
		WHERE t.table_schema = 'public' 
		AND t.table_type = 'BASE TABLE'
		AND (
			c.data_type IN ('character varying', 'text', 'varchar', 'char')
		)
		AND EXISTS (
			SELECT 1 FROM information_schema.columns c2
			WHERE c2.table_name = t.table_name
			AND c2.table_schema = 'public'
			AND c2.column_name = 'name' OR c2.column_name = 'title' OR c2.column_name = 'email'
		)
	`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var tableName, colName, dataType, matchType string
		if err := rows.Scan(&tableName, &colName, &dataType, &matchType); err != nil {
			continue
		}
		results = append(results, map[string]interface{}{
			"source_table": tableName,
			"column":      colName,
			"data_type":   dataType,
			"match_type":  matchType,
		})
	}
	return results, nil
}

func (m *MultiDBManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, pool := range m.pools {
		pool.Close()
		log.Printf("Closed database: %s", name)
	}
	m.pools = make(map[string]*pgxpool.Pool)
}

func (m *MultiDBManager) GetStats(ctx context.Context) map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make(map[string]interface{})
	stats["total_databases"] = len(m.pools)
	stats["primary_database"] = m.primaryDB

	dbs := make([]map[string]interface{}, 0)
	for name, pool := range m.pools {
		var tableCount int
		var totalRows int64

		pool.QueryRow(ctx, `
			SELECT count(*), COALESCE(sum(n_live_tup), 0)::bigint
			FROM pg_stat_user_tables
		`).Scan(&tableCount, &totalRows)

		dbs = append(dbs, map[string]interface{}{
			"name":         name,
			"table_count":  tableCount,
			"total_rows":    totalRows,
			"is_primary":   name == m.primaryDB,
		})
	}
	stats["databases"] = dbs

	return stats
}

func (m *MultiDBManager) SearchCrossDB(ctx context.Context, query string, limit int) ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var allResults []map[string]interface{}
	queryLower := strings.ToLower(query)

	for dbName, pool := range m.pools {
		rows, err := pool.Query(ctx, `
			SELECT table_name, column_name 
			FROM information_schema.columns 
			WHERE table_schema = 'public' 
			AND data_type IN ('character varying', 'text', 'varchar')
			AND column_name ILIKE $1
		`, "%"+queryLower+"%")
		if err != nil {
			continue
		}

		for rows.Next() {
			var tableName, columnName string
			if err := rows.Scan(&tableName, &columnName); err != nil {
				continue
			}
			allResults = append(allResults, map[string]interface{}{
				"database":          dbName,
				"table":             tableName,
				"column":            columnName,
				"search_match_type":  "column_name",
			})
		}
		rows.Close()
	}

	if limit > 0 && len(allResults) > limit {
		allResults = allResults[:limit]
	}

	return allResults, nil
}