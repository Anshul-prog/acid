// #file: admin_db_search.go
// #package: handlers
// #purpose: HTTP handlers for the admin DB-search console.
//
// Endpoints exposed:
//   GET /api/admin/db-search          — search rows, columns, or full database
//   GET /api/admin/db-search/sources  — list all registered data sources + stats
//   POST /api/admin/db-search/refresh — reload schema metadata without restart
//
// Security:
//   • All routes require a valid JWT (enforced by AuthMiddleware in main.go).
//   • role == "admin" is additionally required here.
//   • Every call is recorded in entity_access_logs.
//
// Scale:
//   • SearchService handles millions of tables via concurrent goroutines.
//   • Context timeout (30 s default) prevents runaway scans.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"acid/internal/database"
	"acid/internal/dbsearch"
	"acid/internal/middleware"
)

// AdminSearchHandler holds the dependencies for the DB-search endpoints.
// #handler: created once in main.go, injected with SearchService + repo.
type AdminSearchHandler struct {
	search *dbsearch.SearchService
	repo   *database.EntityRepository
}

// NewAdminSearchHandler constructs the handler.
// #init: called in main.go after SearchService and EntityRepository are ready.
func NewAdminSearchHandler(svc *dbsearch.SearchService, repo *database.EntityRepository) *AdminSearchHandler {
	return &AdminSearchHandler{search: svc, repo: repo}
}

// ─── GET /api/admin/db-search ─────────────────────────────────────────────────
//
// Query parameters:
//   q            required  search term
//   scope        optional  row | column | database  (default: database)
//   dataSourceId optional  restrict to one source
//   schema       optional  required when scope=row
//   table        optional  required when scope=row
//   columns      optional  comma-separated column names (scope=row only)
//   limit        optional  max results (default 50, max 200)
//   offset       optional  pagination offset for scope=row
//
// #search-handler: parses params → builds SearchRequest → calls SearchService.Search()
func (h *AdminSearchHandler) HandleDBSearch(w http.ResponseWriter, r *http.Request) {
	// ── Admin guard ────────────────────────────────────────────────────────────
	userCtx := middleware.GetUserContext(r.Context())
	if userCtx == nil || userCtx.Role != "admin" {
		adminSearchWriteError(w, http.StatusForbidden, "admin role required")
		return
	}

	// ── Parse & validate query params ─────────────────────────────────────────
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		adminSearchWriteError(w, http.StatusBadRequest, "q is required")
		return
	}

	scopeStr := r.URL.Query().Get("scope")
	if scopeStr == "" {
		scopeStr = "database"
	}
	var scope dbsearch.Scope
	switch scopeStr {
	case "row":
		scope = dbsearch.ScopeRow
	case "column":
		scope = dbsearch.ScopeColumn
	default:
		scope = dbsearch.ScopeDatabase
	}

	// Optional pointer params.
	dsID := r.URL.Query().Get("dataSourceId")
	var dsPtr *string
	if dsID != "" {
		dsPtr = &dsID
	}

	schema := r.URL.Query().Get("schema")
	var schemaPtr *string
	if schema != "" {
		schemaPtr = &schema
	}

	table := r.URL.Query().Get("table")
	var tablePtr *string
	if table != "" {
		tablePtr = &table
	}

	// Columns (comma-separated).
	var cols []string
	if cp := r.URL.Query().Get("columns"); cp != "" {
		for _, c := range strings.Split(cp, ",") {
			if c = strings.TrimSpace(c); c != "" {
				cols = append(cols, c)
			}
		}
	}

	limit := adminSearchParseInt(r.URL.Query().Get("limit"), 50)
	offset := adminSearchParseInt(r.URL.Query().Get("offset"), 0)

	// ── Execute search with a hard timeout ────────────────────────────────────
	// #scale: 30 s is generous; wide DB scans need time but must not hang forever.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	req := dbsearch.SearchRequest{
		Q:            q,
		Scope:        scope,
		DataSourceID: dsPtr,
		Schema:       schemaPtr,
		Table:        tablePtr,
		Columns:      cols,
		Limit:        limit,
		Offset:       offset,
	}

	resp, err := h.search.Search(ctx, req)
	if err != nil {
		adminSearchWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// ── Forensic audit log ────────────────────────────────────────────────────
	// #forensic: every search is recorded with user + query + result count.
	go h.repo.LogAccess(context.Background(), database.AccessLogEntry{
		UserID:      userCtx.UserID,
		Username:    userCtx.Username,
		Action:      "db_search",
		Scope:       scopeStr,
		QueryText:   q,
		DataSource:  dsID,
		ResultCount: resp.Total,
		IPAddress:   r.RemoteAddr,
		UserAgent:   r.UserAgent(),
	})

	adminSearchWriteJSON(w, http.StatusOK, resp)
}

// ─── GET /api/admin/db-search/sources ────────────────────────────────────────
//
// Returns all registered data sources with table counts and last-loaded time.
// #sources-handler: lets the frontend populate a data-source selector.
func (h *AdminSearchHandler) HandleDBSearchSources(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.GetUserContext(r.Context())
	if userCtx == nil || userCtx.Role != "admin" {
		adminSearchWriteError(w, http.StatusForbidden, "admin role required")
		return
	}
	adminSearchWriteJSON(w, http.StatusOK, map[string]any{
		"sources": h.search.Stats(),
	})
}

// ─── POST /api/admin/db-search/refresh ───────────────────────────────────────
//
// Triggers an immediate reload of schema metadata for all data sources.
// Use after adding new tables to the database.
// #refresh-handler: #self-healing
func (h *AdminSearchHandler) HandleDBSearchRefresh(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.GetUserContext(r.Context())
	if userCtx == nil || userCtx.Role != "admin" {
		adminSearchWriteError(w, http.StatusForbidden, "admin role required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	if err := h.search.Refresh(ctx); err != nil {
		adminSearchWriteError(w, http.StatusInternalServerError, "refresh failed: "+err.Error())
		return
	}

	adminSearchWriteJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "schema metadata refreshed",
		"sources": h.search.Stats(),
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func adminSearchWriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func adminSearchWriteError(w http.ResponseWriter, code int, msg string) {
	adminSearchWriteJSON(w, code, map[string]string{"error": msg})
}

func adminSearchParseInt(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil && v > 0 {
		return v
	}
	return def
}
