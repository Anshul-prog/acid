#!/usr/bin/env bash
# =============================================================================
#  L.S.D — Local preflight check
#  Run before committing or deploying.
#  Checks: format, vet, tests, lint (if available)
# =============================================================================
set -euo pipefail

GREEN='\033[0;32m'; RED='\033[0;31m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; RESET='\033[0m'
PASS=0; FAIL=0

run() {
    local label=$1; shift
    printf "${BOLD}%-28s${RESET} " "$label"
    if "$@" >/dev/null 2>&1; then
        echo -e "${GREEN}✓ PASS${RESET}"; PASS=$((PASS+1))
    else
        echo -e "${RED}✗ FAIL${RESET}"; FAIL=$((FAIL+1))
        "$@" 2>&1 | sed 's/^/  /' || true
    fi
}

echo ""
echo "┌──────────────────────────────────────────┐"
echo "│   L.S.D — Pre-flight Checks              │"
echo "└──────────────────────────────────────────┘"
echo ""

# Go toolchain checks
run "go mod verify"        go mod verify
run "go vet"               go vet ./...
run "go build"             go build ./...
run "go test"              go test ./...

# Format check (gofmt -l exits 0 even with diffs, so check output)
printf "${BOLD}%-28s${RESET} " "gofmt"
UNFORMATTED=$(gofmt -l . 2>/dev/null | grep -v vendor || true)
if [ -z "$UNFORMATTED" ]; then
    echo -e "${GREEN}✓ PASS${RESET}"; PASS=$((PASS+1))
else
    echo -e "${RED}✗ FAIL — run: gofmt -w .${RESET}"; FAIL=$((FAIL+1))
    echo "$UNFORMATTED" | sed 's/^/  /'
fi

# Optional: golangci-lint
if command -v golangci-lint >/dev/null 2>&1; then
    run "golangci-lint"    golangci-lint run --timeout=3m --issues-exit-code=0
else
    echo -e "${YELLOW}[SKIP] golangci-lint not installed${RESET}"
fi

echo ""
echo "─────────────────────────────────────────"
echo -e "  ${GREEN}Passed: $PASS${RESET}  |  ${RED}Failed: $FAIL${RESET}"
echo "─────────────────────────────────────────"
echo ""

[ "$FAIL" -eq 0 ] || exit 1
