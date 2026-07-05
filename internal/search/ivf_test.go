package search

import (
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

func TestConfigureSelectsBackend(t *testing.T) {
	s := New(nil, nil).Configure(config.RetrievalConfig{Index: "ann", ANN: config.ANNConfig{Threshold: 500, NProbe: 4}})
	idx := s.vindex()
	iv, ok := idx.(db.IVFIndex)
	if !ok {
		t.Fatalf("want IVFIndex, got %T", idx)
	}
	if iv.Threshold != 500 || iv.NProbe != 4 {
		t.Fatalf("IVF tuning not threaded: %+v", iv)
	}

	b := New(nil, nil) // unconfigured
	if _, ok := b.vindex().(db.BruteIndex); !ok {
		t.Fatalf("default backend should be BruteIndex, got %T", b.vindex())
	}
}
