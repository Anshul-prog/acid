#!/usr/bin/env bash
# =============================================================================
#  L.S.D — Full-stack start (backend + open frontend)
#  BACKEND + FRONTEND entry-point
# =============================================================================
#  Usage:
#    chmod +x scripts/start-fullstack.sh
#    ./scripts/start-fullstack.sh
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo ""
echo "┌──────────────────────────────────────────────────────────┐"
echo "│   ◆ BACKEND + FRONTEND  L.S.D Full-Stack Launcher        │"
echo "└──────────────────────────────────────────────────────────┘"
echo ""

# Start backend in background using the hardened start script
bash "$SCRIPT_DIR/start-backend.sh" --daemon

# Give backend a moment to bind the port
sleep 2

# Open frontend
bash "$SCRIPT_DIR/start-frontend.sh"
