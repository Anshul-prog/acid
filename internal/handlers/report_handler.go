package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"acid/internal/cache"
	chpkg "acid/internal/clickhouse"
	"acid/internal/database"
	"acid/internal/schema"
)

type ReportHandler struct {
	repo        *database.DynamicRepository
	registry    *schema.SchemaRegistry
	cache       *cache.MultiLayerCache
	chSearch    *chpkg.SearchRepository
	multiDB     *database.MultiDBManager
	maxPageSize int
	defaultSize int
	timeout     time.Duration
}

func NewReportHandler(repo *database.DynamicRepository, registry *schema.SchemaRegistry, multiDB *database.MultiDBManager) *ReportHandler {
	return &ReportHandler{
		repo:        repo,
		registry:    registry,
		cache:       nil,
		chSearch:    nil,
		multiDB:     multiDB,
		maxPageSize: 1000,
		defaultSize: 20,
		timeout:     30 * time.Second,
	}
}

func (h *ReportHandler) GenerateReport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	reportFormat := r.URL.Query().Get("format")
	if reportFormat == "" {
		reportFormat = "csv"
	}

	if reportFormat != "csv" && reportFormat != "json" && reportFormat != "pdf" {
		h.writeError(w, http.StatusBadRequest, "Invalid format. Use csv, json, or pdf")
		return
	}

	dbName := r.URL.Query().Get("db")
	tableName := r.URL.Query().Get("table")
	searchTerm := r.URL.Query().Get("q")
	limit := 1000
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 10000 {
			limit = parsed
		}
	}

	filename := fmt.Sprintf("report_%s_%s", dbName, time.Now().Format("20060102_150405"))

	if dbName != "" && tableName != "" {
		if !h.registry.TableExists(tableName) {
			h.writeError(w, http.StatusNotFound, "Table not found")
			return
		}

		data, err := h.fetchTableData(ctx, dbName, tableName, searchTerm, limit)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, "Failed to fetch data: "+err.Error())
			return
		}

		switch reportFormat {
		case "csv":
			h.exportCSV(w, filename, data)
		case "json":
			h.exportJSON(w, filename, data)
		case "pdf":
			h.exportPDF(w, filename, data)
		}
		return
	}

	allData := h.fetchAllDatabasesReport(ctx, limit)

	switch reportFormat {
	case "csv":
		h.exportCSV(w, filename+"_all", allData)
	case "json":
		h.exportJSON(w, filename+"_all", allData)
	case "pdf":
		h.exportPDF(w, filename+"_all", allData)
	}
}

func (h *ReportHandler) fetchTableData(ctx context.Context, dbName, tableName, searchTerm string, limit int) ([]map[string]interface{}, error) {
	queryParams := schema.QueryParams{
		TableName: tableName,
		Limit:     limit,
		Filters:   make(map[string]string),
	}

	if searchTerm != "" {
		queryParams.Filters["email"] = searchTerm
	}

	result, err := h.repo.GetRecords(ctx, queryParams)
	if err != nil {
		return nil, err
	}

	enrichedData := make([]map[string]interface{}, len(result.Data))
	for i, record := range result.Data {
		enriched := make(map[string]interface{})

		for k, v := range record {
			enriched[k] = v
		}

		if email, ok := record["email"].(string); ok {
			dupResults := h.searchDuplicates(ctx, email)
			if len(dupResults) > 0 {
				enriched["_cross_refs"] = dupResults
				enriched["_has_duplicates"] = true
			}
		}

		enriched["_report_metadata"] = map[string]interface{}{
			"generated_at": time.Now(),
			"source_db":    dbName,
			"source_table": tableName,
		}

		enrichedData[i] = enriched
	}

	return enrichedData, nil
}

func (h *ReportHandler) searchDuplicates(ctx context.Context, email string) []map[string]interface{} {
	var duplicates []map[string]interface{}

	if h.multiDB == nil {
		return duplicates
	}

	results, err := h.multiDB.FindEntityAcrossDBs(ctx, email, "email")
	if err != nil {
		return duplicates
	}

	for _, r := range results {
		if strings.Contains(r.Status, "found_") {
			duplicates = append(duplicates, map[string]interface{}{
				"database": r.Name,
				"ref_type": "email_match",
				"status":   r.Status,
			})
		}
	}

	return duplicates
}

func (h *ReportHandler) fetchAllDatabasesReport(ctx context.Context, limit int) []map[string]interface{} {
	var allData []map[string]interface{}

	tables := h.registry.GetAllTables()
	for _, table := range tables {
		if len(allData) >= limit {
			break
		}

		params := schema.QueryParams{
			TableName: table.Name,
			Limit:     10,
		}

		result, err := h.repo.GetRecords(ctx, params)
		if err != nil {
			continue
		}

		for _, record := range result.Data {
			record["_source_table"] = table.Name
			record["_report_metadata"] = map[string]interface{}{
				"generated_at": time.Now(),
			}
			allData = append(allData, record)
		}
	}

	return allData
}

