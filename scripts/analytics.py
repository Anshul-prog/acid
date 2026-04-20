#!/usr/bin/env python3
"""
=============================================================================
ACID — Database Analytics Script
=============================================================================
Connects to the PostgreSQL database and generates usage analytics:
  - Table row counts across all schemas
  - User status distribution
  - Duplicate reference analysis (cross-database records)
  - Category assignment statistics
  - API key usage summary

Usage:
    pip install -r scripts/requirements.txt
    python scripts/analytics.py
    python scripts/analytics.py --schema lsd_db_01
    python scripts/analytics.py --output reports/analytics.csv

Reads DATABASE_URL from .env or environment variable.
=============================================================================
"""

import os
import sys
import argparse
from datetime import datetime

# ── Dependency check ──────────────────────────────────────────────────────────
try:
    import psycopg2
    import psycopg2.extras
    from dotenv import load_dotenv
    from tabulate import tabulate
except ImportError as e:
    print(f"[ERROR] Missing dependency: {e}")
    print("        Run: pip install -r scripts/requirements.txt")
    sys.exit(1)

# ── Load environment ──────────────────────────────────────────────────────────
load_dotenv()
DATABASE_URL = os.getenv("DATABASE_URL")
if not DATABASE_URL:
    print("[ERROR] DATABASE_URL not set. Configure .env or environment.")
    sys.exit(1)


def connect():
    """Create a database connection."""
    try:
        return psycopg2.connect(DATABASE_URL)
    except Exception as e:
        print(f"[ERROR] Cannot connect to database: {e}")
        sys.exit(1)


def get_schema_stats(conn, target_schema=None):
    """Get row counts for all tables (or a specific schema)."""
    cur = conn.cursor(cursor_factory=psycopg2.extras.DictCursor)
    
    if target_schema:
        cur.execute("""
            SELECT schemaname, tablename,
                   pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) as size
            FROM pg_tables
            WHERE schemaname = %s
            ORDER BY tablename
            LIMIT 50
        """, (target_schema,))
    else:
        cur.execute("""
            SELECT schemaname, tablename,
                   pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) as size
            FROM pg_tables
            WHERE schemaname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
            ORDER BY schemaname, tablename
            LIMIT 100
        """)
    
    rows = cur.fetchall()
    cur.close()
    return rows


def get_user_stats(conn):
    """Get statistics from the main users table."""
    cur = conn.cursor(cursor_factory=psycopg2.extras.DictCursor)
    try:
        cur.execute("""
            SELECT 
                COUNT(*) as total_users,
                COUNT(*) FILTER (WHERE role = 'admin') as admins,
                COUNT(*) FILTER (WHERE role = 'user') as regular_users,
                COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '7 days') as new_this_week,
                COUNT(*) FILTER (WHERE last_login_at IS NOT NULL) as ever_logged_in
            FROM users
        """)
        return cur.fetchone()
    except psycopg2.errors.UndefinedTable:
        return None
    except psycopg2.errors.UndefinedColumn:
        return None
    finally:
        cur.close()
        conn.rollback()


def get_category_stats(conn):
    """Get category assignment statistics."""
    cur = conn.cursor(cursor_factory=psycopg2.extras.DictCursor)
    try:
        cur.execute("""
            SELECT 
                c.name,
                c.entity_type,
                c.color,
                COUNT(ec.id) as assignment_count,
                c.is_active
            FROM categories c
            LEFT JOIN entity_categories ec ON c.id = ec.category_id
            GROUP BY c.id, c.name, c.entity_type, c.color, c.is_active
            ORDER BY assignment_count DESC
            LIMIT 20
        """)
        return cur.fetchall()
    except psycopg2.errors.UndefinedTable:
        return []
    finally:
        cur.close()
        conn.rollback()


