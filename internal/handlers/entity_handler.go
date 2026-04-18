// #file: entity_handler.go
// #package: handlers
// #purpose: HTTP handlers for the intelligence-grade entity / case / smart-search
//           and work-session endpoints.
//
// Endpoints:
//   GET  /api/smart-search                   — smart query-classified search
//   GET  /api/entities/{id}/profile          — full bio-data profile
//   GET  /api/entities/{id}/export           — download profile (json | csv)
//   GET  /api/cases                          — list cases with filters
//   GET  /api/cases/{id}                     — case detail with linked entities
//   GET  /api/cases/{id}/entities            — full profiles of case entities
//   POST /api/work-sessions                  — start a work session
//   PATCH /api/work-sessions/{id}/end        — end a work session
//   GET  /api/work-sessions                  — list own work sessions
//
// PII masking:
//   Requests from users without the "unmask" scope receive masked document
//   numbers and bank account numbers automatically.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"acid/internal/database"
	"acid/internal/dbsearch"
	"acid/internal/middleware"
)

// EntityHandler holds all dependencies for entity / case endpoints.
// #handler: created once in main.go.
type EntityHandler struct {
	repo   *database.EntityRepository
	search *dbsearch.SearchService
}

// NewEntityHandler constructs the handler.
func NewEntityHandler(repo *database.EntityRepository, svc *dbsearch.SearchService) *EntityHandler {
	return &EntityHandler{repo: repo, search: svc}
}

// ─── Smart Search ─────────────────────────────────────────────────────────────

// HandleSmartSearch classifies the query and dispatches to the best search plan.
//
// GET /api/smart-search?q=...&scope=...&limit=...&dataSourceId=...
//
// #smart-search: when a user types "123456789012" the system detects Aadhaar
//   and hits entity_documents first, falling back to a full-text column scan.
func (h *EntityHandler) HandleSmartSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		entityWriteError(w, http.StatusBadRequest, "q is required")
		return
	}

	scopeStr := r.URL.Query().Get("scope")
	if scopeStr == "" {
		scopeStr = "database"
	}
	scope := dbsearch.ScopeDatabase
	switch scopeStr {
	case "row":
		scope = dbsearch.ScopeRow
	case "column":
		scope = dbsearch.ScopeColumn
	}

	dsID := r.URL.Query().Get("dataSourceId")
	var dsPtr *string
	if dsID != "" {
		dsPtr = &dsID
	}

	limit := entityParseInt(r.URL.Query().Get("limit"), 50)

	// #smart-search: classify the query to inform the caller what type was detected
	qType := dbsearch.ClassifyQuery(q)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	resp, err := h.search.Search(ctx, dbsearch.SearchRequest{
		Q:            q,
		Scope:        scope,
		DataSourceID: dsPtr,
		Limit:        limit,
	})
	if err != nil {
		entityWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Log the search
	userCtx := middleware.GetUserContext(r.Context())
	if userCtx != nil {
		go h.repo.LogAccess(context.Background(), database.AccessLogEntry{
			UserID:      userCtx.UserID,
			Username:    userCtx.Username,
			Action:      "smart_search",
			Scope:       scopeStr,
			QueryText:   q,
			DataSource:  dsID,
			ResultCount: resp.Total,
			IPAddress:   r.RemoteAddr,
			UserAgent:   r.UserAgent(),
		})
	}

	entityWriteJSON(w, http.StatusOK, map[string]any{
		"query":      q,
		"query_type": qType,
		"scope":      scope,
		"total":      resp.Total,
		"results":    resp.Results,
	})
}

// ─── Entity Profile ───────────────────────────────────────────────────────────

// HandleGetEntityProfile returns the complete bio-data profile for one entity.
//
// GET /api/entities/{id}/profile
//
// PII masking: applied unless the user has "unmask" in their scopes.
// #profile-handler
func (h *EntityHandler) HandleGetEntityProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		entityWriteError(w, http.StatusBadRequest, "entity id is required")
		return
	}

	userCtx := middleware.GetUserContext(r.Context())
	maskPII := !hasScope(userCtx, "unmask")

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	profile, err := h.repo.GetEntityProfile(ctx, id, maskPII)
	if err != nil {
		entityWriteError(w, http.StatusNotFound, "entity not found: "+err.Error())
		return
	}

	// #forensic audit
	if userCtx != nil {
		go h.repo.LogAccess(context.Background(), database.AccessLogEntry{
			UserID:    userCtx.UserID,
			Username:  userCtx.Username,
			Action:    "view_profile",
			EntityID:  id,
			IPAddress: r.RemoteAddr,
			UserAgent: r.UserAgent(),
		})
	}

	entityWriteJSON(w, http.StatusOK, profile)
}

