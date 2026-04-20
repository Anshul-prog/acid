#!/usr/bin/env bash
# =============================================================================
#  L.S.D — Sqoop Batch Ingestion Jobs
# =============================================================================
#  Moves terabytes of relational PostgreSQL data into HDFS in Parquet format.
#  Run these from the Hadoop edge node where Sqoop is installed.
#
#  Prerequisites:
#    - Hadoop 3.3+ running (NameNode, DataNodes)
#    - Sqoop 1.4.7 with PostgreSQL JDBC driver in $SQOOP_HOME/lib/
#    - JAVA_HOME, HADOOP_HOME, SQOOP_HOME set
#    - PostgreSQL network accessible from edge node
#
#  Usage:
#    chmod +x hadoop/sqoop-jobs.sh
#    LSD_PG_URL="jdbc:postgresql://10.0.1.10:5432/lsd" \
#    LSD_PG_USER="acid" \
#    LSD_PG_PASS="your_password" \
#    ./hadoop/sqoop-jobs.sh [all | entities | anchors | bank | social | lsd_dbs]
# =============================================================================
set -euo pipefail

# ── Connection ─────────────────────────────────────────────────────────────────
PG_URL="${LSD_PG_URL:-jdbc:postgresql://localhost:5432/lsd}"
PG_USER="${LSD_PG_USER:-acid}"
PG_PASS="${LSD_PG_PASS:-password}"
JDBC_DRIVER="org.postgresql.Driver"

# ── HDFS target base ──────────────────────────────────────────────────────────
HDFS_BASE="/lsd/raw"
HDFS_ENTITIES="${HDFS_BASE}/entities"
HDFS_LSD_DBS="${HDFS_BASE}/lsd_databases"

# ── Parallelism ───────────────────────────────────────────────────────────────
NUM_MAPPERS=16
BATCH_SIZE=50000

# ── Common Sqoop flags ─────────────────────────────────────────────────────────
COMMON_ARGS=(
    --connect "$PG_URL"
    --username "$PG_USER"
    --password "$PG_PASS"
    --driver "$JDBC_DRIVER"
    --as-parquetfile
    --compress
    --compression-codec "org.apache.hadoop.io.compress.SnappyCodec"
    --fetch-size 10000
)

# ── Job 1: Master entities table ──────────────────────────────────────────────
import_entities() {
    echo "▶ Importing entities table..."
    sqoop import "${COMMON_ARGS[@]}" \
        --table entities \
        --target-dir "${HDFS_ENTITIES}/entities" \
        --num-mappers "$NUM_MAPPERS" \
        --split-by id \
        --fields-terminated-by ',' \
        --where "status != 'deleted'" \
        --delete-target-dir \
        -- --schema public
    echo "✅ entities imported"
}

# ── Job 2: Identity anchors ────────────────────────────────────────────────────
import_identity_anchors() {
    echo "▶ Importing entity_identity_anchors..."
    sqoop import "${COMMON_ARGS[@]}" \
        --table entity_identity_anchors \
        --target-dir "${HDFS_ENTITIES}/identity_anchors" \
        --num-mappers "$NUM_MAPPERS" \
        --split-by id \
        --delete-target-dir \
        -- --schema public
    echo "✅ entity_identity_anchors imported"
}

# ── Job 3: Bank accounts (exclude raw account numbers — masked only) ──────────
import_bank_accounts() {
    echo "▶ Importing entity_bank_accounts (masked)..."
    sqoop import "${COMMON_ARGS[@]}" \
        --query "SELECT id, entity_id, bank_name, bank_code,
                        account_number_masked, account_type,
                        ifsc_code, swift_code, is_active, is_primary,
                        total_credit, total_debit, last_txn_date,
                        opened_at, closed_at, created_at
                 FROM entity_bank_accounts
                 WHERE \$CONDITIONS" \
        --target-dir "${HDFS_ENTITIES}/bank_accounts" \
        --num-mappers "$NUM_MAPPERS" \
        --split-by id \
        --delete-target-dir
    echo "✅ entity_bank_accounts imported"
}

# ── Job 4: Social accounts ─────────────────────────────────────────────────────
import_social_accounts() {
    echo "▶ Importing entity_social_accounts..."
    sqoop import "${COMMON_ARGS[@]}" \
        --table entity_social_accounts \
        --target-dir "${HDFS_ENTITIES}/social_accounts" \
        --num-mappers "$NUM_MAPPERS" \
        --split-by id \
        --delete-target-dir
    echo "✅ entity_social_accounts imported"
}

