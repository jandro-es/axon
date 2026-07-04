package dashboard

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jandro-es/axon/internal/db"
)

// handleExport serves any chart dataset as CSV or JSON (FR-64). JSON reuses
// the same data functions the charts poll; CSV flattens with explicit
// columns. The graph dataset is JSON-only (nested nodes/edges).
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	dataset := r.URL.Query().Get("dataset")
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}

	fetch := map[string]func() (any, error){
		"tokens":    func() (any, error) { return s.dataTokens(r.Context(), r) },
		"runs":      func() (any, error) { return s.dataRuns(r.Context(), r) },
		"ingestion": func() (any, error) { return s.dataIngestion(r.Context(), r) },
		"vault":     func() (any, error) { return s.dataVault(r.Context(), r) },
		"graph":     func() (any, error) { return s.dataGraph(r.Context(), r) },
		"activity":  func() (any, error) { return s.dataActivity(r.Context(), r) },
	}
	fn, ok := fetch[dataset]
	if !ok {
		http.Error(w, "unknown dataset (tokens|runs|ingestion|vault|graph|activity)", http.StatusBadRequest)
		return
	}
	data, err := fn()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	name := fmt.Sprintf("axon-%s-%s", dataset, time.Now().UTC().Format("2006-01-02"))
	switch format {
	case "json":
		w.Header().Set("Content-Disposition", `attachment; filename=`+name+`.json`)
		writeJSON(w, data)
	case "csv":
		rows, header, ok := csvRows(dataset, data)
		if !ok {
			http.Error(w, "dataset "+dataset+" is JSON-only (nested); use format=json", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename=`+name+`.csv`)
		cw := csv.NewWriter(w)
		_ = cw.Write(header)
		for _, row := range rows {
			_ = cw.Write(row)
		}
		cw.Flush()
	default:
		http.Error(w, "format must be csv or json", http.StatusBadRequest)
	}
}

// csvRows flattens the flat datasets; ok=false for nested ones (graph).
func csvRows(dataset string, data any) ([][]string, []string, bool) {
	i := strconv.Itoa
	i64 := func(n int64) string { return strconv.FormatInt(n, 10) }
	switch dataset {
	case "tokens":
		rows := data.([]db.TokenBucket)
		out := make([][]string, 0, len(rows))
		for _, b := range rows {
			out = append(out, []string{b.Day, b.Operation, b.Model, i64(b.Input), i64(b.Output), i64(b.CacheRead), i64(b.CacheWrite)})
		}
		return out, []string{"day", "operation", "model", "input", "output", "cache_read", "cache_write"}, true
	case "runs":
		rows := data.([]db.RunRow)
		out := make([][]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, []string{i64(r.ID), r.Automation, r.StartedAt, r.FinishedAt, r.Status, r.SkipReason, i64(r.Tokens)})
		}
		return out, []string{"id", "automation", "started_at", "finished_at", "status", "skip_reason", "tokens"}, true
	case "activity":
		rows := data.([]db.EventRow)
		out := make([][]string, 0, len(rows))
		for _, e := range rows {
			out = append(out, []string{i64(e.ID), e.TS, e.Level, e.Kind, e.Message})
		}
		return out, []string{"id", "ts", "level", "kind", "message"}, true
	case "ingestion":
		m := data.(map[string]any)
		rows := m["series"].([]db.SourceBucket)
		out := make([][]string, 0, len(rows))
		for _, b := range rows {
			out = append(out, []string{b.Day, b.Status, i(b.Count)})
		}
		return out, []string{"day", "status", "count"}, true
	case "vault":
		m := data.(map[string]any)
		rows := m["growth"].([]db.GrowthPoint)
		out := make([][]string, 0, len(rows))
		for _, g := range rows {
			out = append(out, []string{g.Day, i(g.Notes), i(g.Words)})
		}
		return out, []string{"day", "notes", "words"}, true
	default:
		return nil, nil, false
	}
}
