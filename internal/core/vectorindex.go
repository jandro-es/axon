package core

import (
	"context"
	"database/sql"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

// RefreshVectorIndex keeps the ANN index consistent with the current vectors.
// In brute mode it is a no-op. In ann mode it does a full BuildIVF when no
// centroids exist yet, otherwise a cheap AssignPendingCentroids pass for newly
// embedded (overflow) vectors. Callers run it after ReembedPending.
func RefreshVectorIndex(ctx context.Context, sqlDB *sql.DB, ret config.RetrievalConfig) error {
	if ret.IndexMode() != "ann" {
		return nil
	}
	n, err := db.CountCentroids(ctx, sqlDB)
	if err != nil {
		return err
	}
	if n == 0 {
		_, err = db.BuildIVF(ctx, sqlDB)
		return err
	}
	_, err = db.AssignPendingCentroids(ctx, sqlDB)
	return err
}
