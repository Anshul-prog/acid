#!/usr/bin/env bash
# =============================================================================
#  L.S.D — Stop backend daemon
# =============================================================================
set -euo pipefail

BUILD_DIR="build"
PID_FILE="$BUILD_DIR/lsd-api.pid"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RESET='\033[0m'

if [ ! -f "$PID_FILE" ]; then
    echo -e "${YELLOW}[WARN] No PID file found at $PID_FILE — server may not be running.${RESET}"
    exit 0
fi

PID=$(cat "$PID_FILE")

if ! kill -0 "$PID" 2>/dev/null; then
    echo -e "${YELLOW}[WARN] Process $PID not found — removing stale PID file.${RESET}"
    rm -f "$PID_FILE"
    exit 0
fi

echo -e "${GREEN}[INFO] Sending SIGTERM to L.S.D backend (PID $PID) ...${RESET}"
kill -TERM "$PID"

# Wait up to 30 s for graceful shutdown
for i in $(seq 1 30); do
    if ! kill -0 "$PID" 2>/dev/null; then
        echo -e "${GREEN}[INFO] Backend stopped cleanly.${RESET}"
        rm -f "$PID_FILE"
        exit 0
    fi
    sleep 1
done

echo -e "${RED}[WARN] Backend did not stop in 30 s — sending SIGKILL.${RESET}"
kill -KILL "$PID" 2>/dev/null || true
rm -f "$PID_FILE"
echo -e "${GREEN}[INFO] Backend killed.${RESET}"
