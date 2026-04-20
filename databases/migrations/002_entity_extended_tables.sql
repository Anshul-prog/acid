-- =============================================================================
-- MIGRATION 002: Entity Extended Profile Tables
-- =============================================================================
-- Prerequisite: Migration 001 must be applied first.
-- Tables: entity_properties, entity_bank_accounts,
--         entity_social_accounts, entity_remarks
-- =============================================================================

BEGIN;

-- ── entity_properties: Real estate & physical assets ─────────────────────────
CREATE TABLE IF NOT EXISTS entity_properties (
    id              BIGSERIAL       PRIMARY KEY,
    entity_id       BIGINT          NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    property_type   VARCHAR(50)     NOT NULL CHECK (property_type IN (
                        'RESIDENTIAL', 'COMMERCIAL', 'AGRICULTURAL',
                        'INDUSTRIAL', 'VACANT_LAND', 'VEHICLE', 'VESSEL',
                        'AIRCRAFT', 'JEWELRY', 'INTELLECTUAL_PROPERTY', 'OTHER'
                    )),
    -- Location
    address         TEXT,
    city            VARCHAR(100),
    state           VARCHAR(100),
    country         CHAR(2)         DEFAULT 'IN',
    pincode         VARCHAR(20),
    coordinates     POINT,          -- PostGIS-compatible lat/lng
    -- Registration
    registration_number VARCHAR(100),
    registration_date   DATE,
    registration_office VARCHAR(200),
    -- Valuation
    acquisition_value   NUMERIC(18, 2),
    current_value       NUMERIC(18, 2),
    valuation_date      DATE,
    currency            CHAR(3)     DEFAULT 'INR',
    -- Encumbrances
    is_mortgaged        BOOLEAN     DEFAULT false,
    mortgaged_with      VARCHAR(200),
    mortgage_amount     NUMERIC(18, 2),
    -- Ownership
    ownership_percent   NUMERIC(5, 2) DEFAULT 100.00 CHECK (ownership_percent BETWEEN 0.01 AND 100.00),
    co_owners           TEXT[],     -- names of co-owners
    -- Metadata
    source_document_url TEXT,
    remarks             TEXT,
    is_active           BOOLEAN     DEFAULT true,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_entity_props_entity_id ON entity_properties(entity_id);
CREATE INDEX IF NOT EXISTS idx_entity_props_type      ON entity_properties(property_type);
CREATE INDEX IF NOT EXISTS idx_entity_props_reg_no    ON entity_properties(registration_number)
    WHERE registration_number IS NOT NULL;

-- ── entity_bank_accounts: Financial account linkages ─────────────────────────
CREATE TABLE IF NOT EXISTS entity_bank_accounts (
    id              BIGSERIAL       PRIMARY KEY,
    entity_id       BIGINT          NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    bank_name       VARCHAR(200)    NOT NULL,
    bank_code       VARCHAR(20),    -- IFSC / SWIFT / BIC / ABA
    branch_name     VARCHAR(200),
    branch_address  TEXT,
    -- Account identifiers (all masked for security — store only last 4 + hash)
    account_number_masked   VARCHAR(30),    -- e.g. XXXX-XXXX-1234
    account_number_hash     CHAR(64),       -- SHA-256 for dedup/lookup
    account_type    VARCHAR(30)     CHECK (account_type IN (
                        'SAVINGS', 'CURRENT', 'FIXED_DEPOSIT', 'NRE', 'NRO',
                        'DEMAT', 'CREDIT_CARD', 'LOAN', 'CRYPTO', 'OTHER'
                    )),
    ifsc_code       CHAR(11),
    swift_code      VARCHAR(11),
    iban            VARCHAR(34),
    -- Status
    is_active       BOOLEAN         DEFAULT true,
    is_primary      BOOLEAN         DEFAULT false,
    opened_at       DATE,
    closed_at       DATE,
    -- Linked transactions summary (refreshed by analytics job)
    total_credit    NUMERIC(20, 2),
    total_debit     NUMERIC(20, 2),
    last_txn_date   DATE,
    average_balance NUMERIC(20, 2),
    -- Metadata
    source_document_url TEXT,
    remarks         TEXT,
    created_at      TIMESTAMPTZ     DEFAULT NOW(),
    updated_at      TIMESTAMPTZ     DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_entity_bank_entity_id  ON entity_bank_accounts(entity_id);
CREATE INDEX IF NOT EXISTS idx_entity_bank_acct_hash  ON entity_bank_accounts(account_number_hash)
    WHERE account_number_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_entity_bank_ifsc       ON entity_bank_accounts(ifsc_code)
    WHERE ifsc_code IS NOT NULL;

-- ── entity_social_accounts: Social media & online presence ───────────────────
CREATE TABLE IF NOT EXISTS entity_social_accounts (
    id              BIGSERIAL       PRIMARY KEY,
    entity_id       BIGINT          NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    platform        VARCHAR(50)     NOT NULL CHECK (platform IN (
                        'TWITTER', 'FACEBOOK', 'INSTAGRAM', 'LINKEDIN',
                        'YOUTUBE', 'TELEGRAM', 'WHATSAPP', 'REDDIT',
                        'TIKTOK', 'GITHUB', 'EMAIL', 'PHONE', 'OTHER'
                    )),
    handle          VARCHAR(255)    NOT NULL,   -- @username or email or phone
    profile_url     TEXT,
    display_name    VARCHAR(255),
    follower_count  INTEGER,
    is_verified     BOOLEAN         DEFAULT false,    -- blue-tick verification
    is_active       BOOLEAN         DEFAULT true,
    is_anonymous    BOOLEAN         DEFAULT false,    -- suspected anonymous account
    risk_flags      TEXT[],         -- e.g. ['hate_speech', 'financial_fraud']
    profile_snapshot JSONB,         -- cached profile data
    last_scraped_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ     DEFAULT NOW(),
    updated_at      TIMESTAMPTZ     DEFAULT NOW(),

    UNIQUE(entity_id, platform, handle)
);

CREATE INDEX IF NOT EXISTS idx_entity_social_entity_id ON entity_social_accounts(entity_id);
CREATE INDEX IF NOT EXISTS idx_entity_social_handle     ON entity_social_accounts(handle);
CREATE INDEX IF NOT EXISTS idx_entity_social_platform   ON entity_social_accounts(platform);
-- GIN index for full-text on handle + display_name
CREATE INDEX IF NOT EXISTS idx_entity_social_fts
    ON entity_social_accounts
    USING gin(to_tsvector('english', coalesce(handle,'') || ' ' || coalesce(display_name,'')));

-- ── entity_remarks: Analyst notes & case observations ─────────────────────────
CREATE TABLE IF NOT EXISTS entity_remarks (
    id              BIGSERIAL       PRIMARY KEY,
    entity_id       BIGINT          NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    remark_type     VARCHAR(30)     NOT NULL DEFAULT 'GENERAL'
                        CHECK (remark_type IN (
                            'GENERAL', 'ALERT', 'CASE_NOTE', 'VERIFICATION',
                            'SOURCE_INTEL', 'FIELD_REPORT', 'LEGAL', 'FINANCIAL'
                        )),
    severity        VARCHAR(10)     DEFAULT 'INFO'
                        CHECK (severity IN ('INFO', 'LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
    title           VARCHAR(255),
    content         TEXT            NOT NULL,
    tags            TEXT[],         -- free-form tags on this remark
    is_confidential BOOLEAN         DEFAULT false,  -- restrict to specific roles
    case_id         BIGINT,         -- optional links to a case
    authored_by     INTEGER         NOT NULL REFERENCES users(id),
    reviewed_by     INTEGER         REFERENCES users(id),
    reviewed_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ     DEFAULT NOW(),
    updated_at      TIMESTAMPTZ     DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_entity_remarks_entity_id  ON entity_remarks(entity_id);
CREATE INDEX IF NOT EXISTS idx_entity_remarks_type        ON entity_remarks(remark_type);
CREATE INDEX IF NOT EXISTS idx_entity_remarks_severity    ON entity_remarks(severity);
CREATE INDEX IF NOT EXISTS idx_entity_remarks_authored_by ON entity_remarks(authored_by);
CREATE INDEX IF NOT EXISTS idx_entity_remarks_fts
    ON entity_remarks USING gin(to_tsvector('english', coalesce(title,'') || ' ' || coalesce(content,'')));

-- ── Updated_at triggers ───────────────────────────────────────────────────────
DROP TRIGGER IF EXISTS trg_entity_props_updated_at   ON entity_properties;
DROP TRIGGER IF EXISTS trg_entity_bank_updated_at    ON entity_bank_accounts;
DROP TRIGGER IF EXISTS trg_entity_social_updated_at  ON entity_social_accounts;
DROP TRIGGER IF EXISTS trg_entity_remarks_updated_at ON entity_remarks;

CREATE TRIGGER trg_entity_props_updated_at
    BEFORE UPDATE ON entity_properties
    FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TRIGGER trg_entity_bank_updated_at
    BEFORE UPDATE ON entity_bank_accounts
    FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TRIGGER trg_entity_social_updated_at
    BEFORE UPDATE ON entity_social_accounts
    FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TRIGGER trg_entity_remarks_updated_at
    BEFORE UPDATE ON entity_remarks
    FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- ── Composite 360-degree profile view ────────────────────────────────────────
CREATE OR REPLACE VIEW v_entity_360 AS
SELECT
    e.id                    AS entity_id,
    e.first_name,
    e.last_name,
    e.middle_name,
    e.date_of_birth,
    e.gender,
    e.nationality,
    e.primary_email,
    e.primary_phone,
    e.permanent_address,
    e.city,
    e.state,
    e.risk_score,
    e.status,
    -- Identity anchors as JSON array
    (SELECT json_agg(json_build_object(
        'id_type', a.id_type,
        'id_number_masked', regexp_replace(a.id_number, '.(?=.{4})', 'X'),
        'is_verified', a.is_verified,
        'issuing_country', a.issuing_country
    ) ORDER BY a.is_primary DESC, a.created_at)
    FROM entity_identity_anchors a WHERE a.entity_id = e.id)    AS identity_anchors,
    -- Properties
    (SELECT json_agg(json_build_object(
        'type', p.property_type,
        'address', p.address,
        'city', p.city,
        'current_value', p.current_value,
        'currency', p.currency,
        'is_mortgaged', p.is_mortgaged
    ) ORDER BY p.current_value DESC NULLS LAST)
    FROM entity_properties p WHERE p.entity_id = e.id AND p.is_active)  AS properties,
    -- Bank accounts (masked)
    (SELECT json_agg(json_build_object(
        'bank_name', b.bank_name,
        'account_masked', b.account_number_masked,
        'account_type', b.account_type,
        'ifsc_code', b.ifsc_code,
        'is_primary', b.is_primary
    ) ORDER BY b.is_primary DESC, b.opened_at DESC)
    FROM entity_bank_accounts b WHERE b.entity_id = e.id AND b.is_active) AS bank_accounts,
    -- Social accounts
    (SELECT json_agg(json_build_object(
        'platform', s.platform,
        'handle', s.handle,
        'profile_url', s.profile_url,
        'is_verified', s.is_verified,
        'follower_count', s.follower_count,
        'risk_flags', s.risk_flags
    ) ORDER BY s.follower_count DESC NULLS LAST)
    FROM entity_social_accounts s WHERE s.entity_id = e.id AND s.is_active) AS social_accounts,
    -- Category tags
    (SELECT array_agg(c.name ORDER BY c.name)
     FROM entity_categories ec
     JOIN categories c ON c.id = ec.category_id AND c.is_active
     WHERE ec.entity_type = 'entity' AND ec.entity_id = e.id)   AS category_tags,
    -- Remark count by severity
    (SELECT json_object_agg(severity, cnt)
     FROM (SELECT severity, count(*) as cnt FROM entity_remarks
           WHERE entity_id = e.id GROUP BY severity) x)          AS remark_summary,
    e.created_at,
    e.updated_at
FROM entities e;

-- ── Grants ────────────────────────────────────────────────────────────────────
GRANT SELECT, INSERT, UPDATE, DELETE ON entity_properties     TO acid;
GRANT SELECT, INSERT, UPDATE, DELETE ON entity_bank_accounts  TO acid;
GRANT SELECT, INSERT, UPDATE, DELETE ON entity_social_accounts TO acid;
GRANT SELECT, INSERT, UPDATE, DELETE ON entity_remarks        TO acid;
GRANT SELECT ON v_entity_360 TO acid;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO acid;

COMMIT;

DO $$
BEGIN
    RAISE NOTICE '✅ Migration 002 applied successfully';
END $$;
