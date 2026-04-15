#!/usr/bin/env python3
"""
#file: analytics.py
#package: scripts
#purpose: Offline analytics helper for L.S.D.

Connects directly to the PostgreSQL database (using DATABASE_URL from .env)
and produces statistical summaries that the Go server does not expose.

Usage:
    python3 scripts/analytics.py --report summary
    python3 scripts/analytics.py --report cases  --output cases_report.csv
    python3 scripts/analytics.py --report entities --top 20
    python3 scripts/analytics.py --report audit   --days 7

Requirements (all stdlib + psycopg2 only — no cloud, no external services):
    pip install psycopg2-binary python-dotenv
"""

import argparse
import csv
import os
import sys
from datetime import datetime, timedelta

# ── Optional imports (fail with helpful message) ──────────────────────────────
try:
    import psycopg2
    import psycopg2.extras
except ImportError:
    sys.exit("[ERROR] psycopg2 not installed.  Run:  pip install psycopg2-binary")

try:
    from dotenv import load_dotenv
    load_dotenv()
except ImportError:
    pass  # dotenv is optional; fall back to real env vars

# ── Database connection ───────────────────────────────────────────────────────
def get_conn():
    """#db-connect: reads DATABASE_URL from environment (loaded from .env)."""
    dsn = os.getenv("DATABASE_URL")
    if not dsn:
        sys.exit("[ERROR] DATABASE_URL environment variable is not set.\n"
                 "        Copy docs/env.example to .env and fill in your database credentials.")
    # psycopg2 uses keyword=value format; adapt URL if needed
    try:
        return psycopg2.connect(dsn, cursor_factory=psycopg2.extras.RealDictCursor)
    except Exception as e:
        sys.exit(f"[ERROR] Cannot connect to database: {e}")


# ── Reports ───────────────────────────────────────────────────────────────────

def report_summary(conn, args):
    """#report-summary: high-level counts across all intelligence tables."""
    queries = {
        "Total Entities":      "SELECT COUNT(*) FROM entities    WHERE deleted_at IS NULL",
        "Total Cases":         "SELECT COUNT(*) FROM cases        WHERE deleted_at IS NULL",
        "Open Cases":          "SELECT COUNT(*) FROM cases        WHERE status='open' AND deleted_at IS NULL",
        "Total Documents":     "SELECT COUNT(*) FROM entity_documents",
        "Total Contacts":      "SELECT COUNT(*) FROM entity_contacts",
        "Total Social Accounts":"SELECT COUNT(*) FROM entity_social_accounts",
        "Total Bank Accounts": "SELECT COUNT(*) FROM entity_bank_accounts",
        "Work Sessions (today)":"SELECT COUNT(*) FROM work_sessions WHERE started_at >= CURRENT_DATE",
        "Audit Logs (today)":  "SELECT COUNT(*) FROM entity_access_logs WHERE created_at >= CURRENT_DATE",
    }
    print("\n" + "═" * 46)
    print("  L.S.D  —  Intelligence Platform Summary")
    print("  Generated:", datetime.now().strftime("%Y-%m-%d %H:%M:%S"))
    print("═" * 46)
    cur = conn.cursor()
    for label, q in queries.items():
        try:
            cur.execute(q)
            row = cur.fetchone()
            count = list(row.values())[0] if row else 0
            print(f"  {label:<30} {count:>10,}")
        except Exception as e:
            print(f"  {label:<30} ERROR: {e}")
            conn.rollback()
    print("═" * 46 + "\n")


def report_cases(conn, args):
    """#report-cases: exports all cases to CSV (or prints top N)."""
    cur = conn.cursor()
    cur.execute("""
        SELECT case_number, title, status, category, priority,
               investigating_officer, fir_number,
               created_at::date AS date_created
        FROM cases
        WHERE deleted_at IS NULL
        ORDER BY created_at DESC
        LIMIT %s
    """, (args.top,))
    rows = cur.fetchall()

    if args.output:
        with open(args.output, "w", newline="", encoding="utf-8") as f:
            writer = csv.DictWriter(f, fieldnames=rows[0].keys() if rows else [])
            writer.writeheader()
            writer.writerows(rows)
        print(f"[OK] Exported {len(rows)} cases to {args.output}")
    else:
        for r in rows:
            print(f"  [{r['status']:<22}]  {r['case_number']:<20}  {r['title'][:50]}")


def report_entities(conn, args):
    """#report-entities: top entities by case count."""
    cur = conn.cursor()
    cur.execute("""
        SELECT e.full_name, e.entity_type, e.primary_phone, e.primary_email,
               COUNT(cer.case_id) AS case_count
        FROM entities e
        LEFT JOIN case_entity_roles cer ON cer.entity_id = e.id
        WHERE e.deleted_at IS NULL
        GROUP BY e.id, e.full_name, e.entity_type, e.primary_phone, e.primary_email
        ORDER BY case_count DESC
        LIMIT %s
    """, (args.top,))
    rows = cur.fetchall()

    print(f"\n  Top {args.top} Entities by Case Involvement")
    print("  " + "─" * 70)
    for r in rows:
        print(f"  {r['full_name']:<35} {r['entity_type']:<12} Cases: {r['case_count']}")

    if args.output:
        with open(args.output, "w", newline="", encoding="utf-8") as f:
            writer = csv.DictWriter(f, fieldnames=rows[0].keys() if rows else [])
            writer.writeheader()
            writer.writerows(rows)
        print(f"\n[OK] Exported to {args.output}")


def report_audit(conn, args):
    """#report-audit: recent audit log activity — who searched/accessed what."""
    days = getattr(args, 'days', 7)
    since = datetime.now() - timedelta(days=days)
    cur = conn.cursor()
    cur.execute("""
        SELECT username, action, COUNT(*) AS count,
               MAX(created_at) AS last_seen
        FROM entity_access_logs
        WHERE created_at >= %s
        GROUP BY username, action
        ORDER BY count DESC
        LIMIT %s
    """, (since, args.top))
    rows = cur.fetchall()

    print(f"\n  Audit Activity — last {days} day(s)")
    print("  " + "─" * 65)
    for r in rows:
        print(f"  {(r['username'] or 'anonymous'):<25} {r['action']:<30} {r['count']:>5}x")

    if args.output:
        with open(args.output, "w", newline="", encoding="utf-8") as f:
            writer = csv.DictWriter(f, fieldnames=rows[0].keys() if rows else [])
            writer.writeheader()
            writer.writerows(rows)
        print(f"\n[OK] Exported to {args.output}")


# ── CLI entry-point ───────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="L.S.D offline analytics helper",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--report",  choices=["summary","cases","entities","audit"],
                        default="summary", help="Report type")
    parser.add_argument("--top",     type=int, default=50,  help="Max rows")
    parser.add_argument("--days",    type=int, default=7,   help="Days back for audit report")
    parser.add_argument("--output",  default="",            help="Output CSV file path")
    args = parser.parse_args()

    conn = get_conn()
    try:
        {"summary":  report_summary,
         "cases":    report_cases,
         "entities": report_entities,
         "audit":    report_audit}[args.report](conn, args)
    finally:
        conn.close()


if __name__ == "__main__":
    main()
