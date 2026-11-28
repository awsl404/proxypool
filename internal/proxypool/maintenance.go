package proxypool

import (
	"context"
	"database/sql"
	"fmt"
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
	// WAL housekeeping must not depend on optional (potentially destructive)
	// retention/backup maintenance. Only checkpoint here; never delete user data.
	go runWALCheckpointLoop(ctx, store, time.Minute)
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

func runWALCheckpointLoop(ctx context.Context, store *Store, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			// Finish the current bounded checkpoint before stopping. Cancelling
			// SQLite mid-checkpoint races connection cleanup on Windows.
			if err := store.CheckpointWAL(context.WithoutCancel(ctx)); err != nil && ctx.Err() == nil {
				appWarn("WAL checkpoint deferred: %v", err)
			}
		}
	}
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
	appInfo("maintenance finished deleted_unavailable=%d deleted_sources=%d deleted_dead=%d log_removed=%d vacuum=%t wal_checkpoint=%t over_limit=%t backup=%s errors=%d", result.DeletedUnavailable, result.DeletedSources, result.DeletedDead, result.LogLinesRemoved, result.Vacuumed, result.WALCheckpointed, result.OverLimitCleanup, result.BackupPath, len(result.Errors))
	return result, true
}

func setMaintenanceNext(next time.Time) {
	maintenanceState.mu.Lock()
	defer maintenanceState.mu.Unlock()
	maintenanceState.nextRun = &next
}

func RunMaintenance(ctx context.Context, store *Store, cfg Settings) MaintenanceResult {
	result := MaintenanceResult{}
	if cfg.SourceRetentionDays > 0 {
		deleted, err := store.DeleteOldSourceStats(ctx, cfg.SourceRetentionDays)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.DeletedSources = deleted
		}
	}
	if cfg.DeadRetentionDays > 0 {
		deleted, err := store.DeleteOldDeadProxies(ctx, cfg.DeadRetentionDays)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.DeletedDead = deleted
		}
	}
	if cfg.AutoCleanup {
		deleted, err := store.DeleteUnavailable(ctx)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.DeletedUnavailable = deleted
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
	// WAL 兜底：主库 + WAL 超过上限时，先删旧数据再 checkpoint + VACUUM 回收空间。
	if overLimit, limitBytes := store.dataOverLimit(cfg.MaxDBSizeMB); overLimit {
		result.OverLimitCleanup = true
		appWarn("database over size limit: data=%d limit=%d, running emergency cleanup", store.DataSize(), limitBytes)
		if cfg.DeadRetentionDays <= 0 {
			if deleted, err := store.DeleteOldDeadProxies(ctx, defaultSettings().DeadRetentionDays); err != nil {
				result.Errors = append(result.Errors, err.Error())
			} else {
				result.DeletedDead += deleted
			}
		}
		if cfg.SourceRetentionDays <= 0 {
			if deleted, err := store.DeleteOldSourceStats(ctx, defaultSettings().SourceRetentionDays); err != nil {
				result.Errors = append(result.Errors, err.Error())
			} else {
				result.DeletedSources += deleted
			}
		}
	}
	// 每次维护显式 TRUNCATE checkpoint；被活跃事务阻止时记录错误，随后重试。
	if err := store.CheckpointWAL(ctx); err != nil {
		result.Errors = append(result.Errors, err.Error())
	} else {
		result.WALCheckpointed = true
	}
	if cfg.AutoVacuum || result.OverLimitCleanup {
		if err := store.Vacuum(ctx); err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.Vacuumed = true
		}
		// VACUUM 会重写整个库并产生大量 WAL 帧，结束后再截断一次。
		if err := store.CheckpointWAL(ctx); err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
	}
	if over, limit := store.dataOverLimit(cfg.MaxDBSizeMB); over {
		result.Errors = append(result.Errors, fmt.Sprintf("database remains above maintenance threshold: data=%d limit=%d; live records are retained", store.DataSize(), limit))
	}
	return result
}

// dataOverLimit 判断主库 + WAL 是否超过配置的大小上限（MB）。上限为 0 表示不限制。
func (s *Store) dataOverLimit(maxSizeMB int) (bool, int64) {
	if maxSizeMB <= 0 {
		return false, 0
	}
	limitBytes := int64(maxSizeMB) * 1024 * 1024
	return s.DataSize() > limitBytes, limitBytes
}

func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "VACUUM")
	return err
}

// CheckpointWAL 回写并实际截断 WAL。忙碌的外部读事务可能阻止截断，
// 必须检查 SQLite 返回的 busy 状态，不能把执行 PRAGMA 成功当作截断成功。
func (s *Store) CheckpointWAL(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var busy, frames, checkpointed int
	if err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed); err != nil {
		return err
	}
	if busy != 0 || frames > checkpointed {
		return fmt.Errorf("WAL checkpoint busy: frames=%d checkpointed=%d", frames, checkpointed)
	}
	return nil
}

// WALSize 返回 -wal 文件大小（字节），文件不存在时返回 0。
func (s *Store) WALSize() int64 {
	return fileSize(s.path + "-wal")
}

// DataSize 返回主库与 WAL 文件大小之和（字节）。
func (s *Store) DataSize() int64 {
	return s.DBSize() + s.WALSize()
}

func (s *Store) Backup(ctx context.Context, dir string) (string, error) {
	if dir == "" {
		dir = defaultSettings().BackupDir
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "proxypool-"+time.Now().UTC().Format("20060102-150405.000000000")+".db")
	// VACUUM INTO creates a consistent, standalone SQLite snapshot, including committed
	// transactions that may still reside in the source database's WAL file.
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
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

// DeleteOldDeadProxies 删除 updated_at 早于保留期的失效代理归档。
// disabled=1 的记录（用户手动禁用保留的）不受保留期影响。
func (s *Store) DeleteOldDeadProxies(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	res, err := s.db.ExecContext(ctx, `DELETE FROM dead_proxies WHERE disabled = 0 AND updated_at < ?`, cutoff)
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

func nullTimePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}
