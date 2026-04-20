// =============================================================================
// internal/handlers/omni_search_handler.go
// =============================================================================
// Omni-Search: A sub-second, 360-degree entity profile search handler.
//
// Query Strategy:
//   1. ClickHouse bitmap search → global_ids (fastest possible token lookup)
//   2. Per-table raw data fetch from ClickHouse search_{table} indexes
//   3. PostgreSQL enrichment JOIN → identity anchors, bank accounts,
//      social accounts, remarks, and category tags
//   4. Multi-filter: category IDs, status, risk range, date-of-birth
//
// Route: GET /api/omni-search
// Params:
//   q          - search term (name, email, phone, address, ID number, social handle)
//   category   - category name(s), repeatable: ?category=backend&category=manager
//   tag_mode   - 'ANY' (default) or 'ALL'
//   status     - entity status filter, repeatable
//   dob_from   - YYYY-MM-DD
//   dob_to     - YYYY-MM-DD
//   risk_min   - 0-100
//   risk_max   - 0-100
//   limit      - default 20, max 100
//   cursor     - pagination cursor (opaque base64 string)
//   source     - 'clickhouse' (default) | 'postgres' (fallback)
// =============================================================================
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"acid/internal/clickhouse"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OmniSearchHandler serves the 360-degree entity search endpoint.
type OmniSearchHandler struct {
	chSearch *clickhouse.SearchRepository // may be nil (PostgreSQL fallback)
	pgPool   *pgxpool.Pool
}

// NewOmniSearchHandler creates an omni-search handler.
func NewOmniSearchHandler(chSearch *clickhouse.SearchRepository, pgPool *pgxpool.Pool) *OmniSearchHandler {
	return &OmniSearchHandler{
		chSearch: chSearch,
		pgPool:   pgPool,
	}
}

// ── Response types ─────────────────────────────────────────────────────────────

type OmniSearchResult struct {
	SearchTerm    string          `json:"search_term"`
	Filters       OmniFilters     `json:"filters"`
	Total         int             `json:"total"`
	Count         int             `json:"count"`
	HasMore       bool            `json:"has_more"`
	NextCursor    string          `json:"next_cursor,omitempty"`
	TimeTakenMs   int64           `json:"time_taken_ms"`
	SearchEngine  string          `json:"search_engine"` // "clickhouse" | "postgres"
	Results       []EntityProfile `json:"results"`
}

type OmniFilters struct {
	Categories []string `json:"categories,omitempty"`
	TagMode    string   `json:"tag_mode,omitempty"`
	Status     []string `json:"status,omitempty"`
	DobFrom    string   `json:"dob_from,omitempty"`
	DobTo      string   `json:"dob_to,omitempty"`
	RiskMin    *int     `json:"risk_min,omitempty"`
	RiskMax    *int     `json:"risk_max,omitempty"`
}

type EntityProfile struct {
	// Core identity (from ClickHouse search index)
	EntityID    uint64                 `json:"entity_id,omitempty"`
	LocalID     interface{}            `json:"id"`
	SourceTable string                 `json:"source_table"`
	CoreData    map[string]interface{} `json:"core_data"`
	MatchScore  float64                `json:"match_score,omitempty"`

	// Extended profile (from PostgreSQL — populated when entity_id resolves)
	IdentityAnchors []IdentityAnchorInfo `json:"identity_anchors,omitempty"`
	BankAccounts    []BankAccountInfo    `json:"bank_accounts,omitempty"`
	SocialAccounts  []SocialAccountInfo  `json:"social_accounts,omitempty"`
	Properties      []PropertyInfo       `json:"properties,omitempty"`
	RemarkSummary   map[string]int       `json:"remark_summary,omitempty"`
	Categories      []string             `json:"categories,omitempty"`
	RiskScore       *int                 `json:"risk_score,omitempty"`
	EntityStatus    string               `json:"entity_status,omitempty"`
}

