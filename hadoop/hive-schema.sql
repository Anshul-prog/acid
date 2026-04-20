-- =============================================================================
--  L.S.D — Hive OLAP Intelligence Warehouse Schema
-- =============================================================================
--  Hive 3.x (on Hadoop 3.x)
--  Format: ORC with Snappy compression for OLAP workloads
--  Partitioning strategy: by year/month for time-series; by country for entity
--
--  Initialize: hive -f hadoop/hive-schema.sql
-- =============================================================================

-- Use dedicated database
CREATE DATABASE IF NOT EXISTS lsd_warehouse
    COMMENT 'L.S.D Intelligence OLAP Warehouse'
    LOCATION 'hdfs://namenode:9000/lsd/warehouse';

USE lsd_warehouse;

-- =============================================================================
-- External tables (point to Sqoop Parquet exports — do NOT own the data)
-- =============================================================================

-- ── Raw entities (from Sqoop export) ──────────────────────────────────────────
CREATE EXTERNAL TABLE IF NOT EXISTS raw_entities (
    id                  BIGINT,
    first_name          STRING,
    last_name           STRING,
    middle_name         STRING,
    date_of_birth       DATE,
    gender              STRING,
    nationality         STRING,
    primary_email       STRING,
    primary_phone       STRING,
    permanent_address   STRING,
    city                STRING,
    state               STRING,
    country             STRING,
    pincode             STRING,
    risk_score          INT,
    status              STRING,
    source_system       STRING,
    created_at          TIMESTAMP,
    updated_at          TIMESTAMP
)
STORED AS PARQUET
LOCATION 'hdfs://namenode:9000/lsd/raw/entities/entities'
TBLPROPERTIES ('parquet.compress'='SNAPPY');

-- ── Raw identity anchors ──────────────────────────────────────────────────────
CREATE EXTERNAL TABLE IF NOT EXISTS raw_identity_anchors (
    id                  BIGINT,
    entity_id           BIGINT,
    id_type             STRING,
    id_number           STRING,
    id_number_hash      STRING,
    issuing_country     STRING,
    issuing_authority   STRING,
    issue_date          DATE,
    expiry_date         DATE,
    is_verified         BOOLEAN,
    created_at          TIMESTAMP
)
STORED AS PARQUET
LOCATION 'hdfs://namenode:9000/lsd/raw/entities/identity_anchors'
TBLPROPERTIES ('parquet.compress'='SNAPPY');

-- ── Raw bank accounts ─────────────────────────────────────────────────────────
CREATE EXTERNAL TABLE IF NOT EXISTS raw_bank_accounts (
    id                  BIGINT,
    entity_id           BIGINT,
    bank_name           STRING,
    bank_code           STRING,
    account_number_masked STRING,
    account_type        STRING,
    ifsc_code           STRING,
    is_active           BOOLEAN,
    is_primary          BOOLEAN,
    total_credit        DOUBLE,
    total_debit         DOUBLE,
    last_txn_date       DATE,
    opened_at           DATE,
    created_at          TIMESTAMP
)
STORED AS PARQUET
LOCATION 'hdfs://namenode:9000/lsd/raw/entities/bank_accounts'
TBLPROPERTIES ('parquet.compress'='SNAPPY');

-- ── Raw social accounts ───────────────────────────────────────────────────────
CREATE EXTERNAL TABLE IF NOT EXISTS raw_social_accounts (
    id                  BIGINT,
    entity_id           BIGINT,
    platform            STRING,
    handle              STRING,
    profile_url         STRING,
    display_name        STRING,
    follower_count      INT,
    is_verified         BOOLEAN,
    is_active           BOOLEAN,
    risk_flags          ARRAY<STRING>,
    created_at          TIMESTAMP
)
STORED AS PARQUET
LOCATION 'hdfs://namenode:9000/lsd/raw/entities/social_accounts'
TBLPROPERTIES ('parquet.compress'='SNAPPY');

-- ── LSD Generated Databases (lsd_db_01 … lsd_db_10 sample data) ─────────────
CREATE EXTERNAL TABLE IF NOT EXISTS raw_lsd_users (
    id              BIGINT,
    uuid            STRING,
    email           STRING,
    first_name      STRING,
    last_name       STRING,
    phone           STRING,
    status          STRING,
    country         STRING,
    city            STRING,
    domain          STRING,
    job_title       STRING,
    tags            ARRAY<STRING>,
    duplicate_ref   STRING,
    created_at      TIMESTAMP,
    source_schema   STRING    -- partition key: lsd_db_01 … lsd_db_10
)
PARTITIONED BY (source_schema STRING)
STORED AS PARQUET
LOCATION 'hdfs://namenode:9000/lsd/raw/lsd_databases'
TBLPROPERTIES ('parquet.compress'='SNAPPY');