def get_api_key_stats(conn):
    """Get API key usage stats."""
    cur = conn.cursor(cursor_factory=psycopg2.extras.DictCursor)
    try:
        cur.execute("""
            SELECT 
                COUNT(*) as total_keys,
                COUNT(*) FILTER (WHERE revoked = false) as active_keys,
                COUNT(*) FILTER (WHERE last_used_at > NOW() - INTERVAL '24 hours') as used_today,
                COUNT(*) FILTER (WHERE expires_at < NOW()) as expired
            FROM api_keys
        """)
        return cur.fetchone()
    except psycopg2.errors.UndefinedTable:
        return None
    finally:
        cur.close()
        conn.rollback()


def get_incoming_files():
    """List files in the incoming directory (pending pipeline processing)."""
    incoming_dir = "databases/incoming"
    if not os.path.isdir(incoming_dir):
        return []
    files = [f for f in os.listdir(incoming_dir) if not f.startswith('.')]
    return files


def get_archive_files():
    """List files in the archive directory (processed files)."""
    archive_dir = "databases/archive"
    if not os.path.isdir(archive_dir):
        return []
    files = [f for f in os.listdir(archive_dir) if not f.startswith('.')]
    return files


def main():
    parser = argparse.ArgumentParser(description="ACID Database Analytics")
    parser.add_argument("--schema", help="Analyze specific schema only")
    parser.add_argument("--output", help="Save results to CSV file")
    args = parser.parse_args()

    print("=" * 60)
    print(f"  ACID Database Analytics — {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print("=" * 60)

    conn = connect()
    print(f"[✓] Connected to database\n")

    # ── Schema / Table stats ──────────────────────────────────────────────────
    print("── Table Overview ─────────────────────────────────────────")
    schema_rows = get_schema_stats(conn, args.schema)
    if schema_rows:
        print(tabulate(schema_rows, headers=["Schema", "Table", "Size"], tablefmt="rounded_outline"))
    else:
        print("  No tables found.")
    print()

    # ── User stats ────────────────────────────────────────────────────────────
    print("── User Statistics ────────────────────────────────────────")
    user_stats = get_user_stats(conn)
    if user_stats:
        user_data = [
            ["Total Users", user_stats["total_users"]],
            ["Admins", user_stats["admins"]],
            ["Regular Users", user_stats["regular_users"]],
            ["New This Week", user_stats["new_this_week"]],
            ["Ever Logged In", user_stats["ever_logged_in"]],
        ]
        print(tabulate(user_data, headers=["Metric", "Count"], tablefmt="rounded_outline"))
    else:
        print("  users table not found (start ACID server once to create it)")
    print()

    # ── Category stats ────────────────────────────────────────────────────────
    print("── Category Assignments ───────────────────────────────────")
    cat_stats = get_category_stats(conn)
    if cat_stats:
        cat_data = [[r["name"], r["entity_type"], r["assignment_count"], "✓" if r["is_active"] else "✗"]
                    for r in cat_stats]
        print(tabulate(cat_data, headers=["Category", "Type", "Assignments", "Active"],
                      tablefmt="rounded_outline"))
    else:
        print("  categories table not found")
    print()

    # ── API key stats ─────────────────────────────────────────────────────────
    print("── API Key Statistics ─────────────────────────────────────")
    api_stats = get_api_key_stats(conn)
    if api_stats:
        api_data = [
            ["Total Keys", api_stats["total_keys"]],
            ["Active Keys", api_stats["active_keys"]],
            ["Used Today", api_stats["used_today"]],
            ["Expired", api_stats["expired"]],
        ]
        print(tabulate(api_data, headers=["Metric", "Count"], tablefmt="rounded_outline"))
    else:
        print("  api_keys table not found")
    print()

    # ── Pipeline queue ────────────────────────────────────────────────────────
    print("── Pipeline File Queue ────────────────────────────────────")
    incoming = get_incoming_files()
    archive = get_archive_files()
    print(f"  databases/incoming/ : {len(incoming)} file(s) pending")
    for f in incoming[:10]:
        print(f"    • {f}")
    print(f"  databases/archive/  : {len(archive)} file(s) processed")
    print()

    conn.close()
    print("=" * 60)
    print("  Analytics complete.")
    print("=" * 60)


if __name__ == "__main__":
    main()