// ─── Entity Export ────────────────────────────────────────────────────────────

// HandleExportEntityProfile streams the entity profile as a downloadable file.
//
// GET /api/entities/{id}/export?format=json|csv
//
// Returns Content-Disposition: attachment so the browser downloads the file.
// #export-handler: forensically logged as "export" action.
func (h *EntityHandler) HandleExportEntityProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		entityWriteError(w, http.StatusBadRequest, "entity id is required")
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	userCtx := middleware.GetUserContext(r.Context())
	maskPII := !hasScope(userCtx, "unmask")

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	profile, err := h.repo.GetEntityProfile(ctx, id, maskPII)
	if err != nil {
		entityWriteError(w, http.StatusNotFound, "entity not found: "+err.Error())
		return
	}

	// #forensic audit
	if userCtx != nil {
		go h.repo.LogAccess(context.Background(), database.AccessLogEntry{
			UserID:    userCtx.UserID,
			Username:  userCtx.Username,
			Action:    "export",
			EntityID:  id,
			IPAddress: r.RemoteAddr,
			UserAgent: r.UserAgent(),
		})
	}

	safeName := strings.ReplaceAll(profile.Entity.FullName, " ", "_")

	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s_profile.csv"`, safeName))
		if err := database.WriteProfileCSV(w, profile); err != nil {
			fmt.Printf("[export] CSV write error: %v\n", err)
		}
	default: // json
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s_profile.json"`, safeName))
		if err := database.WriteProfileJSON(w, profile); err != nil {
			fmt.Printf("[export] JSON write error: %v\n", err)
		}
	}
}

// ─── Cases ────────────────────────────────────────────────────────────────────

