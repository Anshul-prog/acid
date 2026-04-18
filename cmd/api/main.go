// =============================================================================
// ACID - Advanced Database Interface System
// =============================================================================
// Main entry point for the ACID API Server
//
// WHAT THIS FILE DOES:
// This is where the application starts (entry point). Think of it as the "main door"
// where everything begins. It's like starting a car - this is the ignition.
//
// HOW IT WORKS (SIMPLE EXPLANATION):
// 1. Load configuration from .env file
// 2. Connect to PostgreSQL database
// 3. Set up Redis caching
// 4. Discover database tables automatically
// 5. Set up ClickHouse for fast search
// 6. Initialize security (JWT tokens)
// 7. Create all the API routes/endpoints
// 8. Start the web server
//
// FOR DEVELOPERS: Don't modify this unless you need to add new features!
// =============================================================================
package main

// =============================================================================
// IMPORT PACKAGES - Bringing in external tools we need
// =============================================================================
// These are like bringing different specialists into your team:
// - context: For handling timeouts and cancellations
// - fmt: For printing formatted text
// - log: For logging/debugging
// - net/http: For creating the web server
// - os/signal: For handling system signals (Ctrl+C)
// - time: For time-related functions
// - config: Our own configuration module (see internal/config/)
// - database: Our own database module (see internal/database/)
// - handlers: Our own request handlers (see internal/handlers/)
// - middleware: Security and rate limiting (see internal/middleware/)
// - pipeline: Data processing pipeline
// - schema: Database schema discovery
// - auth: Authentication service
// - cache: Redis caching layer
// - clickhouse: Fast search database
// - asciiart: For displaying the cool banner on startup
// =============================================================================
import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// INTERNAL PACKAGES - Our own code modules (see internal/ folder)
	"acid/internal/auth"         // Authentication & JWT tokens
	"acid/internal/cache"       // Redis caching layer
	"acid/internal/clickhouse"  // ClickHouse search engine
	"acid/internal/config"      // Configuration loading
	"acid/internal/database"   // Database connections & queries
	"acid/internal/handlers"  // HTTP request handlers
	"acid/internal/middleware" // Security & rate limiting
	"acid/internal/pipeline"  // Data processing
	"acid/internal/schema"   // Schema discovery

	// EXTERNAL PACKAGES - Third-party libraries
	asciiart "github.com/romance-dev/ascii-art" // ASCII art banner
	_ "github.com/romance-dev/ascii-art/fonts"  // Font for ASCII art
)

