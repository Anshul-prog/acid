#!/bin/bash

set -e

echo "╔═══════════════════════════════════════════════════════════╗"
echo "║ L.S.D Multi-Database Setup Script                       ║"
echo "╚═══════════════════════════════════════════════════════════╝"

DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
DB_USER="${DB_USER:-postgres}"
DB_PASS="${DB_PASS:-password}"
NUM_DATABASES="${NUM_DATABASES:-10}"
TABLES_PER_DB="${TABLES_PER_DB:-1000}"
USERS_PER_TABLE="${USERS_PER_TABLE:-50}"

export PGPASSWORD="$DB_PASS"

echo "Configuration:"
echo "  Host: $DB_HOST"
echo "  Port: $DB_PORT"
echo "  User: $DB_USER"
echo "  Databases: $NUM_DATABASES"
echo "  Tables per DB: $TABLES_PER_DB"
echo "  Users per table: $USERS_PER_TABLE"
echo ""

for db_idx in $(seq -w 1 $NUM_DATABASES); do
    db_name="lsd_db_$db_idx"
    
    echo "📦 Creating database: $db_name"
    
    psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -tc "
        SELECT 'CREATE DATABASE $db_name' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$db_name')\gexec
    " 2>/dev/null || true
    
    psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$db_name" <<EOF
-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Create users table
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    uuid VARCHAR(36) UNIQUE NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    first_name VARCHAR(100) NOT NULL,
    last_name VARCHAR(100) NOT NULL,
    phone VARCHAR(20),
    status VARCHAR(20) DEFAULT 'active',
    country VARCHAR(2),
    city VARCHAR(100),
    domain VARCHAR(100),
    job_title VARCHAR(100),
    password_hash VARCHAR(255),
    is_active BOOLEAN DEFAULT true,
    tags TEXT[],
    duplicate_ref TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);
CREATE INDEX IF NOT EXISTS idx_users_country ON users(country);
CREATE INDEX IF NOT EXISTS idx_users_domain ON users(domain);

-- Grant permissions
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA PUBLIC TO $DB_USER;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA PUBLIC TO $DB_USER;
EOF

    echo "✅ Database $db_name ready"
done

echo ""
echo "🎉 All databases created successfully!"
echo ""
echo "Run the data generator:"
echo "  go run ./databases/generator.go"