type IdentityAnchorInfo struct {
	IDType       string `json:"id_type"`
	IDMasked     string `json:"id_number_masked"`
	IsVerified   bool   `json:"is_verified"`
	IssueCountry string `json:"issuing_country"`
}

type BankAccountInfo struct {
	BankName    string `json:"bank_name"`
	AccountMask string `json:"account_masked"`
	AccountType string `json:"account_type"`
	IFSCCode    string `json:"ifsc_code,omitempty"`
	IsPrimary   bool   `json:"is_primary"`
}

type SocialAccountInfo struct {
	Platform      string   `json:"platform"`
	Handle        string   `json:"handle"`
	ProfileURL    string   `json:"profile_url,omitempty"`
	IsVerified    bool     `json:"is_verified"`
	FollowerCount *int     `json:"follower_count,omitempty"`
	RiskFlags     []string `json:"risk_flags,omitempty"`
}

type PropertyInfo struct {
	Type         string  `json:"type"`
	Address      string  `json:"address,omitempty"`
	City         string  `json:"city,omitempty"`
	CurrentValue float64 `json:"current_value,omitempty"`
	Currency     string  `json:"currency,omitempty"`
	IsMortgaged  bool    `json:"is_mortgaged"`
}

// ── Handler ────────────────────────────────────────────────────────────────────

