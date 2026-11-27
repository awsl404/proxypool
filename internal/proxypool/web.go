package proxypool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type WebServer struct {
	store      *Store
	pool       *ProxyPool
	configPath string
	sessions   map[string]time.Time
	jobs       map[string]*CollectorJob
	checking   bool
	mu         sync.Mutex
}

type CollectorJob struct {
	ID         string           `json:"id"`
	Status     string           `json:"status"`
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
	Result     *CollectorResult `json:"result,omitempty"`
	Error      string           `json:"error,omitempty"`
	Logs       []string         `json:"logs,omitempty"`
	cancel     context.CancelFunc
}

func NewWebServer(store *Store, pool *ProxyPool, configPath string) *WebServer {
	return &WebServer{
		store:      store,
		pool:       pool,
		configPath: configPath,
		sessions:   map[string]time.Time{},
		jobs:       map[string]*CollectorJob{},
	}
}

func (s *WebServer) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.requireSession(s.handleLogout))
	mux.HandleFunc("/api/me", s.requireSession(s.handleMe))
	mux.HandleFunc("/api/stats", s.requireSession(s.handleStats))
	mux.HandleFunc("/api/status", s.requireSession(s.handleStatus))
	mux.HandleFunc("/api/tasks", s.requireSession(s.handleTasks))
	mux.HandleFunc("/api/maintenance/run", s.requireSession(s.handleMaintenanceRun))
	mux.HandleFunc("/api/settings", s.requireSession(s.handleSettings))
	mux.HandleFunc("/api/logs", s.requireSession(s.handleLogs))
	mux.HandleFunc("/api/proxies/export", s.requireSession(s.handleExportProxies))
	mux.HandleFunc("/api/proxies/check-all", s.requireSession(s.handleCheckAll))
	mux.HandleFunc("/api/proxies/cleanup", s.requireSession(s.handleCleanupProxies))
	mux.HandleFunc("/api/proxies", s.requireSession(s.handleProxies))
	mux.HandleFunc("/api/proxies/import", s.requireSession(s.handleImport))
	mux.HandleFunc("/api/proxies/next", s.handleNextProxy)
	mux.HandleFunc("/api/collector/run", s.requireSession(s.handleCollectorRun))
	mux.HandleFunc("/api/collector/jobs/", s.requireSession(s.handleCollectorJob))
	mux.HandleFunc("/api/collector/sources", s.requireSession(s.handleSourceStats))
	mux.HandleFunc("/api/dead-proxies", s.requireSession(s.handleDeadProxies))
	mux.HandleFunc("/api/dead-proxies/", s.requireSession(s.handleDeadProxyItem))
	mux.HandleFunc("/api/custom-scrapers", s.requireSession(s.handleCustomScrapers))
	mux.HandleFunc("/api/custom-scrapers/", s.requireSession(s.handleCustomScraperItem))
	mux.HandleFunc("/api/proxies/", s.requireSession(s.handleProxyItem))
	mux.Handle("/", webUIHandler())
	return s.requireWebAccess(mux)
}

