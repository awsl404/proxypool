package proxypool

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type SchedulerSnapshot struct {
	Enabled     bool       `json:"enabled"`
	Running     bool       `json:"running"`
	LastRun     *time.Time `json:"last_run,omitempty"`
	NextRun     *time.Time `json:"next_run,omitempty"`
	PauseUntil  *time.Time `json:"pause_until,omitempty"`
	RunsToday   int        `json:"runs_today"`
	LastResult  string     `json:"last_result,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	CurrentTask string     `json:"current_task,omitempty"`
}

var schedulerState = struct {
	mu          sync.Mutex
	running     bool
	lastRun     *time.Time
	nextRun     *time.Time
	pauseUntil  *time.Time
	runsToday   int
	day         string
	lastResult  string
	lastError   string
	currentTask string
}{}

func StartCollectorScheduler(ctx context.Context, store *Store, pool *ProxyPool) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cfg, err := store.Settings(ctx)
				if err != nil {
					setSchedulerError(err.Error())
					continue
				}
				refreshScheduleDay()
				if !cfg.ScheduleEnabled {
					setSchedulerNext(time.Now().Add(time.Duration(maxInt(1, cfg.ScheduleInterval)) * time.Minute))
					continue
				}
				if !scheduleDue(cfg) {
					continue
				}
				go runScheduledCollectors(ctx, store, pool, cfg)
			}
		}
	}()
}

func SchedulerStatus(enabled bool) SchedulerSnapshot {
	schedulerState.mu.Lock()
	defer schedulerState.mu.Unlock()
	return SchedulerSnapshot{
		Enabled:     enabled,
		Running:     schedulerState.running,
		LastRun:     schedulerState.lastRun,
		NextRun:     schedulerState.nextRun,
		PauseUntil:  schedulerState.pauseUntil,
		RunsToday:   schedulerState.runsToday,
		LastResult:  schedulerState.lastResult,
		LastError:   schedulerState.lastError,
		CurrentTask: schedulerState.currentTask,
	}
}

func scheduleDue(cfg Settings) bool {
	now := time.Now()
	schedulerState.mu.Lock()
	defer schedulerState.mu.Unlock()
	if schedulerState.running {
		return false
	}
	if schedulerState.pauseUntil != nil && now.Before(*schedulerState.pauseUntil) {
		return false
	}
	if schedulerState.runsToday >= maxInt(1, cfg.ScheduleMaxRunsDay) {
		next := startOfNextDay(now)
		schedulerState.nextRun = &next
		return false
	}
	if schedulerState.nextRun == nil {
		next := now.Add(time.Duration(maxInt(1, cfg.ScheduleInterval)) * time.Minute)
		schedulerState.nextRun = &next
		return false
	}
	return !now.Before(*schedulerState.nextRun)
}

func runScheduledCollectors(ctx context.Context, store *Store, pool *ProxyPool, cfg Settings) {
	schedulerState.mu.Lock()
	if schedulerState.running {
		schedulerState.mu.Unlock()
		return
	}
	schedulerState.running = true
	schedulerState.currentTask = "定时采集中"
	schedulerState.lastError = ""
	schedulerState.mu.Unlock()

	appInfo("scheduled collector started run_collector=%t run_custom=%t", cfg.ScheduleRunCollector, cfg.ScheduleRunCustom)
	check := cfg.ScheduleCheck
	imported, duplicates, checked, failures := 0, 0, 0, 0
	if cfg.ScheduleRunCollector {
		result := CollectFreeProxies(ctx, store, pool, cfg, CollectorRequest{Check: &check}, appInfoCollectorLogger("scheduled collector"))
		imported += result.Imported
		duplicates += result.Duplicates
		checked += result.Checked
		failures += result.Failed + result.SourcesFailed
	}
	if cfg.ScheduleRunCustom {
		templates, err := store.ListCustomScrapers(ctx)
		if err != nil {
			failures++
			setSchedulerError(err.Error())
		} else {
			for _, tpl := range templates {
				if !tpl.Enabled {
					continue
				}
				setSchedulerTask("运行自定义模板：" + tpl.Name)
				result := RunCustomScraper(ctx, store, pool, cfg, CustomScrapeRequest{TemplateID: tpl.ID, Check: &check}, appInfoCollectorLogger("scheduled custom"))
				imported += result.Imported
				duplicates += result.Duplicates
				checked += result.Checked
				failures += result.Failed + result.SourcesFailed
			}
		}
	}
	now := time.Now().UTC()
	next := time.Now().Add(time.Duration(maxInt(1, cfg.ScheduleInterval)) * time.Minute)
	summary := fmt.Sprintf("导入=%d，重复=%d，验证=%d，失败=%d", imported, duplicates, checked, failures)
	schedulerState.mu.Lock()
	schedulerState.running = false
	schedulerState.currentTask = ""
	schedulerState.lastRun = &now
	schedulerState.nextRun = &next
	schedulerState.runsToday++
	schedulerState.lastResult = summary
	if failures > 0 && cfg.SchedulePauseMinutes > 0 {
		pause := time.Now().Add(time.Duration(cfg.SchedulePauseMinutes) * time.Minute)
		schedulerState.pauseUntil = &pause
		schedulerState.lastError = fmt.Sprintf("失败 %d 项，暂停到 %s", failures, pause.Format("2006-01-02 15:04:05"))
	} else {
		schedulerState.pauseUntil = nil
		schedulerState.lastError = ""
	}
	schedulerState.mu.Unlock()
	appInfo("scheduled collector finished %s", summary)
}

func appInfoCollectorLogger(prefix string) CollectorLogger {
	return func(format string, args ...any) {
		appInfo("%s: %s", prefix, fmt.Sprintf(format, args...))
	}
}

func refreshScheduleDay() {
	today := time.Now().Format("2006-01-02")
	schedulerState.mu.Lock()
	defer schedulerState.mu.Unlock()
	if schedulerState.day == "" {
		schedulerState.day = today
		return
	}
	if schedulerState.day != today {
		schedulerState.day = today
		schedulerState.runsToday = 0
		schedulerState.pauseUntil = nil
	}
}

func setSchedulerNext(next time.Time) {
	schedulerState.mu.Lock()
	defer schedulerState.mu.Unlock()
	schedulerState.nextRun = &next
}

func setSchedulerError(message string) {
	schedulerState.mu.Lock()
	defer schedulerState.mu.Unlock()
	schedulerState.lastError = message
}

func setSchedulerTask(task string) {
	schedulerState.mu.Lock()
	defer schedulerState.mu.Unlock()
	schedulerState.currentTask = task
}

func startOfNextDay(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
}
