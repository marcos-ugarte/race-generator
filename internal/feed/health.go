package feed

import (
	"encoding/json"
	"net/http"
)

// registerHealth mounts /v1/healthz (liveness) and /v1/readyz (readiness:
// poller started + DB readable) without any middleware chain. Health
// probes must not be auth-gated or rate-limited.
func registerHealth(mux *http.ServeMux, deps *Deps, wsConnected func() int) {
	mux.HandleFunc("GET /v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("GET /v1/readyz", func(w http.ResponseWriter, r *http.Request) {
		checks := map[string]string{
			"poller": "ok",
			"db":     "ok",
		}
		if deps.Poller == nil || !deps.Poller.Started() {
			checks["poller"] = "down"
		}
		if err := deps.Reader.Ping(); err != nil {
			checks["db"] = "error"
		}

		ready := true
		for _, v := range checks {
			if v != "ok" {
				ready = false
				break
			}
		}
		status := "ready"
		code := http.StatusOK
		if !ready {
			status = "not_ready"
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    status,
			"checks":    checks,
			"wsClients": wsConnected(),
		})
	})
}