func (s *WebServer) requireWebAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg, err := s.store.Settings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if !webAccessAllowed(r.RemoteAddr, cfg) {
			appWarn("web access denied remote=%s path=%s", r.RemoteAddr, r.URL.Path)
			writeError(w, http.StatusForbidden, errors.New("access denied"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *WebServer) handleExportProxies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	items, err := s.store.ListProxiesFilteredByTargetCapability(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("scheme"), r.URL.Query().Get("status"), r.URL.Query().Get("anonymity"), r.URL.Query().Get("reachability"), r.URL.Query().Get("target_capability"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, p := range items {
		_, _ = io.WriteString(w, proxyURI(p)+"\n")
	}
}

func (s *WebServer) handleCheckAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	scope := normalizeCheckScope(r.URL.Query().Get("scope"))
	schemes := normalizeCheckSchemes(r.URL.Query().Get("scheme"))
	started, err := StartManualHealthRun(context.Background(), s.store, scope, schemes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !started {
		writeJSON(w, map[string]any{"started": false, "message": "检测任务正在运行"})
		return
	}
	appInfo("check-all started scope=%s schemes=%s", scope, schemes)
	writeJSON(w, map[string]any{"started": true, "scope": scope, "scheme": schemes})
}

func (s *WebServer) handleCleanupProxies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	var (
		deleted int64
		err     error
	)
	switch scope {
	case "multi_tags":
		deleted, err = s.store.DeleteMultiTagProxies(r.Context())
	default:
		scope = "unavailable"
		deleted, err = s.store.DeleteUnavailable(r.Context())
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	appInfo("cleanup proxies scope=%s deleted=%d", scope, deleted)
	writeJSON(w, map[string]any{"deleted": deleted, "scope": scope})
}

func (s *WebServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if body.Username != cfg.AdminUser || !VerifyPassword(body.Password, cfg) {
		appWarn("login failed user=%s remote=%s", body.Username, r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, errors.New("invalid credentials"))
		return
	}
	token := randomToken()
	s.mu.Lock()
	s.sessions[token] = time.Now().Add(24 * time.Hour)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "pp_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(24 * time.Hour)})
	appInfo("login success user=%s remote=%s", body.Username, r.RemoteAddr)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *WebServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("pp_session"); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "pp_session", Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *WebServer) handleMe(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"username": cfg.AdminUser})
}

func (s *WebServer) handleStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, st)
}

func (s *WebServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	cfg, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	st, err := s.store.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sourceTotal, sourceFailing, _ := s.store.SourceStatsSummary(r.Context())
	customTotal, customEnabled, _ := s.store.CustomScraperSummary(r.Context())
	healthScheduler := HealthSchedulerStatus(cfg)
	s.mu.Lock()
	runningJobs := 0
	for _, job := range s.jobs {
		if job.Status == "running" || job.Status == "stopping" {
			runningJobs++
		}
	}
	storedJobs := len(s.jobs)
	checkBusy := s.checking || healthScheduler.Running
	s.mu.Unlock()
	writeJSON(w, RuntimeStatus{
		Proxy:           st,
		Outbound:        s.pool.Metrics(),
		Scheduler:       SchedulerStatus(cfg.ScheduleEnabled),
		HealthScheduler: healthScheduler,
		Maintenance:     MaintenanceStatus(cfg.MaintenanceEnabled),
		RunningJobs:     runningJobs,
		StoredJobs:      storedJobs,
		HealthCheckBusy: checkBusy,
		DBSizeBytes:     s.store.DBSize(),
		WALSizeBytes:    s.store.WALSize(),
		LogSizeBytes:    fileSize(cfg.LogFile),
		SourceTotal:     sourceTotal,
		SourceFailing:   sourceFailing,
		CustomTotal:     customTotal,
		CustomEnabled:   customEnabled,
		GeneratedAt:     time.Now().UTC(),
	})
}

func (s *WebServer) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	cfg, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.mu.Lock()
	jobs := make([]CollectorJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		snapshot := *job
		snapshot.Logs = append([]string(nil), job.Logs...)
		snapshot.cancel = nil
		jobs = append(jobs, snapshot)
	}
	s.mu.Unlock()
	sort.SliceStable(jobs, func(i, j int) bool {
		return jobs[i].StartedAt.After(jobs[j].StartedAt)
	})
	writeJSON(w, RuntimeTasks{
		HealthScheduler:    HealthSchedulerStatus(cfg),
		CollectorScheduler: SchedulerStatus(cfg.ScheduleEnabled),
		Maintenance:        MaintenanceStatus(cfg.MaintenanceEnabled),
		CollectorJobs:      jobs,
		GeneratedAt:        time.Now().UTC(),
	})
}

func (s *WebServer) handleMaintenanceRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	cfg, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	result, started := RunMaintenanceNow(r.Context(), s.store, cfg)
	writeJSON(w, map[string]any{"started": started, "result": result})
}

