# Database Workspace

This folder is the single place to store and operate on SQL files for this project.

## Folder structure

- `databases/incoming/` → newly received SQL files
- `databases/migrations/` → ordered schema changes to apply
- `databases/seeds/` → test/sample data files
- `databases/archive/` → old SQL files kept for history

## Windows usage

Use the database manager script:

```bat
scripts\database-manager.bat list
scripts\database-manager.bat apply databases\migrations\001_example.sql
scripts\database-manager.bat apply-all migrations
```

## Requirements

- `psql` must be installed and available in PATH
- `DATABASE_URL` must be set (or present in `.env`)

## Rule

Keep all database SQL work inside `/databases` so storage, retrieval, and updates are organized in one predictable location.
