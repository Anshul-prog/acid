-- =============================================================================
-- MIGRATION 003: Multi-Filter Tagging System & Search Infrastructure
-- =============================================================================
-- Enhances the existing categories system with:
--   1. Per-entity tag assignments (entities table FK)
--   2. Optimized multi-filter search function
--   3. Tag statistics materialized view
-- Prerequisite: Migrations 001 + 002 must be applied first.
-- =============================================================================

BEGIN;

-- ── Extend categories table with tag semantics ────────────────────────────────
-- Migrate existing 'employee' entity_type to support generic entities too
ALTER TABLE categories
    ADD COLUMN IF NOT EXISTS tag_group     VARCHAR(50) DEFAULT 'role',
    ADD COLUMN IF NOT EXISTS is_system_tag BOOLEAN     DEFAULT false,
    ADD COLUMN IF NOT EXISTS sort_order    INTEGER     DEFAULT 0;

-- Add check for common dev/org tags the user specified
INSERT INTO categories (name, description, color, entity_type, icon, tag_group)
VALUES
    ('sdk',         'SDK / Library contributor',          '#6366f1', 'entity', '📦', 'skill'),
    ('frontend',    'Frontend / UI developer',            '#3b82f6', 'entity', '🎨', 'skill'),
    ('backend',     'Backend / Systems developer',        '#10b981', 'entity', '⚙️',  'skill'),
    ('manager',     'Engineering / Project Manager',      '#f59e0b', 'entity', '📋', 'role'),
    ('devops',      'DevOps / Infrastructure engineer',   '#ef4444', 'entity', '🚀', 'skill'),
    ('data-eng',    'Data Engineer / ML Ops',             '#8b5cf6', 'entity', '📊', 'skill'),
    ('legal',       'Legal / Compliance personnel',       '#14b8a6', 'entity', '⚖️',  'role'),
    ('finance',     'Finance / Accounting',               '#f97316', 'entity', '💰', 'role'),
    ('suspect',     'Under active investigation',         '#dc2626', 'entity', '🔍', 'status'),
    ('cleared',     'Cleared / No findings',              '#22c55e', 'entity', '✅', 'status'),
    ('flagged',     'Requires immediate review',          '#f97316', 'entity', '🚩', 'status'),
    ('vip',         'High-value / VIP individual',        '#a855f7', 'entity', '⭐', 'status')
ON CONFLICT (name) DO NOTHING;

-- ── entity_tags: Direct assign categories/tags to `entities` table rows ───────
-- This is separate from entity_categories (which is for generic entity_type strings).
-- entity_tags is FK-strict to entities.id — allows DB-level integrity.
CREATE TABLE IF NOT EXISTS entity_tags (
    id          BIGSERIAL   PRIMARY KEY,
    entity_id   BIGINT      NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    category_id INTEGER     NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    assigned_by INTEGER     REFERENCES users(id),
    assigned_at TIMESTAMPTZ DEFAULT NOW(),
    note        TEXT,
    UNIQUE(entity_id, category_id)
);

CREATE INDEX IF NOT EXISTS idx_entity_tags_entity_id   ON entity_tags(entity_id);
CREATE INDEX IF NOT EXISTS idx_entity_tags_category_id ON entity_tags(category_id);
CREATE INDEX IF NOT EXISTS idx_entity_tags_assigned_by ON entity_tags(assigned_by);
CREATE INDEX IF NOT EXISTS idx_categories_tag_group    ON categories(tag_group);

-- ── Multi-filter search function ──────────────────────────────────────────────
-- Combines:
--   - Full-text search across name, email, phone
--   - Category/tag filter (ANY or ALL mode)
--   - Status filter
--   - Date-of-birth range filter
--   - Pagination via cursor
-- Returns entities matching ALL supplied criteria.
CREATE OR REPLACE FUNCTION search_entities_multi_filter(
    p_search_term   TEXT        DEFAULT NULL,
    p_category_ids  INTEGER[]   DEFAULT NULL,   -- filter by category IDs (AND logic)
    p_tag_mode      VARCHAR     DEFAULT 'ANY',   -- 'ANY' = OR, 'ALL' = AND across categories
    p_status        VARCHAR[]   DEFAULT NULL,    -- e.g. ARRAY['active','flagged']
    p_dob_from      DATE        DEFAULT NULL,
    p_dob_to        DATE        DEFAULT NULL,
    p_risk_min      SMALLINT    DEFAULT NULL,
    p_risk_max      SMALLINT    DEFAULT NULL,
    p_page_size     INTEGER     DEFAULT 20,
    p_offset        INTEGER     DEFAULT 0
) RETURNS TABLE (
    entity_id       BIGINT,
    first_name      VARCHAR,
    last_name       VARCHAR,
    primary_email   VARCHAR,
    primary_phone   VARCHAR,
    date_of_birth   DATE,
    risk_score      SMALLINT,
    status          VARCHAR,
    city            VARCHAR,
    state           VARCHAR,
    category_names  TEXT[],
    identity_types  TEXT[],
    rank            REAL,
    total_count     BIGINT
) AS $$
DECLARE
    v_tsquery  TSQUERY;
    v_cat_count INTEGER := 0;
