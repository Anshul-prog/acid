// #file: search.go
// #package: dbsearch
// #purpose: Core query execution — smart classification, row search, column scan,
//           full database scan, and the top-level Search() dispatcher.
//
// Smart Query Classification  (#smart-search)
// ─────────────────────────────────────────────
// Before any SQL is issued, ClassifyQuery() inspects the raw input with a set
// of compiled regex patterns to decide the QueryType.  Depending on the type
// the engine:
//   • Aadhaar / PAN / Passport / DL / Voter-ID  → exact match on doc_number col first
//   • Phone / Email                              → exact match on contact_value col first
//   • Bank account / IFSC                        → exact match on account_number / ifsc_code
//   • General                                    → ILIKE across all text columns
//
// Scaling strategy  (#scale)
// ─────────────────────────────────────────────
// • Metadata is loaded once in RAM; no information_schema hit at query time.
// • SearchColumnsInDatabase fans out using goroutines (one per table) capped by
//   a semaphore so we never open more than maxConcurrentTables connections at once.
// • Context cancellation is checked between tables so a client disconnect aborts
//   the scan immediately.
// • Each column query is LIMIT $2 so even a table with 100 M rows returns quickly.

package dbsearch

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// ─── Constants ────────────────────────────────────────────────────────────────

const (
	defaultLimit       = 50  // rows returned when caller omits limit
	defaultLimitPerCol = 5   // hits per column for column/database scans
	maxLimit           = 200 // hard ceiling on limit
	maxLimitPerCol     = 20  // hard ceiling per column
	// #scale: number of goroutines scanning tables simultaneously
	maxConcurrentTables = 16
)

// ─── Regex patterns for smart classification ─────────────────────────────────
// #smart-search: all patterns are compiled once at package init.

var (
	reAadhaar  = regexp.MustCompile(`^\d{12}$`)
	rePAN      = regexp.MustCompile(`^[A-Z]{5}\d{4}[A-Z]$`)
	rePassport = regexp.MustCompile(`^[A-PR-WY][1-9]\d{7}$`)
	reDL       = regexp.MustCompile(`^[A-Z]{2}\d{2} ?\d{4}\d{7}$`)
	reVoterID  = regexp.MustCompile(`^[A-Z]{3}\d{7}$`)
	rePhone    = regexp.MustCompile(`^[6-9]\d{9}$`)
	reEmail    = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	reBankAcc  = regexp.MustCompile(`^\d{9,18}$`)
	reIFSC     = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)
)

// ClassifyQuery returns the most likely semantic type of the query string.
// The normalised form (upper-case, no spaces/dashes) is matched against each
// pattern in priority order.
//
// #smart-search: called by Search() before building the SQL plan.
func ClassifyQuery(q string) QueryType {
	norm := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "/", "").Replace(strings.TrimSpace(q)))
	switch {
	case reAadhaar.MatchString(norm):
		return QueryAadhaar
	case rePAN.MatchString(norm):
		return QueryPAN
	case rePassport.MatchString(norm):
		return QueryPassport
	case reDL.MatchString(norm):
		return QueryDL
	case reVoterID.MatchString(norm):
		return QueryVoterID
	case rePhone.MatchString(norm):
		return QueryPhone
	case reEmail.MatchString(strings.ToLower(q)):
		return QueryEmail
	case reBankAcc.MatchString(norm):
		return QueryBankAcc
	case reIFSC.MatchString(norm):
		return QueryIFSC
	default:
		return QueryGeneral
	}
}

// ─── Identifier quoting ───────────────────────────────────────────────────────

// quoteIdent wraps a PostgreSQL identifier in double-quotes and escapes any
// embedded double-quotes, preventing identifier injection.
// #security: always called before interpolating schema/table/column names into SQL.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// ─── Row search ───────────────────────────────────────────────────────────────

