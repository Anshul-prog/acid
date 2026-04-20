// =============================================================================
// ACID - Advanced Database Interface System
// =============================================================================
// Main entry point for the ACID API Server
//
// This file:
// 1. Loads configuration from .env file
// 2. Connects to PostgreSQL database
// 3. Sets up Redis caching
// 4. Discovers database tables automatically
// 5. Connects to ClickHouse for fast search
// 6. Initializes CDC data sync pipeline
// 7. Sets up authentication with JWT
// 8. Creates all API routes/endpoints
// 9. Starts the web server
//
// DO NOT MODIFY unless adding new features!
// =============================================================================
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	// OUR INTERNAL PACKAGES
	"acid/internal/auth"       // JWT authentication
	"acid/internal/cache"      // Redis caching layer
	"acid/internal/clickhouse" // ClickHouse search engine
	"acid/internal/config"     // Configuration loading
	"acid/internal/database"   // Database connections
	"acid/internal/dbsearch"   // Entity search intelligence
	"acid/internal/handlers"   // HTTP request handlers
	"acid/internal/middleware" // Security & rate limiting
	"acid/internal/pipeline"   // Data processing
	"acid/internal/schema"     // Schema discovery
	"acid/internal/watcher"    // Drop & Sync file watcher

	// EXTERNAL PACKAGES
	"github.com/jackc/pgx/v5/pgxpool"
	asciiart "github.com/romance-dev/ascii-art"
	_ "github.com/romance-dev/ascii-art/fonts"
)

