// =============================================================================
// ACID - Advanced Database Interface System
// =============================================================================
// This is the main configuration file that tells Go what package names to use.
// We renamed from "highperf-api" to "acid" to match the project name.
//
// QUICK EXPLANATION FOR NEWBIES:
// - "module" = your project's unique name (like a package name)
// - "go 1.24.4" = the version of Go programming language we need
// - "require" = external packages/libraries we need to download
// =============================================================================
module acid

go 1.24.4

// =============================================================================
// REQUIRED PACKAGES - External libraries we need
// =============================================================================
// These are the core dependencies that make our API work:
require (
	github.com/ClickHouse/clickhouse-go/v2 v2.42.0  // ClickHouse database driver
	github.com/jackc/pgx/v5 v5.8.0                    // PostgreSQL database driver
	github.com/joho/godotenv v1.5.1                    // Load .env configuration files
	github.com/redis/go-redis/v9 v9.17.2                // Redis cache driver
	golang.org/x/time v0.14.0                         // Time utilities
)

// =============================================================================
// INDIRECT PACKAGES - Dependencies of our dependencies (automatically downloaded)
// =============================================================================
// These are packages that our required packages need to work:
require (
	github.com/ClickHouse/ch-go v0.69.0 // indirect - ClickHouse Go bindings
	github.com/andybalholm/brotli v1.2.0 // indirect - compression
	github.com/cespare/xxhash/v2 v2.3.0 // indirect - fast hashing
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-faster/city v1.0.1 // indirect - city data
	github.com/go-faster/errors v0.7.1 // indirect - error handling
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect - JWT authentication
	github.com/google/uuid v1.6.0 // indirect - UUID generation
	github.com/jackc/pgpassfile v1.0.0 // indirect - PostgreSQL password file
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect - connection pooling
	github.com/klauspost/compress v1.18.0 // indirect - compression
	github.com/paulmach/orb v0.12.0 // indirect - geometry
	github.com/pierrec/lz4/v4 v4.1.22 // indirect - compression
	github.com/richardlehane/mscfb v1.0.4 // indirect - file formats
	github.com/richardlehane/msoleps v1.0.4 // indirect - office formats
	github.com/rogpeppe/go-internal v1.14.1 // indirect - Go utilities
	github.com/romance-dev/ascii-art v1.0.1 // indirect - ASCII art banner
	github.com/saintfish/chardet v0.0.0-20230101081208-5e3ef4b5456d // indirect - charset detection
	github.com/segmentio/asm v1.2.1 // indirect - assembly
	github.com/shopspring/decimal v1.4.0 // indirect - decimal math
	github.com/tiendc/go-deepcopy v1.7.1 // indirect - deep copy
	github.com/xuri/efp v0.0.1 // indirect - Excel
	github.com/xuri/excelize/v2 v2.10.0 // indirect - Excel files
	github.com/xuri/nfp v0.0.2-0.20250530014748-2ddeb826f9a9 // indirect
	go.opentelemetry.io/otel v1.39.0 // indirect - observability
	go.opentelemetry.io/otel/trace v1.39.0 // indirect - tracing
	go.yaml.in/yaml/v3 v3.0.4 // indirect - YAML parsing
	golang.org/x/crypto v0.48.0 // indirect - cryptography
	golang.org/x/net v0.49.0 // indirect - networking
	golang.org/x/sync v0.19.0 // indirect - synchronization
	golang.org/x/sys v0.41.0 // indirect - system calls
	golang.org/x/text v0.34.0 // indirect - text handling
)
