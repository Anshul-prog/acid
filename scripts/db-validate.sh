#!/usr/bin/env bash
# =============================================================================
#  L.S.D — Database workspace validator
#  Validates migration files: ordering, naming, no duplicate versions.
# =============================================================================
set -euo pipefail

GREEN='\033[0;32m'; RED='\033[0;31m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; RESET='\033[0m'
PASS=0; FAIL=0; WARN=0

ok()   { echo -e "${GREEN}[✓]${RESET} $*"; PASS=$((PASS+1)); }
fail() { echo -e "${RED}[✗]${RESET} $*"; FAIL=$((FAIL+1)); }
warn() { echo -e "${YELLOW}[⚠]${RESET} $*"; WARN=$((WARN+1)); }

MIGRATIONS_DIR="${1:-databases/migrations}"

echo ""
echo "┌──────────────────────────────────────────────────┐"
echo "│   L.S.D — Database Workspace Validator           │"
echo "└──────────────────────────────────────────────────┘"
echo ""
echo "Scanning: $MIGRATIONS_DIR"
echo ""

# ── Directory existence ───────────────────────────────────────────────────────
if [ ! -d "$MIGRATIONS_DIR" ]; then
    fail "Migrations directory '$MIGRATIONS_DIR' not found"
    echo -e "${RED}Cannot proceed — no migration directory.${RESET}"
    exit 1
fi
ok "Migrations directory exists"

# ── Collect files ─────────────────────────────────────────────────────────────
mapfile -t FILES < <(find "$MIGRATIONS_DIR" -name "*.sql" | sort)

if [ ${#FILES[@]} -eq 0 ]; then
    warn "No .sql files found in $MIGRATIONS_DIR"
else
    ok "${#FILES[@]} migration file(s) found"
fi

# ── Naming convention check ───────────────────────────────────────────────────
echo ""
echo "── Naming Convention ─────────────────────────────"
# Expected pattern: NNNN_<description>.sql  (e.g. 0001_create_users.sql)
NAMING_PATTERN='^[0-9]{4}_[a-z0-9_]+\.sql$'
BAD_NAMES=()
for f in "${FILES[@]}"; do
    base=$(basename "$f")
    if ! echo "$base" | grep -qE "$NAMING_PATTERN"; then
        BAD_NAMES+=("$base")
        fail "Bad name: $base (expected NNNN_description.sql)"
    fi
done
if [ ${#BAD_NAMES[@]} -eq 0 ]; then
    ok "All files follow naming convention"
fi

# ── Duplicate version check ────────────────────────────────────────────────────
echo ""
echo "── Duplicate Versions ────────────────────────────"
VERSIONS=()
DUPS=()
for f in "${FILES[@]}"; do
    base=$(basename "$f")
    ver=$(echo "$base" | grep -oE '^[0-9]+' || true)
    if [ -n "$ver" ]; then
        if printf '%s\n' "${VERSIONS[@]}" | grep -q "^$ver$" 2>/dev/null; then
            DUPS+=("$base (version $ver)")
        fi
        VERSIONS+=("$ver")
    fi
done
if [ ${#DUPS[@]} -eq 0 ]; then
    ok "No duplicate version numbers"
else
    for d in "${DUPS[@]}"; do fail "Duplicate version: $d"; done
fi

# ── Sequence gap check ────────────────────────────────────────────────────────
echo ""
echo "── Sequence Gaps ─────────────────────────────────"
SORTED_VERSIONS=($(printf '%s\n' "${VERSIONS[@]}" | sort -n))
if [ ${#SORTED_VERSIONS[@]} -gt 1 ]; then
    PREV=""
    GAP_FOUND=false
    for ver in "${SORTED_VERSIONS[@]}"; do
        num_ver=$((10#$ver))
        if [ -n "$PREV" ]; then
            num_prev=$((10#$PREV))
            expected=$((num_prev + 1))
            if [ "$num_ver" -ne "$expected" ]; then
                warn "Version gap: $PREV → $ver (missing $expected)"
                GAP_FOUND=true
            fi
        fi
        PREV="$ver"
    done
    if ! $GAP_FOUND; then
        ok "No sequence gaps"
    fi
else
    ok "Single or no migrations — no gaps to check"
fi

# ── File size sanity ──────────────────────────────────────────────────────────
echo ""
echo "── File Sizes ────────────────────────────────────"
for f in "${FILES[@]}"; do
    size=$(wc -c < "$f")
    if [ "$size" -eq 0 ]; then
        fail "Empty file: $(basename "$f")"
    fi
done
if [ $FAIL -eq 0 ]; then
    ok "All migration files are non-empty"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "─────────────────────────────────────────────────"
echo -e "  ${GREEN}Passed: $PASS${RESET}  |  ${YELLOW}Warnings: $WARN${RESET}  |  ${RED}Failed: $FAIL${RESET}"
echo "─────────────────────────────────────────────────"
echo ""

[ "$FAIL" -eq 0 ] || exit 1