func (h *ReportHandler) exportCSV(w http.ResponseWriter, filename string, data []map[string]interface{}) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.csv", filename))

	writer := csv.NewWriter(w)
	defer writer.Flush()

	if len(data) == 0 {
		writer.Write([]string{"no_data"})
		return
	}

	var headers []string
	for k := range data[0] {
		if k == "_report_metadata" || k == "_cross_refs" {
			continue
		}
		headers = append(headers, k)
	}

	headers = append(headers, "has_duplicates")
	if refs, ok := data[0]["_cross_refs"].([]map[string]interface{}); ok && len(refs) > 0 {
		for _, ref := range refs {
			headers = append(headers, fmt.Sprintf("ref_%s", ref["database"]))
		}
	}

	writer.Write(headers)

	for _, row := range data {
		var record []string
		for _, h := range headers {
			if h == "has_duplicates" {
				if dup, ok := row["_has_duplicates"].(bool); ok && dup {
					record = append(record, "true")
				} else {
					record = append(record, "false")
				}
				continue
			}

			if strings.HasPrefix(h, "ref_") {
				record = append(record, "duplicate")
				continue
			}

			if v, ok := row[h]; ok {
				switch val := v.(type) {
				case string:
					record = append(record, val)
				case nil:
					record = append(record, "")
				default:
					record = append(record, fmt.Sprintf("%v", val))
				}
			} else {
				record = append(record, "")
			}
		}
		writer.Write(record)
	}

	log.Printf("📄 CSV Report generated: %s.csv (%d records)", filename, len(data))
}

func (h *ReportHandler) exportJSON(w http.ResponseWriter, filename string, data []map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.json", filename))

	response := map[string]interface{}{
		"generated_at": time.Now().Format(time.RFC3339),
		"record_count": len(data),
		"format":       "json",
		"records":      data,
	}

	json.NewEncoder(w).Encode(response)
	log.Printf("📄 JSON Report generated: %s.json (%d records)", filename, len(data))
}

func (h *ReportHandler) exportPDF(w http.ResponseWriter, filename string, data []map[string]interface{}) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.txt", filename))

	fmt.Fprintf(w, "╔════════════════════════════════════════════════════════════════════════════╗\n")
	fmt.Fprintf(w, "║                    L.S.D GENERATED REPORT                         ║\n")
	fmt.Fprintf(w, "╚════════════════════════════════════════════════════════════════════════════╝\n\n")
	fmt.Fprintf(w, "Generated: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "Total Records: %d\n\n", len(data))
	fmt.Fprintf(w, "────────────────────────────────────────────────────────────────────\n\n")

	if len(data) == 0 {
		fmt.Fprintf(w, "No data available for the report.\n")
		return
	}

	for i, row := range data {
		if i >= 100 {
			fmt.Fprintf(w, "\n... and %d more records\n", len(data)-100)
			break
		}

		if table, ok := row["_source_table"].(string); ok {
			fmt.Fprintf(w, "[Table: %s]\n", table)
		}

		email := ""
		firstName := ""
		lastName := ""
		status := ""
		country := ""
		city := ""

		if v, ok := row["email"].(string); ok {
			email = v
		}
		if v, ok := row["first_name"].(string); ok {
			firstName = v
		}
		if v, ok := row["last_name"].(string); ok {
			lastName = v
		}
		if v, ok := row["status"].(string); ok {
			status = v
		}
		if v, ok := row["country"].(string); ok {
			country = v
		}
		if v, ok := row["city"].(string); ok {
			city = v
		}

		fmt.Fprintf(w, "  #%d | %s %s <%s>\n", i+1, firstName, lastName, email)
		fmt.Fprintf(w, "      Status: %s | Country: %s | City: %s\n", status, country, city)

		if dup, ok := row["_has_duplicates"].(bool); ok && dup {
			fmt.Fprintf(w, "      ⚠️  DUPLICATE: Found in multiple databases\n")
		}

		fmt.Fprintf(w, "\n")
	}

	fmt.Fprintf(w, "────────────────────────────────────────────────────────────────────\n")
	fmt.Fprintf(w, "End of Report\n")

	log.Printf("📄 Text Report generated: %s.txt (%d records)", filename, len(data))
}

func (h *ReportHandler) ListDatabases(w http.ResponseWriter, r *http.Request) {
	if h.multiDB == nil {
		h.writeError(w, http.StatusServiceUnavailable, "Multi-DB manager not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	meta, err := h.multiDB.GetDatabaseMeta(ctx)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to get database meta")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"databases": meta,
		"count":     len(meta),
	})
}

func (h *ReportHandler) GetCrossRef(w http.ResponseWriter, r *http.Request) {
	entityValue := r.URL.Query().Get("q")
	if entityValue == "" {
		h.writeError(w, http.StatusBadRequest, "Query parameter 'q' is required")
		return
	}

	columnName := r.URL.Query().Get("column")
	if columnName == "" {
		columnName = "email"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if h.multiDB == nil {
		h.writeError(w, http.StatusServiceUnavailable, "Multi-DB manager not initialized")
		return
	}

	results, err := h.multiDB.FindEntityAcrossDBs(ctx, entityValue, columnName)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Search failed: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"query":          entityValue,
		"column":         columnName,
		"matches":        results,
		"total_matches":  len(results),
		"has_duplicates": len(results) > 1,
	})
}

const ReportNumDatabases = 10
const ReportNumTablesPerDB = 1000
const ReportUsersPerTable = 50

func (h *ReportHandler) GenerateSystemReport(w http.ResponseWriter, r *http.Request) {
	reportFormat := r.URL.Query().Get("format")
	if reportFormat == "" {
		reportFormat = "csv"
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"generated_at": time.Now().Format(time.RFC3339),
		"report_type":  "system_full",
		"format":       reportFormat,
		"status":       "ready",
		"summary": map[string]interface{}{
			"total_databases": ReportNumDatabases,
			"total_tables":    ReportNumDatabases * ReportNumTablesPerDB,
			"total_users":     ReportNumDatabases * ReportNumTablesPerDB * ReportUsersPerTable,
		},
	})
}

func (h *ReportHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *ReportHandler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{"error": message})
}
