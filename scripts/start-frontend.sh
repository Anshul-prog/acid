#!/usr/bin/env bash
# =============================================================================
#  L.S.D — Frontend launch helper  (Linux / macOS)
#  FRONTEND entry-point
# =============================================================================
#  The frontend is served by the Go backend from the web/ directory.
#  This script verifies the backend is running then opens the browser.
#
#  Usage:
#    chmod +x scripts/start-frontend.sh
#    ./scripts/start-frontend.sh
# =============================================================================
set -euo pipefail

# ── Colours ───────────────────────────────────────────────────────────────────
GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'
MAGENTA='\033[0;35m'; BOLD='\033[1m'; RESET='\033[0m'

ts() { date '+%Y-%m-%d %H:%M:%S'; }
info()  { echo -e "[$(ts)] ${GREEN}[FRONTEND INFO]${RESET}  $*"; }
warn()  { echo -e "[$(ts)] ${YELLOW}[FRONTEND WARN]${RESET}  $*"; }
error() { echo -e "[$(ts)] ${RED}[FRONTEND ERROR]${RESET} $*"; }

# ── Banner ────────────────────────────────────────────────────────────────────
echo ""
echo -e "${MAGENTA}╔══════════════════════════════════════════════════════════╗${RESET}"
echo -e "${MAGENTA}║${RESET}  ${BOLD}${MAGENTA}◆ FRONTEND${RESET} L.S.D Intelligence Platform UI            ${MAGENTA}║${RESET}"
echo -e "${MAGENTA}╚══════════════════════════════════════════════════════════╝${RESET}"
echo ""

# ── Load .env ─────────────────────────────────────────────────────────────────
if [ -f .env ]; then
    set -a; source .env; set +a
fi

PORT="${PORT:-8080}"
BASE_URL="http://localhost:$PORT"

# ── Wait for backend ──────────────────────────────────────────────────────────
MAX_WAIT=30
WAITED=0
info "Waiting for backend at $BASE_URL/api/health ..."

until curl -sf "$BASE_URL/api/health" >/dev/null 2>&1; do
    if [ $WAITED -ge $MAX_WAIT ]; then
        error "Backend did not start within ${MAX_WAIT}s."
        error "Run './scripts/start-backend.sh' in another terminal first."
        exit 1
    fi
    sleep 1
    WAITED=$((WAITED + 1))
    printf "."
done
echo ""
info "Backend is online ✓"

# ── Open browser ──────────────────────────────────────────────────────────────
info "Opening frontend: $BASE_URL/"
info "Available pages:"
info "  Home      → $BASE_URL/"
info "  Login     → $BASE_URL/login"
info "  Register  → $BASE_URL/register"
info "  Dashboard → $BASE_URL/dashboard"
info "  Docs      → $BASE_URL/docs"
echo ""

# Try to open browser cross-platform
if command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$BASE_URL/" 2>/dev/null &
elif command -v open >/dev/null 2>&1; then
    open "$BASE_URL/" 2>/dev/null &
else
    warn "Cannot auto-open browser. Navigate to: $BASE_URL/"
fi

info "Frontend is served by the Go backend from the web/ directory."
info "All data fetching goes through /api/* endpoints — no separate frontend server needed."
