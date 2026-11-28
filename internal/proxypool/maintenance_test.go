package proxypool

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestWALSafetyLoopWorksWithMaintenanceDisabled(t *testing.T) {
	store := newTestStore(t)
	cfg, err := store.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaintenanceEnabled = false
	if err := store.UpdateSettings(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if store.WALSize() == 0 {
		t.Fatal("expected WAL frames")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); runWALCheckpointLoop(ctx, store, 5*time.Millisecond) }()
	defer func() { cancel(); <-done }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			if store.WALSize() == 0 {
				return
			}
		case <-timeout.C:
			t.Fatal("independent checkpoint did not reclaim WAL with maintenance disabled")
		}
	}
}

func TestCheckpointWALTruncatesAndPreservesData(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO source_stats(url) VALUES(?)`, strings.Repeat("x", 4096)+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	before := store.WALSize()
	if before == 0 {
		t.Fatal("expected non-empty WAL")
	}
	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatal(err)
	}
	if after := store.WALSize(); after != 0 {
		t.Fatalf("WAL not truncated: before=%d after=%d", before, after)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_stats`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 25 {
		t.Fatalf("checkpoint lost data: %d", count)
	}
	t.Logf("WAL bytes: %d -> %d; retained %d rows", before, store.WALSize(), count)
}

func TestCheckpointWALReportsPinnedReader(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := other.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_stats`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `PRAGMA busy_timeout=10`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO source_stats(url) VALUES('reader-pinned')`); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckpointWAL(ctx); err == nil {
		t.Fatal("pinned read transaction must not be reported as checkpoint success")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatal(err)
	}
	if store.WALSize() != 0 {
		t.Fatal("WAL did not shrink after reader released")
	}
}

func TestVacuumReclaimsDeletedDatabasePages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `CREATE TABLE growth_probe (body BLOB); INSERT INTO growth_probe VALUES(zeroblob(2097152))`); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatal(err)
	}
	before := store.DBSize()
	if _, err := store.db.ExecContext(ctx, `DELETE FROM growth_probe`); err != nil {
		t.Fatal(err)
	}
	if err := store.Vacuum(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatal(err)
	}
	after := store.DBSize()
	if after >= before {
		t.Fatalf("VACUUM failed to reclaim pages: before=%d after=%d", before, after)
	}
	t.Logf("main database bytes: %d -> %d", before, after)
}
