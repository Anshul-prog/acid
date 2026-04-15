// #file: audit.go
// #package: middleware
// #purpose: Forensic audit-logging middleware.
//
// Every HTTP request that passes through AuditLogger is recorded in
// entity_access_logs with:
//   • acting user ID + username (extracted from the JWT context)
//   • client IP address
//   • HTTP method + path
//   • response status code
//   • exact start timestamp and duration
//
// The log write is fire-and-forget (background goroutine) so it never adds
// latency to the primary response.  Failures print to stderr only.

package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// #audit-writer: tiny interface so the middleware doesn't import the full
//   database package (avoids circular imports).
type auditWriter interface {
	Exec(ctx context.Context, sql string, args ...any) error
}

// pgxAuditWriter wraps *pgxpool.Pool to satisfy auditWriter.
type pgxAuditWriter struct{ pool *pgxpool.Pool }

func (w *pgxAuditWriter) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := w.pool.Exec(ctx, sql, args...)
	return err
}

// statusRecorder wraps http.ResponseWriter to capture the written status code.
// #response-capture: needed because http.ResponseWriter doesn't expose status.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// AuditLogger returns an http.Handler middleware that records every request.
//
// Usage (main.go):
//
//	handler = middleware.AuditLogger(pool)(handler)
//
// #forensic: wraps the entire router so no request escapes logging.
func AuditLogger(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	writer := &pgxAuditWriter{pool: pool}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			dur := time.Since(start)

			// Extract identity from JWT context (set by AuthMiddleware).
			userCtx := GetUserContext(r.Context())
			userID := ""
			username := ""
			if userCtx != nil {
				userID = fmt.Sprintf("%d", userCtx.UserID)
				username = userCtx.Username
			}

			// Fire-and-forget: log write must not block or fail the response.
			// We use context.Background() intentionally here so that a log write is not
			// cancelled when the HTTP request context ends (e.g. client disconnects).
			// A 3-second timeout is applied to bound the goroutine lifetime.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()

				const q = `
					INSERT INTO entity_access_logs
					  (user_id, username, action, query_text, ip_address, user_agent, result_count)
					VALUES ($1, $2, $3, $4, $5, $6, $7)
				`
				action := fmt.Sprintf("%s %s → %d (%dms)",
					r.Method, r.URL.Path, rec.status, dur.Milliseconds())

				err := writer.Exec(ctx, q,
					userID, username,
					action,
					r.URL.RawQuery,
					clientIP(r),
					r.UserAgent(),
					0,
				)
				if err != nil {
					fmt.Printf("[audit] log write failed: %v\n", err)
				}
			}()
		})
	}
}

// clientIP extracts the real client IP, preferring X-Forwarded-For.
// #ip-extraction: handles reverse-proxy deployments.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	return r.RemoteAddr
}
