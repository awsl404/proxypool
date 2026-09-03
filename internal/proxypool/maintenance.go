package proxypool

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type MaintenanceSnapshot struct {
	Enabled    bool               `json:"enabled"`
	Running    bool               `json:"running"`
	LastRun    *time.Time         `json:"last_run,omitempty"`
	NextRun    *time.Time         `json:"next_run,omitempty"`
	LastResult *MaintenanceResult `json:"last_result,omitempty"`
}

var maintenanceState = struct {
	mu      sync.Mutex
	running bool
	lastRun *time.Time
	nextRun *time.Time
	result  *MaintenanceResult
}{}

func StartMaintenanceLoop(ctx context.Context, store *Store) {
	go func() {
		for {
			cfg, err := store.Settings(ctx)
			interval := 3600
			enabled := false
			if err == nil {
				interval = maxInt(60, cfg.MaintenanceInterval)
				enabled = cfg.MaintenanceEnabled
			}
			next := time.Now().Add(time.Duration(interval) * time.Second)
			setMaintenanceNext(next)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(interval) * time.Second):
			}
			if !enabled {
				continue
			}
			_, _ = runMaintenanceWithState(ctx, store, cfg)
		}
	}()
}

func MaintenanceStatus(enabled bool) MaintenanceSnapshot {
	maintenanceState.mu.Lock()
	defer maintenanceState.mu.Unlock()
	return MaintenanceSnapshot{
		Enabled:    enabled,
		Running:    maintenanceState.running,
		LastRun:    maintenanceState.lastRun,
		NextRun:    maintenanceState.nextRun,
		LastResult: maintenanceState.result,
	}
}

func RunMaintenanceNow(ctx context.Context, store *Store, cfg Settings) (MaintenanceResult, bool) {
	return runMaintenanceWithState(ctx, store, cfg)
}

func runMaintenanceWithState(ctx context.Context, store *Store, cfg Settings) (MaintenanceResult, bool) {
	maintenanceState.mu.Lock()
	if maintenanceState.running {
		maintenanceState.mu.Unlock()
		return MaintenanceResult{Errors: []string{"维护任务正在运行"}}, false
	}
	maintenanceState.running = true
	maintenanceState.mu.Unlock()

	appInfo("maintenance started")
	result := RunMaintenance(ctx, store, cfg)
	now := time.Now().UTC()
	maintenanceState.mu.Lock()
	maintenanceState.running = false
	maintenanceState.lastRun = &now
	maintenanceState.result = &result
	maintenanceState.mu.Unlock()
	appInfo("maintenance finished deleted_unavailable=%d deleted_sources=%d log_removed=%d vacuum=%t backup=%s errors=%d", result.DeletedUnavailable, result.DeletedSources, result.LogLinesRemoved, result.Vacuumed, result.BackupPath, len(result.Errors))
	return result, true
}

func setMaintenanceNext(next time.Time) {
	maintenanceState.mu.Lock()
	defer maintenanceState.mu.Unlock()
	maintenanceState.nextRun = &next
}

func RunMaintenance(ctx context.Context, store *Store, cfg Settings) MaintenanceResult {
	result := MaintenanceResult{}
	if cfg.AutoCleanup {
		deleted, err := store.DeleteUnavailable(ctx)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.DeletedUnavailable = deleted
		}
	}
	if cfg.SourceRetentionDays > 0 {
		deleted, err := store.DeleteOldSourceStats(ctx, cfg.SourceRetentionDays)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.DeletedSources = deleted
		}
	}
	if cfg.LogRetentionDays > 0 {
		removed, err := appLogger.TrimRetention(cfg.LogFile, cfg.LogRetentionDays)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.LogLinesRemoved = removed
		}
	}
	if cfg.AutoBackup {
		path, err := store.Backup(ctx, cfg.BackupDir)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.BackupPath = path
		}
	}
	if cfg.AutoVacuum {
		if err := store.Vacuum(ctx); err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.Vacuumed = true
		}
	}
	return result
}

func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "VACUUM")
	return err
}

func (s *Store) Backup(ctx context.Context, dir string) (string, error) {
	_ = ctx
	if dir == "" {
		dir = defaultSettings().BackupDir
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "proxypool-"+time.Now().Format("20060102-150405")+".db")
	if err := copyFile(s.path, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func (s *Store) DeleteOldSourceStats(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	res, err := s.db.ExecContext(ctx, `DELETE FROM source_stats WHERE last_run IS NOT NULL AND last_run < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) SourceStatsSummary(ctx context.Context) (total, failing int, err error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN fetch_fail > 0 THEN 1 ELSE 0 END), 0) FROM source_stats`)
	err = row.Scan(&total, &failing)
	return total, failing, err
}

func (s *Store) CustomScraperSummary(ctx context.Context) (total, enabled int, err error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN enabled = 1 THEN 1 ELSE 0 END), 0) FROM custom_scrapers`)
	err = row.Scan(&total, &enabled)
	return total, enabled, err
}

func (s *Store) DBSize() int64 {
	return fileSize(s.path)
}

func fileSize(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func nullTimePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}
