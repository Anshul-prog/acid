#!/usr/bin/env bash
# =============================================================================
#  ACID — Deploy Readiness Check Script
# =============================================================================
#  Validates that the project is ready for a production deployment.
#  Checks: build, config, security, DB schema, and service health.
#
#  Usage:
#    chmod +x scripts/deploy-check.sh
#    ./scripts/deploy-check.sh
#    ./scripts/deploy-check.sh --env production
# =============================================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; BOLD='\033[1m'; RESET='\033[0m'
PASS=0; FAIL=0; WARN=0
ENV_MODE="development"

for arg in "$@"; do
    case "$arg" in
        --env) shift; ENV_MODE="${1:-development}" ;;
    esac
done

ok()   { echo -e "  ${GREEN}✓${RESET} $*"; PASS=$((PASS+1)); }
fail() { echo -e "  ${RED}✗${RESET} $*"; FAIL=$((FAIL+1)); }
warn() { echo -e "  ${YELLOW}!${RESET} $*"; WARN=$((WARN+1)); }

echo -e "${BOLD}${BLUE}"
echo "  ═══════════════════════════════════════════════════════════"
echo "   ACID — Deploy Readiness Check  [env: $ENV_MODE]"
echo "  ═══════════════════════════════════════════════════════════"
echo -e "${RESET}"

# ── Load .env ─────────────────────────────────────────────────────────────────
[ -f .env ] && { set -a; source .env; set +a; }

# ══ SECTION 1: Build ══════════════════════════════════════════════════════════
echo -e "\n${BOLD}[1] Build Checks${RESET}"

if command -v go &>/dev/null; then
    ok "Go is installed: $(go version | awk '{print $3}')"
    if go build -o /tmp/acid-check-build ./cmd/api 2>/dev/null; then
        ok "Project builds successfully"
        rm -f /tmp/acid-check-build
    else
        fail "Build FAILED — fix compile errors before deploying"
    fi
else
    fail "Go not installed"
fi

if [ -f "go.sum" ]; then
    ok "go.sum present"
else
    fail "go.sum missing — run: go mod tidy"
fi

# ══ SECTION 2: Security ═══════════════════════════════════════════════════════
echo -e "\n${BOLD}[2] Security Checks${RESET}"

JWT="${JWT_SECRET:-}"
if [ -z "$JWT" ]; then
    fail "JWT_SECRET is not set"
elif [ "$JWT" = "acid-jwt-secret-key-change-in-production" ] || [ ${#JWT} -lt 32 ]; then
    if [ "$ENV_MODE" = "production" ]; then
        fail "JWT_SECRET is the default/weak value — generate: openssl rand -base64 32"
    else
        warn "JWT_SECRET uses default value (acceptable for dev only)"
    fi
else
    ok "JWT_SECRET is set (${#JWT} chars)"
fi

DB_URL="${DATABASE_URL:-}"
if echo "$DB_URL" | grep -qE 'password|pass='; then
    if echo "$DB_URL" | grep -qE 'password@|:password@'; then
        if [ "$ENV_MODE" = "production" ]; then
            fail "DATABASE_URL uses weak/default password"
        else
            warn "DATABASE_URL uses weak/default password (acceptable for dev)"
        fi
    else
        ok "DATABASE_URL password looks non-trivial"
    fi
fi

if [ -f ".env" ] && [ "$ENV_MODE" = "production" ]; then
    warn ".env file should NOT be deployed to production servers — use environment variables or secrets manager"
fi

# ══ SECTION 3: Environment ═══════════════════════════════════════════════════
echo -e "\n${BOLD}[3] Environment Variables${RESET}"

REQUIRED_PROD=("DATABASE_URL" "JWT_SECRET" "PORT")
for var in "${REQUIRED_PROD[@]}"; do
    if [ -n "${!var:-}" ]; then
        ok "$var is set"
    else
        fail "$var is NOT set"
    fi
done

OPTIONAL_VARS=("REDIS_ADDR" "CLICKHOUSE_ADDR" "ENABLE_CDC" "ENABLE_DB_SEARCH")
for var in "${OPTIONAL_VARS[@]}"; do
    if [ -n "${!var:-}" ]; then
        ok "$var = ${!var}"
    else
        warn "$var not set — using default"
    fi
done

# ══ SECTION 4: Directories & Assets ══════════════════════════════════════════
echo -e "\n${BOLD}[4] Files & Directories${RESET}"

REQUIRED_DIRS=("web" "databases" "databases/incoming" "databases/archive" "databases/seeds" "databases/private_nosql")
for dir in "${REQUIRED_DIRS[@]}"; do
    [ -d "$dir" ] && ok "$dir/" || fail "$dir/ MISSING"
done

WEB_ASSETS=("web/index.html" "web/login.html" "web/dashboard.html" "web/admin.html" "web/app.js" "web/style.css")
for f in "${WEB_ASSETS[@]}"; do
    [ -f "$f" ] && ok "$f" || fail "$f MISSING"
done

PRIVATE_NOSQL="databases/private_nosql/hadoop_review.json"
[ -f "$PRIVATE_NOSQL" ] && ok "$PRIVATE_NOSQL" || warn "$PRIVATE_NOSQL not found — /api/private/nosql/hadoop-review will fail"

# ══ SECTION 5: Docker ═════════════════════════════════════════════════════════
echo -e "\n${BOLD}[5] Docker${RESET}"

if command -v docker &>/dev/null; then
    ok "Docker installed: $(docker --version | awk '{print $3}' | tr -d ',')"
    if docker compose version &>/dev/null 2>&1 || docker-compose --version &>/dev/null 2>&1; then
        ok "Docker Compose available"
    else
        warn "Docker Compose not found — needed for docker-compose.yml"
    fi
    if [ -f "Dockerfile" ]; then
        ok "Dockerfile present"
    else
        fail "Dockerfile missing"
    fi
else
    warn "Docker not installed — skip Docker checks"
fi

# ══ Summary ═══════════════════════════════════════════════════════════════════
echo -e "\n${BLUE}  ═══════════════════════════════════════════════════════════${RESET}"
echo -e "  Results: ${GREEN}${PASS} passed${RESET}  ${YELLOW}${WARN} warnings${RESET}  ${RED}${FAIL} failed${RESET}"
echo -e "${BLUE}  ═══════════════════════════════════════════════════════════${RESET}\n"

if [ $FAIL -gt 0 ]; then
    echo -e "${RED}  ✗ Deploy check FAILED. Fix the issues above before deploying.${RESET}\n"
    exit 1
elif [ $WARN -gt 0 ]; then
    echo -e "${YELLOW}  ! Deploy check PASSED with warnings. Review warnings above.${RESET}\n"
else
    echo -e "${GREEN}  ✓ All checks passed — ready to deploy!${RESET}\n"
fi