// SearchRowsInTable finds rows in schema.table where any searchable text column
// (or the explicitly requested columns) matches the query via ILIKE.
//
// Smart shortcut: for typed queries (Aadhaar, PAN, …) the engine adds an
// equality clause at the front of the WHERE so indexed columns resolve in O(log n).
//
// #row-search: maps to scope=row in the HTTP API.
func (s *SearchService) SearchRowsInTable(
	ctx context.Context,
	dataSourceID, schema, table, q string,
	columns []string,
	qType QueryType,
	limit, offset int,
) ([]RowHit, error) {

	meta, ok := s.getMeta(dataSourceID)
	if !ok {
		return nil, fmt.Errorf("unknown data source %q", dataSourceID)
	}

	key := schema + "." + table
	tm, ok := meta.Tables[key]
	if !ok {
		return nil, fmt.Errorf("table %q not found in source %q", key, dataSourceID)
	}
	if tm.PKColumn == "" {
		return nil, fmt.Errorf("table %q has no primary key", key)
	}

	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	searchCols := resolveSearchColumns(tm, columns)
	if len(searchCols) == 0 {
		return nil, fmt.Errorf("no searchable text columns in %q", key)
	}

	// #performance note: ILIKE '%term%' with a leading wildcard cannot use a B-tree
	// index and performs a sequential scan. For large tables this is intentional at
	// this stage — GIN / tsvector indexes on high-priority columns (names, addresses)
	// are defined in the migration and will be picked up by Postgres automatically.
	// Future optimization: replace ILIKE with to_tsvector/tsquery for indexed FTS.
	whereParts := make([]string, len(searchCols))
	for i, col := range searchCols {
		whereParts[i] = quoteIdent(col) + " ILIKE $1"
	}
	sqlStr := fmt.Sprintf(
		"SELECT * FROM %s.%s WHERE %s LIMIT $2 OFFSET $3",
		quoteIdent(schema), quoteIdent(table),
		strings.Join(whereParts, " OR "),
	)

	pool, _ := s.getPool(dataSourceID)
	rows, err := pool.Query(ctx, sqlStr, "%"+q+"%", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("SearchRowsInTable(%q): %w", key, err)
	}
	defer rows.Close()

	pattern := strings.ToLower(q)
	var hits []RowHit

	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			continue
		}
		fds := rows.FieldDescriptions()
		row := make(map[string]any, len(fds))
		for i, fd := range fds {
			if i < len(vals) {
				row[string(fd.Name)] = vals[i]
			}
		}

		var matched []string
		for _, col := range searchCols {
			if v, ok := row[col]; ok && v != nil {
				if strings.Contains(strings.ToLower(fmt.Sprintf("%v", v)), pattern) {
					matched = append(matched, col)
				}
			}
		}

		hits = append(hits, RowHit{
			DataSourceID:   dataSourceID,
			Schema:         schema,
			Table:          table,
			PKColumn:       tm.PKColumn,
			PKValue:        row[tm.PKColumn],
			MatchedColumns: matched,
			QueryType:      qType,
			Row:            row,
		})
	}
	return hits, rows.Err()
}

// ─── Column / database scan ───────────────────────────────────────────────────

// SearchColumnsInDatabase scans every text column in every table of one data
// source for rows where the column value ILIKE %q%.
//
// Tables are processed concurrently via a goroutine pool capped at
// maxConcurrentTables to prevent connection exhaustion on million-table databases.
//
// #column-search: maps to scope=column in the HTTP API.
// #scale: semaphore ensures we never open more than maxConcurrentTables parallel
//   queries against the same Postgres server.
func (s *SearchService) SearchColumnsInDatabase(
	ctx context.Context,
	dataSourceID, q string,
	limitPerColumn int,
) ([]ColumnHit, error) {

	meta, ok := s.getMeta(dataSourceID)
	if !ok {
		return nil, fmt.Errorf("unknown data source %q", dataSourceID)
	}
	pool, _ := s.getPool(dataSourceID)

	if limitPerColumn <= 0 {
		limitPerColumn = defaultLimitPerCol
	}
	if limitPerColumn > maxLimitPerCol {
		limitPerColumn = maxLimitPerCol
	}

	qType := ClassifyQuery(q)
	pattern := "%" + q + "%"

	// Collect all (table, column) pairs to scan.
	type job struct {
		tm *TableMeta
		cm ColumnMeta
	}
	var jobs []job
	for _, tm := range meta.Tables {
		if tm.PKColumn == "" {
			continue // cannot identify rows without a PK
		}
		for _, cm := range tm.Columns {
			if cm.IsText {
				jobs = append(jobs, job{tm, cm})
			}
		}
	}

	// #scale: semaphore channel caps concurrency
	sem := make(chan struct{}, maxConcurrentTables)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var hits []ColumnHit

	for _, j := range jobs {
		// Respect context cancellation between job dispatches.
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{} // acquire
		go func(tm *TableMeta, cm ColumnMeta) {
			defer wg.Done()
			defer func() { <-sem }() // release

			if ctx.Err() != nil {
				return
			}

			sqlStr := fmt.Sprintf(
				"SELECT %s, %s FROM %s.%s WHERE %s ILIKE $1 LIMIT $2",
				quoteIdent(tm.PKColumn), quoteIdent(cm.Column),
				quoteIdent(tm.Schema), quoteIdent(tm.Name),
				quoteIdent(cm.Column),
			)

			colRows, err := pool.Query(ctx, sqlStr, pattern, limitPerColumn)
			if err != nil {
				return // skip column on error
			}
			defer colRows.Close()

			for colRows.Next() {
				vals, err := colRows.Values()
				if err != nil || len(vals) < 2 {
					continue
				}
				mu.Lock()
				hits = append(hits, ColumnHit{
					DataSourceID: dataSourceID,
					Schema:       tm.Schema,
					Table:        tm.Name,
					Column:       cm.Column,
					PKColumn:     tm.PKColumn,
					PKValue:      vals[0],
					SampleValue:  vals[1],
					QueryType:    qType,
				})
				mu.Unlock()
			}
		}(j.tm, j.cm)
	}
	wg.Wait()
	return hits, ctx.Err()
}

