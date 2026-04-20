// =============================================================================
// internal/watcher/drop_sync.go
// =============================================================================
// Drop & Sync: Background worker that watches databases/incoming/ for .sql
// files dropped by the administrator. When a file is detected, it:
//   1. Waits briefly for the file write to finish
//   2. Executes each SQL statement in a transaction
//   3. Parses CREATE TABLE statements and hot-reloads the SchemaRegistry
//   4. Optionally triggers CDCManager to sync the new table to ClickHouse
//   5. Archives the file to databases/archive/ (or databases/incoming/failed/)
//
// Usage (from main.go):
//
//	w := watcher.NewDropSyncWatcher(watcher.Config{
//	    WatchDir:   "databases/incoming",
//	    ArchiveDir: "databases/archive",
//	    FailedDir:  "databases/incoming/failed",
//	    PGPool:     pool.Pool,
//	    Registry:   registry,
//	    CDCTrigger: func(table string) error { return cdcManager.TriggerTableSync(table) },
//	})
//	go w.Run(ctx)
// =============================================================================
package watcher

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"acid/internal/schema"

	"github.com/fsnotify/fsnotify"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CDCTriggerFunc is called after a new table is created so the CDC manager
// can start syncing it to ClickHouse immediately.
type CDCTriggerFunc func(tableName string) error

// Config holds configuration for the DropSyncWatcher.
type Config struct {
	WatchDir   string          // e.g. "databases/incoming"
	ArchiveDir string          // e.g. "databases/archive"
	FailedDir  string          // e.g. "databases/incoming/failed"
	PGPool     *pgxpool.Pool
	Registry   *schema.SchemaRegistry
	CDCTrigger CDCTriggerFunc  // optional — may be nil
}

// DropSyncWatcher watches a directory for incoming .sql files and applies
// them as live database migrations without dropping the server connection.
type DropSyncWatcher struct {
	cfg        Config
	watcher    *fsnotify.Watcher
	processing sync.Map // path → bool, prevents duplicate event handling
	logger     *log.Logger
}

// NewDropSyncWatcher creates and configures a new watcher.
// Returns an error if the watch directory cannot be created.
func NewDropSyncWatcher(cfg Config) (*DropSyncWatcher, error) {
	for _, dir := range []string{cfg.WatchDir, cfg.ArchiveDir, cfg.FailedDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("watcher: cannot create dir %q: %w", dir, err)
		}
	}

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watcher: fsnotify init failed: %w", err)
	}

	if err := fw.Add(cfg.WatchDir); err != nil {
		fw.Close()
		return nil, fmt.Errorf("watcher: cannot watch %q: %w", cfg.WatchDir, err)
	}

	return &DropSyncWatcher{
		cfg:     cfg,
		watcher: fw,
		logger:  log.New(os.Stdout, "[DropSync] ", log.LstdFlags|log.Lshortfile),
	}, nil
}

// Run starts the event loop. Call in a goroutine. Blocks until ctx is cancelled.
func (w *DropSyncWatcher) Run(ctx context.Context) {
	w.logger.Printf("🔭 Watching %q for incoming SQL files...", w.cfg.WatchDir)
	defer w.watcher.Close()

	// Process any files already sitting in the directory at startup
	w.processExistingFiles(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Println("⏹  Drop & Sync watcher stopped")
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			// Only react to newly created / renamed-into files
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
				if isSQLFile(event.Name) {
					go w.scheduleProcess(ctx, event.Name)
				}
			}

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.logger.Printf("⚠️  Watcher error: %v", err)
		}
	}
}

// scheduleProcess waits for the file to be fully written then processes it.
func (w *DropSyncWatcher) scheduleProcess(ctx context.Context, path string) {
	// Deduplicate rapid-fire events for the same file
	if _, alreadyQueued := w.processing.LoadOrStore(path, true); alreadyQueued {
		return
	}
	defer w.processing.Delete(path)

	// Give the writer time to finish flushing (max 10 seconds wait)
	if err := waitForFileReady(path, 10*time.Second); err != nil {
		w.logger.Printf("⚠️  File not ready %q: %v (skipping)", path, err)
		return
	}

	w.logger.Printf("📥 New SQL file detected: %s", filepath.Base(path))
	if err := w.processFile(ctx, path); err != nil {
		w.logger.Printf("❌ Failed to process %q: %v", filepath.Base(path), err)
		_ = w.moveFile(path, w.cfg.FailedDir)
	}
}

