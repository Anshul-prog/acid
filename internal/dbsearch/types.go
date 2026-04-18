// #package: dbsearch
// #purpose: Core intelligence search engine for L.S.D — handles millions of tables
//           across multiple PostgreSQL data sources with smart query classification.
//
// Architecture Overview:
//   ┌─────────────┐   ┌───────────────────┐   ┌──────────────────────┐
//   │ HTTP Handler│──▶│  SearchService     │──▶│ DataSourceMeta (map) │
//   └─────────────┘   │  (one per server)  │   │  schema + PK index   │
//                     └───────────────────┘   └──────────────────────┘
//                              │ dispatches to
//           ┌──────────────────┼──────────────────┐
//           ▼                  ▼                  ▼
//     SearchRows       SearchColumns        Full DB Scan
//     (one table)    (one data source)    (all sources)
//
// Query Classification:  before any SQL is issued the raw input is classified
//   (Aadhaar / PAN / Passport / DL / Phone / Email / Bank / General)
//   so the engine can hit the right index first and fall back to ILIKE only
//   when necessary.
//
// Scale:  metadata is loaded ONCE at startup and cached in memory, so even
//   with millions of tables the search loop itself never hits information_schema
//   at query-time. Only the actual data tables are queried.

package dbsearch

// ─── Scope ───────────────────────────────────────────────────────────────────

// Scope defines the search boundary a caller wants to use.
// #scope-types: Sent as the "scope" query-parameter in the HTTP API.
type Scope string

const (
	// ScopeRow   → search inside one specific table (requires schema + table).
	ScopeRow Scope = "row"
	// ScopeColumn → find which columns in ONE data source contain a value.
	ScopeColumn Scope = "column"
	// ScopeDatabase → scan every registered data source (broadest, slowest).
	ScopeDatabase Scope = "database"
)

// ─── Query Classification ─────────────────────────────────────────────────────

// QueryType is the auto-detected category of the raw search string.
// #smart-search: ClassifyQuery() sets this before the SQL is built, allowing
//   the engine to choose the fastest index (exact match on doc_number beats ILIKE).
type QueryType string

const (
	QueryAadhaar  QueryType = "aadhaar"          // 12-digit national ID (India)
	QueryPAN      QueryType = "pan"              // Permanent Account Number
	QueryPassport QueryType = "passport"         // Passport number
	QueryDL       QueryType = "driving_license"  // Driving Licence number
	QueryVoterID  QueryType = "voter_id"         // Voter / EPIC ID
	QueryPhone    QueryType = "phone"            // Mobile / landline
	QueryEmail    QueryType = "email"            // E-mail address
	QueryBankAcc  QueryType = "bank_account"     // Bank account number
	QueryIFSC     QueryType = "ifsc"             // IFSC bank branch code
	QueryGeneral  QueryType = "general"          // Free text / name / keyword
)

// ─── Metadata types ───────────────────────────────────────────────────────────

// ColumnMeta holds everything the engine needs to know about one column.
// #metadata: populated once at startup from information_schema.columns.
type ColumnMeta struct {
	Schema    string // e.g. "public"
	Table     string // e.g. "entities"
	Column    string // e.g. "full_name"
	DataType  string // PostgreSQL type string, e.g. "character varying"
	IsText    bool   // safe for ILIKE search
	IsNumeric bool   // safe for range / equality search
}

// TableMeta holds the schema snapshot for one table.
// #metadata: built by buildDataSourceMeta() from ColumnMeta + PK discovery.
type TableMeta struct {
	Schema   string
	Name     string
	PKColumn string       // leading primary-key column (empty → no PK)
	Columns  []ColumnMeta // in ordinal_position order
}

// DataSourceMeta is the complete in-memory schema for one PostgreSQL connection.
// #multi-source: the map key in SearchService.metas is a caller-defined ID
//   such as "default", "ds_delhi", "ds_mumbai".
type DataSourceMeta struct {
	ID     string                   // data-source identifier
	Tables map[string]*TableMeta    // key: "schema.table"
}

// ─── Result types ─────────────────────────────────────────────────────────────

// RowHit represents one matched row returned by a row-scoped search.
// #result: appears in SearchResponse.Results when Scope == ScopeRow.
type RowHit struct {
	DataSourceID   string         `json:"data_source_id"`
	Schema         string         `json:"schema"`
	Table          string         `json:"table"`
	PKColumn       string         `json:"pk_column"`
	PKValue        any            `json:"pk_value"`
	MatchedColumns []string       `json:"matched_columns"`
	QueryType      QueryType      `json:"query_type"`
	Row            map[string]any `json:"row"`
}

// ColumnHit represents one value found while scanning across columns / tables.
// #result: appears in SearchResponse.Results when Scope == ScopeColumn or ScopeDatabase.
type ColumnHit struct {
	DataSourceID string    `json:"data_source_id"`
	Schema       string    `json:"schema"`
	Table        string    `json:"table"`
	Column       string    `json:"column"`
	PKColumn     string    `json:"pk_column"`
	PKValue      any       `json:"pk_value"`
	SampleValue  any       `json:"sample_value"`
	QueryType    QueryType `json:"query_type"`
}

// ─── Request / Response ───────────────────────────────────────────────────────

// SearchRequest carries every parameter the HTTP handler passes to Search().
// #request: constructed by the handler from URL query-params.
type SearchRequest struct {
	Q            string   // raw user input (never empty)
	Scope        Scope    // row | column | database
	DataSourceID *string  // nil → search all sources
	Schema       *string  // required for ScopeRow
	Table        *string  // required for ScopeRow
	Columns      []string // optional: limit columns for ScopeRow
	Limit        int      // clamped to maxLimit inside Search()
	Offset       int      // page offset for ScopeRow
}

// SearchResponse is the JSON envelope sent back to the client.
// #response: written by the HTTP handler via json.NewEncoder.
type SearchResponse struct {
	Scope     Scope     `json:"scope"`
	Query     string    `json:"query"`
	QueryType QueryType `json:"query_type"` // auto-detected
	Results   any       `json:"results"`
	Total     int       `json:"total"`
}
