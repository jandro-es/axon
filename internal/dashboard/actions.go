package dashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/actions"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/vault"
)

func (s *Server) handleActions(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.ActionsEnabled || s.cfg.DB == nil {
		http.Error(w, "actions are disabled for this profile", http.StatusNotFound)
		return
	}
	if r.Header.Get("X-Axon-Actions") != "1" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rows, err := db.ListActions(r.Context(), s.cfg.DB, db.ListActionsOpts{IncludeAll: true})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	payload := buildActionsPayload(rows, time.Now())
	// The Obsidian vault name (its folder basename) lets the SPA build
	// `obsidian://open?vault=…&file=…` deep links per action. Only present when
	// a vault is wired; the SPA falls back to a plain label otherwise.
	if s.cfg.Vault != nil {
		payload["vault"] = filepath.Base(s.cfg.Vault.Root())
	}
	writeJSON(w, payload)
}

// buildActionsPayload turns the derived rows into the tab's data: the non-archived
// actions (each tagged with its read-time bucket), a GTD counts summary, and a
// 30-day completion trend from done_date. Pure — unit-tested without HTTP.
func buildActionsPayload(rows []db.Action, today time.Time) map[string]any {
	weekAgo := today.AddDate(0, 0, -7).Format("2006-01-02")

	type item struct {
		db.Action
		Bucket string `json:"bucket"`
	}
	items := make([]item, 0, len(rows))
	counts := map[string]int{"open": 0, "overdue": 0, "today": 0, "waiting": 0, "someday": 0, "done7": 0}
	doneByDay := map[string]int{}
	for _, row := range rows {
		if row.Archived {
			continue
		}
		b := actions.BucketFields(row.State, row.Due, row.Scheduled, row.Start, row.Tags, today)
		items = append(items, item{row, b})
		switch b {
		case "done":
			if row.DoneDate != "" {
				doneByDay[row.DoneDate]++
			}
			if row.DoneDate >= weekAgo {
				counts["done7"]++
			}
		case "cancelled":
			// not counted
		default:
			counts["open"]++
			if _, ok := counts[b]; ok {
				counts[b]++
			}
		}
	}
	trend := make([]map[string]any, 0, 30)
	for i := 29; i >= 0; i-- {
		d := today.AddDate(0, 0, -i).Format("2006-01-02")
		trend = append(trend, map[string]any{"day": d, "done": doneByDay[d]})
	}
	return map[string]any{"actions": items, "counts": counts, "trend": trend}
}

func (s *Server) handleActionComplete(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.ActionsEnabled || s.cfg.Vault == nil {
		http.Error(w, "actions are disabled for this profile", http.StatusNotFound)
		return
	}
	if r.Header.Get("X-Axon-Actions") != "1" ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var in struct {
		Path string `json:"path"`
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if in.Path == "" || in.Hash == "" {
		http.Error(w, "path and hash required", http.StatusBadRequest)
		return
	}
	date := time.Now().Format("2006-01-02")
	err := s.cfg.Vault.CompleteAction(r.Context(), in.Path, in.Hash, date)
	if errors.Is(err, vault.ErrActionNotFound) {
		http.Error(w, "action not found (already done or changed) — refresh", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.cfg.DB != nil {
		_, _ = db.MarkActionDone(r.Context(), s.cfg.DB, in.Hash, date)
	}
	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(events.Event{
			Level: events.LevelInfo, Kind: "action.done",
			Message: "completed action in " + in.Path,
			Data:    map[string]any{"profile": s.cfg.Profile, "path": in.Path, "date": date},
		})
	}
	writeJSON(w, map[string]any{"ok": true, "path": in.Path, "date": date})
}
