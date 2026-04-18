# ACID - Complete User Guide

## Table of Contents
1. [What is ACID?](#what-is-acid)
2. [Quick Start](#quick-start)
3. [Understanding the Interface](#understanding-the-interface)
4. [Managing Tables](#managing-tables)
5. [Searching Data](#searching-data)
6. [Generating Reports](#generating-reports)
7. [Troubleshooting](#troubleshooting)

---

## What is ACID?

**ACID** (Advanced Database Interface System) is a complete system that lets you manage your PostgreSQL database through a beautiful web interface - without needing to write code!

Think of it like this:
- **PostgreSQL** = Your filing cabinet (where all data is stored)
- **ACID** = A smart assistant that helps you find, organize, and manage files
- **ClickHouse** = A super-fast search engine that finds anything instantly
- **Redis** = A sticky note system (temporary memory to speed things up)

### What Can ACID Do?
- ✅ View all your database tables automatically
- ✅ Search across ALL tables at once
- ✅ Download data as Excel/CSV/PDF
- ✅ See real-time database health
- ✅ Automate data synchronization to search engine
- ✅ Manage users and API keys

---

## Quick Start

### Option 1: Using Docker (Recommended)
```bash
# 1. Make sure Docker is installed
docker --version

# 2. Copy the project
git clone <this-project>
cd acid

# 3. Start everything
docker-compose up -d

# 4. Open browser
#    Go to http://localhost:8080/admin
```

### Option 2: Manual Setup
```bash
# 1. Install Go 1.24+
# 2. Install PostgreSQL 15+
# 3. Install Redis (optional)
# 4. Install ClickHouse (optional for fast search)

# 5. Copy and edit settings
cp .env.example .env
# Edit .env with your database details

# 6. Run
go run ./cmd/api

# 7. Open browser
#    Go to http://localhost:8080/admin
```

---

## Understanding the Interface

### The Admin Panel Layout

When you open `/admin`, you see:

```
┌─────────────────────────────────────────────────────────┐
│ ACID Admin Panel                              [Refresh] [Logout]
├──────────┬──────────────────────────────────────────────┤
│ 📊      │  📈 DASHBOARD                             │
│ Dashboard│  ┌────────┐ ┌────────┐ ┌────────┐ ┌───────┐│
│ 📋      │  │   10   │ │   50   │ │ 1000  │ │  89  │ │
│ Tables   │  │Databases│ │ Tables │ │Records│ │Searches│
│ 🔍      │  └────────┘ └────────┘ └────────┘ └───────┘
│ Search  │                                            │
│ 🗄️     │  ┌─────────────────────────────────────┐  │
│Databases│  │  📊 Quick Stats                     │  │
│ 📄     │  │  Cache: 87% | 23ms | 5 users...   │  │
│ Reports │  └─────────────────────────────────────┘  │
│ 👥     │                                            │
│ Users  │                                            │
│ ⚙️    │                                            │
│ Settings                                            │
├──────────┴──────────────────────��───────────────────────┤
│ 🔌 Status: PostgreSQL● Redis● ClickHouse● CDC●         │
└─────────────────────────────────────────────────────────┘
```

### Sidebar Navigation

| Icon | Section | What It Does |
|------|---------|--------------|
| 📈 Dashboard | Overview of your entire system |
| 📋 Tables | Browse all database tables |
| 🔍 Search | Global search across everything |
| 🗄️ Databases | Manage multiple databases |
| 📄 Reports | Download data in various formats |
| 👥 Users | Manage user accounts |
| ⚙️ Settings | System configuration |

---

## Managing Tables

### Viewing Tables
1. Click **📋 Tables** in sidebar
2. You'll see ALL tables automatically discovered
3. Click any table to view its data

### Understanding the Table View
```
┌─────────────────────────────────────────────────────────┐
│ 📋 Database Tables          [Refresh] [+ Add Table]       │
├─────────────────────────────────────────────────────────┤
│ 🔍 Search: [________________________]                   │
├──────────┬─────────┬──────────┬─────────┬──────────┤
│ Table    │ Columns │  P.K.   │Indexes │ Actions  │
├──────────┼─────────┼──────────┼─────────┼──────────┤
│ users    │   17    │   id    │    3   │ [View][S] │
│ orders   │   12    │ order_id│    2   │ [View][S] │
│ products │   8     │ sku    │     1  │ [View][S] │
└──────────┴─────────┴──────────┴─────────┴──────────┘
```

### Column Types Explained
- **Columns**: How many fields each record has
- **P.K. (Primary Key)**: The unique identifier field
- **Indexes**: Fields optimized for searching

---

## Searching Data

### Quick Search (Single Table)
1. Go to **Tables**
2. Select a table
3. Type in search box
4. Results appear instantly!

### Global Search (All Tables at Once)
1. Click **🔍 Search** in sidebar
2. Type your search term (e.g., "john")
3. Click **Search** or press Enter
4. Results show ALL matching tables at once!

```
🔍 Global Search Results:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Found 150 results across 5 tables in 23ms
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
users (50 results)
┌─────────────────────────────────────────────┐
│ id │ name        │ email                 │
│ 1  │ John Doe   │ john@email.com        │
│ 2  │ Johnny    │ johnny@email.com     │
└─────────────────────────────────────────────┘
orders (30 results)
products (70 results)
```

### Understanding Duplicate Tags
If a record exists in multiple places, you'll see tags like:
```
[🔄] [duplicate_ref:db_02.users_0001]
```

This means this user record also exists in another table/database!

---

## Generating Reports

### Step-by-Step
1. Click **📄 Reports** in sidebar
2. Choose options:
   - **Select Database**: All databases or specific one
   - **Report Format**: CSV (Excel), JSON, or PDF
   - **Search Filter**: Optional filter (e.g., "email contains...")
   - **Limit Records**: Max records to export (1-10000)
3. Click **Download Report**
4. File automatically downloads!

### Format Comparison
| Format | Best For | Opens In |
|--------|---------|----------|
| CSV | Excel,数据分析 | Excel, Numbers, Google Sheets |
| JSON | Developers, APIs | Code editor, any tool |
| PDF | Printing, Sharing | Adobe, Browser |

---

## Troubleshooting

### "Database Not Connected"
```
❌ Check:
1. Is PostgreSQL running?
2. Is database URL correct in .env?
3. Are credentials correct?
```

**Solution**: 
```bash
# Check PostgreSQL is running
pg_isready -h localhost -p 5432

# Restart Docker if needed
docker-compose restart postgres
```

### "Can't Login"
```
❌ Possible reasons:
1. Wrong password
2. Account not created
3. Session expired
```

**Solution**:
1. Go to `/register` to create account
2. Or contact admin to reset password

### "No Tables Showing"
```
❌ Possible reasons:
1. Database is empty
2. No tables in public schema
3. Permission issues
```

**Solution**:
1. Check your database has tables
2. Ensure tables are in 'public' schema
3. Verify user has SELECT permission

### "Search Not Working"
```
❌ ClickHouse might be offline
```

**Solution**:
- Search still works using PostgreSQL (slightly slower but functional!)
- Status indicator shows ClickHouse as "Offline" but search still works

---

## Keyboard Shortcuts

| Shortcut | Action |
|---------|-------|
| Ctrl + F | Focus search box |
| Ctrl + R | Refresh current view |
| Escape | Close modal |

---

## Need More Help?

1. Check the API documentation: `/docs`
2. View Swagger/OpenAPI: `/swagger.yaml`
3. Check server logs in Docker dashboard

---

## For Developers

If you need to modify something:

### File Structure
```
acid/
├── cmd/api/              # ← START HERE - Main application entry
├── internal/
│   ├── config/         # Settings and configuration
│   ├── database/      # Database connections
│   ├── handlers/     # Request handling (how URLs work)
│   ├── middleware/  # Security (auth, rate limiting)
│   ├── schema/       # Table discovery
│   ├── clickhouse/   # Search engine integration
│   └── cache/        # Redis caching
├── web/              # Frontend HTML/CSS/JS files
├── databases/        # Database setup scripts
├── scripts/          # Automation scripts
└── docs/            # Documentation
```

### Never Touch These (Unless You Know What You're Doing!)
- `internal/database/pool.go` - Database connection pool
- `internal/auth/` - User authentication system  
- `internal/clickhouse/cdc.go` - Data sync system
- `go.mod` - Go dependencies

### Safe to Modify
- `.env` - Your configuration
- `web/admin.html` - Adding new UI elements
- `docs/` - Adding documentation

---

**Made with ❤️ for easy database management**