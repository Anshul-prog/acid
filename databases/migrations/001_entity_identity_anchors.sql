-- =============================================================================
-- MIGRATION 001: Entity Identity Anchors (Government ID System)
-- =============================================================================
-- Purpose: Establish a master entity table and attach immutable Government ID
--          anchors (Passport, Aadhaar, PAN, Driving License, Voter ID) as the
--          canonical deduplication key across the LSD platform.
--
-- Run with: psql $DATABASE_URL -f databases/migrations/001_entity_identity_anchors.sql
-- =============================================================================

BEGIN;

-- ── Master Entities Table ─────────────────────────────────────────────────────
-- This is the single source of truth for every unique real-world person.
-- All identity anchors, bank accounts, properties etc. reference this table.
CREATE TABLE IF NOT EXISTS entities (
    id                  BIGSERIAL       PRIMARY KEY,
    first_name          VARCHAR(100)    NOT NULL,
    last_name           VARCHAR(100),
    middle_name         VARCHAR(100),
    date_of_birth       DATE,
    gender              CHAR(1)         CHECK (gender IN ('M', 'F', 'O', NULL)),
    nationality         CHAR(2)         DEFAULT 'IN',    -- ISO 3166-1 alpha-2
    primary_email       VARCHAR(255),
    primary_phone       VARCHAR(30),
    permanent_address   TEXT,
    current_address     TEXT,
    city                VARCHAR(100),
    state               VARCHAR(100),
    country             CHAR(2)         DEFAULT 'IN',
    pincode             VARCHAR(20),
    profile_photo_url   TEXT,
    risk_score          SMALLINT        DEFAULT 0 CHECK (risk_score BETWEEN 0 AND 100),
    status              VARCHAR(20)     DEFAULT 'active'
                            CHECK (status IN ('active', 'inactive', 'flagged', 'deceased', 'unknown')),
    source_system       VARCHAR(100),   -- Where this record originally came from
    source_record_id    VARCHAR(255),   -- Original PK in the source system
    is_duplicate        BOOLEAN         DEFAULT false,
    canonical_entity_id BIGINT          REFERENCES entities(id),  -- points to master if duplicate
    created_at          TIMESTAMPTZ     DEFAULT NOW(),
    updated_at          TIMESTAMPTZ     DEFAULT NOW(),
    created_by          INTEGER         REFERENCES users(id),
    notes               TEXT
);

-- ── Government ID Anchor Table ────────────────────────────────────────────────
-- Each row = one verified Government ID document belonging to an entity.
-- The hash field enforces uniqueness across the entire system — no two entities
-- can share the same physical ID number.
CREATE TABLE IF NOT EXISTS entity_identity_anchors (
    id              BIGSERIAL       PRIMARY KEY,
    entity_id       BIGINT          NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    id_type         VARCHAR(30)     NOT NULL CHECK (id_type IN (
                        'AADHAAR',          -- India: 12-digit UID
                        'PAN',              -- India: Permanent Account Number
                        'PASSPORT',         -- International travel document
                        'DRIVING_LICENSE',  -- State-issued DL
                        'VOTER_ID',         -- Election Commission ID (EPIC)
                        'NPR',              -- National Population Register
                        'SSN',              -- US Social Security Number
                        'NIN',              -- UK National Insurance Number
                        'OTHER'
                    )),
    id_number           VARCHAR(100)    NOT NULL,
    -- SHA-256 hash of UPPER(TRIM(id_number)) — used for dedup lookups
    -- Generated as: encode(sha256(upper(trim(id_number))::bytea), 'hex')
    id_number_hash      CHAR(64)        NOT NULL,
    issuing_country     CHAR(2)         DEFAULT 'IN',
    issuing_authority   VARCHAR(200),   -- e.g. 'UIDAI', 'Income Tax Dept'
    issuing_state       VARCHAR(50),    -- for DL / Voter ID
    issue_date          DATE,
    expiry_date         DATE,
    is_verified         BOOLEAN         DEFAULT false,
    verified_at         TIMESTAMPTZ,
    verified_by         INTEGER         REFERENCES users(id),
    verification_source VARCHAR(100),   -- 'MANUAL', 'API', 'UIDAI_OTP', etc.
    is_primary          BOOLEAN         DEFAULT false,  -- one primary per entity
    raw_ocr_data        JSONB,          -- raw OCR output from document scan
    document_urls       TEXT[],         -- links to scanned copies
    created_at          TIMESTAMPTZ     DEFAULT NOW(),
    updated_at          TIMESTAMPTZ     DEFAULT NOW(),

    -- ⭐ UNIQUENESS: same ID type + same ID number hash MUST be the same entity
    CONSTRAINT uq_identity_anchor UNIQUE (id_type, id_number_hash)
);

