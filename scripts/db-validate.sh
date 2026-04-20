#!/usr/bin/env bash
# =============================================================================
#  ACID — Database Validation Script
# =============================================================================
#  Validates that the connected database matches the expected ACID schema.
#  Checks for required tables, indexes, and row counts.
#
#  Usage:
#    chmod +x scripts/db-validate.sh
#    ./scripts/db-validate.sh                  # uses .env DATABASE_URL
#    DATABASE_URL=postgres://... ./scripts/db-validate.sh
# =============================================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; RESET='\033[0m'
PASS=0; FAIL=0; WARN=0

info()  { echo -e "${BLUE}[INFO]${RESET}  $*"; }
ok()    { echo -e "${GREEN}[PASS]${RESET}  $*"; PASS=$((PASS+1)); }
fail()  { echo -e "${RED}[FAIL]${RESET}  $*"; FAIL=$((FAIL+1)); }
warn()  { echo -e "${YELLOW}[WARN]${RESET}  $*"; WARN=$((WARN+1)); }

# ── Load .env ─────────────────────────────────────────────────────────────────
if [ -f .env ]; then
    set -a; source .env; set +a
fi

if [ -z "${DATABASE_URL:-}" ]; then
    fail "DATABASE_URL is not set. Set it in .env or as environment variable."
    exit 1
fi

info "Connecting to: ${DATABASE_URL%%@*}@***"

# ── Helper: run a SQL query ────────────────────────────────────────────────────
run_sql() {
    psql "$DATABASE_URL" -t -A -c "$1" 2>/dev/null || echo "ERROR"
}

# ── Check psql available ──────────────────────────────────────────────────────
if ! command -v psql &>/dev/null; then
    warn "psql not found — skipping live DB checks. Install postgresql-client."
    exit 0
fi

echo ""
echo -e "${BLUE}══════════════════════════════════════════${RESET}"
echo -e "${BLUE}  ACID Database Validation${RESET}"
echo -e "${BLUE}══════════════════════════════════════════${RESET}"

# ── 1. Connectivity ───────────────────────────────────────────────────────────
info "Checking connectivity..."
RESULT=$(run_sql "SELECT 1")
if [ "$RESULT" = "1" ]; then
    ok "Database is reachable"
else
    fail "Cannot connect to database"
    exit 1
fi

# ── 2. Required core tables ───────────────────────────────────────────────────
info "Checking required tables..."
REQUIRED_TABLES=("users" "sessions" "api_keys" "categories" "entity_categories")

for table in "${REQUIRED_TABLES[@]}"; do
    EXISTS=$(run_sql "SELECT COUNT(*) FROM information_schema.tables WHERE table_name='$table' AND table_schema='public'")
    if [ "$EXISTS" = "1" ]; then
        COUNT=$(run_sql "SELECT COUNT(*) FROM $table")
        ok "Table '$table' exists ($COUNT rows)"
    else
        warn "Table '$table' is missing — run migrations or start the server once to auto-create"
    fi
done

# ── 3. Check users table columns ──────────────────────────────────────────────
info "Checking users table schema..."
REQUIRED_COLS=("id" "email" "username" "password_hash" "role" "name" "created_at")
for col in "${REQUIRED_COLS[@]}"; do
    EXISTS=$(run_sql "SELECT COUNT(*) FROM information_schema.columns WHERE table_name='users' AND column_name='$col' AND table_schema='public'")
    if [ "$EXISTS" = "1" ]; then
        ok "  users.$col ✓"
    else
        fail "  users.$col MISSING"
    fi
done

# ── 4. Index checks ───────────────────────────────────────────────────────────
info "Checking indexes..."
REQUIRED_INDEXES=(
    "users|email"
    "api_keys|key_hash"
    "categories|entity_type"
    "entity_categories|entity_type, entity_id"
)

for entry in "${REQUIRED_INDEXES[@]}"; do
    TABLE="${entry%%|*}"
    COLUMN="${entry##*|}"
    EXISTS=$(run_sql "SELECT COUNT(*) FROM pg_indexes WHERE tablename='$TABLE' AND indexdef LIKE '%$COLUMN%' AND schemaname='public'")
    if [ "${EXISTS:-0}" -ge "1" ]; then
        ok "  Index on $TABLE($COLUMN) ✓"
    else
        warn "  No index found on $TABLE($COLUMN) — may impact search performance"
    fi
done

# ── 5. Check database settings ────────────────────────────────────────────────
info "Checking connection limits..."
MAX_CONN=$(run_sql "SHOW max_connections")
ok "max_connections = $MAX_CONN"

IDLE_TIMEOUT=$(run_sql "SHOW idle_in_transaction_session_timeout")
ok "idle_in_transaction_session_timeout = $IDLE_TIMEOUT"

# ── 6. incoming / archive directories ────────────────────────────────────────
info "Checking filesystem directories..."
DIRS=("databases/incoming" "databases/archive" "databases/seeds" "databases/private_nosql")
for dir in "${DIRS[@]}"; do
    if [ -d "$dir" ]; then
        FILE_COUNT=$(find "$dir" -maxdepth 1 -type f | wc -l)
        ok "  $dir/ exists ($FILE_COUNT files)"
    else
        warn "  $dir/ missing — create with: mkdir -p $dir"
    fi
done

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo -e "${BLUE}══════════════════════════════════════════${RESET}"
echo -e "  Results: ${GREEN}$PASS passed${RESET}  ${YELLOW}$WARN warnings${RESET}  ${RED}$FAIL failed${RESET}"
echo -e "${BLUE}══════════════════════════════════════════${RESET}"

if [ $FAIL -gt 0 ]; then
    echo -e "${RED}Validation FAILED. Fix issues above before running ACID.${RESET}"
    exit 1
else
    echo -e "${GREEN}All validation checks passed!${RESET}"
fi
