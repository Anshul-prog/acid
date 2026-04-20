-- =============================================================================
--  L.S.D — Pig Latin ETL Transformation Scripts
-- =============================================================================
--  Apache Pig 0.17+ on Hadoop 3.x
--
--  Run:
--    pig -x mapreduce hadoop/pig-etl.pig
--    pig -x mapreduce -param input=/lsd/raw/entities hadoop/pig-etl.pig
--
--  These scripts perform heavy ETL transformations that would be too complex
--  or slow to run in Hive/SQL. Pig's dataflow model makes chained
--  transformations and custom UDFs easy to express.
-- =============================================================================

-- =============================================================================
-- SCRIPT 1: Entity Deduplication via ID Hash
-- Finds entities that share the same identity anchor hash (exact duplicates)
-- and prepares a merge recommendation file.
-- =============================================================================

-- Load identity anchors from HDFS
anchors = LOAD 'hdfs://namenode:9000/lsd/raw/entities/identity_anchors'
    USING parquet.pig.ParquetLoader()
    AS (id:long, entity_id:long, id_type:chararray,
        id_number_hash:chararray, issuing_country:chararray, is_verified:boolean);

-- Group by (id_type, id_number_hash) to find collisions
anchors_grouped = GROUP anchors BY (id_type, id_number_hash);

-- Find all groups with more than one distinct entity_id
duplicates = FOREACH anchors_grouped {
    entity_ids = anchors.entity_id;
    unique_entities = DISTINCT entity_ids;
    GENERATE
        group.id_type                   AS id_type,
        group.id_number_hash            AS id_hash,
        unique_entities                 AS entity_ids,
        COUNT(unique_entities)          AS dup_count;
}

dup_candidates = FILTER duplicates BY dup_count > 1L;

-- Flatten to (entity_id_a, entity_id_b, id_type, id_hash) pairs
dup_pairs = FOREACH dup_candidates {
    pairs = CROSS entity_ids, entity_ids;
    GENERATE
        FLATTEN(pairs) AS (entity_a:long, entity_b:long),
        id_type, id_hash;
}

-- Filter: only keep A < B to avoid symmetric duplicates
clean_pairs = FILTER dup_pairs BY entity_a < entity_b;

-- Store results for downstream processing (Hive merge or Go CDC)
STORE clean_pairs INTO 'hdfs://namenode:9000/lsd/processed/entity_duplicates'
    USING PigStorage(',');

-- =============================================================================
-- SCRIPT 2: Full-Text Token Extraction for Search Index Building
-- Extracts all searchable tokens from entity records, normalizes them,
-- and produces a token → (entity_id, frequency) output for MapReduce indexer.
-- =============================================================================

-- Load entities
entities = LOAD 'hdfs://namenode:9000/lsd/raw/entities/entities'
    USING parquet.pig.ParquetLoader()
    AS (id:long, first_name:chararray, last_name:chararray,
        primary_email:chararray, primary_phone:chararray,
        city:chararray, state:chararray, country:chararray);

-- Combine all searchable text fields into a single string
entity_text = FOREACH entities GENERATE
    id AS entity_id,
    LOWER(CONCAT_WS(' ',
        (first_name   IS NOT NULL ? first_name   : ''),
        (last_name    IS NOT NULL ? last_name    : ''),
        (primary_email IS NOT NULL ? primary_email : ''),
        (primary_phone IS NOT NULL ? primary_phone : ''),
        (city         IS NOT NULL ? city         : ''),
        (state        IS NOT NULL ? state        : ''),
        (country      IS NOT NULL ? country      : '')
    )) AS searchable_text;

-- Tokenize: split on whitespace & punctuation
-- TOKENIZE splits on whitespace; for a more sophisticated tokenizer,
-- replace with a custom Java UDF.
tokenized = FOREACH entity_text GENERATE
    entity_id,
    FLATTEN(TOKENIZE(searchable_text, ' .,;:@-_')) AS token;

-- Normalize: lowercase, filter short/noise tokens
normalized = FOREACH tokenized GENERATE
    entity_id,
    LOWER(TRIM(token)) AS token;

clean_tokens = FILTER normalized BY
    token IS NOT NULL AND
    SIZE(token) >= 2L AND
    SIZE(token) <= 50L;

-- Deduplicate per entity
deduped_per_entity = DISTINCT clean_tokens;

-- Group by token → list of entity_ids
token_index = GROUP deduped_per_entity BY token;

inverted_index = FOREACH token_index GENERATE
    group                           AS token,
    deduped_per_entity.entity_id    AS entity_ids,
    COUNT(deduped_per_entity)       AS frequency;

-- Store as tab-separated for MapReduce to consume
STORE inverted_index INTO 'hdfs://namenode:9000/lsd/processed/token_index_raw'
    USING PigStorage('\t');

-- =============================================================================
-- SCRIPT 3: Social Network Risk Propagation
-- Entities that share social handles or emails with flagged/high-risk entities
-- receive an elevated risk score propagation.
-- =============================================================================

-- Load social accounts
social = LOAD 'hdfs://namenode:9000/lsd/raw/entities/social_accounts'
    USING parquet.pig.ParquetLoader()
    AS (id:long, entity_id:long, platform:chararray, handle:chararray, is_active:boolean);

-- Load entity risk scores
entities_risk = LOAD 'hdfs://namenode:9000/lsd/raw/entities/entities'
    USING parquet.pig.ParquetLoader()
    AS (id:long, risk_score:int, status:chararray);

-- Find shared handles across different entities
social_active = FILTER social BY is_active == true;
social_grouped = GROUP social_active BY (platform, handle);

shared_handles = FOREACH social_grouped {
    unique_entities = DISTINCT social_active.entity_id;
    GENERATE
        group.platform          AS platform,
        group.handle            AS handle,
        unique_entities         AS sharing_entities,
        COUNT(unique_entities)  AS shared_count;
}

-- Only handles shared by 2+ entities
risk_network = FILTER shared_handles BY shared_count > 1L;

STORE risk_network INTO 'hdfs://namenode:9000/lsd/processed/social_risk_network'
    USING PigStorage(',');

-- =============================================================================
-- SCRIPT 4: Financial Flow Analysis
-- Aggregates bank account totals per entity and computes net_credit_debit_ratio
-- as a risk signal input.
-- =============================================================================

bank = LOAD 'hdfs://namenode:9000/lsd/raw/entities/bank_accounts'
    USING parquet.pig.ParquetLoader()
    AS (id:long, entity_id:long, bank_name:chararray, account_type:chararray,
        total_credit:double, total_debit:double, is_active:boolean);

active_bank = FILTER bank BY is_active == true;
bank_grouped = GROUP active_bank BY entity_id;

financial_summary = FOREACH bank_grouped GENERATE
    group AS entity_id,
    COUNT(active_bank)              AS account_count,
    SUM(active_bank.total_credit)   AS total_credit,
    SUM(active_bank.total_debit)    AS total_debit,
    (SUM(active_bank.total_credit) - SUM(active_bank.total_debit)) AS net_balance;

-- Flag entities with debit > credit (potential cash drain / fraud signal)
high_debit = FILTER financial_summary BY
    total_debit > total_credit AND total_credit > 0.0;

STORE high_debit INTO 'hdfs://namenode:9000/lsd/processed/financial_risk_signals'
    USING PigStorage(',');
