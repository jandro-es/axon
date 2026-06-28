package dashboard

import (
	"context"
	"database/sql"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/events"
)

// PersistEvents subscribes to the event bus and writes every event to the events
// table, giving the dashboard activity feed a durable history (Component 09 §3:
// the bus fans out to SSE *and* the events table). It runs until ctx is
// cancelled. Secrets never appear in event data (emitters' responsibility).
func PersistEvents(ctx context.Context, bus *events.Bus, sqlDB *sql.DB) {
	if bus == nil || sqlDB == nil {
		return
	}
	sub := bus.Subscribe()
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			_ = db.InsertEvent(ctx, sqlDB, e.TS, string(e.Level), e.Kind, e.Message, e.Data)
		}
	}
}