BEGIN
    -- Build tsquery from search term
    IF p_search_term IS NOT NULL AND trim(p_search_term) != '' THEN
        v_tsquery := plainto_tsquery('english', p_search_term);
    END IF;

    IF p_category_ids IS NOT NULL THEN
        v_cat_count := array_length(p_category_ids, 1);
    END IF;

    RETURN QUERY
    WITH base AS (
        SELECT
            e.id,
            e.first_name,
            e.last_name,
            e.primary_email,
            e.primary_phone,
            e.date_of_birth,
            e.risk_score,
            e.status,
            e.city,
            e.state,
            -- Category names as array
            (SELECT array_agg(c.name ORDER BY c.name)
             FROM entity_tags et
             JOIN categories c ON c.id = et.category_id
             WHERE et.entity_id = e.id)                   AS category_names,
            -- Identity types as array
            (SELECT array_agg(DISTINCT a.id_type ORDER BY a.id_type)
             FROM entity_identity_anchors a
             WHERE a.entity_id = e.id)                    AS identity_types,
            -- Full-text rank score
            CASE WHEN v_tsquery IS NOT NULL THEN
                ts_rank_cd(
                    to_tsvector('english',
                        coalesce(e.first_name, '') || ' ' ||
                        coalesce(e.last_name, '') || ' ' ||
                        coalesce(e.primary_email, '') || ' ' ||
                        coalesce(e.primary_phone, '') || ' ' ||
                        coalesce(e.city, '') || ' ' ||
                        coalesce(e.permanent_address, '')
                    ),
                    v_tsquery,
                    32  -- normalization: divide by doc length
                )
            ELSE 1.0 END                                  AS rank
        FROM entities e
        WHERE
            -- 1. Full-text match
            (v_tsquery IS NULL OR to_tsvector('english',
                coalesce(e.first_name,'') || ' ' ||
                coalesce(e.last_name,'') || ' ' ||
                coalesce(e.primary_email,'') || ' ' ||
                coalesce(e.primary_phone,'') || ' ' ||
                coalesce(e.city,'') || ' ' ||
                coalesce(e.permanent_address,'')
            ) @@ v_tsquery)

            -- 2. Status filter
            AND (p_status IS NULL OR e.status = ANY(p_status))

            -- 3. Date of birth range
            AND (p_dob_from IS NULL OR e.date_of_birth >= p_dob_from)
            AND (p_dob_to   IS NULL OR e.date_of_birth <= p_dob_to)

            -- 4. Risk score range
            AND (p_risk_min IS NULL OR e.risk_score >= p_risk_min)
            AND (p_risk_max IS NULL OR e.risk_score <= p_risk_max)

            -- 5. Category / tag filter
            AND (
                p_category_ids IS NULL
                OR (
                    CASE p_tag_mode
                    WHEN 'ALL' THEN
                        -- Entity must have ALL requested categories
                        (SELECT count(DISTINCT et.category_id)
                         FROM entity_tags et
                         WHERE et.entity_id = e.id
                           AND et.category_id = ANY(p_category_ids)) = v_cat_count
                    ELSE
                        -- Entity must have AT LEAST ONE of the requested categories
                        EXISTS (
                            SELECT 1 FROM entity_tags et
                            WHERE et.entity_id = e.id
                              AND et.category_id = ANY(p_category_ids)
                        )
                    END
                )
            )
    ),
    counted AS (
        SELECT *, count(*) OVER() AS total_count FROM base
        ORDER BY rank DESC, id DESC
        LIMIT p_page_size OFFSET p_offset
    )
    SELECT
        c.id, c.first_name, c.last_name, c.primary_email, c.primary_phone,
        c.date_of_birth, c.risk_score, c.status::VARCHAR, c.city, c.state,
        c.category_names, c.identity_types, c.rank, c.total_count
    FROM counted c;
END;
$$ LANGUAGE plpgsql STABLE;

-- ── Tag statistics view ───────────────────────────────────────────────────────
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_tag_stats AS
SELECT
    c.id                AS category_id,
    c.name              AS tag_name,
    c.color,
    c.icon,
    c.tag_group,
    c.entity_type,
    COUNT(et.entity_id) AS entity_count,
    MAX(et.assigned_at) AS last_assigned_at
FROM categories c
LEFT JOIN entity_tags et ON et.category_id = c.id
GROUP BY c.id, c.name, c.color, c.icon, c.tag_group, c.entity_type;

CREATE UNIQUE INDEX IF NOT EXISTS idx_mv_tag_stats_category_id ON mv_tag_stats(category_id);

-- Refresh this view after bulk tag operations:
-- REFRESH MATERIALIZED VIEW CONCURRENTLY mv_tag_stats;

-- ── Grants ────────────────────────────────────────────────────────────────────
GRANT SELECT, INSERT, UPDATE, DELETE ON entity_tags TO acid;
GRANT USAGE, SELECT ON SEQUENCE entity_tags_id_seq TO acid;
GRANT EXECUTE ON FUNCTION search_entities_multi_filter TO acid;
GRANT SELECT ON mv_tag_stats TO acid;

COMMIT;

DO $$
BEGIN
    RAISE NOTICE '✅ Migration 003 applied successfully';
END $$;
