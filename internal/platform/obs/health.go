package obs

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Check is a named readiness probe.
type Check struct {
	Name string
	Fn   func(ctx context.Context) error
}

// HealthMux serves GET /healthz (liveness) and GET /readyz (runs checks).
func HealthMux(checks ...Check) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		res, code := map[string]string{}, http.StatusOK
		for _, c := range checks {
			if err := c.Fn(ctx); err != nil {
				res[c.Name], code = "fail", http.StatusServiceUnavailable
			} else {
				res[c.Name] = "ok"
			}
		}
		writeJSON(w, code, res)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
