package db

import (
	"context"
	"testing"
	"time"
)

func TestRecordAndLatestEvalRun(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := Open(MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := Migrate(sqlDB); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := LatestEvalRun(ctx, sqlDB, "routine", "ollama:qwen"); err != nil || ok {
		t.Fatalf("empty table: ok=%v err=%v, want ok=false", ok, err)
	}

	older := EvalRun{Family: "routine", ModelRef: "ollama:qwen", Digest: "d1", Passed: 6, Total: 10, PassPct: 60, RanAt: time.Unix(1000, 0).UTC()}
	newer := EvalRun{Family: "routine", ModelRef: "ollama:qwen", Digest: "d2", Passed: 9, Total: 10, PassPct: 90, RanAt: time.Unix(2000, 0).UTC()}
	for _, r := range []EvalRun{older, newer} {
		if err := RecordEvalRun(ctx, sqlDB, r); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := LatestEvalRun(ctx, sqlDB, "routine", "ollama:qwen")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.PassPct != 90 || got.Digest != "d2" {
		t.Fatalf("latest = %+v, want pct 90 digest d2", got)
	}
	if _, ok, _ := LatestEvalRun(ctx, sqlDB, "classify", "ollama:qwen"); ok {
		t.Fatal("different family must not match")
	}
}
