package feed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
)

// keyShape validates an API key: URL-safe base64-ish, 16..64 chars. Keys
// that do not match are dropped at parse time so a malformed env can never
// silently widen access.
var keyShape = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// ParseKeysCSV turns a comma-separated key list into a set, skipping empty
// and malformed entries. Returns the set plus the count of rejected
// entries so the caller can warn at boot.
func ParseKeysCSV(csv string) (map[string]struct{}, int) {
	out := map[string]struct{}{}
	rejected := 0
	for _, raw := range strings.Split(csv, ",") {
		k := strings.TrimSpace(raw)
		if k == "" {
			continue
		}
		if !keyShape.MatchString(k) {
			rejected++
			continue
		}
		out[k] = struct{}{}
	}
	return out, rejected
}

// AuthConfig holds the resolved key pools and the require-key switch
// shared by the REST chain and the WS upgrade handler.
type AuthConfig struct {
	RequireKey bool
	APIKeys    map[string]struct{}
	AdminKeys  map[string]struct{}
}

// extractAPIKey pulls the candidate key, header first then query. The
// query form exists only for browser WebSocket clients (the JS WebSocket
// API cannot set custom headers); server-to-server callers should prefer
// the X-API-Key header.
func extractAPIKey(r *http.Request) string {
	raw := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("apikey"))
	}
	return raw
}

// authorised reports whether the request carries a key in either the
// partner pool or the admin pool. Admin keys pass the public gate.
func (a AuthConfig) authorised(r *http.Request) bool {
	if !a.RequireKey {
		return true
	}
	raw := extractAPIKey(r)
	if raw == "" {
		return false
	}
	if _, ok := a.APIKeys[raw]; ok {
		return true
	}
	if _, ok := a.AdminKeys[raw]; ok {
		return true
	}
	return false
}

// writeAuthError emits a generic 401 that never leaks whether a key
// exists. A key prefix is logged so a 401 storm is visible in container
// logs.
func writeAuthError(w http.ResponseWriter, r *http.Request, lg *log.Logger) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	raw := extractAPIKey(r)
	body := map[string]string{"error": "api_key_required"}
	prefix := "<empty>"
	if raw != "" {
		body["error"] = "api_key_invalid"
		if len(raw) >= 8 {
			prefix = raw[:8]
		} else {
			prefix = raw
		}
	}
	if lg != nil {
		lg.Printf("[FEED/auth] auth_failed prefix=%s remote=%s path=%s", prefix, r.RemoteAddr, r.URL.Path)
	}
	_ = json.NewEncoder(w).Encode(body)
}

// authMiddleware gates the REST surface. No-op when RequireKey is false.
func authMiddleware(a AuthConfig, lg *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authorised(r) {
			writeAuthError(w, r, lg)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestIDKey is the context key for the per-request correlation ID.
type ctxKey int

const requestIDKey ctxKey = 0

// requestIDMiddleware stamps an X-Request-Id (echoing an inbound one when
// present) and stashes it on the context.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if rid == "" {
			rid = newRequestID()
		}
		w.Header().Set("X-Request-Id", rid)
		ctx := context.WithValue(r.Context(), requestIDKey, rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// recoverMiddleware turns a panicking handler into a 500 instead of
// crashing the process. Logs the stack with the request ID.
func recoverMiddleware(lg *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if lg != nil {
					rid, _ := r.Context().Value(requestIDKey).(string)
					lg.Printf("[FEED/panic] rid=%s path=%s recovered=%v\n%s", rid, r.URL.Path, rec, debug.Stack())
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal_error"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// newRequestID returns a short random hex token. Falls back to a fixed
// label if the system RNG is unavailable (never in practice).
func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req-unknown"
	}
	return hex.EncodeToString(b[:])
}

// chain wraps h with the REST middleware stack (outer→inner):
// recover → requestID → auth.
func chain(a AuthConfig, lg *log.Logger, h http.Handler) http.Handler {
	h = authMiddleware(a, lg, h)
	h = requestIDMiddleware(h)
	h = recoverMiddleware(lg, h)
	return h
}