// processExistingFiles handles .sql files already in the watch dir at startup.
func (w *DropSyncWatcher) processExistingFiles(ctx context.Context) {
	entries, err := os.ReadDir(w.cfg.WatchDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() && isSQLFile(e.Name()) {
			path := filepath.Join(w.cfg.WatchDir, e.Name())
			w.logger.Printf("📂 Processing existing file at startup: %s", e.Name())
			go w.scheduleProcess(ctx, path)
		}
	}
}

// processFile is the core execution path for a single .sql file.
func (w *DropSyncWatcher) processFile(ctx context.Context, filePath string) error {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	sqlContent := string(raw)
	statements := splitSQL(sqlContent)
	if len(statements) == 0 {
		w.logger.Printf("⏭  File %q contains no executable SQL — archiving", filepath.Base(filePath))
		return w.moveFile(filePath, w.cfg.ArchiveDir)
	}

	w.logger.Printf("⚙️  Executing %d statements from %s", len(statements), filepath.Base(filePath))

	// Execute inside a transaction for atomicity
	txCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	tx, err := w.cfg.PGPool.Begin(txCtx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(txCtx) }()

	for i, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := tx.Exec(txCtx, stmt); err != nil {
			return fmt.Errorf("statement %d failed: %w\nSQL: %.200s", i+1, err, stmt)
		}
	}

	if err := tx.Commit(txCtx); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}

	w.logger.Printf("✅ All statements executed for %s", filepath.Base(filePath))

	// ── Hot-reload schema registry ────────────────────────────────────────────
	createdTables := extractCreatedTables(sqlContent)
	for _, table := range createdTables {
		w.logger.Printf("🔄 Hot-reloading schema for table: %s", table)
		if err := w.cfg.Registry.AddTable(table); err != nil {
			w.logger.Printf("⚠️  Schema reload warning for %q: %v", table, err)
		} else {
			w.logger.Printf("✅ SchemaRegistry updated: %s", table)
		}

		// ── Trigger CDC sync for the new table ────────────────────────────────
		if w.cfg.CDCTrigger != nil {
			go func(t string) {
				w.logger.Printf("🔁 Triggering CDC sync for: %s", t)
				if err := w.cfg.CDCTrigger(t); err != nil {
					w.logger.Printf("⚠️  CDC trigger failed for %q: %v", t, err)
				} else {
					w.logger.Printf("✅ CDC sync triggered for: %s", t)
				}
			}(table)
		}
	}

	// Move to archive
	return w.moveFile(filePath, w.cfg.ArchiveDir)
}

