package proxypool

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type HealthPassResult struct {
	Scope      string
	Schemes    string
	Candidates int
	Checked    int
	Error      string
}

type HealthSchedulerSnapshot struct {
	Enabled         bool       `json:"enabled"`
	Running         bool       `json:"running"`
	LastRun         *time.Time `json:"last_run,omitempty"`
	NextRun         *time.Time `json:"next_run,omitempty"`
	RunsToday       int        `json:"runs_today"`
	LastResult      string     `json:"last_result,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	CurrentTask     string     `json:"current_task,omitempty"`
	Scope           string     `json:"scope"`
	Schemes         string     `json:"schemes"`
	IntervalSeconds int        `json:"interval_seconds"`
}

var healthSchedulerState = struct {
	mu          sync.Mutex
	running     bool
	lastRun     *time.Time
	nextRun     *time.Time
	runsToday   int
	day         string
	lastResult  string
	lastError   string
	currentTask string
}{}

func StartHealthLoop(ctx context.Context, store *Store) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cfg, err := store.Settings(ctx)
				if err != nil {
					setHealthSchedulerError(err.Error())
					continue
				}
				if !healthScheduleDue(cfg) {
					continue
				}
				startHealthRun(ctx, store, cfg, cfg.CheckScheduleScope, cfg.CheckScheduleSchemes, true)
			}
		}
	}()
}

func StartManualHealthRun(ctx context.Context, store *Store, scope, schemes string) (bool, error) {
	cfg, err := store.Settings(ctx)
	if err != nil {
		return false, err
	}
	return startHealthRun(ctx, store, cfg, scope, schemes, false), nil
}

func HealthSchedulerStatus(cfg Settings) HealthSchedulerSnapshot {
	healthSchedulerState.mu.Lock()
	defer healthSchedulerState.mu.Unlock()
	return HealthSchedulerSnapshot{
		Enabled:         cfg.CheckScheduleEnabled,
		Running:         healthSchedulerState.running,
		LastRun:         healthSchedulerState.lastRun,
		NextRun:         healthSchedulerState.nextRun,
		RunsToday:       healthSchedulerState.runsToday,
		LastResult:      healthSchedulerState.lastResult,
		LastError:       healthSchedulerState.lastError,
		CurrentTask:     healthSchedulerState.currentTask,
		Scope:           normalizeCheckScope(cfg.CheckScheduleScope),
		Schemes:         normalizeCheckSchemes(cfg.CheckScheduleSchemes),
		IntervalSeconds: healthCheckInterval(cfg),
	}
}

func healthScheduleDue(cfg Settings) bool {
	now := time.Now()
	healthSchedulerState.mu.Lock()
	defer healthSchedulerState.mu.Unlock()
	refreshHealthScheduleDayLocked(now)
	if !cfg.CheckScheduleEnabled {
		if !healthSchedulerState.running {
			healthSchedulerState.nextRun = nil
		}
		return false
	}
	if healthSchedulerState.running {
		return false
	}
	if healthSchedulerState.nextRun == nil {
		next := now.Add(2 * time.Second)
		healthSchedulerState.nextRun = &next
		return false
	}
	return !now.Before(*healthSchedulerState.nextRun)
}

func startHealthRun(ctx context.Context, store *Store, cfg Settings, scope, schemes string, scheduled bool) bool {
	scope = normalizeCheckScope(scope)
	schemes = normalizeCheckSchemes(schemes)
	taskPrefix := "手动检测"
	if scheduled {
		taskPrefix = "自动检测"
	}
	healthSchedulerState.mu.Lock()
	if healthSchedulerState.running {
		healthSchedulerState.mu.Unlock()
		return false
	}
	healthSchedulerState.running = true
	healthSchedulerState.currentTask = fmt.Sprintf("%s：%s / %s", taskPrefix, healthScopeLabel(scope), healthSchemesLabel(schemes))
	healthSchedulerState.lastError = ""
	healthSchedulerState.mu.Unlock()

	go func() {
		result := runHealthPassFiltered(ctx, store, scope, schemes)
		now := time.Now().UTC()
		summary := fmt.Sprintf("%s，%s，候选=%d，检测=%d", healthScopeLabel(result.Scope), healthSchemesLabel(result.Schemes), result.Candidates, result.Checked)
		next := time.Now().Add(time.Duration(healthCheckInterval(cfg)) * time.Second)
		healthSchedulerState.mu.Lock()
		healthSchedulerState.running = false
		healthSchedulerState.currentTask = ""
		healthSchedulerState.lastRun = &now
		healthSchedulerState.lastResult = summary
		healthSchedulerState.lastError = result.Error
		if scheduled {
			healthSchedulerState.runsToday++
			healthSchedulerState.nextRun = &next
		} else if cfg.CheckScheduleEnabled {
			healthSchedulerState.nextRun = &next
		}
		healthSchedulerState.mu.Unlock()
		appInfo("health check finished scheduled=%t scope=%s schemes=%s candidates=%d checked=%d error=%s", scheduled, result.Scope, result.Schemes, result.Candidates, result.Checked, result.Error)
	}()
	return true
}

func refreshHealthScheduleDayLocked(now time.Time) {
	today := now.Format("2006-01-02")
	if healthSchedulerState.day == "" {
		healthSchedulerState.day = today
		return
	}
	if healthSchedulerState.day != today {
		healthSchedulerState.day = today
		healthSchedulerState.runsToday = 0
	}
}

func setHealthSchedulerError(message string) {
	healthSchedulerState.mu.Lock()
	defer healthSchedulerState.mu.Unlock()
	healthSchedulerState.lastError = message
}

func healthCheckInterval(cfg Settings) int {
	return maxInt(10, cfg.CheckIntervalSeconds)
}

func healthScopeLabel(scope string) string {
	switch normalizeCheckScope(scope) {
	case "unavailable":
		return "仅不可用"
	case "unchecked":
		return "仅未检测"
	case "available":
		return "仅可用"
	default:
		return "全部启用"
	}
}

func healthSchemesLabel(schemes string) string {
	normalized := normalizeCheckSchemes(schemes)
	if normalized == "" {
		return "全部协议"
	}
	return strings.ToUpper(strings.ReplaceAll(normalized, ",", "/"))
}