// =============================================================================
// MAIN FUNCTION - The Starting Point
// =============================================================================
// This function runs when you start the application. It's the first thing that executes.
// Think of it as the "main switch" that turns everything on.
//
// WHAT HAPPENS HERE (STEP BY STEP):
// 1. Load all configuration settings
// 2. Display the cool ASCII art banner
// 3. Connect to PostgreSQL (main database)
// 4. Set up Redis caching
// 5. Auto-discover all database tables
// 6. Connect to ClickHouse (search engine)
// 7. Set up CDC (Change Data Capture) pipeline
// 8. Set up authentication with JWT tokens
// 9. Create all API routes
// 10. Start the HTTP server
// =============================================================================
func main() {
	// =============================================================================
	// STEP 1: LOAD CONFIGURATION
	// =============================================================================
	// Load all settings from .env file and environment variables
	// See internal/config/config.go for all the options
	cfg := config.LoadConfig()

	// Create a background context (used for database operations)
	ctx := context.Background()

	// =============================================================================
	// STEP 2: DISPLAY STARTUP BANNER
	// =============================================================================
	// Show the cool ACID banner when starting
	asciiart.NewFigure("ACID", "isometric1", true).Print()
	log.Printf("🚀 ACID API Server Starting...")
	log.Println("══════════════════════════════════════════════════════════════════")

	// =============================================================================
	// STEP 3: CONNECT TO POSTGRESQL
	// =============================================================================
	// Connect to the main PostgreSQL database
	// This is where all your data is stored
	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()
	log.Println("✅ Database connected")

	// =============================================================================
	// STEP 4: SET UP REDIS CACHING
	// =============================================================================
	// Set up Redis for caching (makes things faster)
	// Redis stores temporary data to reduce database load
	redisCache := cache.NewRedisCache(
		cfg.RedisAddr,
		cfg.RedisPassword,
		cfg.RedisDB,
		5*time.Minute, // Cache data for 5 minutes
	)

	// Create multi-layer cache (with in-memory + Redis)
	multiCache := cache.NewMultiLayerCache(redisCache, 30*time.Second)
	log.Println("✅ Multi-layer cache initialized")

	// =============================================================================
	// STEP 5: AUTO-DISCOVER DATABASE SCHEMA
	// =============================================================================
	// Automatically discover all tables in the database
	// This is what makes ACID dynamic - it figures out your database structure!
	registry := schema.NewSchemaRegistry(pool.Pool)
	if err := registry.LoadSchema(ctx); err != nil {
		log.Fatalf("Failed to load schema: %v", err)
	}
	log.Printf("✅ Schema loaded: %d tables discovered", len(registry.GetAllTables()))

	// =============================================================================
	// STEP 6: CONNECT TO CLICKHOUSE (SEARCH ENGINE)
	// =============================================================================
	// Connect to ClickHouse for fast full-text search
	// ClickHouse is optimized for search queries
	chPool, err := clickhouse.NewConnectionPool(clickhouse.Config{
		Addr:     cfg.ClickHouseAddr,
		Database: cfg.ClickHouseDB,
		Username: cfg.ClickHouseUser,
		Password: cfg.ClickHousePassword,
	}, 5) // 5 connection pool size

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

	// =============================================================================
	// STEP 7: CREATE DYNAMIC REQUEST HANDLER
	// =============================================================================
	// This handler automatically works with ANY table in your database
	// No code changes needed when you add new tables!
	dynamicRepo := database.NewDynamicRepository(pool.Pool, registry)
	dynamicHandler := handlers.NewDynamicHandler(
		dynamicRepo,
		registry,
		multiCache,
		chSearch,
		50,   // max page size (max records per page)
		20,   // default page size
		120*time.Second, // request timeout
	)

	// =============================================================================
	// STEP 8: SET UP CDC (CHANGE DATA CAPITUDE)
	// =============================================================================
	// CDC syncs data from PostgreSQL to ClickHouse in real-time
	// This keeps your search index up to date automatically
	var cdcManager *clickhouse.CDCManager
	if chPool != nil && chPool.IsAvailable() && cfg.EnableCDC {
		cdcConfig := clickhouse.CDCConfig{
			BatchSize:       10000,    // Process 10k records at a time
			SyncInterval:    30 * time.Second, // Check for changes every 30s
			ParallelWorkers: 5,        // 5 workers for parallel processing
			ChunkSize:       100000,   // Process 100k records per chunk
		}

		cdcManager = clickhouse.NewCDCManager(pool.Pool, chSearch, registry, cdcConfig)
		dynamicHandler.SetCDCManager(cdcManager)
		cdcManager.Start()
		log.Println("✅ CDC Manager started with auto-discovery")
	}

	// =============================================================================
	// STEP 9: SET UP DATA PIPELINE PROCESSOR
	// =============================================================================
	// Process data from files (CSV, JSON, etc.)
	pipelineProcessor := pipeline.NewPipelineProcessor(pool.Pool, "./ErrorFiles")

	// Connect pipeline to CDC (triggers sync after processing)
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

	// =============================================================================
	// STEP 10: SET UP AUTHENTICATION (JWT TOKENS)
	// =============================================================================
	// JWT (JSON Web Tokens) is how we secure the API
	// Tokens verify identity without requiring password every time
	jwtSecret := cfg.JWTSecret
	if jwtSecret == "" {
		// Default secret for development only! CHANGE IN PRODUCTION!
		jwtSecret = "acid-jwt-secret-key-2026-change-in-production"
	}

	authService := auth.NewAuthService(jwtSecret)
	authHandler := handlers.NewAuthHandler(pool.Pool, authService)
	authMiddleware := middleware.NewAuthMiddleware(authService, pool.Pool)

	// =============================================================================
	// STEP 11: CREATE HTTP ROUTER (API ROUTES)
	// =============================================================================
	// The router decides which function handles which URL
	// Think of it as a phone switchboard - directing calls to the right person
	mux := http.NewServeMux()

	// ============================================================================
	// PUBLIC ROUTES - Anyone can access these (no login required)
	// ============================================================================

// Web Pages (HTML files served to browsers)
        mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
                http.ServeFile(w, r, "./web/login.html")
        })
        mux.HandleFunc("GET /register", func(w http.ResponseWriter, r *http.Request) {
                http.ServeFile(w, r, "./web/register.html")
        })
        mux.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
                http.ServeFile(w, r, "./web/dashboard.html")
        })

        // ACID Admin Panel - Complete Management UI
        mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
                http.ServeFile(w, r, "./web/admin.html")
        })

        // API Documentation page
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/docs.html")
	})

	// Authentication API endpoints (login, register, etc.)
	mux.HandleFunc("POST /api/auth/register", authHandler.Register)
	mux.HandleFunc("POST /api/auth/login", authHandler.Login)
	mux.HandleFunc("POST /api/auth/logout", authHandler.Logout)
	mux.HandleFunc("GET /api/auth/me", authMiddleware.RequireAuth(http.HandlerFunc(authHandler.GetMe)).ServeHTTP)

        // API Keys (Protected)
        mux.HandleFunc("GET /api/auth/api-keys", authMiddleware.RequireAuth(http.HandlerFunc(authHandler.ListAPIKeys)).ServeHTTP)
        mux.HandleFunc("POST /api/auth/api-keys", authMiddleware.RequireAuth(http.HandlerFunc(authHandler.CreateAPIKey)).ServeHTTP)
        mux.HandleFunc("DELETE /api/auth/api-keys/{id}", authMiddleware.RequireAuth(http.HandlerFunc(authHandler.RevokeAPIKey)).ServeHTTP)

        // Health Check
        mux.HandleFunc("GET /api/health", dynamicHandler.HealthCheck)

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
        // Assets folder (for scalar-standalone.js, images, etc.)
        mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("./web/assets"))))

        // Index Page (Documentation)
        mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
                if r.URL.Path == "/" {
                        http.ServeFile(w, r, "./web/index.html")
                } else {
                        http.NotFound(w, r)
                }
        })

        // ═══════════════════════════════════════════════════════════
        // 🔒 PROTECTED ROUTES (with Auth Middleware)
        // ═══════════════════════════════════════════════════════════

        // Table Endpoints
        mux.Handle("GET /api/tables", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.ListTables)))
        mux.Handle("GET /api/tables/{table}/schema", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.GetTableSchema)))
        mux.Handle("GET /api/tables/{table}/records", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.GetRecords)))
        mux.Handle("GET /api/tables/{table}/records/{pk}", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.GetRecordByPK)))
        mux.Handle("GET /api/tables/{table}/stats", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.GetTableStats)))
        mux.Handle("GET /api/tables/{table}/search", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.SearchRecords)))

        // Search Endpoints
        mux.Handle("GET /api/search/", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.SearchOptimized)))
        mux.Handle("GET /api/search/duplicates", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.SearchGlobalWithDuplicates)))

        // Pipeline Endpoints
        mux.Handle("POST /api/pipeline/start", authMiddleware.RequireAuth(http.HandlerFunc(pipelineHandler.StartJob)))
        mux.Handle("GET /api/pipeline/jobs/{job_id}", authMiddleware.RequireAuth(http.HandlerFunc(pipelineHandler.GetJobStatus)))
        mux.Handle("GET /api/pipeline/jobs", authMiddleware.RequireAuth(http.HandlerFunc(pipelineHandler.ListJobs)))
        mux.Handle("GET /api/pipeline/jobs/{job_id}/stream", authMiddleware.RequireAuth(http.HandlerFunc(pipelineHandler.StreamJobProgress)))
        mux.Handle("GET /api/pipeline/jobs/{job_id}/logs", authMiddleware.RequireAuth(http.HandlerFunc(pipelineHandler.GetJobLogs)))

        // CDC Status
        mux.Handle("GET /api/cdc/status", authMiddleware.RequireAuth(http.HandlerFunc(dynamicHandler.GetCDCStatus)))

        // ═══════════════════════════════════════════════════════════
        // 📊 REPORT & MULTI-DB ENDPOINTS
        // ═══════════════════════════════════════════════════════════

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

        log.Println("✅ Multi-DB manager initialized with 10 database support")
        log.Println("📊 Report generation endpoints enabled")

        // ═══════════════════════════════════════════════════════════
        // 🛡️ GLOBAL MIDDLEWARE
        // ═══════════════════════════════════════════════════════════

        handler := middleware.RateLimiter(mux)
        handler = middleware.CORS(handler)
        handler = middleware.Logger(handler)

        server := &http.Server{
                Addr:         fmt.Sprintf(":%s", cfg.Port),
                Handler:      handler,
                ReadTimeout:  15 * time.Second,
                WriteTimeout: 15 * time.Second,
                IdleTimeout:  60 * time.Second,
        }

        go func() {
                if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
                        log.Fatalf("Server failed to start: %v", err)
                }
        }()

        quit := make(chan os.Signal, 1)
        signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
        <-quit

        log.Println("═══════════════════════════════════════════════════════════")
        log.Println("🛑 Shutting down server gracefully...")
        log.Println("═══════════════════════════════════════════════════════════")

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
