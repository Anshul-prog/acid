package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"acid/internal/middleware"
	"acid/internal/models"
)

type CategoryHandler struct {
	db *sql.DB
}

func NewCategoryHandler(db *sql.DB) *CategoryHandler {
	return &CategoryHandler{db: db}
}

// =============================================================================
// CATEGORY CRUD OPERATIONS
// =============================================================================

// List categories - GET /api/categories
func (h *CategoryHandler) ListCategories(w http.ResponseWriter, r *http.Request) {
	entityType := r.URL.Query()..Get("entity_type")
	
	query := `
		SELECT id, name, description, color, entity_type, icon, created_at, updated_at, created_by, is_active
		FROM categories
		WHERE is_active = true
	`
	args := []interface{}{}
	
	if entityType != "" {
		query += " AND entity_type = $1"
		args = append(args, entityType)
	}
	query += " ORDER BY name"
	
	rows, err := h.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	
	var categories []map[string]interface{}
	for rows.Next() {
		var cat models.Category
		if err := rows.Scan(&cat.ID, &cat.Name, &cat.Description, &cat.Color, &cat.EntityType, &cat.Icon, &cat.CreatedAt, &cat.UpdatedAt, &cat.CreatedBy, &cat.IsActive); err != nil {
			continue
		}
		categories = append(categories, map[string]interface{}{
			"id":          cat.ID,
			"name":       cat.Name,
			"description": cat.Description,
			"color":      cat.Color,
			"entity_type": cat.EntityType,
			"icon":       cat.Icon,
			"created_at": cat.CreatedAt,
			"updated_at": cat.UpdatedAt,
		})
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"categories": categories,
		"count": len(categories),
	})
}

// Get single category - GET /api/categories/{id}
func (h *CategoryHandler) GetCategory(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path)
	
	var cat models.Category
	err := h.db.QueryRowContext(r.Context(), `
		SELECT id, name, description, color, entity_type, icon, created_at, updated_at, created_by, is_active
		FROM categories WHERE id = $1
	`, id).Scan(&cat.ID, &cat.Name, &cat.Description, &cat.Color, &cat.EntityType, &cat.Icon, &cat.CreatedAt, &cat.UpdatedAt, &cat.CreatedBy, &cat.IsActive)
	
	if err == sql.ErrNoRows {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           cat.ID,
		"name":        cat.Name,
		"description": cat.Description,
		"color":       cat.Color,
		"entity_type": cat.EntityType,
		"icon":        cat.Icon,
	})
}

// Create category - POST /api/categories
func (h *CategoryHandler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Color      string `json:"color"`
		EntityType string `json:"entity_type"`
		Icon      string `json:"icon"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	if req.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}
	
	if req.EntityType == "" {
		req.EntityType = "employee" // Default
	}
	
	var newID int
	err := h.db.QueryRowContext(r.Context(), `
		INSERT INTO categories (name, description, color, entity_type, icon, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING id
	`, req.Name, req.Description, req.Color, req.EntityType, req.Icon).Scan(&newID)
	
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      newID,
		"name":   req.Name,
		"status": "created",
	})
}

// Update category - PUT /api/categories/{id}
func (h *CategoryHandler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path)
	
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Color      string `json:"color"`
		EntityType string `json:"entity_type"`
		Icon      string `json:"icon"`
		IsActive   *bool  `json:"is_active"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	// Build dynamic update query
	query := "UPDATE categories SET updated_at = NOW()"
	args := []interface{}{}
	argNum := 1
	
	if req.Name != "" {
		query += fmt.Sprintf(", name = $%d", argNum)
		args = append(args, req.Name)
		argNum++
	}
	if req.Description != "" {
		query += fmt.Sprintf(", description = $%d", argNum)
		args = append(args, req.Description)
		argNum++
	}
	if req.Color != "" {
		query += fmt.Sprintf(", color = $%d", argNum)
		args = append(args, req.Color)
		argNum++
	}
	if req.EntityType != "" {
		query += fmt.Sprintf(", entity_type = $%d", argNum)
		args = append(args, req.EntityType)
		argNum++
	}
	if req.Icon != "" {
		query += fmt.Sprintf(", icon = $%d", argNum)
		args = append(args, req.Icon)
		argNum++
	}
	if req.IsActive != nil {
		query += fmt.Sprintf(", is_active = $%d", argNum)
		args = append(args, *req.IsActive)
		argNum++
	}
	
	query += fmt.Sprintf(" WHERE id = $%d", argNum)
	args = append(args, id)
	
	result, err := h.db.ExecContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     id,
		"status": "updated",
	})
}

// Delete category - DELETE /api/categories/{id}
func (h *CategoryHandler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path)
	
	// Soft delete - set is_active = false
	result, err := h.db.ExecContext(r.Context(), 
		"UPDATE categories SET is_active = false, updated_at = NOW() WHERE id = $1", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     id,
		"status": "deleted",
	})
}

