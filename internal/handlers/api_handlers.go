package handlers

import (
	"encoding/json"
	"net/http"
)

type APIHandler struct{}

func NewAPIHandler() *APIHandler {
	return &APIHandler{}
}

func (h *APIHandler) GetAPIInfo(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"name":    "Dynamic Database API",
		"version": "2.0",
		"endpoints": []string{
			"GET /health",
			"GET /api/health",
			"GET /api/info",
			"POST /api/auth/register",
			"POST /api/auth/login",
			"POST /api/auth/logout",
			"GET /api/auth/me",
			"GET /api/auth/api-keys",
			"POST /api/auth/api-keys",
			"DELETE /api/auth/api-keys/{id}",
			"GET /api/tables",
			"GET /api/tables/{table}/schema",
			"GET /api/tables/{table}/records",
			"GET /api/tables/{table}/records/{pk}",
			"GET /api/tables/{table}/search",
			"GET /api/tables/{table}/stats",
			"GET /api/search",
			"GET /api/search/",
			"GET /api/cdc/status",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(info)
}
