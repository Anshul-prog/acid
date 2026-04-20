#!/usr/bin/env bash
# =============================================================================
#  ACID — Preflight Check Script
# =============================================================================
#  Runs pre-startup checks before launching the ACID server.
#  Called automatically by start-backend.sh or run standalone.
#
#  Usage:
#    chmod +x scripts/preflight.sh
#    ./scripts/preflight.sh
# =============================================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; RESET='\033[0m'

ok()   { echo -e "${GREEN}[✓]${RESET} $*"; }
fail() { echo -e "${RED}[✗]${RESET} $*"; EXIT_CODE=1; }
warn() { echo -e "${YELLOW}[!]${RESET} $*"; }
EXIT_CODE=0

echo -e "${BLUE}══════════════════════════════════════════${RESET}"
echo -e "${BLUE}  ACID Preflight Checks${RESET}"
echo -e "${BLUE}══════════════════════════════════════════${RESET}"

# ── Load .env ─────────────────────────────────────────────────────────────────
if [ -f .env ]; then
    set -a; source .env; set +a
    ok ".env file loaded"
else
    warn "No .env file found — using system environment variables"
fi

# ── 1. Go toolchain ───────────────────────────────────────────────────────────
if command -v go &>/dev/null; then
    GO_VERSION=$(go version | awk '{print $3}')
    ok "Go found: $GO_VERSION"
    # Require Go 1.22+
    GO_MINOR=$(echo "$GO_VERSION" | grep -oP '(?<=go1\.)\d+' || echo "0")
    if [ "${GO_MINOR:-0}" -lt 22 ]; then
        warn "Go 1.22+ recommended (found $GO_VERSION). Some features may not work."
    fi
else
    fail "Go is not installed. Download from https://go.dev/dl/"
fi

# ── 2. Required environment variables ─────────────────────────────────────────
REQUIRED_VARS=("DATABASE_URL" "JWT_SECRET")
for var in "${REQUIRED_VARS[@]}"; do
    if [ -n "${!var:-}" ]; then
        ok "Env var $var is set"
    else
        fail "Env var $var is NOT set — required for ACID to start"
    fi
done

# ── 3. PORT check ─────────────────────────────────────────────────────────────
PORT="${PORT:-8080}"
if ss -tlnp 2>/dev/null | grep -q ":$PORT " || netstat -an 2>/dev/null | grep -q ":$PORT "; then
    warn "Port $PORT appears to already be in use — another server may be running"
else
    ok "Port $PORT is available"
fi

# ── 4. PostgreSQL reachability ────────────────────────────────────────────────
if command -v pg_isready &>/dev/null && [ -n "${DATABASE_URL:-}" ]; then
    PG_HOST=$(echo "$DATABASE_URL" | grep -oP '(?<=@)[^:/]+' || echo "localhost")
    PG_PORT=$(echo "$DATABASE_URL" | grep -oP '(?<=:)\d+(?=/)' | tail -1 || echo "5432")
    if pg_isready -h "$PG_HOST" -p "$PG_PORT" -t 5 &>/dev/null; then
        ok "PostgreSQL is reachable at $PG_HOST:$PG_PORT"
    else
        fail "PostgreSQL NOT reachable at $PG_HOST:$PG_PORT — check DATABASE_URL and DB status"
    fi
else
    warn "pg_isready not available — skipping PostgreSQL connectivity check"
fi

# ── 5. Redis (optional) ───────────────────────────────────────────────────────
REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"
REDIS_HOST="${REDIS_ADDR%%:*}"
REDIS_PORT="${REDIS_ADDR##*:}"
if command -v redis-cli &>/dev/null; then
    if redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" ping &>/dev/null; then
        ok "Redis is reachable at $REDIS_ADDR"
    else
        warn "Redis not reachable at $REDIS_ADDR — caching will be disabled (non-fatal)"
    fi
else
    warn "redis-cli not found — skipping Redis check (non-fatal)"
fi

# ── 6. ClickHouse (optional) ──────────────────────────────────────────────────
CH_ADDR="${CLICKHOUSE_ADDR:-localhost:9000}"
CH_HTTP_PORT="${CH_ADDR##*:}"
# Convert native port to HTTP port
if [ "$CH_HTTP_PORT" = "9000" ]; then CH_HTTP_PORT=8123; fi
CH_HOST="${CH_ADDR%%:*}"
if curl -sf "http://$CH_HOST:$CH_HTTP_PORT/ping" &>/dev/null; then
    ok "ClickHouse is reachable at $CH_HOST:$CH_HTTP_PORT"
else
    warn "ClickHouse not reachable — search will fall back to PostgreSQL (non-fatal)"
fi

# ── 7. Required directories ───────────────────────────────────────────────────
REQUIRED_DIRS=("web" "databases" "databases/incoming" "databases/archive" "databases/seeds" "databases/private_nosql")
for dir in "${REQUIRED_DIRS[@]}"; do
    if [ -d "$dir" ]; then
        ok "Directory $dir/ exists"
    else
        warn "Directory $dir/ missing — creating..."
        mkdir -p "$dir"
        touch "$dir/.gitkeep"
    fi
done

# ── 8. web assets check ───────────────────────────────────────────────────────
WEB_FILES=("web/index.html" "web/login.html" "web/dashboard.html" "web/admin.html")
for f in "${WEB_FILES[@]}"; do
    if [ -f "$f" ]; then
        ok "Web asset $f exists"
    else
        fail "Web asset $f is MISSING — frontend will not work"
    fi
done

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo -e "${BLUE}══════════════════════════════════════════${RESET}"
if [ $EXIT_CODE -eq 0 ]; then
    echo -e "${GREEN}  All preflight checks passed! Ready to launch.${RESET}"
else
    echo -e "${RED}  Some preflight checks FAILED. Fix issues above.${RESET}"
fi
echo -e "${BLUE}══════════════════════════════════════════${RESET}"

exit $EXIT_CODE