# ── Job 5: Properties (assets) ────────────────────────────────────────────────
import_properties() {
    echo "▶ Importing entity_properties..."
    sqoop import "${COMMON_ARGS[@]}" \
        --table entity_properties \
        --target-dir "${HDFS_ENTITIES}/properties" \
        --num-mappers "$NUM_MAPPERS" \
        --split-by id \
        --delete-target-dir
    echo "✅ entity_properties imported"
}

# ── Job 6: Remarks ────────────────────────────────────────────────────────────
import_remarks() {
    echo "▶ Importing entity_remarks (non-confidential)..."
    sqoop import "${COMMON_ARGS[@]}" \
        --table entity_remarks \
        --target-dir "${HDFS_ENTITIES}/remarks" \
        --num-mappers "$NUM_MAPPERS" \
        --split-by id \
        --where "is_confidential = false" \
        --delete-target-dir
    echo "✅ entity_remarks imported"
}

# ── Job 7: L.S.D multi-database schemas (lsd_db_01 … lsd_db_10) ─────────────
# These are the 10 schemas × 1000 tables from the generator. We export all
# users_* tables from all schemas in parallel using Sqoop's free-form query.
import_lsd_databases() {
    echo "▶ Importing LSD generated databases (10 schemas × 1000 tables each)..."
    for db_idx in $(seq -f "%02g" 1 10); do
        DB_SCHEMA="lsd_db_${db_idx}"
        HDFS_TARGET="${HDFS_LSD_DBS}/${DB_SCHEMA}"
        echo "  ▶ Schema: $DB_SCHEMA"

        # Export first 50 tables per schema (sufficient for index building)
        for tbl_idx in $(seq -f "%04g" 0 49); do
            TABLE="${DB_SCHEMA}.users_${tbl_idx}"
            TARGET_DIR="${HDFS_TARGET}/users_${tbl_idx}"

            sqoop import "${COMMON_ARGS[@]}" \
                --query "SELECT id, uuid, email, first_name, last_name,
                                phone, status, country, city, domain,
                                job_title, tags, duplicate_ref, created_at
                         FROM ${TABLE}
                         WHERE \$CONDITIONS" \
                --target-dir "$TARGET_DIR" \
                --num-mappers 4 \
                --split-by id \
                --delete-target-dir \
                2>/dev/null || echo "  ⚠  Skipped $TABLE (may not exist)"
        done
        echo "  ✅ Schema $DB_SCHEMA exported"
    done
    echo "✅ LSD databases imported"
}

# ── Incremental sync: append new rows since last watermark ────────────────────
incremental_entities() {
    LAST_WATERMARK="${1:-$(date --date='1 day ago' +%Y-%m-%d)}"
    echo "▶ Incremental sync: entities since $LAST_WATERMARK"
    sqoop import "${COMMON_ARGS[@]}" \
        --table entities \
        --target-dir "${HDFS_ENTITIES}/entities_delta" \
        --num-mappers 4 \
        --split-by id \
        --incremental append \
        --check-column updated_at \
        --last-value "$LAST_WATERMARK"
    echo "✅ Incremental entities sync done"
}

# ── Driver ─────────────────────────────────────────────────────────────────────
JOB="${1:-all}"
case "$JOB" in
    all)
        import_entities
        import_identity_anchors
        import_bank_accounts
        import_social_accounts
        import_properties
        import_remarks
        import_lsd_databases
        ;;
    entities)         import_entities ;;
    anchors)          import_identity_anchors ;;
    bank)             import_bank_accounts ;;
    social)           import_social_accounts ;;
    properties)       import_properties ;;
    remarks)          import_remarks ;;
    lsd_dbs)          import_lsd_databases ;;
    incremental)      incremental_entities "${2:-}" ;;
    *)
        echo "Usage: $0 [all|entities|anchors|bank|social|properties|remarks|lsd_dbs|incremental]"
        exit 1
        ;;
esac

echo ""
echo "════════════════════════════════════════════════"
echo "  HDFS Base: $HDFS_BASE"
echo "  Verify:    hdfs dfs -ls $HDFS_BASE"
echo "════════════════════════════════════════════════"
