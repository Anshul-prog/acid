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
	"syscall"
	"time"

	// OUR INTERNAL PACKAGES
	"acid/internal/auth"          // JWT authentication
	"acid/internal/cache"        // Redis caching layer
	"acid/internal/clickhouse"  // ClickHouse search engine
	"acid/internal/config"      // Configuration loading
	"acid/internal/database"   // Database connections
	"acid/internal/dbsearch"  // Entity search intelligence
	"acid/internal/handlers"  // HTTP request handlers
	"acid/internal/middleware" // Security & rate limiting
	"acid/internal/pipeline"  // Data processing
	"acid/internal/schema"   // Schema discovery

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

	// Public API
	mux.HandleFunc("GET /api/info", apiHandler.GetAPIInfo)
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

	log.Println("✅ Multi-DB manager initialized")
	log.Println("📊 Report generation endpoints enabled")

	// ============================================================================
	// STEP 12: SET UP CATEGORY SYSTEM
	// ============================================================================
	categoryHandler := handlers.NewCategoryHandler(pool.Pool)
	log.Println("✅ Category system initialized")

	// Intelligent Search (dbsearch)
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
	// STEP 12: SET UP MIDDLEWARE CHAIN
	// ============================================================================
	handler := middleware.RateLimiter(mux)
	handler = middleware.CORS(handler)
	handler = middleware.Logger(handler)
	handler = middleware.AuditLogger(pool.Pool)(handler)
	handler = middleware.Recovery(handler)

	// ============================================================================
	// STEP 13: START SERVER
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