// HandleOmniSearch handles GET /api/omni-search
func (h *OmniSearchHandler) HandleOmniSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	// ── Parse query params ──────────────────────────────────────────────────
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	categories := r.URL.Query()["category"] // multi-value
	tagMode := strings.ToUpper(r.URL.Query().Get("tag_mode"))
	if tagMode != "ALL" {
		tagMode = "ANY"
	}
	statuses := r.URL.Query()["status"]
	dobFrom := r.URL.Query().Get("dob_from")
	dobTo := r.URL.Query().Get("dob_to")
	cursor := r.URL.Query().Get("cursor")
	sourceOverride := r.URL.Query().Get("source") // "postgres" forces PG-only

	limit := 20
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		if l > 100 {
			l = 100
		}
		limit = l
	}

	var riskMin, riskMax *int
	if v, err := strconv.Atoi(r.URL.Query().Get("risk_min")); err == nil {
		riskMin = &v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("risk_max")); err == nil {
		riskMax = &v
	}

	if q == "" && len(categories) == 0 && len(statuses) == 0 {
		jsonError(w, "at least one of: q, category, or status is required", http.StatusBadRequest)
		return
	}

	filters := OmniFilters{
		Categories: categories,
		TagMode:    tagMode,
		Status:     statuses,
		DobFrom:    dobFrom,
		DobTo:      dobTo,
		RiskMin:    riskMin,
		RiskMax:    riskMax,
	}

	var result *OmniSearchResult
	var err error

	// ── Route to search engine ──────────────────────────────────────────────
	if h.chSearch != nil && h.chSearch.IsAvailable() && sourceOverride != "postgres" {
		result, err = h.searchViaClickHouse(ctx, q, filters, limit, cursor)
	} else {
		result, err = h.searchViaPostgres(ctx, q, filters, limit, cursor)
	}

	if err != nil {
		log.Printf("[OmniSearch] error: %v", err)
		jsonError(w, "search failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	result.SearchTerm = q
	result.Filters = filters
	result.TimeTakenMs = time.Since(start).Milliseconds()
	result.Count = len(result.Results)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Search-Engine", result.SearchEngine)
	w.Header().Set("X-Search-Time-Ms", strconv.FormatInt(result.TimeTakenMs, 10))
	json.NewEncoder(w).Encode(result)
}

// ── ClickHouse Search Path ─────────────────────────────────────────────────────

func (h *OmniSearchHandler) searchViaClickHouse(
	ctx context.Context,
	q string, filters OmniFilters,
	limit int, cursor string,
) (*OmniSearchResult, error) {

	// Step 1: Bitmap search for global_ids
	chResult, err := h.chSearch.SearchFullHistoryBitmap(ctx, q, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("clickhouse bitmap search: %w", err)
	}

	result := &OmniSearchResult{
		SearchEngine: "clickhouse",
		HasMore:      chResult.HasMore,
		NextCursor:   chResult.NextCursor,
		Total:        chResult.Count,
	}

	if len(chResult.Data) == 0 {
		result.Results = []EntityProfile{}
		return result, nil
	}

	// Step 2: Enrich each record with PostgreSQL extended profile
	profiles := make([]EntityProfile, 0, len(chResult.Data))
	for _, record := range chResult.Data {
		profile := EntityProfile{
			SourceTable: getString(record, "_source_table"),
			CoreData:    sanitizeCoreData(record),
		}

		if gid, ok := record["global_id"].(string); ok {
			if id, _ := strconv.ParseUint(gid, 10, 64); id > 0 {
				profile.EntityID = id
			}
		}
		if localID, ok := record["id"]; ok {
			profile.LocalID = localID
		}

		// Attempt PostgreSQL enrichment using source_record_id / email / phone
		h.enrichProfile(ctx, &profile, record)

		// Apply category filter if specified
		if len(filters.Categories) > 0 {
			if !profileMatchesCategories(profile.Categories, filters.Categories, filters.TagMode) {
				continue
			}
		}

		profiles = append(profiles, profile)
	}

	result.Results = profiles
	return result, nil
}

// ── PostgreSQL Search Path (fallback / pure PG) ────────────────────────────────

func (h *OmniSearchHandler) searchViaPostgres(
	ctx context.Context,
	q string, filters OmniFilters,
	limit int, cursor string,
) (*OmniSearchResult, error) {

	// Build the multi-filter query
	args := []interface{}{}
	argIdx := 1

	// Category IDs: resolve names to IDs
	var categoryIDs []int
	if len(filters.Categories) > 0 {
		ids, err := h.resolveCategoryNames(ctx, filters.Categories)
		if err != nil {
			return nil, fmt.Errorf("resolve categories: %w", err)
		}
		categoryIDs = ids
	}

	// Parse offset from cursor (simple integer for PG fallback)
	offset := 0
	if cursor != "" {
		if v, err := strconv.Atoi(cursor); err == nil {
			offset = v
		}
	}

	// Call the PostgreSQL multi-filter function
	sqlQ := `SELECT
		entity_id, first_name, last_name, primary_email, primary_phone,
		date_of_birth, risk_score, status, city, state,
		category_names, identity_types, rank, total_count
	FROM search_entities_multi_filter($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	var dobFrom, dobTo *string
	if filters.DobFrom != "" {
		dobFrom = &filters.DobFrom
	}
	if filters.DobTo != "" {
		dobTo = &filters.DobTo
	}

	var statusArr interface{}
	if len(filters.Status) > 0 {
		statusArr = filters.Status
	}

	var catIDsArr interface{}
	if len(categoryIDs) > 0 {
		catIDsArr = categoryIDs
	}

	args = []interface{}{
		nvlStr(q),        // p_search_term
		catIDsArr,         // p_category_ids
		filters.TagMode,   // p_tag_mode
		statusArr,         // p_status
		dobFrom,           // p_dob_from
		dobTo,             // p_dob_to
		filters.RiskMin,   // p_risk_min
		filters.RiskMax,   // p_risk_max
		limit,             // p_page_size
		offset,            // p_offset
	}

	_ = argIdx
	_ = sqlQ
	rows, err := h.pgPool.Query(ctx, sqlQ, args...)
	if err != nil {
		return nil, fmt.Errorf("multi-filter query: %w", err)
	}
	defer rows.Close()

	var profiles []EntityProfile
	var totalCount int64

	for rows.Next() {
		var (
			entityID    int64
			firstName   string
			lastName    *string
			email       *string
			phone       *string
			dob         *time.Time
			riskScore   *int16
			status      string
			city        *string
			state       *string
			catNames    []string
			idTypes     []string
			rank        float32
			total       int64
		)
		if err := rows.Scan(
			&entityID, &firstName, &lastName, &email, &phone,
			&dob, &riskScore, &status, &city, &state,
			&catNames, &idTypes, &rank, &total,
		); err != nil {
			log.Printf("[OmniSearch] scan error: %v", err)
			continue
		}
		totalCount = total

		coreData := map[string]interface{}{
			"first_name": firstName,
			"last_name":  derefStr(lastName),
			"email":      derefStr(email),
			"phone":      derefStr(phone),
			"city":       derefStr(city),
			"state":      derefStr(state),
		}
		if dob != nil {
			coreData["date_of_birth"] = dob.Format("2006-01-02")
		}
		if riskScore != nil {
			rs := int(*riskScore)
			coreData["risk_score"] = rs
		}

		var rs *int
		if riskScore != nil {
			v := int(*riskScore)
			rs = &v
		}

		profile := EntityProfile{
			LocalID:      entityID,
			SourceTable:  "entities",
			CoreData:     coreData,
			Categories:   catNames,
			MatchScore:   float64(rank),
			EntityStatus: status,
			RiskScore:    rs,
		}

		// Fetch extended data for this entity
		h.enrichProfileByEntityID(ctx, &profile, entityID)

		profiles = append(profiles, profile)
	}

	nextCursor := ""
	hasMore := int64(offset+limit) < totalCount
	if hasMore {
		nextCursor = strconv.Itoa(offset + limit)
	}

	return &OmniSearchResult{
		SearchEngine: "postgres",
		Total:        int(totalCount),
		HasMore:      hasMore,
		NextCursor:   nextCursor,
		Results:      profiles,
	}, nil
}

// ── Profile enrichment from PostgreSQL ────────────────────────────────────────

// enrichProfile attempts to find the entity in `entities` by email/phone
// then loads extended profile data.
func (h *OmniSearchHandler) enrichProfile(ctx context.Context, profile *EntityProfile, record map[string]interface{}) {
	// Try to match entity by email or phone from the core record
	email := getString(record, "email")
	phone := getString(record, "phone")
	if email == "" && phone == "" {
		return
	}

	var entityID int64
	err := h.pgPool.QueryRow(ctx,
		`SELECT id FROM entities WHERE primary_email = $1 OR primary_phone = $2 LIMIT 1`,
		nvlStr(email), nvlStr(phone),
	).Scan(&entityID)
	if err != nil {
		return // entity not found in entities table — that's fine
	}

	profile.LocalID = entityID
	h.enrichProfileByEntityID(ctx, profile, entityID)
}

func (h *OmniSearchHandler) enrichProfileByEntityID(ctx context.Context, profile *EntityProfile, entityID int64) {
	enrichCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// Identity anchors
	rows, err := h.pgPool.Query(enrichCtx,
		`SELECT id_type, regexp_replace(id_number, '.(?=.{4})', 'X'), is_verified, issuing_country
		 FROM entity_identity_anchors WHERE entity_id = $1 ORDER BY is_primary DESC LIMIT 10`, entityID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var anchor IdentityAnchorInfo
			_ = rows.Scan(&anchor.IDType, &anchor.IDMasked, &anchor.IsVerified, &anchor.IssueCountry)
			profile.IdentityAnchors = append(profile.IdentityAnchors, anchor)
		}
	}

	// Bank accounts
	bRows, err := h.pgPool.Query(enrichCtx,
		`SELECT bank_name, coalesce(account_number_masked,''), coalesce(account_type,'OTHER'),
		        coalesce(ifsc_code,''), is_primary
		 FROM entity_bank_accounts WHERE entity_id = $1 AND is_active ORDER BY is_primary DESC LIMIT 5`, entityID)
	if err == nil {
		defer bRows.Close()
		for bRows.Next() {
			var b BankAccountInfo
			_ = bRows.Scan(&b.BankName, &b.AccountMask, &b.AccountType, &b.IFSCCode, &b.IsPrimary)
			profile.BankAccounts = append(profile.BankAccounts, b)
		}
	}

	// Social accounts
	sRows, err := h.pgPool.Query(enrichCtx,
		`SELECT platform, handle, coalesce(profile_url,''), is_verified, follower_count, risk_flags
		 FROM entity_social_accounts WHERE entity_id = $1 AND is_active ORDER BY follower_count DESC NULLS LAST LIMIT 10`, entityID)
	if err == nil {
		defer sRows.Close()
		for sRows.Next() {
			var s SocialAccountInfo
			_ = sRows.Scan(&s.Platform, &s.Handle, &s.ProfileURL, &s.IsVerified, &s.FollowerCount, &s.RiskFlags)
			profile.SocialAccounts = append(profile.SocialAccounts, s)
		}
	}

	// Properties
	pRows, err := h.pgPool.Query(enrichCtx,
		`SELECT property_type, coalesce(address,''), coalesce(city,''),
		        coalesce(current_value,0), coalesce(currency,'INR'), is_mortgaged
		 FROM entity_properties WHERE entity_id = $1 AND is_active ORDER BY current_value DESC NULLS LAST LIMIT 5`, entityID)
	if err == nil {
		defer pRows.Close()
		for pRows.Next() {
			var p PropertyInfo
			_ = pRows.Scan(&p.Type, &p.Address, &p.City, &p.CurrentValue, &p.Currency, &p.IsMortgaged)
			profile.Properties = append(profile.Properties, p)
		}
	}

	// Categories
	catRows, err := h.pgPool.Query(enrichCtx,
		`SELECT c.name FROM entity_tags et
		 JOIN categories c ON c.id = et.category_id AND c.is_active
		 WHERE et.entity_id = $1 ORDER BY c.name`, entityID)
	if err == nil {
		defer catRows.Close()
		for catRows.Next() {
			var name string
			_ = catRows.Scan(&name)
			profile.Categories = append(profile.Categories, name)
		}
	}

	// Remark summary
	rmRows, err := h.pgPool.Query(enrichCtx,
		`SELECT severity, count(*) FROM entity_remarks WHERE entity_id = $1 GROUP BY severity`, entityID)
	if err == nil {
		defer rmRows.Close()
		summary := make(map[string]int)
		for rmRows.Next() {
			var sev string
			var cnt int
			_ = rmRows.Scan(&sev, &cnt)
			summary[sev] = cnt
		}
		if len(summary) > 0 {
			profile.RemarkSummary = summary
		}
	}

	// Risk score & status from entity
	h.pgPool.QueryRow(enrichCtx,
		`SELECT risk_score, status FROM entities WHERE id = $1`, entityID,
	).Scan(&profile.RiskScore, &profile.EntityStatus)
}

// ── Multi-filter helpers ───────────────────────────────────────────────────────

func (h *OmniSearchHandler) resolveCategoryNames(ctx context.Context, names []string) ([]int, error) {
	if len(names) == 0 {
		return nil, nil
	}
	rows, err := h.pgPool.Query(ctx,
		`SELECT id FROM categories WHERE name = ANY($1) AND is_active`, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids, nil
}

func profileMatchesCategories(profileCats, filterCats []string, mode string) bool {
	if len(filterCats) == 0 {
		return true
	}
	catSet := make(map[string]bool, len(profileCats))
	for _, c := range profileCats {
		catSet[strings.ToLower(c)] = true
	}
	if mode == "ALL" {
		for _, fc := range filterCats {
			if !catSet[strings.ToLower(fc)] {
				return false
			}
		}
		return true
	}
	// ANY mode
	for _, fc := range filterCats {
		if catSet[strings.ToLower(fc)] {
			return true
		}
	}
	return false
}

// ── Utility helpers ────────────────────────────────────────────────────────────

func sanitizeCoreData(record map[string]interface{}) map[string]interface{} {
	skip := map[string]bool{"_source_table": true, "global_id": true}
	out := make(map[string]interface{}, len(record))
	for k, v := range record {
		if !skip[k] {
			out[k] = v
		}
	}
	return out
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nvlStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
