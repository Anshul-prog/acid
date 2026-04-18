// recovery.go — global HTTP panic-recovery middleware.
//
// Catches any panic that escapes a handler, logs the stack trace, and
// returns a structured JSON 500 response so the server never crashes.
// This is the last line of defence and MUST be the outermost middleware.
package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"
)

// Recovery wraps every request in a deferred panic catcher.
// It logs the stack trace to stderr and writes a structured JSON error to the
// client so callers always receive a machine-readable response body.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				// Print structured panic log to stderr.
				fmt.Printf("[PANIC] %s %s | reason: %v | stack:\n%s\n",
					r.Method, r.URL.Path, rec, stack)

				// Only write headers if they haven't been sent yet.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error":     "internal server error",
					"timestamp": time.Now().UTC().Format(time.RFC3339),
					"path":      r.URL.Path,
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
