package dashboard

import (
	"net/http"
)

// handleHealth reports daemon/DB health plus the last run status per automation.
// It contains no secrets and powers both the dashboard status pill and tooling.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{
		"status":  "ok",
		"profile": s.cfg.Profile,
	}

	dbOK := true
	if s.cfg.DB != nil {
		if err := s.cfg.DB.PingContext(ctx); err != nil {
			dbOK = false
			out["status"] = "degraded"
		}
	}
	out["db"] = dbOK
	out["ask_enabled"] = s.cfg.AskEnabled

	if s.cfg.Health != nil {
		for k, v := range s.cfg.Health(ctx) {
			out[k] = v
		}
	}
	writeJSON(w, out)
}
