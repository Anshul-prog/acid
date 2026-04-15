-- ═══════════════════════════════════════════════════════════════════════════
-- L.S.D  –  Cyber-Cell Intelligence Schema
-- Migration 001 – Entities, Cases, Documents, Contacts, Social, Bank,
--                  Work Sessions, Entity Audit Logs
-- PostgreSQL 14+
-- Run with:  psql "$DATABASE_URL" -f 001_cyber_cell_schema.sql
-- ═══════════════════════════════════════════════════════════════════════════

BEGIN;

-- #extension: pgcrypto provides gen_random_uuid() for UUID primary keys
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ───────────────────────────────────────────────────────────────────────────
-- HELPER: auto-update updated_at on every write
-- #trigger: applied to all tables that have an updated_at column
-- ───────────────────────────────────────────────────────────────────────────
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$;

-- ═══════════════════════════════════════════════════════════════════════════
-- ENTITIES  (persons, organisations, devices)
-- #entity: central table — every "subject of interest" is stored here.
-- Soft-delete via deleted_at; versioning via updated_at trail.
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS entities (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type     VARCHAR(20) NOT NULL DEFAULT 'person'
                    CHECK (entity_type IN ('person','organization','device','unknown')),

    -- Bio-data
    full_name       VARCHAR(500) NOT NULL,
    display_name    VARCHAR(200),
    alias           TEXT[],                          -- alternate names / handles
    date_of_birth   DATE,
    gender          VARCHAR(20),
    nationality     VARCHAR(100) DEFAULT 'Indian',
    religion        VARCHAR(100),
    occupation      VARCHAR(200),

    -- Primary contact shortcuts (denormalised for fast lookup)
    primary_phone   VARCHAR(20),
    primary_email   VARCHAR(255),

    -- Photo stored as URL (served by Go's file server or object store path)
    photo_url       TEXT,

    -- Extensible metadata bag (JSONB for arbitrary fields)
    meta            JSONB DEFAULT '{}',

    -- #soft-delete + versioning
    created_by      VARCHAR(200),
    updated_by      VARCHAR(200),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ                      -- NULL = active
);

CREATE INDEX IF NOT EXISTS idx_entities_full_name   ON entities USING gin(to_tsvector('simple', full_name));
CREATE INDEX IF NOT EXISTS idx_entities_phone       ON entities(primary_phone) WHERE primary_phone IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_entities_email       ON entities(primary_email) WHERE primary_email IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_entities_type        ON entities(entity_type);
CREATE INDEX IF NOT EXISTS idx_entities_active      ON entities(created_at DESC) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_entities_updated_at
    BEFORE UPDATE ON entities
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ═══════════════════════════════════════════════════════════════════════════
-- ENTITY ADDRESSES
-- #address: supports multiple address types per entity with validity window.
-- valid_from / valid_to implement temporal versioning — old addresses are
-- never deleted, only their valid_to is set, preserving history.
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS entity_addresses (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id     UUID        NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    address_type  VARCHAR(50) NOT NULL DEFAULT 'current'
                  CHECK (address_type IN ('current','permanent','work','native','other')),

    address_line1 TEXT,
    address_line2 TEXT,
    city          VARCHAR(200),
    district      VARCHAR(200),
    state         VARCHAR(200),
    pincode       VARCHAR(10),
    country       VARCHAR(100) DEFAULT 'India',
    landmark      TEXT,

    is_verified   BOOLEAN     DEFAULT FALSE,
    is_primary    BOOLEAN     DEFAULT FALSE,

    -- #versioning: set valid_to when address changes, insert new row
    valid_from    TIMESTAMPTZ DEFAULT NOW(),
    valid_to      TIMESTAMPTZ,                       -- NULL = currently valid

    created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_addr_entity    ON entity_addresses(entity_id);
CREATE INDEX IF NOT EXISTS idx_addr_pincode   ON entity_addresses(pincode) WHERE pincode IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_addr_current   ON entity_addresses(entity_id, valid_to) WHERE valid_to IS NULL;

-- ═══════════════════════════════════════════════════════════════════════════
-- ENTITY DOCUMENTS
-- #documents: Aadhaar, PAN, Passport, Driving Licence, Voter ID, etc.
-- doc_number stored RAW (encrypted at rest via Postgres pg_crypto if needed).
-- doc_number_masked pre-computed masked version for display without "unmask" scope.
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS entity_documents (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id         UUID        NOT NULL REFERENCES entities(id) ON DELETE CASCADE,

    doc_type          VARCHAR(50) NOT NULL
                      CHECK (doc_type IN ('aadhaar','pan','passport','driving_license',
                                          'voter_id','ration_card','birth_certificate','other')),

    doc_number        VARCHAR(100) NOT NULL,
    doc_number_masked VARCHAR(100),                  -- pre-masked for non-admin display
    issued_by         VARCHAR(200),
    issued_date       DATE,
    expiry_date       DATE,
    scan_url          TEXT,                          -- path to scanned document image
    is_verified       BOOLEAN     DEFAULT FALSE,

    created_at        TIMESTAMPTZ DEFAULT NOW(),

    -- same doc_number+type pair should not be duplicated
    UNIQUE(doc_type, doc_number)
);

CREATE INDEX IF NOT EXISTS idx_docs_entity      ON entity_documents(entity_id);
CREATE INDEX IF NOT EXISTS idx_docs_number      ON entity_documents(doc_number);
CREATE INDEX IF NOT EXISTS idx_docs_type_number ON entity_documents(doc_type, doc_number);

-- ═══════════════════════════════════════════════════════════════════════════
-- ENTITY CONTACTS  (phones, emails, WhatsApp, etc.)
-- #contacts: multiple contacts per entity; is_primary flags the default.
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS entity_contacts (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id     UUID        NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    contact_type  VARCHAR(30) NOT NULL
                  CHECK (contact_type IN ('phone','email','whatsapp','telegram','fax','other')),
    contact_value VARCHAR(500) NOT NULL,
    label         VARCHAR(100),                      -- e.g. "Office", "Home"
    is_primary    BOOLEAN     DEFAULT FALSE,
    is_verified   BOOLEAN     DEFAULT FALSE,
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_contacts_entity ON entity_contacts(entity_id);
CREATE INDEX IF NOT EXISTS idx_contacts_value  ON entity_contacts(contact_value);

-- ═══════════════════════════════════════════════════════════════════════════
-- ENTITY SOCIAL ACCOUNTS
-- #social: Instagram, Facebook, Twitter/X, LinkedIn, Telegram, YouTube, etc.
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS entity_social_accounts (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id           UUID        NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    platform            VARCHAR(50) NOT NULL
                        CHECK (platform IN ('instagram','facebook','twitter','linkedin',
                                            'telegram','whatsapp','youtube','tiktok',
                                            'snapchat','koo','sharechat','other')),
    handle              VARCHAR(500) NOT NULL,
    profile_url         TEXT,
    followers_count     BIGINT,
    is_verified_account BOOLEAN     DEFAULT FALSE,
    is_active           BOOLEAN     DEFAULT TRUE,
    notes               TEXT,
    created_at          TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_social_entity   ON entity_social_accounts(entity_id);
CREATE INDEX IF NOT EXISTS idx_social_handle   ON entity_social_accounts(handle);
CREATE INDEX IF NOT EXISTS idx_social_platform ON entity_social_accounts(platform);

-- ═══════════════════════════════════════════════════════════════════════════
-- ENTITY BANK ACCOUNTS
-- #bank: account numbers stored raw; account_number_masked for display.
-- upi_ids is a TEXT array for multiple UPI handles.
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS entity_bank_accounts (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id             UUID        NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    account_number        VARCHAR(50),
    account_number_masked VARCHAR(50),
    bank_name             VARCHAR(200),
    ifsc_code             VARCHAR(20),
    account_type          VARCHAR(30)
                          CHECK (account_type IN ('savings','current','salary','nri','other')),
    branch_name           VARCHAR(200),
    branch_address        TEXT,
    upi_ids               TEXT[],
    is_primary            BOOLEAN     DEFAULT FALSE,
    created_at            TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bank_entity  ON entity_bank_accounts(entity_id);
CREATE INDEX IF NOT EXISTS idx_bank_account ON entity_bank_accounts(account_number) WHERE account_number IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_bank_ifsc    ON entity_bank_accounts(ifsc_code) WHERE ifsc_code IS NOT NULL;

-- ═══════════════════════════════════════════════════════════════════════════
-- CASES  (FIR / investigation cases)
-- #case: each case has a unique case_number and can link to many entities.
-- Soft-delete via deleted_at.
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS cases (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    case_number           VARCHAR(100) UNIQUE NOT NULL,
    title                 TEXT        NOT NULL,
    description           TEXT,
    status                VARCHAR(20) NOT NULL DEFAULT 'open'
                          CHECK (status IN ('open','under_investigation','closed','pending',
                                            'chargesheet_filed','acquitted','convicted','archived')),
    category              VARCHAR(100),               -- e.g. "Cybercrime", "Financial Fraud"
    sub_category          VARCHAR(100),
    jurisdiction          VARCHAR(200),               -- police station / court
    investigating_officer VARCHAR(200),
    io_badge_number       VARCHAR(50),
    fir_number            VARCHAR(100),
    fir_date              DATE,
    court_name            VARCHAR(200),
    next_hearing_date     DATE,
    priority              VARCHAR(20) DEFAULT 'normal'
                          CHECK (priority IN ('low','normal','high','critical')),

    -- #versioning
    created_by            VARCHAR(200),
    updated_by            VARCHAR(200),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at             TIMESTAMPTZ,
    deleted_at            TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_cases_number   ON cases(case_number);
CREATE INDEX IF NOT EXISTS idx_cases_status   ON cases(status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_cases_active   ON cases(created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_cases_fts      ON cases USING gin(to_tsvector('simple', title || ' ' || COALESCE(description,'')));

CREATE TRIGGER trg_cases_updated_at
    BEFORE UPDATE ON cases
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ═══════════════════════════════════════════════════════════════════════════
-- CASE–ENTITY ROLES
-- #roles: many-to-many between cases and entities; each link has a role.
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS case_entity_roles (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id          UUID        NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    entity_id        UUID        NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    role             VARCHAR(50) NOT NULL
                     CHECK (role IN ('accused','victim','witness','suspect',
                                     'informant','complainant','absconder','other')),
    role_description TEXT,
    added_by         VARCHAR(200),
    added_at         TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(case_id, entity_id, role)
);

CREATE INDEX IF NOT EXISTS idx_cer_case   ON case_entity_roles(case_id);
CREATE INDEX IF NOT EXISTS idx_cer_entity ON case_entity_roles(entity_id);

-- ═══════════════════════════════════════════════════════════════════════════
-- WORK SESSIONS
-- #work-sessions: every time an investigator begins working on a record they
-- open a session. The session stores started_at; when they save/close it gets
-- ended_at. This gives a forensic timeline of "who worked on what and when."
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS work_sessions (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     VARCHAR(200) NOT NULL,
    username    VARCHAR(200) NOT NULL,
    description TEXT,
    entity_id   UUID        REFERENCES entities(id) ON DELETE SET NULL,
    case_id     UUID        REFERENCES cases(id)    ON DELETE SET NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,                         -- NULL = session still open
    changes     JSONB       DEFAULT '[]'             -- array of change descriptors
);

CREATE INDEX IF NOT EXISTS idx_ws_user     ON work_sessions(user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_ws_entity   ON work_sessions(entity_id) WHERE entity_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_ws_case     ON work_sessions(case_id)   WHERE case_id   IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_ws_open     ON work_sessions(user_id)   WHERE ended_at  IS NULL;

-- ═══════════════════════════════════════════════════════════════════════════
-- ENTITY ACCESS / SEARCH AUDIT LOG
-- #forensic: immutable append-only log.  Every search, view, and export is
-- written here with the acting user's ID, username, IP, and timestamp.
-- Never update or delete rows in this table.
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS entity_access_logs (
    id          BIGSERIAL   PRIMARY KEY,
    user_id     VARCHAR(200),
    username    VARCHAR(200),
    action      VARCHAR(100) NOT NULL,   -- search | view_profile | view_case | export | unmask
    scope       VARCHAR(50),             -- row | column | database (for search actions)
    query_text  TEXT,
    entity_id   UUID,
    case_id     UUID,
    data_source VARCHAR(200),
    result_count INTEGER,
    ip_address  VARCHAR(45),
    user_agent  TEXT,
    session_id  VARCHAR(200),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_eal_user    ON entity_access_logs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_eal_action  ON entity_access_logs(action, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_eal_entity  ON entity_access_logs(entity_id) WHERE entity_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_eal_created ON entity_access_logs(created_at DESC);

-- ═══════════════════════════════════════════════════════════════════════════
-- DATA SOURCES REGISTRY
-- #multi-source: stores connection strings for all registered databases.
-- The Go server reads this table at startup to build its pools map.
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS data_sources (
    id          VARCHAR(100) PRIMARY KEY,            -- e.g. "default", "ds_delhi"
    label       VARCHAR(200) NOT NULL,
    dsn         TEXT         NOT NULL,               -- postgres://user:pass@host/db
    is_enabled  BOOLEAN      DEFAULT TRUE,
    description TEXT,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TRIGGER trg_ds_updated_at
    BEFORE UPDATE ON data_sources
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ═══════════════════════════════════════════════════════════════════════════
-- Insert "default" data source pointing at this same DB
-- (placeholder — real DSN injected by ops / env var)
-- ═══════════════════════════════════════════════════════════════════════════
INSERT INTO data_sources(id, label, dsn, description)
VALUES ('default', 'Primary Database', 'ENV:DATABASE_URL', 'Main L.S.D PostgreSQL database')
ON CONFLICT(id) DO NOTHING;

COMMIT;
