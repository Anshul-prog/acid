#!/usr/bin/env bash
# =============================================================================
#  ACID — Generate Databases Script
# =============================================================================
#  Runs the Go data generator (databases/generator.go) to populate the
#  PostgreSQL database with 10 databases × 1000 tables × 50 users of
#  realistic sample data, including cross-database duplicate references.
#
#  Pre-requisites:
#    - DATABASE_URL in .env or environment must point to a running PostgreSQL
#    - The ACID server should NOT be running (locks may conflict)
#
#  Usage:
#    chmod +x scripts/generate_databases.sh
#    ./scripts/generate_databases.sh                  # uses .env
#    NUM_DATABASES=5 ./scripts/generate_databases.sh  # override count
#
#  Output:
#    - Creates schemas: lsd_db_01 … lsd_db_10
#    - Each schema has 1000 tables with 50 user records
#    - 15% of records have cross-database duplicate references
#    - Generates databases/metadata.json summary
#    - Generates reports/sample_data_report.csv (first 100 rows per table)
#
#  Incoming files:
#    Place CSV/Excel files in databases/incoming/ to have them picked up
#    by the pipeline processor (POST /api/pipeline/start).
# =============================================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; RESET='\033[0m'

echo -e "${BLUE}╔═══════════════════════════════════════════════════════════╗${RESET}"
echo -e "${BLUE}║   ACID — Multi-Database Sample Data Generator              ║${RESET}"
echo -e "${BLUE}╚═══════════════════════════════════════════════════════════╝${RESET}"
echo ""

# ── Load .env ─────────────────────────────────────────────────────────────────
if [ -f .env ]; then
    set -a; source .env; set +a
    echo -e "${GREEN}[✓]${RESET} Loaded .env"
else
    echo -e "${YELLOW}[!]${RESET} No .env found — using system env"
fi

if [ -z "${DATABASE_URL:-}" ]; then
    echo -e "${RED}[✗]${RESET} DATABASE_URL not set. Set it in .env"
    exit 1
fi

echo -e "${BLUE}[INFO]${RESET} Target database: ${DATABASE_URL%%@*}@***"
echo ""

# ── Validate Go is installed ──────────────────────────────────────────────────
if ! command -v go &>/dev/null; then
    echo -e "${RED}[✗]${RESET} Go is not installed. Cannot run generator."
    exit 1
fi

# ── Ensure output dirs exist ──────────────────────────────────────────────────
mkdir -p databases/incoming databases/archive databases/seeds reports

# ── Export env overrides ──────────────────────────────────────────────────────
export NUM_DATABASES="${NUM_DATABASES:-10}"
export TABLES_PER_DB="${TABLES_PER_DB:-1000}"
export USERS_PER_TABLE="${USERS_PER_TABLE:-50}"

echo -e "${BLUE}[INFO]${RESET} Generating:"
echo "        • $NUM_DATABASES databases"
echo "        • $TABLES_PER_DB tables per database"  
echo "        • $USERS_PER_TABLE users per table"
echo "        • Total: $((NUM_DATABASES * TABLES_PER_DB * USERS_PER_TABLE)) records"
echo ""
echo -e "${YELLOW}[WARN]${RESET} This may take several minutes for large datasets..."
echo ""

# ── Run generator ─────────────────────────────────────────────────────────────
START_TIME=$(date +%s)

go run ./databases/generator.go

END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

echo ""
echo -e "${GREEN}╔═══════════════════════════════════════════════════════════╗${RESET}"
echo -e "${GREEN}║   Generation complete in ${DURATION}s!                               ║${RESET}"
echo -e "${GREEN}╚═══════════════════════════════════════════════════════════╝${RESET}"
echo ""
echo "  Next steps:"
echo "  1. Start ACID server:     ./scripts/run.bat  (Windows)"
echo "                             ./scripts/start-backend.sh  (Linux)"
echo "  2. Import CSV/Excel:      Copy files to databases/incoming/"
echo "                             POST /api/pipeline/start"
echo "  3. Search data:           GET /api/search?q=<query>"
