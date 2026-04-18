#!/usr/bin/env bash
# =============================================================================
#  L.S.D — Deployment readiness check
#  Run before pushing to production.
#  Verifies env vars, required ports, DB connectivity, binary build.
# =============================================================================
set -euo pipefail

GREEN='\033[0;32m'; RED='\033[0;31m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; RESET='\033[0m'
PASS=0; FAIL=0; WARN=0

ok()   { echo -e "${GREEN}[✓]${RESET} $*"; PASS=$((PASS+1)); }
fail() { echo -e "${RED}[✗]${RESET} $*"; FAIL=$((FAIL+1)); }
warn() { echo -e "${YELLOW}[⚠]${RESET} $*"; WARN=$((WARN+1)); }

echo ""
echo "┌──────────────────────────────────────────────────┐"
echo "│   L.S.D — Deployment Readiness Check             │"
echo "│   BACKEND layer                                   │"
echo "└──────────────────────────────────────────────────┘"
echo ""

# Load env
if [ -f .env ]; then
    set -a; source .env; set +a
    ok ".env loaded"
else
    warn ".env not found — using system environment"
fi

# ── Required environment variables ───────────────────────────────────────────
echo ""
echo "── Environment Variables ─────────────────────────"
check_env() {
    local key=$1; local val="${!key:-}"
    if [ -n "$val" ]; then
        ok "$key is set"
    else
        fail "$key is NOT set"
    fi
}
check_env DATABASE_URL
check_env JWT_SECRET
check_env PORT

# Warn about default JWT secret
if [ "${JWT_SECRET:-}" = "lsd-jwt-secret-key-2026-change-in-production" ]; then
    fail "JWT_SECRET is still the default value — MUST change in production!"
fi

# ── Binary build ──────────────────────────────────────────────────────────────
echo ""
echo "── Build ─────────────────────────────────────────"
if go build -o /tmp/lsd-api-check ./cmd/api 2>/dev/null; then
    ok "Binary builds successfully"
    rm -f /tmp/lsd-api-check
else
    fail "Binary build failed"
fi

# ── Port availability ─────────────────────────────────────────────────────────
echo ""
echo "── Port ──────────────────────────────────────────"
PORT_NUM="${PORT:-8080}"
if command -v ss >/dev/null 2>&1; then
    if ss -tlnp 2>/dev/null | grep -q ":$PORT_NUM "; then
        warn "Port $PORT_NUM is already in use — another process may be running"
    else
        ok "Port $PORT_NUM is available"
    fi
elif command -v lsof >/dev/null 2>&1; then
    if lsof -i ":$PORT_NUM" 2>/dev/null | grep -q LISTEN; then
        warn "Port $PORT_NUM is already in use"
    else
        ok "Port $PORT_NUM is available"
    fi
else
    warn "Cannot check port availability (ss/lsof not found)"
fi

# ── Database connectivity ────────────────────────────────────────────────────
echo ""
echo "── Database ──────────────────────────────────────"
if command -v pg_isready >/dev/null 2>&1 && [ -n "${DATABASE_URL:-}" ]; then
    PG_HOST=$(echo "$DATABASE_URL" | grep -oP '(?<=@)[^:/]+' || echo "localhost")
    PG_PORT=$(echo "$DATABASE_URL" | grep -oP '(?<=:)\d+(?=/)' | tail -1 || echo "5432")
    if pg_isready -h "$PG_HOST" -p "$PG_PORT" -t 5 >/dev/null 2>&1; then
        ok "PostgreSQL is reachable at $PG_HOST:$PG_PORT"
    else
        fail "PostgreSQL NOT reachable at $PG_HOST:$PG_PORT"
    fi
else
    warn "pg_isready not found or DATABASE_URL not set — skipping DB check"
fi

# ── Web assets ────────────────────────────────────────────────────────────────
echo ""
echo "── Web Assets ────────────────────────────────────"
for f in web/index.html web/login.html web/register.html web/dashboard.html web/docs.html web/style.css web/app.js; do
    if [ -f "$f" ]; then
        ok "$f exists"
    else
        fail "$f MISSING"
    fi
done

# ── Required directories ──────────────────────────────────────────────────────
echo ""
echo "── Directories ───────────────────────────────────"
for d in logs ErrorFiles; do
    if [ -d "$d" ]; then
        ok "Directory $d exists"
    else
        warn "Directory $d missing — will be created at runtime"
    fi
done

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "─────────────────────────────────────────────────"
echo -e "  ${GREEN}Passed: $PASS${RESET}  |  ${YELLOW}Warnings: $WARN${RESET}  |  ${RED}Failed: $FAIL${RESET}"
echo "─────────────────────────────────────────────────"
echo ""

if [ "$FAIL" -gt 0 ]; then
    echo -e "${RED}Deployment readiness: NOT READY ($FAIL failures)${RESET}"
    exit 1
elif [ "$WARN" -gt 0 ]; then
    echo -e "${YELLOW}Deployment readiness: WARNINGS PRESENT — review before deploying${RESET}"
    exit 0
else
    echo -e "${GREEN}Deployment readiness: ALL CHECKS PASSED${RESET}"
    exit 0
fi
