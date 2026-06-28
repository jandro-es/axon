package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleSSE streams live events off the in-process bus to the browser. Each
// event is delivered as a `data: <json>` SSE frame within milliseconds of being
// published (NFR-07 / S4: ≤5s end-to-end). A periodic ping keeps proxies and the
// connection alive.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if s.cfg.Bus == nil {
		http.Error(w, "no event bus", http.StatusServiceUnavailable)
		return
	}
	sub := s.cfg.Bus.Subscribe()
	defer sub.Close()

	// Greet so the client knows the stream is live.
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			payload, err := json.Marshal(e)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Kind, payload)
			flusher.Flush()
		}
	}
}