// HandleListCases returns a paginated, filtered list of cases.
//
// GET /api/cases?status=open&category=Cybercrime&q=keyword&limit=50&offset=0
// #cases-list-handler
func (h *EntityHandler) HandleListCases(w http.ResponseWriter, r *http.Request) {
	filter := database.ListCasesFilter{
		Status:   r.URL.Query().Get("status"),
		Category: r.URL.Query().Get("category"),
		Q:        r.URL.Query().Get("q"),
		Limit:    entityParseInt(r.URL.Query().Get("limit"), 50),
		Offset:   entityParseInt(r.URL.Query().Get("offset"), 0),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cases, total, err := h.repo.ListCases(ctx, filter)
	if err != nil {
		entityWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	entityWriteJSON(w, http.StatusOK, map[string]any{
		"cases":  cases,
		"total":  total,
		"limit":  filter.Limit,
		"offset": filter.Offset,
	})
}

// HandleGetCase returns one case with its linked entity summaries.
//
// GET /api/cases/{id}
// #case-detail-handler
func (h *EntityHandler) HandleGetCase(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		entityWriteError(w, http.StatusBadRequest, "case id is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	detail, err := h.repo.GetCaseDetail(ctx, id)
	if err != nil {
		entityWriteError(w, http.StatusNotFound, "case not found: "+err.Error())
		return
	}

	userCtx := middleware.GetUserContext(r.Context())
	if userCtx != nil {
		go h.repo.LogAccess(context.Background(), database.AccessLogEntry{
			UserID:    userCtx.UserID,
			Username:  userCtx.Username,
			Action:    "view_case",
			CaseID:    id,
			IPAddress: r.RemoteAddr,
			UserAgent: r.UserAgent(),
		})
	}

	entityWriteJSON(w, http.StatusOK, detail)
}

// HandleGetCaseEntities returns the full profiles for every entity in a case.
//
// GET /api/cases/{id}/entities
// #case-entities-handler: may be slow for large cases — 15 s timeout.
func (h *EntityHandler) HandleGetCaseEntities(w http.ResponseWriter, r *http.Request) {
	caseID := r.PathValue("id")
	if caseID == "" {
		entityWriteError(w, http.StatusBadRequest, "case id is required")
		return
	}

	userCtx := middleware.GetUserContext(r.Context())
	maskPII := !hasScope(userCtx, "unmask")

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	// First get the case detail to know which entities are linked.
	detail, err := h.repo.GetCaseDetail(ctx, caseID)
	if err != nil {
		entityWriteError(w, http.StatusNotFound, "case not found: "+err.Error())
		return
	}

	// Fetch full profile for each entity concurrently.
	type profileResult struct {
		profile *database.EntityProfile
		role    string
		err     error
	}
	ch := make(chan profileResult, len(detail.Entities))

	for _, ew := range detail.Entities {
		ew := ew
		go func() {
			p, e := h.repo.GetEntityProfile(ctx, ew.EntityID, maskPII)
			ch <- profileResult{profile: p, role: ew.Role, err: e}
		}()
	}

	var profiles []map[string]any
	for range detail.Entities {
		res := <-ch
		if res.err != nil || res.profile == nil {
			continue
		}
		profiles = append(profiles, map[string]any{
			"role":    res.role,
			"profile": res.profile,
		})
	}

	entityWriteJSON(w, http.StatusOK, map[string]any{
		"case":     detail.Case,
		"entities": profiles,
		"count":    len(profiles),
	})
}

// ─── Work Sessions ────────────────────────────────────────────────────────────

// HandleStartWorkSession creates a new open work session.
//
// POST /api/work-sessions
// Body: { "description":"...", "entity_id":"...", "case_id":"..." }
// #work-session-start
func (h *EntityHandler) HandleStartWorkSession(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.GetUserContext(r.Context())
	if userCtx == nil {
		entityWriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var body struct {
		Description string `json:"description"`
		EntityID    string `json:"entity_id"`
		CaseID      string `json:"case_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body = struct {
			Description string `json:"description"`
			EntityID    string `json:"entity_id"`
			CaseID      string `json:"case_id"`
		}{}
	}

	ws := database.WorkSession{
		UserID:      userCtx.UserID,
		Username:    userCtx.Username,
		Description: body.Description,
	}
	if body.EntityID != "" {
		ws.EntityID = &body.EntityID
	}
	if body.CaseID != "" {
		ws.CaseID = &body.CaseID
	}

	id, err := h.repo.StartWorkSession(r.Context(), ws)
	if err != nil {
		entityWriteError(w, http.StatusInternalServerError, "failed to start session: "+err.Error())
		return
	}

	entityWriteJSON(w, http.StatusCreated, map[string]any{
		"session_id": id,
		"started_at": time.Now().UTC(),
		"message":    "work session started",
	})
}

// HandleEndWorkSession marks an open session as ended.
//
// PATCH /api/work-sessions/{id}/end
// #work-session-end
func (h *EntityHandler) HandleEndWorkSession(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.GetUserContext(r.Context())
	if userCtx == nil {
		entityWriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		entityWriteError(w, http.StatusBadRequest, "session id is required")
		return
	}

	userID := userCtx.UserID
	if err := h.repo.EndWorkSession(r.Context(), sessionID, userID); err != nil {
		entityWriteError(w, http.StatusInternalServerError, "failed to end session: "+err.Error())
		return
	}

	entityWriteJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"ended_at":   time.Now().UTC(),
		"message":    "work session ended",
	})
}

// HandleListWorkSessions returns recent work sessions for the authenticated user.
//
// GET /api/work-sessions?limit=20
// #work-session-list
func (h *EntityHandler) HandleListWorkSessions(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.GetUserContext(r.Context())
	if userCtx == nil {
		entityWriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	limit := entityParseInt(r.URL.Query().Get("limit"), 20)
	userID := userCtx.UserID

	sessions, err := h.repo.ListWorkSessions(r.Context(), userID, limit)
	if err != nil {
		entityWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	entityWriteJSON(w, http.StatusOK, map[string]any{
		"sessions": sessions,
		"count":    len(sessions),
	})
}

// ─── Package-level helpers ────────────────────────────────────────────────────

func entityWriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func entityWriteError(w http.ResponseWriter, code int, msg string) {
	entityWriteJSON(w, code, map[string]string{"error": msg})
}

func entityParseInt(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil && v > 0 {
		return v
	}
	return def
}

// hasScope returns true if the user context contains the given scope string.
// #rbac: used to decide whether PII should be unmasked.
func hasScope(uc *middleware.UserContext, scope string) bool {
	if uc == nil {
		return false
	}
	for _, s := range uc.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}