func (s *WebServer) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := s.store.Settings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		cfg.AdminPassword = ""
		writeJSON(w, cfg)
	case http.MethodPut:
		var cfg Settings
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.store.UpdateSettings(r.Context(), cfg); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		saved, err := s.store.Settings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if err := SaveSettingsFile(s.configPath, saved); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if err := InitLogger(saved); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		appInfo("settings saved user=%s log_level=%s log_file=%s", saved.AdminUser, saved.LogLevel, saved.LogFile)
		writeJSON(w, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *WebServer) handleProxies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodDelete:
		deleted, err := s.store.DeleteProxiesFilteredByTargetCapability(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("scheme"), r.URL.Query().Get("status"), r.URL.Query().Get("anonymity"), r.URL.Query().Get("reachability"), r.URL.Query().Get("target_capability"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		appInfo("proxies deleted by filter deleted=%d", deleted)
		writeJSON(w, map[string]any{"deleted": deleted})
		return
	default:
		methodNotAllowed(w)
		return
	}
	if page, pageSize, paged := requestPagination(r, 20); paged {
		items, err := s.store.ListProxiesFilteredPageByTargetCapability(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("scheme"), r.URL.Query().Get("status"), r.URL.Query().Get("anonymity"), r.URL.Query().Get("reachability"), r.URL.Query().Get("target_capability"), page, pageSize)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, items)
		return
	}
	items, err := s.store.ListProxiesFilteredByTargetCapability(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("scheme"), r.URL.Query().Get("status"), r.URL.Query().Get("anonymity"), r.URL.Query().Get("reachability"), r.URL.Query().Get("target_capability"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, items)
}

func (s *WebServer) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	text, err := readImportText(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result := ImportProxies(r.Context(), s.store, text)
	appInfo("proxies imported imported=%d duplicates=%d failed=%d", result.Imported, result.Duplicates, result.Failed)
	writeJSON(w, result)
}

