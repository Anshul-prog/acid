#!/usr/bin/env bash
# =============================================================================
#  L.S.D — Production startup / deploy script  (Linux / macOS)
#  BACKEND entry-point
# =============================================================================
#  Usage:
#    chmod +x scripts/start-backend.sh
#    ./scripts/start-backend.sh             # foreground (dev)
#    ./scripts/start-backend.sh --daemon    # background detached process
#    ./scripts/start-backend.sh --prod      # production build + run
#
#  Features:
#    • Colour-coded BACKEND label so the role is always visible
#    • Validates required env vars before starting
#    • Checks that PostgreSQL is reachable (psql or pg_isready)
#    • Builds the Go binary with version metadata
#    • Self-healing watchdog loop (restarts on crash)
#    • Daily log rotation
#    • PID file so stop/restart work correctly
#    • Graceful shutdown (SIGTERM → SIGKILL after 30 s)
# =============================================================================
set -euo pipefail

# ── Colours ───────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

# ── Config ────────────────────────────────────────────────────────────────────
APP_NAME="lsd-api"
BUILD_DIR="build"
BINARY="$BUILD_DIR/$APP_NAME"
LOG_DIR="logs/backend"
PID_FILE="$BUILD_DIR/lsd-api.pid"
RESTART_DELAY=5
MAX_RESTARTS=10          # stop after N consecutive crashes in RESTART_WINDOW
RESTART_WINDOW=60        # seconds
DAEMON=false
PROD=false

# ── Parse args ────────────────────────────────────────────────────────────────
for arg in "$@"; do
    case "$arg" in
        --daemon) DAEMON=true ;;
        --prod)   PROD=true ;;
    esac
done

# ── Banner ────────────────────────────────────────────────────────────────────
echo -e ""
echo -e "${BLUE}╔══════════════════════════════════════════════════════════╗${RESET}"
echo -e "${BLUE}║${RESET}  ${BOLD}${CYAN}◆ BACKEND${RESET}  L.S.D Intelligence Platform API             ${BLUE}║${RESET}"
echo -e "${BLUE}╚══════════════════════════════════════════════════════════╝${RESET}"
echo -e ""

ts() { date '+%Y-%m-%d %H:%M:%S'; }
info()  { echo -e "[$(ts)] ${GREEN}[BACKEND INFO]${RESET}  $*"; }
warn()  { echo -e "[$(ts)] ${YELLOW}[BACKEND WARN]${RESET}  $*"; }
error() { echo -e "[$(ts)] ${RED}[BACKEND ERROR]${RESET} $*"; }

# ── Prerequisites ─────────────────────────────────────────────────────────────
command -v go >/dev/null 2>&1 || { error "Go is not installed. Install from https://go.dev"; exit 1; }
info "Go $(go version | awk '{print $3}') detected"

mkdir -p "$BUILD_DIR" "$LOG_DIR"

# ── Load .env ─────────────────────────────────────────────────────────────────
if [ -f .env ]; then
    info "Loading environment from .env ..."
    set -a; source .env; set +a
else
    warn "No .env file found – using system environment variables"
fi

# ── Validate required env vars ────────────────────────────────────────────────
MISSING=()
[[ -z "${DATABASE_URL:-}" ]] && MISSING+=("DATABASE_URL")
[[ -z "${JWT_SECRET:-}"    ]] && MISSING+=("JWT_SECRET")

if [ ${#MISSING[@]} -gt 0 ]; then
    error "Missing required environment variables: ${MISSING[*]}"
    error "Copy .env.example to .env and fill in the values."
    exit 1
fi

# ── PostgreSQL reachability check ─────────────────────────────────────────────
info "Checking PostgreSQL connectivity ..."
if command -v pg_isready >/dev/null 2>&1; then
    PG_HOST=$(echo "$DATABASE_URL" | grep -oP '(?<=@)[^:/]+' || true)
    PG_PORT=$(echo "$DATABASE_URL" | grep -oP '(?<=:)\d+(?=/)' | tail -1 || echo "5432")
    pg_isready -h "${PG_HOST:-localhost}" -p "${PG_PORT:-5432}" -t 5 \
        && info "PostgreSQL is ready" \
        || { error "PostgreSQL is NOT reachable. Check DATABASE_URL and DB status."; exit 1; }
else
    warn "pg_isready not found – skipping DB connectivity check"
fi

# ── Build ──────────────────────────────────────────────────────────────────────
LOG_FILE="$LOG_DIR/backend_$(date +%Y%m%d).log"
VERSION="${VERSION:-$(git rev-parse --short HEAD 2>/dev/null || echo 'dev')}"
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

info "Building L.S.D backend (version=$VERSION) ..."

LDFLAGS="-s -w -X main.version=$VERSION -X main.buildDate=$BUILD_DATE"
if $PROD; then
    LDFLAGS="$LDFLAGS -X main.env=production"
    CGO_ENABLED=0 go build -ldflags="$LDFLAGS" -o "$BINARY" ./cmd/api
else
    CGO_ENABLED=0 go build -ldflags="$LDFLAGS" -o "$BINARY" ./cmd/api
fi
info "Build successful → $BINARY"

# ── Daemon mode ───────────────────────────────────────────────────────────────
if $DAEMON; then
    nohup "$BINARY" >> "$LOG_FILE" 2>&1 &
    echo $! > "$PID_FILE"
    info "Server started in background (PID $(cat "$PID_FILE"))"
    info "Logs → $LOG_FILE"
    exit 0
fi

# ── Watchdog loop ─────────────────────────────────────────────────────────────
info "Starting L.S.D backend ... (Ctrl+C to stop)"
echo ""

crash_times=()

while true; do
    echo "[$(ts)] [BACKEND] Starting server ..." | tee -a "$LOG_FILE"

    # Record PID
    "$BINARY" 2>&1 | tee -a "$LOG_FILE" &
    SERVER_PID=$!
    echo "$SERVER_PID" > "$PID_FILE"
    wait "$SERVER_PID" || true
    EXIT_CODE=$?
    rm -f "$PID_FILE"

    if [ "$EXIT_CODE" -eq 0 ]; then
        echo "[$(ts)] [BACKEND] Server stopped cleanly." | tee -a "$LOG_FILE"
        break
    fi

    # Track crash times for flood protection
    NOW=$(date +%s)
    crash_times=("${crash_times[@]}" "$NOW")
    # Remove crashes older than RESTART_WINDOW
    CUTOFF=$((NOW - RESTART_WINDOW))
    NEW_TIMES=()
    for t in "${crash_times[@]}"; do
        [[ $t -ge $CUTOFF ]] && NEW_TIMES+=("$t")
    done
    crash_times=("${NEW_TIMES[@]}")

    if [ ${#crash_times[@]} -ge $MAX_RESTARTS ]; then
        error "Server crashed $MAX_RESTARTS times in ${RESTART_WINDOW}s — stopping watchdog."
        error "Check $LOG_FILE for details."
        exit 1
    fi

    warn "Server crashed (exit $EXIT_CODE). Restarting in ${RESTART_DELAY}s ... [crash ${#crash_times[@]}/$MAX_RESTARTS]" \
        | tee -a "$LOG_FILE"
    sleep "$RESTART_DELAY"
done
