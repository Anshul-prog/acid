CREATE DATABASE IF NOT EXISTS lsd;

-- Search Index Tables (for each database)
CREATE TABLE IF NOT EXISTS lsd.search_users (
    global_id UInt64 CODEC(ZSTD(3)),
    original_data String CODEC(ZSTD(3)),
    is_deleted UInt8 DEFAULT 0,
    synced_at DateTime CODEC(DoubleDelta, ZSTD(3)),
    updated_at DateTime CODEC(DoubleDelta, ZSTD(3))
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY toYYYYMM(updated_at)
ORDER BY (global_id)
SETTINGS index_granularity = 16384;

-- Token Stream for full-text search
CREATE TABLE IF NOT EXISTS lsd.search_token_entity (
    token_hash UInt64,
    token String,
    global_id UInt64,
    table_name String DEFAULT '',
    updated_at DateTime DEFAULT now()
) ENGINE = MergeTree()
ORDER BY (token_hash, global_id)
SETTINGS index_granularity = 8192;

-- Bitmap index for fast intersections
CREATE TABLE IF NOT EXISTS lsd.search_token_bitmap (
    token_hash UInt64,
    ids_bitmap AggregateFunction(groupBitmap, UInt64),
    updated_at DateTime DEFAULT now()
) ENGINE = AggregatingMergeTree()
ORDER BY (token_hash)
SETTINGS index_granularity = 16384;

-- Token stats for search suggestions
CREATE TABLE IF NOT EXISTS lsd.search_token_stats (
    token_hash UInt64,
    token String,
    table_name String DEFAULT '',
    count UInt64 DEFAULT 0,
    updated_at DateTime DEFAULT now()
) ENGINE = SummingMergeTree()
PARTITION BY toYYYYMM(updated_at)
ORDER BY (token_hash, table_name)
SETTINGS index_granularity = 16384;

-- Dead letter table for failed indexing
CREATE TABLE IF NOT EXISTS lsd.search_index_errors (
    table_name String,
    start_id UInt64,
    end_id UInt64,
    attempts UInt8,
    last_error String,
    sample_data String,
    created_at DateTime DEFAULT now()
) ENGINE = MergeTree()
ORDER BY (table_name, created_at)
SETTINGS index_granularity = 8192;

-- Entity layer table for cross-db search
CREATE TABLE IF NOT EXISTS lsd.entity_registry (
    entity_id UInt64,
    entity_type String,
    entity_value String,
    source_db String,
    source_table String,
    source_column String,
    updated_at DateTime DEFAULT now()
) ENGINE = MergeTree()
ORDER BY (entity_type, entity_value, source_db)
SETTINGS index_granularity = 16384;

-- Create materialized views for token aggregation
CREATE MATERIALIZED VIEW IF NOT EXISTS lsd.mv_search_token_bitmap
ENGINE = AggregatingMergeTree()
PARTITION BY toYYYYMM(updated_at)
ORDER BY (token_hash)
AS SELECT
    token_hash,
    groupBitmapState(global_id) as ids_bitmap,
    max(updated_at) as updated_at
FROM lsd.search_token_entity
GROUP BY token_hash;

-- Optimize settings
ALTER TABLE lsd.search_users FINAL;
ALTER TABLE lsd.search_token_entity FINAL;
ALTER TABLE lsd.search_token_stats FINAL;