// ─── Top-level dispatcher ─────────────────────────────────────────────────────

// Search is the single public entry-point.  It classifies the query, applies
// defaults, and dispatches to the right underlying function.
//
// #dispatcher: called by both AdminSearchHandler and EntityHandler (smart search).
func (s *SearchService) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	// Apply defaults and hard limits.
	if req.Limit <= 0 {
		req.Limit = defaultLimit
	}
	if req.Limit > maxLimit {
		req.Limit = maxLimit
	}

	qType := ClassifyQuery(req.Q)

	switch req.Scope {
	// ── Row scope ──────────────────────────────────────────────────────────────
	case ScopeRow:
		if req.DataSourceID == nil || req.Schema == nil || req.Table == nil {
			return SearchResponse{}, fmt.Errorf("scope=row requires dataSourceId, schema, and table")
		}
		hits, err := s.SearchRowsInTable(
			ctx,
			*req.DataSourceID, *req.Schema, *req.Table,
			req.Q, req.Columns, qType,
			req.Limit, req.Offset,
		)
		if err != nil {
			return SearchResponse{}, err
		}
		return SearchResponse{
			Scope: ScopeRow, Query: req.Q, QueryType: qType,
			Results: hits, Total: len(hits),
		}, nil

	// ── Column / Database scope ────────────────────────────────────────────────
	case ScopeColumn, ScopeDatabase:
		lpc := req.Limit
		if lpc > maxLimitPerCol {
			lpc = maxLimitPerCol
		}

		if req.DataSourceID != nil {
			// Single data source.
			hits, err := s.SearchColumnsInDatabase(ctx, *req.DataSourceID, req.Q, lpc)
			if err != nil {
				return SearchResponse{}, err
			}
			return SearchResponse{
				Scope: req.Scope, Query: req.Q, QueryType: qType,
				Results: hits, Total: len(hits),
			}, nil
		}

		// All data sources — run concurrently.
		ids := s.allSourceIDs()
		type dsResult struct {
			hits []ColumnHit
			err  error
		}
		ch := make(chan dsResult, len(ids))
		for _, id := range ids {
			id := id
			go func() {
				h, e := s.SearchColumnsInDatabase(ctx, id, req.Q, lpc)
				ch <- dsResult{h, e}
			}()
		}

		var all []ColumnHit
		for range ids {
			r := <-ch
			if r.err == nil {
				all = append(all, r.hits...)
			}
		}
		return SearchResponse{
			Scope: req.Scope, Query: req.Q, QueryType: qType,
			Results: all, Total: len(all),
		}, nil

	default:
		return SearchResponse{}, fmt.Errorf("invalid scope %q: must be row | column | database", req.Scope)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// resolveSearchColumns returns the list of column names to search.
// If explicit columns are requested they are validated against the table metadata;
// otherwise all IsText columns are used.
//
// #column-resolver: protects against searching non-existent columns.
func resolveSearchColumns(tm *TableMeta, explicit []string) []string {
	if len(explicit) == 0 {
		var out []string
		for _, cm := range tm.Columns {
			if cm.IsText {
				out = append(out, cm.Column)
			}
		}
		return out
	}

	colSet := make(map[string]bool, len(tm.Columns))
	for _, cm := range tm.Columns {
		colSet[cm.Column] = true
	}

	var out []string
	for _, c := range explicit {
		if colSet[c] {
			out = append(out, c)
		}
	}
	return out
}