func (s *WebServer) handleNextProxy(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !validAPIToken(r, cfg.APIToken) {
		writeError(w, http.StatusUnauthorized, errors.New("invalid token"))
		return
	}
	anonymity := r.URL.Query().Get("anonymity")
	if anonymity == "" {
		anonymity = cfg.ServeAnonymity
	}
	reachability := firstNonEmpty(r.URL.Query().Get("reachability"), r.URL.Query().Get("region"))
	if reachability == "" {
		reachability = cfg.ServeReachability
	}
	cfg.ServeAnonymity = anonymity
	cfg.ServeReachability = reachability
	if strategy := r.URL.Query().Get("strategy"); strategy != "" {
		cfg.ServeStrategy = normalizeServeStrategy(strategy)
	}
	items, err := s.pool.Candidates(r.Context(), r.URL.Query().Get("target"), cfg, 1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(items) == 0 {
		writeError(w, http.StatusNotFound, errors.New("no proxy available"))
		return
	}
	s.store.MarkUsed(r.Context(), items[0].ID)
	writeJSON(w, items[0])
}

func (s *WebServer) handleProxyItem(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/proxies/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid proxy id"))
		return
	}
	if len(parts) == 2 && parts[1] == "check" {
		s.handleCheckProxy(w, r, id)
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, err := s.store.GetProxy(r.Context(), id)
		if err != nil {
			if notFound(err) {
				writeError(w, http.StatusNotFound, errors.New("proxy not found"))
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, p)
	case http.MethodDelete:
		if err := s.store.DeleteProxy(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	case http.MethodPatch:
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.store.SetProxyEnabled(r.Context(), id, body.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *WebServer) handleCheckProxy(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	p, err := s.store.GetProxy(r.Context(), id)
	if err != nil {
		if notFound(err) {
			writeError(w, http.StatusNotFound, errors.New("proxy not found"))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cfg, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	checkCtx, cancel := context.WithTimeout(r.Context(), proxyCheckTimeout(cfg))
	checkResult := CheckProxyReachability(checkCtx, p, cfg)
	cancel()
	_ = s.store.MarkCheckResult(r.Context(), id, checkResult, cfg)
	if !checkResult.OK() {
		errText := "检测失败"
		if checkResult.Error != nil {
			errText = checkResult.Error.Error()
		}
		writeJSON(w, map[string]any{"ok": false, "error": errText, "reachability": checkResult.Reachability, "target_capability": checkResult.TargetCapability})
		return
	}
	anonymity := DetectAnonymity(r.Context(), p, cfg)
	_ = s.store.MarkAnonymity(r.Context(), id, anonymity)
	writeJSON(w, map[string]any{"ok": true, "latency_ms": checkResult.Latency.Milliseconds(), "anonymity": anonymity, "reachability": checkResult.Reachability, "target_capability": checkResult.TargetCapability})
}

func (s *WebServer) handleCollectorRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	cfg, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var req CollectorRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	id := randomToken()
	jobCtx, cancel := context.WithCancel(context.Background())
	job := &CollectorJob{ID: id, Status: "running", StartedAt: time.Now().UTC(), cancel: cancel}
	s.mu.Lock()
	s.jobs[id] = job
	s.appendCollectorJobLogLocked(job, "任务已创建")
	s.pruneCollectorJobsLocked()
	s.mu.Unlock()
	go func() {
		appInfo("collector job started id=%s", id)
		result := CollectFreeProxies(jobCtx, s.store, s.pool, cfg, req, s.collectorJobLogger(id))
		now := time.Now().UTC()
		s.mu.Lock()
		if job.Status == "stopping" || jobCtx.Err() != nil {
			job.Status = "stopped"
			s.appendCollectorJobLogLocked(job, "任务已停止")
		} else {
			job.Status = "done"
			s.appendCollectorJobLogLocked(job, "任务已完成")
		}
		job.FinishedAt = &now
		job.Result = &result
		job.cancel = nil
		s.mu.Unlock()
		appInfo("collector job finished id=%s imported=%d duplicates=%d failed=%d", id, result.Imported, result.Duplicates, result.Failed)
	}()
	writeJSON(w, map[string]any{"job_id": id, "status": "running"})
}

func (s *WebServer) handleCollectorJob(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/collector/jobs/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "stop" {
		s.handleCollectorJobStop(w, r, id)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	s.mu.Lock()
	job, ok := s.jobs[id]
	var snapshot CollectorJob
	if ok {
		snapshot = *job
		snapshot.Logs = append([]string(nil), job.Logs...)
		snapshot.cancel = nil
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("collector job not found"))
		return
	}
	writeJSON(w, snapshot)
}

func (s *WebServer) handleCollectorJobStop(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, errors.New("collector job not found"))
		return
	}
	if job.Status != "running" && job.Status != "stopping" {
		status := job.Status
		s.mu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "status": status})
		return
	}
	job.Status = "stopping"
	s.appendCollectorJobLogLocked(job, "收到停止请求，正在取消任务")
	cancel := job.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	writeJSON(w, map[string]any{"ok": true, "status": "stopping"})
}

func (s *WebServer) handleSourceStats(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query().Get("q")
		failedOnly := parseBoolString(r.URL.Query().Get("failed"))
		if page, pageSize, paged := requestPagination(r, 20); paged {
			stats, err := s.store.ListSourceStatsPage(r.Context(), query, failedOnly, page, pageSize)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, stats)
			return
		}
		limit := parsePositive(r.URL.Query().Get("limit"), 200)
		stats, err := s.store.ListSourceStats(r.Context(), query, failedOnly, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, stats)
	case http.MethodDelete:
		sourceURL := strings.TrimSpace(r.URL.Query().Get("url"))
		if sourceURL != "" {
			deleted, err := s.store.DeleteSourceStat(r.Context(), sourceURL)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			appInfo("source stat deleted url=%s deleted=%d", sourceHost(sourceURL), deleted)
			writeJSON(w, map[string]any{"deleted": deleted})
			return
		}
		deleted, err := s.store.ClearSourceStats(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		appInfo("source stats cleared deleted=%d", deleted)
		writeJSON(w, map[string]any{"deleted": deleted})
	default:
		methodNotAllowed(w)
	}
}

func (s *WebServer) handleDeadProxies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		page, pageSize, _ := requestPagination(r, 20)
		items, err := s.store.ListDeadProxiesPage(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("disabled"), page, pageSize)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, items)
	case http.MethodDelete:
		deleted, err := s.store.ClearDeadProxies(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		appInfo("dead proxy archive cleared deleted=%d", deleted)
		writeJSON(w, map[string]any{"deleted": deleted})
	default:
		methodNotAllowed(w)
	}
}