-- Discover partitions automatically
MSCK REPAIR TABLE raw_lsd_users;

-- =============================================================================
-- Managed ORC tables (Hive owns the data — optimized for OLAP)
-- =============================================================================

-- ── Entity intelligence warehouse table (partitioned by country, year) ─────────
CREATE TABLE IF NOT EXISTS wh_entities (
    entity_id           BIGINT,
    full_name           STRING,
    date_of_birth       DATE,
    gender              STRING,
    primary_email       STRING,
    primary_phone       STRING,
    city                STRING,
    state               STRING,
    risk_score          INT,
    status              STRING,
    id_types            ARRAY<STRING>,  -- list of verified ID types
    bank_count          INT,
    social_count        INT,
    property_count      INT,
    net_asset_value     DOUBLE,
    created_at          TIMESTAMP
)
COMMENT 'Entity intelligence cube - partitioned by country and year'
PARTITIONED BY (country STRING, year INT)
CLUSTERED BY (entity_id) INTO 256 BUCKETS
STORED AS ORC
TBLPROPERTIES (
    'orc.compress'='SNAPPY',
    'orc.bloom.filter.columns'='primary_email,primary_phone,risk_score',
    'transactional'='true'
);

-- ── Populate warehouse from raw (run daily via Oozie/Airflow) ─────────────────
-- INSERT OVERWRITE TABLE wh_entities PARTITION (country, year)
-- SELECT
--     e.id,
--     trim(e.first_name || ' ' || coalesce(e.last_name, '')),
--     e.date_of_birth, e.gender,
--     e.primary_email, e.primary_phone,
--     e.city, e.state, e.risk_score, e.status,
--     collect_set(a.id_type),
--     count(DISTINCT b.id),
--     count(DISTINCT s.id),
--     count(DISTINCT p.id),
--     sum(coalesce(p.current_value, 0)),
--     e.created_at,
--     coalesce(e.country, 'UNKNOWN'),
--     year(e.created_at)
-- FROM raw_entities e
-- LEFT JOIN raw_identity_anchors a ON a.entity_id = e.id
-- LEFT JOIN raw_bank_accounts    b ON b.entity_id = e.id AND b.is_active
-- LEFT JOIN raw_social_accounts  s ON s.entity_id = e.id AND s.is_active
-- GROUP BY e.id, e.first_name, e.last_name, e.date_of_birth, e.gender,
--          e.primary_email, e.primary_phone, e.city, e.state, e.risk_score,
--          e.status, e.created_at, e.country;

-- ── Duplicate detection table ──────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS wh_entity_duplicates (
    entity_id_a     BIGINT,
    entity_id_b     BIGINT,
    match_type      STRING,  -- 'EXACT_ID', 'FUZZY_NAME', 'PHONE_EMAIL'
    similarity      DOUBLE,
    detected_at     TIMESTAMP
)
STORED AS ORC
TBLPROPERTIES ('orc.compress'='SNAPPY');

-- ── Risk analytics table ───────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS wh_risk_analytics
PARTITIONED BY (analysis_date DATE)
STORED AS ORC
TBLPROPERTIES ('orc.compress'='SNAPPY')
AS
SELECT
    e.status,
    e.country,
    floor(e.risk_score / 10) * 10 AS risk_bucket,
    count(*) AS entity_count,
    avg(e.risk_score) AS avg_risk,
    current_date() AS analysis_date
FROM raw_entities e
GROUP BY e.status, e.country, floor(e.risk_score / 10) * 10;

-- ── Useful analytical queries ─────────────────────────────────────────────────
-- Top 10 high-risk entities by country
-- SELECT country, full_name, risk_score, id_types
-- FROM wh_entities
-- WHERE risk_score >= 80 AND country = 'IN'
-- ORDER BY risk_score DESC
-- LIMIT 10;

-- Entities with multiple bank accounts (potential flag)
-- SELECT entity_id, full_name, bank_count, risk_score
-- FROM wh_entities
-- WHERE bank_count >= 5
-- ORDER BY bank_count DESC;
