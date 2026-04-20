#!/usr/bin/env bash
# =============================================================================
#  ACID — Stop Backend Script
# =============================================================================
#  Gracefully stops the ACID API server started by start-backend.sh.
#  Reads the PID from build/acid-api.pid, sends SIGTERM, waits 30s,
#  then force-kills if still running.
#
#  Usage:
#    ./scripts/stop-backend.sh
# =============================================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RESET='\033[0m'

PID_FILE="build/acid-api.pid"
GRACE_PERIOD=30

echo -e "${YELLOW}[ACID]${RESET} Stopping ACID backend server..."

if [ ! -f "$PID_FILE" ]; then
    echo -e "${YELLOW}[WARN]${RESET} PID file not found at $PID_FILE"
    # Try to find by process name
    PIDS=$(pgrep -f "acid-server" 2>/dev/null || true)
    if [ -z "$PIDS" ]; then
        echo -e "${YELLOW}[WARN]${RESET} No running ACID server process found."
        exit 0
    fi
    echo -e "${YELLOW}[WARN]${RESET} Found acid-server processes: $PIDS"
    echo "$PIDS" | xargs kill -TERM 2>/dev/null || true
    echo -e "${GREEN}[OK]${RESET} Sent SIGTERM to acid-server processes."
    exit 0
fi

PID=$(cat "$PID_FILE")

if ! kill -0 "$PID" 2>/dev/null; then
    echo -e "${YELLOW}[WARN]${RESET} Process $PID is not running (stale PID file)."
    rm -f "$PID_FILE"
    exit 0
fi

echo -e "${YELLOW}[INFO]${RESET} Sending SIGTERM to PID $PID..."
kill -TERM "$PID"

# Wait for graceful shutdown
WAITED=0
while kill -0 "$PID" 2>/dev/null && [ $WAITED -lt $GRACE_PERIOD ]; do
    sleep 1
    WAITED=$((WAITED+1))
    printf "\r  Waiting for shutdown... %ds" "$WAITED"
done
echo ""

if kill -0 "$PID" 2>/dev/null; then
    echo -e "${RED}[WARN]${RESET} Process did not stop after ${GRACE_PERIOD}s — force killing..."
    kill -KILL "$PID" 2>/dev/null || true
    sleep 1
fi

rm -f "$PID_FILE"
echo -e "${GREEN}[OK]${RESET} ACID server stopped."