func (s *WebServer) handleDeadProxyItem(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/dead-proxies/")
	id, err := strconv.ParseInt(strings.Trim(trimmed, "/"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid dead proxy id"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Disabled bool `json:"disabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.store.SetDeadProxyDisabled(r.Context(), id, body.Disabled); err != nil {
			if notFound(err) {
				writeError(w, http.StatusNotFound, errors.New("dead proxy not found"))
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	case http.MethodDelete:
		if err := s.store.DeleteDeadProxy(r.Context(), id); err != nil {
			if notFound(err) {
				writeError(w, http.StatusNotFound, errors.New("dead proxy not found"))
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *WebServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := s.store.Settings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		lines := parsePositive(r.URL.Query().Get("lines"), 200)
		query := r.URL.Query().Get("q")
		level := r.URL.Query().Get("level")
		content, err := tailFile(cfg.LogFile, lines, query, level)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeJSON(w, map[string]any{"lines": []string{}})
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"lines": content})
	case http.MethodDelete:
		cfg, err := s.store.Settings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if err := appLogger.Truncate(cfg.LogFile); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		appInfo("logs cleared")
		writeJSON(w, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *WebServer) handleCustomScrapers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if page, pageSize, paged := requestPagination(r, 20); paged {
			items, err := s.store.ListCustomScrapersPage(r.Context(), page, pageSize)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, items)
			return
		}
		items, err := s.store.ListCustomScrapers(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, items)
	case http.MethodPost:
		var tpl CustomScraperTemplate
		if err := json.NewDecoder(r.Body).Decode(&tpl); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		saved, err := s.store.SaveCustomScraper(r.Context(), tpl)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		appInfo("custom scraper saved id=%d name=%s", saved.ID, saved.Name)
		writeJSON(w, saved)
	default:
		methodNotAllowed(w)
	}
}

func (s *WebServer) handleCustomScraperItem(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/custom-scrapers/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if parts[0] == "sample" {
		writeJSON(w, CustomScraperSample())
		return
	}
	if parts[0] == "preview" {
		s.handleCustomScraperDraftPreview(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid custom scraper id"))
		return
	}
	if len(parts) == 2 && parts[1] == "preview" {
		s.handleCustomScraperPreview(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "run" {
		s.handleCustomScraperRun(w, r, id)
		return
	}
	switch r.Method {
	case http.MethodGet:
		tpl, err := s.store.GetCustomScraper(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, tpl)
	case http.MethodPut:
		var tpl CustomScraperTemplate
		if err := json.NewDecoder(r.Body).Decode(&tpl); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		tpl.ID = id
		saved, err := s.store.SaveCustomScraper(r.Context(), tpl)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.store.DeleteCustomScraper(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		appInfo("custom scraper deleted id=%d", id)
		writeJSON(w, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *WebServer) handleCustomScraperPreview(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	cfg, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	tpl, err := s.store.GetCustomScraper(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Mode          string `json:"mode"`
		SpecificProxy string `json:"specific_proxy"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	preview := PreviewCustomScraper(r.Context(), s.pool, cfg, tpl, req.Mode, req.SpecificProxy)
	writeJSON(w, preview)
}

func (s *WebServer) handleCustomScraperDraftPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	cfg, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var req struct {
		Template      CustomScraperTemplate `json:"template"`
		Mode          string                `json:"mode"`
		SpecificProxy string                `json:"specific_proxy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	preview := PreviewCustomScraper(r.Context(), s.pool, cfg, req.Template, req.Mode, req.SpecificProxy)
	writeJSON(w, preview)
}

func (s *WebServer) handleCustomScraperRun(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	cfg, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var req CustomScrapeRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	req.TemplateID = id
	jobID := randomToken()
	jobCtx, cancel := context.WithCancel(context.Background())
	job := &CollectorJob{ID: jobID, Status: "running", StartedAt: time.Now().UTC(), cancel: cancel}
	s.mu.Lock()
	s.jobs[jobID] = job
	s.appendCollectorJobLogLocked(job, "任务已创建")
	s.pruneCollectorJobsLocked()
	s.mu.Unlock()
	go func() {
		appInfo("custom scraper job started id=%s template=%d", jobID, id)
		result := RunCustomScraper(jobCtx, s.store, s.pool, cfg, req, s.collectorJobLogger(jobID))
		now := time.Now().UTC()
		s.mu.Lock()
		if job.Status == "stopping" || jobCtx.Err() != nil {
			job.Status = "stopped"
			s.appendCollectorJobLogLocked(job, "任务已停止")
		} else {
			job.Status = "done"
			s.appendCollectorJobLogLocked(job, "任务已完成")
		}
		job.FinishedAt = &now
		job.Result = &result
		job.cancel = nil
		s.mu.Unlock()
		appInfo("custom scraper job finished id=%s template=%d imported=%d duplicates=%d failed=%d", jobID, id, result.Imported, result.Duplicates, result.Failed)
	}()
	writeJSON(w, map[string]any{"job_id": jobID, "status": "running"})
}

func (s *WebServer) collectorJobLogger(id string) CollectorLogger {
	return func(format string, args ...any) {
		message := fmtLog(format, args...)
		s.mu.Lock()
		if job, ok := s.jobs[id]; ok {
			s.appendCollectorJobLogLocked(job, message)
		}
		s.mu.Unlock()
	}
}

func (s *WebServer) appendCollectorJobLogLocked(job *CollectorJob, message string) {
	line := time.Now().Format("15:04:05") + " " + message
	job.Logs = append(job.Logs, line)
	if len(job.Logs) > 300 {
		job.Logs = append([]string(nil), job.Logs[len(job.Logs)-300:]...)
	}
}

func fmtLog(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

func (s *WebServer) pruneCollectorJobsLocked() {
	if len(s.jobs) <= 20 {
		return
	}
	for id, job := range s.jobs {
		if job.Status == "done" && job.FinishedAt != nil && time.Since(*job.FinishedAt) > time.Hour {
			delete(s.jobs, id)
		}
	}
}

func readImportText(r *http.Request) (string, error) {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return "", err
		}
		if text := r.FormValue("text"); text != "" {
			return text, nil
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			return "", err
		}
		defer file.Close()
		return readLimited(file)
	}
	var body struct {
		Text string `json:"text"`
	}
	if strings.Contains(ct, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", err
		}
		return body.Text, nil
	}
	return readLimited(r.Body)
}

func readLimited(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, 32<<20))
	return string(b), err
}

func (s *WebServer) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.hasSession(r) {
			writeError(w, http.StatusUnauthorized, errors.New("login required"))
			return
		}
		next(w, r)
	}
}

func (s *WebServer) hasSession(r *http.Request) bool {
	c, err := r.Cookie("pp_session")
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(s.sessions, c.Value)
		return false
	}
	return true
}

func validAPIToken(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	if r.URL.Query().Get("token") == token {
		return true
	}
	auth := r.Header.Get("Authorization")
	return auth == "Bearer "+token
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
}

func requestPagination(r *http.Request, defaultPageSize int) (int, int, bool) {
	q := r.URL.Query()
	paged := q.Has("page") || q.Has("page_size")
	page := parsePositive(q.Get("page"), 1)
	pageSize := parsePositive(q.Get("page_size"), defaultPageSize)
	page, pageSize = normalizePage(page, pageSize)
	return page, pageSize, paged
}

func tailFile(path string, maxLines int, query, level string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	query = strings.ToLower(strings.TrimSpace(query))
	level = strings.ToUpper(strings.TrimSpace(level))
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if query != "" && !strings.Contains(strings.ToLower(line), query) {
			continue
		}
		if level != "" && !strings.Contains(line, "["+level+"]") {
			continue
		}
		lines = append(lines, line)
		if len(lines) > maxLines {
			lines = lines[len(lines)-maxLines:]
		}
	}
	return lines, scanner.Err()
}

func StartWeb(ctx context.Context, addr string, store *Store, pool *ProxyPool, configPath string) *http.Server {
	web := NewWebServer(store, pool, configPath)
	srv := &http.Server{Addr: addr, Handler: web.Routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		appInfo("Web UI listening on http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			appError("web server stopped: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	return srv
}