func main() {
	// ============================================================================
	// STEP 1: LOAD CONFIGURATION
	// ============================================================================
	cfg := config.LoadConfig()
	ctx := context.Background()

	// Display startup banner
	asciiart.NewFigure("ACID", "isometric1", true).Print()
	log.Printf("🚀 ACID API Server Starting...")
	log.Println("══════════════════════════════════════════════════════════════════")

	// ============================================================================
	// STEP 2: CONNECT TO POSTGRESQL
	// ============================================================================
	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()
	log.Println("✅ Database connected")

	// ============================================================================
	// STEP 3: SET UP REDIS CACHING
	// ============================================================================
	redisCache := cache.NewRedisCache(
		cfg.RedisAddr,
		cfg.RedisPassword,
		cfg.RedisDB,
		5*time.Minute,
	)
	multiCache := cache.NewMultiLayerCache(redisCache, 30*time.Second)
	log.Println("✅ Multi-layer cache initialized")

	// ============================================================================
	// STEP 4: AUTO-DISCOVER DATABASE SCHEMA
	// ============================================================================
	registry := schema.NewSchemaRegistry(pool.Pool)
	if err := registry.LoadSchema(ctx); err != nil {
		log.Fatalf("Failed to load schema: %v", err)
	}
	log.Printf("✅ Schema loaded: %d tables discovered", len(registry.GetAllTables()))

	// ============================================================================
	// STEP 4b: ENSURE CATEGORY SYSTEM TABLES EXIST
	// ============================================================================
	_, err = pool.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS categories (
			id SERIAL PRIMARY KEY,
			name VARCHAR(100) NOT NULL UNIQUE,
			description TEXT,
			color VARCHAR(20) DEFAULT '#3b82f6',
			entity_type VARCHAR(50) NOT NULL DEFAULT 'employee',
			icon VARCHAR(50),
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			created_by INTEGER REFERENCES users(id),
			is_active BOOLEAN DEFAULT true
		);
		CREATE TABLE IF NOT EXISTS entity_categories (
			id SERIAL PRIMARY KEY,
			entity_type VARCHAR(50) NOT NULL,
			entity_id INTEGER NOT NULL,
			category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
			assigned_at TIMESTAMP DEFAULT NOW(),
			assigned_by INTEGER REFERENCES users(id),
			UNIQUE(entity_type, entity_id, category_id)
		);
	`)
	if err != nil {
		log.Printf("⚠️  Category tables creation warning: %v", err)
	} else {
		log.Println("✅ Category system tables ensured")
	}

	// ============================================================================
	// STEP 5: CONNECT TO CLICKHOUSE (SEARCH ENGINE)
	// ============================================================================
	chPool, err := clickhouse.NewConnectionPool(clickhouse.Config{
		Addr:     cfg.ClickHouseAddr,
		Database: cfg.ClickHouseDB,
		Username: cfg.ClickHouseUser,
		Password: cfg.ClickHousePassword,
	}, 5)

	if err != nil {
		log.Printf("⚠️  ClickHouse pool creation failed: %v", err)
	}

	var chSearch *clickhouse.SearchRepository
	if chPool != nil && chPool.IsAvailable() {
		chSearch = clickhouse.NewSearchRepository(chPool, registry)
		log.Println("✅ ClickHouse search repository initialized (5 connections)")
	} else {
		log.Println("⚠️  ClickHouse not available, search uses PostgreSQL")
	}

	// ============================================================================
	// STEP 6: CREATE DYNAMIC REQUEST HANDLER
	// ============================================================================
	dynamicRepo := database.NewDynamicRepository(pool.Pool, registry)
	dynamicHandler := handlers.NewDynamicHandler(
		dynamicRepo,
		registry,
		multiCache,
		chSearch,
		50, 20,
		120*time.Second,
	)

	// ============================================================================
	// STEP 7: SET UP CDC (CHANGE DATA CAPTURE)
	// ============================================================================
	// ── Run database migrations on startup ────────────────────────────────────
	if err := runMigrationsFromDir(ctx, pool.Pool, "./databases/migrations"); err != nil {
		log.Printf("⚠️  Startup migrations warning: %v", err)
	} else {
		log.Println("✅ Startup migrations applied")
	}

	var cdcManager *clickhouse.CDCManager
	if chPool != nil && chPool.IsAvailable() && cfg.EnableCDC {
		cdcConfig := clickhouse.CDCConfig{
			BatchSize:       10000,
			SyncInterval:    30 * time.Second,
			ParallelWorkers: 5,
			ChunkSize:       100000,
		}
		cdcManager = clickhouse.NewCDCManager(pool.Pool, chSearch, registry, cdcConfig)
		dynamicHandler.SetCDCManager(cdcManager)
		cdcManager.Start()
		log.Println("✅ CDC Manager started with auto-discovery")
	}

	// ============================================================================
	// STEP 8: SET UP DATA PIPELINE PROCESSOR
	// ============================================================================
	pipelineProcessor := pipeline.NewPipelineProcessor(pool.Pool, "./ErrorFiles")

	if cdcManager != nil {
		pipelineProcessor.SetCDCTrigger(func(tableName string) error {
			log.Printf("🔄 Pipeline completed for table: %s, triggering CDC sync...", tableName)
			if err := cdcManager.TriggerTableSync(tableName); err != nil {
				log.Printf("⚠️  CDC sync failed for %s: %v", tableName, err)
				return err
			}
			log.Printf("✅ CDC sync completed for table: %s", tableName)
			return nil
		})
		log.Println("✅ Pipeline-to-CDC integration enabled")
	}

	pipelineHandler := handlers.NewPipelineHandler(pipelineProcessor)

	// ============================================================================
	// STEP 8b: START DROP & SYNC FILE WATCHER
	// ============================================================================
	{
		var cdcTrigger watcher.CDCTriggerFunc
		if cdcManager != nil {
			cdcTrigger = func(tableName string) error {
				return cdcManager.TriggerTableSync(tableName)
			}
		}
		dropSync, err := watcher.NewDropSyncWatcher(watcher.Config{
			WatchDir:   "./databases/incoming",
			ArchiveDir: "./databases/archive",
			FailedDir:  "./databases/incoming/failed",
			PGPool:     pool.Pool,
			Registry:   registry,
			CDCTrigger: cdcTrigger,
		})
		if err != nil {
			log.Printf("⚠️  Drop & Sync watcher init failed: %v", err)
		} else {
			go dropSync.Run(ctx)
			log.Println("✅ Drop & Sync watcher started (watching databases/incoming/)")
		}
	}

	// ============================================================================
	// STEP 9: SET UP DB-SEARCH (ENTITY INTELLIGENCE)
	// ============================================================================
	entityRepo := database.NewEntityRepository(pool.Pool)
	apiHandler := handlers.NewAPIHandler()

	var adminSearchHandler *handlers.AdminSearchHandler
	var entityHandler *handlers.EntityHandler

	if cfg.EnableDBSearch {
		pools := map[string]*pgxpool.Pool{"default": pool.Pool}
		searchSvc, err := dbsearch.NewSearchService(ctx, pools)
		if err != nil {
			log.Printf("⚠️  DB-search metadata load failed: %v (search disabled)", err)
		} else {
			log.Printf("✅ DB-search ready — %d data sources", len(searchSvc.DataSourceIDs()))
			adminSearchHandler = handlers.NewAdminSearchHandler(searchSvc, entityRepo)
			entityHandler = handlers.NewEntityHandler(entityRepo, searchSvc)

			go func() {
				ticker := time.NewTicker(10 * time.Minute)
				defer ticker.Stop()
				for range ticker.C {
					if err := searchSvc.Refresh(context.Background()); err != nil {
						log.Printf("⚠️  Schema refresh error: %v", err)
					} else {
						log.Println("🔄 Schema metadata refreshed")
					}
				}
			}()
		}
	} else {
		log.Println("ℹ️  DB-search disabled")
	}

	// ============================================================================
	// STEP 10: SET UP AUTHENTICATION (JWT)
	// ============================================================================
	jwtSecret := cfg.JWTSecret
	if jwtSecret == "" {
		jwtSecret = "acid-jwt-secret-key-change-in-production"
	}

	authService := auth.NewAuthService(jwtSecret)
	authHandler := handlers.NewAuthHandler(pool.Pool, authService)
	authMiddleware := middleware.NewAuthMiddleware(authService, pool.Pool)

	// ============================================================================
	// STEP 11: CREATE HTTP ROUTER
	// ============================================================================
	mux := http.NewServeMux()

	// ==========================
	// PUBLIC ROUTES
	// ==========================

	// Web Pages
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/index.html")
	})
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/login.html")
	})
	mux.HandleFunc("GET /register", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/register.html")
	})
	mux.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/dashboard.html")
	})
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/admin.html")
	})
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/docs.html")
	})
	mux.HandleFunc("GET /labs/hadoop-review", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/isolated/hadoop-review/index.html")
	})

	// Public API
	mux.HandleFunc("GET /api/info", apiHandler.GetAPIInfo)
	mux.HandleFunc("GET /api/private/nosql/hadoop-review", func(w http.ResponseWriter, r *http.Request) {
		payload, err := os.ReadFile("./databases/private_nosql/hadoop_review.json")
		if err != nil {
			http.Error(w, `{"error":"private json data unavailable"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(payload)
	})
	mux.HandleFunc("POST /api/auth/register", authHandler.Register)
	mux.HandleFunc("POST /api/auth/login", authHandler.Login)

	// Health Check (public for load balancers)
	mux.HandleFunc("GET /api/health", dynamicHandler.HealthCheck)
	mux.HandleFunc("GET /health", dynamicHandler.HealthCheck)

	// Static Files
	mux.HandleFunc("GET /swagger.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/swagger.yaml")
	})
	mux.HandleFunc("GET /style.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/style.css")
	})
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/app.js")
	})
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("./web/assets"))))
	mux.Handle("GET /labs/hadoop-review/static/", http.StripPrefix("/labs/hadoop-review/static/", http.FileServer(http.Dir("./web/isolated/hadoop-review"))))

	// ==========================
	// PROTECTED ROUTES (require authentication)
	// ==========================

	// Auth endpoints
	mux.HandleFunc("POST /api/auth/logout", authHandler.Logout)
	mux.Handle("GET /api/auth/me", authMiddleware.RequireAuth(http.HandlerFunc(authHandler.GetMe)))
	mux.Handle("GET /api/auth/api-keys", authMiddleware.RequireAuth(http.HandlerFunc(authHandler.ListAPIKeys)))
	mux.Handle("POST /api/auth/api-keys", authMiddleware.RequireAuth(http.HandlerFunc(authHandler.CreateAPIKey)))
	mux.Handle("DELETE /api/auth/api-keys/{id}", authMiddleware.RequireAuth(http.HandlerFunc(authHandler.RevokeAPIKey)))

	// Table endpoints
	mux.Handle("GET /api/tables", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.ListTables)))
	mux.Handle("GET /api/tables/{table}/schema", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.GetTableSchema)))
	mux.Handle("GET /api/tables/{table}/records", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.GetRecords)))
	mux.Handle("GET /api/tables/{table}/records/{pk}", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.GetRecordByPK)))
	mux.Handle("GET /api/tables/{table}/stats", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.GetTableStats)))
	mux.Handle("GET /api/tables/{table}/search", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.SearchRecords)))

	// Search endpoints
	mux.Handle("GET /api/search", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.SearchOptimized)))
	mux.Handle("GET /api/search/", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.SearchOptimized)))
	mux.Handle("GET /api/search/duplicates", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.SearchGlobalWithDuplicates)))

	// Pipeline endpoints
	mux.Handle("POST /api/pipeline/start", authMiddleware.RequireAuth(http.HandlerFunc(pipelineHandler.StartJob)))
	mux.Handle("GET /api/pipeline/jobs", authMiddleware.RequireAuth(http.HandlerFunc(pipelineHandler.ListJobs)))
	mux.Handle("GET /api/pipeline/jobs/{job_id}", authMiddleware.RequireAuth(http.HandlerFunc(pipelineHandler.GetJobStatus)))
	mux.Handle("GET /api/pipeline/jobs/{job_id}/stream", authMiddleware.RequireAuth(http.HandlerFunc(pipelineHandler.StreamJobProgress)))
	mux.Handle("GET /api/pipeline/jobs/{job_id}/logs", authMiddleware.RequireAuth(http.HandlerFunc(pipelineHandler.GetJobLogs)))

	// CDC Status
	mux.Handle("GET /api/cdc/status", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.GetCDCStatus)))

	// Report & Multi-DB endpoints
	multiDBManager := database.NewMultiDBManager()
	if err := multiDBManager.AddDatabase(ctx, "primary", cfg.DatabaseURL); err != nil {
		log.Printf("⚠️  Primary DB config warning: %v", err)
	}
	multiDBManager.SetPrimaryDB("primary")

	reportHandler := handlers.NewReportHandler(dynamicRepo, registry, multiDBManager)
	mux.Handle("GET /api/databases", authMiddleware.RequireAuth(http.HandlerFunc(reportHandler.ListDatabases)))
	mux.Handle("GET /api/reports", authMiddleware.RequireAuth(http.HandlerFunc(reportHandler.GenerateReport)))
	mux.Handle("GET /api/system-report", authMiddleware.RequireAuth(http.HandlerFunc(reportHandler.GenerateSystemReport)))
	mux.Handle("GET /api/crossref", authMiddleware.RequireAuth(http.HandlerFunc(reportHandler.GetCrossRef)))

	log.Println("✅ Multi-DB manager initialized")
	log.Println("📊 Report generation endpoints enabled")

	// ============================================================================
	// STEP 12: SET UP CATEGORY SYSTEM (MUST be before routes)
	// ============================================================================
	categoryHandler := handlers.NewCategoryHandler(pool.Pool)
	log.Println("✅ Category system initialized")

	// ============================================================================
	// CATEGORY MANAGEMENT SYSTEM
	// ============================================================================
	// Category CRUD - Create, read, update, delete categories (for tags/positions)
	mux.Handle("GET /api/categories", authMiddleware.RequireAuth(http.HandlerFunc(categoryHandler.ListCategories)))
	mux.Handle("GET /api/categories/{id}", authMiddleware.RequireAuth(http.HandlerFunc(categoryHandler.GetCategory)))
	mux.Handle("POST /api/categories", authMiddleware.RequireAuth(http.HandlerFunc(categoryHandler.CreateCategory)))
	mux.Handle("PUT /api/categories/{id}", authMiddleware.RequireAuth(http.HandlerFunc(categoryHandler.UpdateCategory)))
	mux.Handle("DELETE /api/categories/{id}", authMiddleware.RequireAuth(http.HandlerFunc(categoryHandler.DeleteCategory)))

	// Entity-Category assignments - Assign categories to entities
	mux.Handle("POST /api/categories/assign", authMiddleware.RequireAuth(http.HandlerFunc(categoryHandler.AssignCategory)))
	mux.Handle("POST /api/categories/unassign", authMiddleware.RequireAuth(http.HandlerFunc(categoryHandler.UnassignCategory)))
	mux.Handle("GET /api/categories/entity/{entity_type}/{entity_id}", authMiddleware.RequireAuth(http.HandlerFunc(categoryHandler.GetEntityCategories)))
	mux.Handle("GET /api/categories/{id}/entities", authMiddleware.RequireAuth(http.HandlerFunc(categoryHandler.GetCategoryEntities)))

	log.Println("🏷️ Category API routes registered")

	// ============================================================================
	// OMNI-SEARCH ENGINE (360-degree entity profiles)
	// ============================================================================
	omniSearchHandler := handlers.NewOmniSearchHandler(chSearch, pool.Pool)
	mux.Handle("GET /api/omni-search",
		authMiddleware.RequireAuth(http.HandlerFunc(omniSearchHandler.HandleOmniSearch)))
	log.Println("🔍 Omni-Search engine registered at /api/omni-search")
	mux.Handle("GET /api/smart-search", authMiddleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if entityHandler == nil {
			http.Error(w, `{"error":"search not enabled"}`, http.StatusNotImplemented)
			return
		}
		entityHandler.HandleSmartSearch(w, r)
	})))

	mux.Handle("GET /api/entities/{id}/profile", authMiddleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if entityHandler == nil {
			http.Error(w, `{"error":"not enabled"}`, http.StatusNotImplemented)
			return
		}
		entityHandler.HandleGetEntityProfile(w, r)
	})))

	mux.Handle("GET /api/entities/{id}/export", authMiddleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if entityHandler == nil {
			http.Error(w, `{"error":"not enabled"}`, http.StatusNotImplemented)
			return
		}
		entityHandler.HandleExportEntityProfile(w, r)
	})))

	// Cases
	mux.Handle("GET /api/cases", authMiddleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if entityHandler == nil {
			http.Error(w, `{"error":"not enabled"}`, http.StatusNotImplemented)
			return
		}
		entityHandler.HandleListCases(w, r)
	})))

	mux.Handle("GET /api/cases/{id}", authMiddleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if entityHandler == nil {
			http.Error(w, `{"error":"not enabled"}`, http.StatusNotImplemented)
			return
		}
		entityHandler.HandleGetCase(w, r)
	})))

	// Work sessions
	mux.Handle("POST /api/work-sessions", authMiddleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if entityHandler == nil {
			http.Error(w, `{"error":"not enabled"}`, http.StatusNotImplemented)
			return
		}
		entityHandler.HandleStartWorkSession(w, r)
	})))

	mux.Handle("GET /api/work-sessions", authMiddleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if entityHandler == nil {
			http.Error(w, `{"error":"not enabled"}`, http.StatusNotImplemented)
			return
		}
		entityHandler.HandleListWorkSessions(w, r)
	})))

	// Admin db-search
	mux.Handle("GET /api/admin/db-search", authMiddleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if adminSearchHandler == nil {
			http.Error(w, `{"error":"not enabled"}`, http.StatusNotImplemented)
			return
		}
		adminSearchHandler.HandleDBSearch(w, r)
	})))

	mux.Handle("GET /api/admin/db-search/sources", authMiddleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if adminSearchHandler == nil {
			http.Error(w, `{"error":"not enabled"}`, http.StatusNotImplemented)
			return
		}
		adminSearchHandler.HandleDBSearchSources(w, r)
	})))

	// ============================================================================
	// STEP 13: SET UP MIDDLEWARE CHAIN
	// ============================================================================
	handler := middleware.RateLimiter(mux)
	handler = middleware.CORS(handler)
	handler = middleware.Logger(handler)
	handler = middleware.AuditLogger(pool.Pool)(handler)
	handler = middleware.Recovery(handler)

	// ============================================================================
	// STEP 14: START SERVER
	// ============================================================================
	server := &http.Server{
		Addr:              fmt.Sprintf(":%s", cfg.Port),
		Handler:           handler,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	log.Printf("✅ Server running on http://localhost:%s", cfg.Port)
	log.Printf("📋 Admin Panel: http://localhost:%s/admin", cfg.Port)

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("══════════════════════════════════════════════════════════════════")
	log.Println("🛑 Shutting down server gracefully...")
	log.Println("══════════════════════════════════════════════════════════")

	if cdcManager != nil {
		log.Println("⏸️  Stopping CDC Manager...")
		cdcManager.Stop()
	}

	if chPool != nil {
		log.Println("🔌 Closing ClickHouse connection pool...")
		if err := chPool.Close(); err != nil {
			log.Printf("⚠️  Error closing ClickHouse pool: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("❌ Server forced to shutdown: %v", err)
	}

	log.Println("✅ Server exited properly")
}

// runMigrationsFromDir applies all *.sql files in migrationsDir in
// lexicographic order using the provided PostgreSQL pool. Each migration
// is recorded in migration_history so re-runs are idempotent.
func runMigrationsFromDir(ctx context.Context, pool *pgxpool.Pool, migrationsDir string) error {
	// Ensure tracking table
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS migration_history (
			filename    VARCHAR(255) PRIMARY KEY,
			applied_at  TIMESTAMPTZ DEFAULT NOW()
		)`)
	if err != nil {
		return fmt.Errorf("migration_history table: %w", err)
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no migrations dir yet — not an error
		}
		return fmt.Errorf("read migrations dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}

		// Check if already applied
		var applied int
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM migration_history WHERE filename = $1`, e.Name(),
		).Scan(&applied)
		if applied > 0 {
			continue
		}

		fPath := filepath.Join(migrationsDir, e.Name())
		raw, err := os.ReadFile(fPath)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", e.Name(), err)
		}

		log.Printf("[migrations] Applying %s...", e.Name())
		if _, err := pool.Exec(ctx, string(raw)); err != nil {
			return fmt.Errorf("apply migration %q: %w", e.Name(), err)
		}

		if _, err := pool.Exec(ctx,
			`INSERT INTO migration_history(filename) VALUES($1) ON CONFLICT DO NOTHING`, e.Name(),
		); err != nil {
			log.Printf("[migrations] ⚠️ Failed to record %q: %v", e.Name(), err)
		}
		log.Printf("[migrations] ✅ Applied: %s", e.Name())
	}
	return nil
}