// moveFile moves src to dest/<timestamp>-<basename>.
func (w *DropSyncWatcher) moveFile(src, destDir string) error {
	base := filepath.Base(src)
	timestamp := time.Now().Format("20060102-150405")
	dest := filepath.Join(destDir, timestamp+"-"+base)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	if err := os.Rename(src, dest); err != nil {
		// Rename may fail across filesystems; fall back to copy+delete
		return copyAndDelete(src, dest)
	}
	w.logger.Printf("📦 Archived: %s → %s", base, filepath.Base(dest))
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// isSQLFile returns true for files ending in .sql (case-insensitive).
func isSQLFile(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".sql"
}

// waitForFileReady polls until the file size stops growing (i.e. write finished).
func waitForFileReady(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var prevSize int64 = -1

	for time.Now().Before(deadline) {
		info, err := os.Stat(path)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if info.Size() == prevSize && prevSize >= 0 {
			return nil // stable
		}
		prevSize = info.Size()
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("file %q did not stabilize within %v", path, timeout)
}

// splitSQL splits a SQL string into individual statements, handling:
//   - -- single-line comments
//   - /* block comments */
//   - String literals (don't split on ; inside quotes)
//   - Dollar-quoted strings ($$ ... $$)
func splitSQL(sql string) []string {
	var statements []string
	var current strings.Builder
	inSingleQuote := false
	inBlockComment := false
	inLineComment := false
	dollarQuote := ""
	runes := []rune(sql)
	n := len(runes)

	for i := 0; i < n; i++ {
		ch := runes[i]

		// ── Line comment end ─────────────────────────────────────────────────
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			current.WriteRune(ch)
			continue
		}

		// ── Block comment end ─────────────────────────────────────────────────
		if inBlockComment {
			if ch == '*' && i+1 < n && runes[i+1] == '/' {
				inBlockComment = false
				current.WriteRune('*')
				current.WriteRune('/')
				i++
			} else {
				current.WriteRune(ch)
			}
			continue
		}

		// ── Dollar-quote mode ─────────────────────────────────────────────────
		if dollarQuote != "" {
			s := string(runes[i:])
			if strings.HasPrefix(s, dollarQuote) {
				current.WriteString(dollarQuote)
				i += len([]rune(dollarQuote)) - 1
				dollarQuote = ""
			} else {
				current.WriteRune(ch)
			}
			continue
		}

		// ── Single-quote string ───────────────────────────────────────────────
		if inSingleQuote {
			current.WriteRune(ch)
			if ch == '\'' {
				if i+1 < n && runes[i+1] == '\'' {
					// escaped quote
					current.WriteRune(runes[i+1])
					i++
				} else {
					inSingleQuote = false
				}
			}
			continue
		}

		// ── Detect start of comment/quote/dollar-quote ────────────────────────
		if ch == '-' && i+1 < n && runes[i+1] == '-' {
			inLineComment = true
			current.WriteRune('-')
			current.WriteRune('-')
			i++
			continue
		}
		if ch == '/' && i+1 < n && runes[i+1] == '*' {
			inBlockComment = true
			current.WriteRune('/')
			current.WriteRune('*')
			i++
			continue
		}
		if ch == '\'' {
			inSingleQuote = true
			current.WriteRune(ch)
			continue
		}
		if ch == '$' {
			// Detect dollar-quote tag: $$, $body$, $func$, etc.
			end := strings.Index(string(runes[i+1:]), "$")
			if end >= 0 {
				tag := string(runes[i : i+end+2])
				if isDollarTag(tag) {
					dollarQuote = tag
					current.WriteString(tag)
					i += len([]rune(tag)) - 1
					continue
				}
			}
		}

		// ── Statement separator ───────────────────────────────────────────────
		if ch == ';' {
			stmt := strings.TrimSpace(current.String())
			if stmt != "" && !isMetaCommand(stmt) {
				statements = append(statements, stmt)
			}
			current.Reset()
			continue
		}

		current.WriteRune(ch)
	}

	// Flush trailing statement without semicolon
	if stmt := strings.TrimSpace(current.String()); stmt != "" && !isMetaCommand(stmt) {
		statements = append(statements, stmt)
	}

	return statements
}

// isDollarTag validates a PostgreSQL dollar-quote tag like $$, $body$, $func$.
func isDollarTag(tag string) bool {
	if len(tag) < 2 {
		return false
	}
	matched, _ := regexp.MatchString(`^\$[a-zA-Z0-9_]*\$$`, tag)
	return matched
}

// isMetaCommand detects psql meta-commands like \connect, \i, \set.
func isMetaCommand(s string) bool {
	return len(s) > 0 && s[0] == '\\'
}

// extractCreatedTables parses SQL and returns names of newly CREATE'd tables.
var createTableRe = regexp.MustCompile(
	`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:"?([a-zA-Z0-9_]+)"?\.)?"?([a-zA-Z0-9_]+)"?`,
)

func extractCreatedTables(sql string) []string {
	seen := make(map[string]bool)
	var tables []string
	for _, match := range createTableRe.FindAllStringSubmatch(sql, -1) {
		name := match[2]
		if name != "" && !seen[name] {
			seen[name] = true
			tables = append(tables, name)
		}
	}
	return tables
}

// copyAndDelete is a cross-filesystem fallback for os.Rename.
func copyAndDelete(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return err
	}
	return os.Remove(src)
}