// =============================================================================
// ENTITY-CATEGORY ASSIGNMENTS
// =============================================================================

// Assign category to entity - POST /api/categories/assign
func (h *CategoryHandler) AssignCategory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntityType string `json:"entity_type"`
		EntityID  int    `json:"entity_id"`
		CategoryID int  `json:"category_id"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	if req.EntityType == "" || req.EntityID == 0 || req.CategoryID == 0 {
		http.Error(w, "entity_type, entity_id, and category_id are required", http.StatusBadRequest)
		return
	}
	
	_, err := h.db.ExecContext(r.Context(), `
		INSERT INTO entity_categories (entity_type, entity_id, category_id, assigned_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (entity_type, entity_id, category_id) DO NOTHING
	`, req.EntityType, req.EntityID, req.CategoryID)
	
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "assigned",
	})
}

// Remove category from entity - POST /api/categories/unassign
func (h *CategoryHandler) UnassignCategory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntityType string `json:"entity_type"`
		EntityID  int    `json:"entity_id"`
		CategoryID int  `json:"category_id"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	_, err := h.db.ExecContext(r.Context(), `
		DELETE FROM entity_categories 
		WHERE entity_type = $1 AND entity_id = $2 AND category_id = $3
	`, req.EntityType, req.EntityID, req.CategoryID)
	
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "unassigned",
	})
}

// Get categories for an entity - GET /api/categories/entity/{entity_type}/{entity_id}
func (h *CategoryHandler) GetEntityCategories(w http.ResponseWriter, r *http.Request) {
	entityType, entityID := getEntityInfoFromPath(r.URL.Path)
	
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT c.id, c.name, c.description, c.color, c.icon, ec.assigned_at
		FROM entity_categories ec
		JOIN categories c ON c.id = ec.category_id
		WHERE ec.entity_type = $1 AND ec.entity_id = $2 AND c.is_active = true
		ORDER BY c.name
	`, entityType, entityID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	
	var categories []map[string]interface{}
	for rows.Next() {
		var catID int
		var name, description, color, icon string
		var assignedAt time.Time
		if err := rows.Scan(&catID, &name, &description, &color, &icon, &assignedAt); err != nil {
			continue
		}
		categories = append(categories, map[string]interface{}{
			"id":          catID,
			"name":       name,
			"description": description,
			"color":      color,
			"icon":       icon,
			"assigned_at": assignedAt,
		})
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"categories": categories,
		"count": len(categories),
	})
}

// Get all entities with a category - GET /api/categories/{category_id}/entities
func (h *CategoryHandler) GetCategoryEntities(w http.ResponseWriter, r *http.Request) {
	categoryID := extractID(r.URL.Path)
	entityType := r.URL.Query().Get("entity_type")
	
	query := `
		SELECT ec.entity_id, ec.entity_type, ec.assigned_at, u.username, u.email
		FROM entity_categories ec
		LEFT JOIN users u ON ec.entity_type = 'user' AND u.id = ec.entity_id
		WHERE ec.category_id = $1
	`
	args := []interface{}{categoryID}
	
	if entityType != "" {
		query += " AND ec.entity_type = $2"
		args = append(args, entityType)
	}
	query += " ORDER BY ec.assigned_at DESC"
	
	rows, err := h.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	
	var entities []map[string]interface{}
	for rows.Next() {
		var entityID int
		var entityType string
		var assignedAt time.Time
		var username, email sql.NullString
		if err := rows.Scan(&entityID, &entityType, &assignedAt, &username, &email); err != nil {
			continue
		}
		entities = append(entities, map[string]interface{}{
			"entity_id":   entityID,
			"entity_type": entityType,
			"assigned_at": assignedAt,
			"username":   username.String,
			"email":      email.String,
		})
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"entities": entities,
		"count": len(entities),
	})
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

// extractID extracts numeric ID from URL path like /api/categories/123
func extractID(path string) int {
	// Find the last numeric part of the path
	var id int
	n, _ := fmt.Sscanf(path, "%d", &id)
	if n == 0 {
		// Try parsing from end
		for i := len(path) - 1; i >= 0; i-- {
			if path[i] >= '0' && path[i] <= '9' {
				start := i
				for start > 0 && path[start-1] >= '0' && path[start-1] <= '9' {
					start--
				}
				strconv.ParseInt(path[start:i+1], 10, 32)
				id, _ = strconv.Atoi(path[start : i+1])
				break
			}
		}
	}
	return id
}

// Extract entity_type and entity_id from path like /api/categories/entity/user/123
func getEntityInfoFromPath(path string) (string, int) {
	// Simple parsing - split by /
	var parts []string
	var current string
	for _, c := range path {
		if c == '/' {
			if current != "" {
				parts = append(parts, current)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	
	// Get last two parts
	if len(parts) >= 2 {
		entityID, _ := strconv.Atoi(parts[len(parts)-1])
		return parts[len(parts)-2], entityID
	}
	return "", 0
}