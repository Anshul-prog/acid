// #file: metadata.go
// #package: dbsearch
// #purpose: Load and cache the full schema (tables + columns + PKs) from every
//           PostgreSQL data source at server startup.
//
// Two queries are run per data source:
//   1. loadColumns  – reads information_schema.columns (all user tables/columns)
//   2. loadPKs      – reads table_constraints + key_column_usage (PK columns)
//
// Results are assembled into a DataSourceMeta held in memory for the lifetime
// of the process.  Re-loading is triggered by SearchService.Refresh().
//
// Scale note: even with millions of tables these queries only touch the
// Postgres catalogue, not user data, so they complete in < 1 s on typical
// hardware.  The resulting map fits comfortably in RAM (a million-table entry
// uses roughly 200–400 MB depending on name lengths).

package dbsearch

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Column discovery ─────────────────────────────────────────────────────────

// loadColumns queries information_schema.columns and returns every user-visible
// column across all schemas (excluding pg_catalog and information_schema).
//
// #metadata-loader: called once per data source during SearchService init.
func loadColumns(ctx context.Context, pool *pgxpool.Pool) ([]ColumnMeta, error) {
	const q = `
		SELECT
			table_schema,
			table_name,
			column_name,
			data_type
		FROM information_schema.columns
		WHERE table_schema NOT IN ('information_schema', 'pg_catalog')
		ORDER BY table_schema, table_name, ordinal_position
	`

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("loadColumns: query: %w", err)
	}
	defer rows.Close()

	var cols []ColumnMeta
	for rows.Next() {
		var schema, table, column, dataType string
		if err := rows.Scan(&schema, &table, &column, &dataType); err != nil {
			return nil, fmt.Errorf("loadColumns: scan: %w", err)
		}

		cm := ColumnMeta{
			Schema:   schema,
			Table:    table,
			Column:   column,
			DataType: dataType,
		}

		// #column-classification: IsText → safe for ILIKE
		//                         IsNumeric → safe for = / BETWEEN
		switch dataType {
		case "text", "character varying", "character", "varchar",
			"uuid", "name", "citext", "tsvector":
			cm.IsText = true
		case "integer", "bigint", "smallint", "numeric",
			"real", "double precision", "decimal", "money":
			cm.IsNumeric = true
		}

		cols = append(cols, cm)
	}
	return cols, rows.Err()
}

// ─── Primary-key discovery ────────────────────────────────────────────────────

// loadPKs returns a map of "schema.table" → first PK column name.
// Only the leading column of a composite PK is stored (used for row identification).
//
// #pk-discovery: used by SearchColumnsInDatabase so every hit includes a pk_value
//   that lets the frontend open the exact row.
func loadPKs(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	const q = `
		SELECT
			kcu.table_schema,
			kcu.table_name,
			kcu.column_name
		FROM information_schema.table_constraints  tc
		JOIN information_schema.key_column_usage   kcu
		  ON  tc.constraint_name = kcu.constraint_name
		  AND tc.table_schema    = kcu.table_schema
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND tc.table_schema NOT IN ('information_schema', 'pg_catalog')
		ORDER BY kcu.table_schema, kcu.table_name, kcu.ordinal_position
	`

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("loadPKs: query: %w", err)
	}
	defer rows.Close()

	pks := make(map[string]string)
	for rows.Next() {
		var schema, table, column string
		if err := rows.Scan(&schema, &table, &column); err != nil {
			return nil, fmt.Errorf("loadPKs: scan: %w", err)
		}
		key := schema + "." + table
		if _, exists := pks[key]; !exists {
			// Keep only the first (leading) PK column.
			pks[key] = column
		}
	}
	return pks, rows.Err()
}

// ─── Assembly ─────────────────────────────────────────────────────────────────

// buildDataSourceMeta combines raw column and PK data into a DataSourceMeta.
//
// #metadata-builder: O(n) over number of columns; called once per data source.
func buildDataSourceMeta(cols []ColumnMeta, pks map[string]string, id string) *DataSourceMeta {
	tables := make(map[string]*TableMeta, 512)

	for _, col := range cols {
		key := col.Schema + "." + col.Table
		tm, ok := tables[key]
		if !ok {
			tm = &TableMeta{Schema: col.Schema, Name: col.Table}
			tables[key] = tm
		}
		tm.Columns = append(tm.Columns, col)
	}

	for key, pkCol := range pks {
		if tm, ok := tables[key]; ok {
			tm.PKColumn = pkCol
		}
	}

	return &DataSourceMeta{ID: id, Tables: tables}
}