-- ── Indexes for fast entity resolution ───────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_entity_anchors_entity_id
    ON entity_identity_anchors(entity_id);

CREATE INDEX IF NOT EXISTS idx_entity_anchors_hash
    ON entity_identity_anchors(id_number_hash);

CREATE INDEX IF NOT EXISTS idx_entity_anchors_type_hash
    ON entity_identity_anchors(id_type, id_number_hash);

CREATE INDEX IF NOT EXISTS idx_entities_names
    ON entities USING gin(to_tsvector('english', coalesce(first_name,'') || ' ' || coalesce(last_name,'')));

CREATE INDEX IF NOT EXISTS idx_entities_email
    ON entities(primary_email);

CREATE INDEX IF NOT EXISTS idx_entities_phone
    ON entities(primary_phone);

CREATE INDEX IF NOT EXISTS idx_entities_status
    ON entities(status);

CREATE INDEX IF NOT EXISTS idx_entities_dob
    ON entities(date_of_birth);

CREATE INDEX IF NOT EXISTS idx_entities_canonical
    ON entities(canonical_entity_id) WHERE canonical_entity_id IS NOT NULL;

-- ── Automatic hash generation trigger ────────────────────────────────────────
CREATE OR REPLACE FUNCTION trg_hash_id_number()
RETURNS TRIGGER AS $$
BEGIN
    NEW.id_number_hash := encode(sha256(upper(trim(NEW.id_number))::bytea), 'hex');
    NEW.updated_at     := NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_entity_anchor_hash ON entity_identity_anchors;
CREATE TRIGGER trg_entity_anchor_hash
    BEFORE INSERT OR UPDATE OF id_number ON entity_identity_anchors
    FOR EACH ROW EXECUTE FUNCTION trg_hash_id_number();

-- ── Entity deduplication function ─────────────────────────────────────────────
-- Given an ID type + raw number, find if an entity already exists.
CREATE OR REPLACE FUNCTION find_entity_by_govt_id(
    p_id_type   VARCHAR,
    p_id_number VARCHAR
) RETURNS TABLE (
    entity_id       BIGINT,
    anchor_id       BIGINT,
    is_verified     BOOLEAN,
    entity_status   VARCHAR
) AS $$
DECLARE
    v_hash CHAR(64);
BEGIN
    v_hash := encode(sha256(upper(trim(p_id_number))::bytea), 'hex');
    RETURN QUERY
        SELECT e.id, a.id, a.is_verified, e.status
        FROM entity_identity_anchors a
        JOIN entities e ON e.id = a.entity_id
        WHERE a.id_type = p_id_type
          AND a.id_number_hash = v_hash
        LIMIT 1;
END;
$$ LANGUAGE plpgsql STABLE;

-- ── Updated_at auto-update triggers ──────────────────────────────────────────
CREATE OR REPLACE FUNCTION trg_set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at := NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_entities_updated_at ON entities;
CREATE TRIGGER trg_entities_updated_at
    BEFORE UPDATE ON entities
    FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- ── Grants ────────────────────────────────────────────────────────────────────
GRANT SELECT, INSERT, UPDATE, DELETE ON entities TO acid;
GRANT SELECT, INSERT, UPDATE, DELETE ON entity_identity_anchors TO acid;
GRANT USAGE, SELECT ON SEQUENCE entities_id_seq TO acid;
GRANT USAGE, SELECT ON SEQUENCE entity_identity_anchors_id_seq TO acid;
GRANT EXECUTE ON FUNCTION find_entity_by_govt_id TO acid;
GRANT EXECUTE ON FUNCTION trg_hash_id_number TO acid;

COMMIT;

-- ── Verification ─────────────────────────────────────────────────────────────
DO $$
BEGIN
    ASSERT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'entities'),
        'entities table not created';
    ASSERT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'entity_identity_anchors'),
        'entity_identity_anchors table not created';
    RAISE NOTICE '✅ Migration 001 applied successfully';
END $$;
