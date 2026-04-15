#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════════════
#  L.S.D  —  Linux / macOS startup script
#  Usage:  chmod +x scripts/start.sh && ./scripts/start.sh
#  Features:
#    • Builds the Go binary
#    • Loads .env
#    • Auto-restarts on crash (self-healing watchdog loop)
#    • Rotates logs daily
# ═══════════════════════════════════════════════════════════════════════════
set -euo pipefail

APP_NAME="lsd-server"
BUILD_DIR="build"
BINARY="$BUILD_DIR/$APP_NAME"
LOG_DIR="logs"
LOG_FILE="$LOG_DIR/lsd_$(date +%Y%m%d).log"
RESTART_DELAY=5

# ── Banner ────────────────────────────────────────────────────────────────────
echo ""
echo " ██╗     ███████╗██████╗ "
echo " ██║     ██╔════╝██╔══██╗"
echo " ██║     ███████╗██║  ██║"
echo " ██║     ╚════██║██║  ██║"
echo " ███████╗███████║██████╔╝"
echo " ╚══════╝╚══════╝╚═════╝  Intelligence Platform"
echo ""

# ── Prerequisites ─────────────────────────────────────────────────────────────
command -v go >/dev/null 2>&1 || { echo "[ERROR] Go is not installed."; exit 1; }

mkdir -p "$BUILD_DIR" "$LOG_DIR"

# ── Load .env ─────────────────────────────────────────────────────────────────
if [ -f .env ]; then
    echo "[INFO] Loading environment from .env ..."
    set -a
    # shellcheck disable=SC1091
    source .env
    set +a
    echo "[INFO] Environment loaded."
else
    echo "[WARN] No .env file found. Using system environment variables."
fi

# ── Build ──────────────────────────────────────────────────────────────────────
echo "[INFO] Building L.S.D ..."
go build -o "$BINARY" ./cmd/api
echo "[INFO] Build successful: $BINARY"

# ── Self-healing watchdog loop ────────────────────────────────────────────────
echo "[INFO] Starting L.S.D server ... (Ctrl+C to stop)"
echo ""

while true; do
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Starting server ..." | tee -a "$LOG_FILE"

    "$BINARY" 2>&1 | tee -a "$LOG_FILE"
    EXIT_CODE=${PIPESTATUS[0]}

    if [ "$EXIT_CODE" -eq 0 ]; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Server stopped cleanly." | tee -a "$LOG_FILE"
        break
    fi

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Server crashed (exit $EXIT_CODE). Restarting in ${RESTART_DELAY}s ..." \
        | tee -a "$LOG_FILE"
    sleep "$RESTART_DELAY"
done
