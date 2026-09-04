package proxypool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"proxypool/internal/proxypool/storage"

	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	queries *storage.Queries
	path    string
}

const maxUnpagedRows = 100000

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, queries: storage.New(db), path: path}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	for _, stmt := range migrationStatements() {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, "proxies", "source", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "proxies", "tags", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "proxies", "anonymity", "TEXT NOT NULL DEFAULT 'unknown'"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "proxies", "reachability", "TEXT NOT NULL DEFAULT 'unknown'"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "proxies", "target_capability", "TEXT NOT NULL DEFAULT 'unknown'"); err != nil {
		return err
	}
	if err := s.normalizeStoredProxyTags(ctx); err != nil {
		return err
	}
	for _, col := range deadProxyColumnMigrations {
		if err := s.ensureColumn(ctx, "dead_proxies", col.name, col.decl); err != nil {
			return err
		}
	}
	for _, col := range customScraperColumnMigrations {
		if err := s.ensureColumn(ctx, "custom_scrapers", col.name, col.decl); err != nil {
			return err
		}
	}
	return s.ensureDefaultSettings(ctx)
}

var deadProxyColumnMigrations = []struct {
	name string
	decl string
}{
	{"tags", "TEXT NOT NULL DEFAULT ''"},
	{"source", "TEXT NOT NULL DEFAULT ''"},
	{"fail_count", "INTEGER NOT NULL DEFAULT 0"},
	{"last_error", "TEXT NOT NULL DEFAULT ''"},
	{"disabled", "INTEGER NOT NULL DEFAULT 0"},
	{"last_seen", "DATETIME"},
	{"created_at", "DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP"},
	{"updated_at", "DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP"},
}

func (s *Store) ListCustomScrapers(ctx context.Context) ([]CustomScraperTemplate, error) {
	rows, err := s.queries.ListCustomScrapers(ctx, storage.ListCustomScrapersParams{
		RowOffset: 0,
		RowLimit:  maxUnpagedRows,
	})
	if err != nil {
		return nil, err
	}
	out := []CustomScraperTemplate{}
	for _, row := range rows {
		out = append(out, customScraperFromStorage(row))
	}
	return out, nil
}

func (s *Store) ListCustomScrapersPage(ctx context.Context, page, pageSize int) (Page[CustomScraperTemplate], error) {
	page, pageSize = normalizePage(page, pageSize)
	total, err := s.queries.CountCustomScrapers(ctx)
	if err != nil {
		return Page[CustomScraperTemplate]{}, err
	}
	rows, err := s.queries.ListCustomScrapers(ctx, storage.ListCustomScrapersParams{
		RowOffset: int64(pageOffset(page, pageSize)),
		RowLimit:  int64(pageSize),
	})
	if err != nil {
		return Page[CustomScraperTemplate]{}, err
	}
	out := []CustomScraperTemplate{}
	for _, row := range rows {
		out = append(out, customScraperFromStorage(row))
	}
	return newPage(out, page, pageSize, int(total)), nil
}

func (s *Store) GetCustomScraper(ctx context.Context, id int64) (CustomScraperTemplate, error) {
	row, err := s.queries.GetCustomScraper(ctx, id)
	if err != nil {
		return CustomScraperTemplate{}, err
	}
	return customScraperFromStorage(row), nil
}

func (s *Store) SaveCustomScraper(ctx context.Context, tpl CustomScraperTemplate) (CustomScraperTemplate, error) {
	now := time.Now().UTC()
	tpl = normalizeCustomScraper(tpl)
	if tpl.ID == 0 {
		id, err := s.queries.InsertCustomScraper(ctx, insertCustomScraperParams(tpl, now))
		if err != nil {
			return tpl, err
		}
		tpl.ID = id
		tpl.CreatedAt = now
		tpl.UpdatedAt = now
		return tpl, nil
	}
	err := s.queries.UpdateCustomScraper(ctx, updateCustomScraperParams(tpl, now))
	tpl.UpdatedAt = now
	return tpl, err
}

func (s *Store) DeleteCustomScraper(ctx context.Context, id int64) error {
	return s.queries.DeleteCustomScraper(ctx, id)
}

func (s *Store) MarkCustomScraperRun(ctx context.Context, id int64, result string) error {
	now := time.Now().UTC()
	return s.queries.MarkCustomScraperRun(ctx, storage.MarkCustomScraperRunParams{
		LastRun:    sql.NullTime{Time: now, Valid: true},
		LastResult: result,
		UpdatedAt:  now,
		ID:         id,
	})
}

func (s *Store) RecordSourceStat(ctx context.Context, url string, fetched bool, imported, duplicates, checked int, lastErr string) error {
	fetchOK := 0
	fetchFail := 0
	if fetched {
		fetchOK = 1
	} else {
		fetchFail = 1
	}
	return s.queries.UpsertSourceStat(ctx, storage.UpsertSourceStatParams{
		Url:          url,
		FetchOk:      int64(fetchOK),
		FetchFail:    int64(fetchFail),
		Imported:     int64(imported),
		Duplicates:   int64(duplicates),
		Checked:      int64(checked),
		LastError:    lastErr,
		LastRun:      sql.NullTime{Time: time.Now().UTC(), Valid: true},
		LastImported: int64(imported),
	})
}

func (s *Store) ListSourceStats(ctx context.Context, query string, failedOnly bool, limit int) ([]SourceStat, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.queries.ListSourceStats(ctx, storage.ListSourceStatsParams{
		Query:      strings.TrimSpace(query),
		FailedOnly: boolToInt(failedOnly),
		RowOffset:  0,
		RowLimit:   int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := []SourceStat{}
	for _, row := range rows {
		out = append(out, sourceStatFromStorage(row))
	}
	return out, nil
}

func (s *Store) ListSourceStatsPage(ctx context.Context, query string, failedOnly bool, page, pageSize int) (Page[SourceStat], error) {
	page, pageSize = normalizePage(page, pageSize)
	params := storage.CountSourceStatsParams{
		Query:      strings.TrimSpace(query),
		FailedOnly: boolToInt(failedOnly),
	}
	total, err := s.queries.CountSourceStats(ctx, params)
	if err != nil {
		return Page[SourceStat]{}, err
	}
	rows, err := s.queries.ListSourceStats(ctx, storage.ListSourceStatsParams{
		Query:      params.Query,
		FailedOnly: params.FailedOnly,
		RowOffset:  int64(pageOffset(page, pageSize)),
		RowLimit:   int64(pageSize),
	})
	if err != nil {
		return Page[SourceStat]{}, err
	}
	out := []SourceStat{}
	for _, row := range rows {
		out = append(out, sourceStatFromStorage(row))
	}
	return newPage(out, page, pageSize, int(total)), nil
}

func (s *Store) DeleteSourceStat(ctx context.Context, sourceURL string) (int64, error) {
	return s.queries.DeleteSourceStat(ctx, sourceURL)
}

func (s *Store) ClearSourceStats(ctx context.Context) (int64, error) {
	return s.queries.ClearSourceStats(ctx)
}

func sourceStatFromStorage(row storage.SourceStat) SourceStat {
	st := SourceStat{
		URL:          row.Url,
		FetchOK:      int(row.FetchOk),
		FetchFail:    int(row.FetchFail),
		Imported:     int(row.Imported),
		Duplicates:   int(row.Duplicates),
		Checked:      int(row.Checked),
		LastError:    row.LastError,
		LastImported: int(row.LastImported),
	}
	if row.LastRun.Valid {
		st.LastRun = &row.LastRun.Time
	}
	return st
}

func scanDeadProxy(row proxyScanner) (DeadProxy, error) {
	var p DeadProxy
	var disabled int
	var lastSeen sql.NullTime
	err := row.Scan(&p.ID, &p.Scheme, &p.Host, &p.Port, &p.Username, &p.Password, &p.Tags, &p.Source, &p.FailCount, &p.LastError, &disabled, &lastSeen, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	p.Tags = normalizeTagList(firstNonEmpty(p.Tags, p.Scheme))
	p.Disabled = disabled == 1
	if lastSeen.Valid {
		p.LastSeen = &lastSeen.Time
	}
	return p, nil
}

func (s *Store) ArchiveDeadProxy(ctx context.Context, p Proxy, failCount int, lastErr string) error {
	now := time.Now().UTC()
	p.Tags = normalizeTagList(firstNonEmpty(p.Tags, p.Scheme))
	if failCount <= 0 {
		failCount = 1
	}
	if strings.TrimSpace(lastErr) == "" {
		lastErr = "proxy check failed"
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO dead_proxies(
  scheme, host, port, username, password, tags, source, fail_count, last_error, disabled, last_seen, created_at, updated_at
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)
ON CONFLICT(scheme, host, port, username, password) DO UPDATE SET
  tags = excluded.tags,
  source = CASE WHEN excluded.source != '' THEN excluded.source ELSE dead_proxies.source END,
  fail_count = MAX(dead_proxies.fail_count, excluded.fail_count),
  last_error = excluded.last_error,
  last_seen = excluded.last_seen,
  updated_at = excluded.updated_at
`, p.Scheme, p.Host, p.Port, p.Username, p.Password, p.Tags, p.Source, failCount, lastErr, now, now, now)
	return err
}

func (s *Store) ShouldSkipDeadProxy(ctx context.Context, p Proxy, threshold int) (bool, DeadProxy, error) {
	threshold = maxInt(1, threshold)
	row := s.db.QueryRowContext(ctx, `
SELECT id, scheme, host, port, username, password, tags, source, fail_count, last_error, disabled, last_seen, created_at, updated_at
FROM dead_proxies
WHERE scheme = ?
  AND host = ?
  AND port = ?
  AND username = ?
  AND password = ?
  AND disabled = 0
  AND fail_count >= ?
`, p.Scheme, p.Host, p.Port, p.Username, p.Password, threshold)
	dead, err := scanDeadProxy(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, DeadProxy{}, nil
		}
		return false, DeadProxy{}, err
	}
	return true, dead, nil
}

func (s *Store) ListDeadProxiesPage(ctx context.Context, query, disabledFilter string, page, pageSize int) (Page[DeadProxy], error) {
	page, pageSize = normalizePage(page, pageSize)
	query = strings.TrimSpace(query)
	disabledSQL := ""
	var disabledArg int
	switch strings.ToLower(strings.TrimSpace(disabledFilter)) {
	case "true", "1", "disabled":
		disabledSQL = " AND disabled = ?"
		disabledArg = 1
	case "false", "0", "active":
		disabledSQL = " AND disabled = ?"
		disabledArg = 0
	}
	countSQL := `
SELECT COUNT(*)
FROM dead_proxies
WHERE (? = '' OR host LIKE '%' || ? || '%' OR username LIKE '%' || ? || '%' OR source LIKE '%' || ? || '%' OR last_error LIKE '%' || ? || '%')` + disabledSQL
	args := []any{query, query, query, query, query}
	if disabledSQL != "" {
		args = append(args, disabledArg)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return Page[DeadProxy]{}, err
	}
	listSQL := `
SELECT id, scheme, host, port, username, password, tags, source, fail_count, last_error, disabled, last_seen, created_at, updated_at
FROM dead_proxies
WHERE (? = '' OR host LIKE '%' || ? || '%' OR username LIKE '%' || ? || '%' OR source LIKE '%' || ? || '%' OR last_error LIKE '%' || ? || '%')` + disabledSQL + `
ORDER BY disabled ASC, fail_count DESC, updated_at DESC, id DESC
LIMIT ? OFFSET ?`
	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, pageSize, pageOffset(page, pageSize))
	rows, err := s.db.QueryContext(ctx, listSQL, listArgs...)
	if err != nil {
		return Page[DeadProxy]{}, err
	}
	defer rows.Close()
	items := []DeadProxy{}
	for rows.Next() {
		item, err := scanDeadProxy(rows)
		if err != nil {
			return Page[DeadProxy]{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[DeadProxy]{}, err
	}
	return newPage(items, page, pageSize, total), nil
}

func (s *Store) SetDeadProxyDisabled(ctx context.Context, id int64, disabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE dead_proxies SET disabled = ?, updated_at = ? WHERE id = ?`, boolToInt(disabled), time.Now().UTC(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteDeadProxy(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM dead_proxies WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ClearDeadProxies(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM dead_proxies`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func proxyFromStorage(row storage.Proxy) Proxy {
	p := Proxy{
		ID:               row.ID,
		Scheme:           row.Scheme,
		Host:             row.Host,
		Port:             int(row.Port),
		Username:         row.Username,
		Password:         row.Password,
		Tags:             normalizeTagList(firstNonEmpty(row.Tags, row.Scheme)),
		Source:           row.Source,
		Anonymity:        normalizeAnonymity(row.Anonymity),
		Reachability:     normalizeReachability(row.Reachability),
		TargetCapability: normalizeTargetCapability(row.TargetCapability),
		Enabled:          row.Enabled == 1,
		Status:           row.Status,
		FailCount:        int(row.FailCount),
		SuccessCount:     int(row.SuccessCount),
		LatencyMS:        row.LatencyMs,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
	if row.LastChecked.Valid {
		p.LastChecked = &row.LastChecked.Time
	}
	if row.LastUsed.Valid {
		p.LastUsed = &row.LastUsed.Time
	}

	return p
}

func sqlValueInt(v any) int {
	switch value := v.(type) {
	case nil:
		return 0
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case []byte:
		n, _ := strconv.ParseFloat(string(value), 64)
		return int(n)
	case string:
		n, _ := strconv.ParseFloat(value, 64)
		return int(n)
	default:
		return 0
	}
}

func customScraperFromStorage(row storage.CustomScraper) CustomScraperTemplate {
	tpl := CustomScraperTemplate{
		ID:             row.ID,
		Name:           row.Name,
		Enabled:        row.Enabled == 1,
		URLTemplate:    row.UrlTemplate,
		RequestMethod:  row.RequestMethod,
		RequestHeaders: row.RequestHeaders,
		RequestBody:    row.RequestBody,
		PageMode:       row.PageMode,
		PageStart:      int(row.PageStart),
		PageEnd:        int(row.PageEnd),
		PageSize:       int(row.PageSize),
		MaxPages:       int(row.MaxPages),
		RequestTimeout: int(row.RequestTimeoutSeconds),
		Concurrency:    int(row.Concurrency),
		RetryCount:     int(row.RetryCount),
		StopFailures:   int(row.StopAfterFailures),
		PagePattern:    row.PagePattern,
		PageGroup:      row.PageGroup,
		DefaultScheme:  row.DefaultScheme,
		ExtractPattern: row.ExtractPattern,
		ResponseType:   row.ResponseType,
		JSONListPath:   row.JsonListPath,
		JSONProxyPath:  row.JsonProxyPath,
		JSONSchemePath: row.JsonSchemePath,
		JSONHostPath:   row.JsonHostPath,
		JSONPortPath:   row.JsonPortPath,
		JSONUserPath:   row.JsonUserPath,
		JSONPassPath:   row.JsonPasswordPath,
		SchemeGroup:    row.SchemeGroup,
		HostGroup:      row.HostGroup,
		PortGroup:      row.PortGroup,
		UserGroup:      row.UserGroup,
		PasswordGroup:  row.PasswordGroup,
		Notes:          row.Notes,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		LastResult:     row.LastResult,
	}
	if row.LastRun.Valid {
		tpl.LastRun = &row.LastRun.Time
	}
	return tpl
}

func insertCustomScraperParams(tpl CustomScraperTemplate, now time.Time) storage.InsertCustomScraperParams {
	return storage.InsertCustomScraperParams{
		Name:                  tpl.Name,
		Enabled:               int64(boolToInt(tpl.Enabled)),
		UrlTemplate:           tpl.URLTemplate,
		RequestMethod:         tpl.RequestMethod,
		RequestHeaders:        tpl.RequestHeaders,
		RequestBody:           tpl.RequestBody,
		PageMode:              tpl.PageMode,
		PageStart:             int64(tpl.PageStart),
		PageEnd:               int64(tpl.PageEnd),
		PageSize:              int64(tpl.PageSize),
		MaxPages:              int64(tpl.MaxPages),
		RequestTimeoutSeconds: int64(tpl.RequestTimeout),
		Concurrency:           int64(tpl.Concurrency),
		RetryCount:            int64(tpl.RetryCount),
		StopAfterFailures:     int64(tpl.StopFailures),
		PagePattern:           tpl.PagePattern,
		PageGroup:             tpl.PageGroup,
		DefaultScheme:         tpl.DefaultScheme,
		ExtractPattern:        tpl.ExtractPattern,
		ResponseType:          tpl.ResponseType,
		JsonListPath:          tpl.JSONListPath,
		JsonProxyPath:         tpl.JSONProxyPath,
		JsonSchemePath:        tpl.JSONSchemePath,
		JsonHostPath:          tpl.JSONHostPath,
		JsonPortPath:          tpl.JSONPortPath,
		JsonUserPath:          tpl.JSONUserPath,
		JsonPasswordPath:      tpl.JSONPassPath,
		SchemeGroup:           tpl.SchemeGroup,
		HostGroup:             tpl.HostGroup,
		PortGroup:             tpl.PortGroup,
		UserGroup:             tpl.UserGroup,
		PasswordGroup:         tpl.PasswordGroup,
		Notes:                 tpl.Notes,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

func updateCustomScraperParams(tpl CustomScraperTemplate, now time.Time) storage.UpdateCustomScraperParams {
	params := storage.UpdateCustomScraperParams{
		Name:                  tpl.Name,
		Enabled:               int64(boolToInt(tpl.Enabled)),
		UrlTemplate:           tpl.URLTemplate,
		RequestMethod:         tpl.RequestMethod,
		RequestHeaders:        tpl.RequestHeaders,
		RequestBody:           tpl.RequestBody,
		PageMode:              tpl.PageMode,
		PageStart:             int64(tpl.PageStart),
		PageEnd:               int64(tpl.PageEnd),
		PageSize:              int64(tpl.PageSize),
		MaxPages:              int64(tpl.MaxPages),
		RequestTimeoutSeconds: int64(tpl.RequestTimeout),
		Concurrency:           int64(tpl.Concurrency),
		RetryCount:            int64(tpl.RetryCount),
		StopAfterFailures:     int64(tpl.StopFailures),
		PagePattern:           tpl.PagePattern,
		PageGroup:             tpl.PageGroup,
		DefaultScheme:         tpl.DefaultScheme,
		ExtractPattern:        tpl.ExtractPattern,
		ResponseType:          tpl.ResponseType,
		JsonListPath:          tpl.JSONListPath,
		JsonProxyPath:         tpl.JSONProxyPath,
		JsonSchemePath:        tpl.JSONSchemePath,
		JsonHostPath:          tpl.JSONHostPath,
		JsonPortPath:          tpl.JSONPortPath,
		JsonUserPath:          tpl.JSONUserPath,
		JsonPasswordPath:      tpl.JSONPassPath,
		SchemeGroup:           tpl.SchemeGroup,
		HostGroup:             tpl.HostGroup,
		PortGroup:             tpl.PortGroup,
		UserGroup:             tpl.UserGroup,
		PasswordGroup:         tpl.PasswordGroup,
		Notes:                 tpl.Notes,
		UpdatedAt:             now,
		ID:                    tpl.ID,
	}
	return params
}

func (s *Store) ensureColumn(ctx context.Context, table, column, decl string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+decl)
	return err
}

func (s *Store) ensureDefaultSettings(ctx context.Context) error {
	def := defaultSettings()
	values := map[string]string{
		"web_addr":                       def.WebAddr,
		"http_proxy_addr":                def.HTTPProxyAddr,
		"socks_proxy_addr":               def.SOCKSProxyAddr,
		"admin_user":                     def.AdminUser,
		"admin_password":                 def.AdminPassword,
		"admin_password_hash":            def.AdminPasswordHash,
		"api_token":                      def.APIToken,
		"proxy_username":                 def.ProxyUsername,
		"proxy_password":                 def.ProxyPassword,
		"health_url":                     def.HealthURL,
		"domestic_health_url":            def.DomesticHealthURL,
		"foreign_health_url":             def.ForeignHealthURL,
		"anonymity_url":                  def.AnonymityURL,
		"serve_anonymity":                def.ServeAnonymity,
		"serve_reachability":             def.ServeReachability,
		"serve_strategy":                 def.ServeStrategy,
		"collector_sources":              def.CollectorSources,
		"collector_mode":                 def.CollectorMode,
		"collector_proxy":                def.CollectorProxy,
		"check_concurrency":              strconv.Itoa(def.CheckConcurrency),
		"check_interval_seconds":         strconv.Itoa(def.CheckIntervalSeconds),
		"check_schedule_enabled":         boolString(def.CheckScheduleEnabled),
		"check_schedule_scope":           def.CheckScheduleScope,
		"check_schedule_schemes":         def.CheckScheduleSchemes,
		"fail_threshold":                 strconv.Itoa(def.FailThreshold),
		"timeout_seconds":                strconv.Itoa(def.TimeoutSeconds),
		"tls_insecure_skip_verify":       boolString(def.TLSInsecureSkipVerify),
		"check_success_keywords":         def.CheckSuccessKeywords,
		"check_block_keywords":           def.CheckBlockKeywords,
		"maintenance_enabled":            boolString(def.MaintenanceEnabled),
		"maintenance_interval_seconds":   strconv.Itoa(def.MaintenanceInterval),
		"log_retention_days":             strconv.Itoa(def.LogRetentionDays),
		"source_retention_days":          strconv.Itoa(def.SourceRetentionDays),
		"backup_dir":                     def.BackupDir,
		"auto_backup":                    boolString(def.AutoBackup),
		"auto_vacuum":                    boolString(def.AutoVacuum),
		"auto_cleanup_unavailable":       boolString(def.AutoCleanup),
		"schedule_enabled":               boolString(def.ScheduleEnabled),
		"schedule_interval_minutes":      strconv.Itoa(def.ScheduleInterval),
		"schedule_run_collector":         boolString(def.ScheduleRunCollector),
		"schedule_run_custom":            boolString(def.ScheduleRunCustom),
		"schedule_check_after_import":    boolString(def.ScheduleCheck),
		"schedule_max_runs_per_day":      strconv.Itoa(def.ScheduleMaxRunsDay),
		"schedule_failure_pause_minutes": strconv.Itoa(def.SchedulePauseMinutes),
		"log_level":                      def.LogLevel,
		"log_file":                       def.LogFile,
		"bind_local_only":                boolString(def.BindLocalOnly),
		"web_allowlist":                  def.WebAllowlist,
		"web_blocklist":                  def.WebBlocklist,
		"proxy_allowlist":                def.ProxyAllowlist,
		"proxy_blocklist":                def.ProxyBlocklist,
	}
	for k, v := range values {
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings(key, value) VALUES(?, ?)`, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	cfg := defaultSettings()
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return cfg, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return cfg, err
		}
		switch k {
		case "web_addr":
			cfg.WebAddr = v
		case "http_proxy_addr":
			cfg.HTTPProxyAddr = v
		case "socks_proxy_addr":
			cfg.SOCKSProxyAddr = v
		case "admin_user":
			cfg.AdminUser = v
		case "admin_password":
			cfg.AdminPassword = v
		case "admin_password_hash":
			cfg.AdminPasswordHash = v
		case "api_token":
			cfg.APIToken = v
		case "proxy_username":
			cfg.ProxyUsername = v
		case "proxy_password":
			cfg.ProxyPassword = v
		case "health_url":
			cfg.HealthURL = v
		case "domestic_health_url":
			cfg.DomesticHealthURL = v
		case "foreign_health_url":
			cfg.ForeignHealthURL = v
		case "anonymity_url":
			cfg.AnonymityURL = v
		case "serve_anonymity":
			cfg.ServeAnonymity = normalizeAnonymityFilter(v)
		case "serve_reachability":
			cfg.ServeReachability = normalizeReachabilityFilter(v)
		case "serve_strategy":
			cfg.ServeStrategy = normalizeServeStrategy(v)
		case "collector_sources":
			cfg.CollectorSources = v
		case "collector_mode":
			cfg.CollectorMode = normalizeCollectorMode(v)
		case "collector_proxy":
			cfg.CollectorProxy = v
		case "check_concurrency":
			cfg.CheckConcurrency = parsePositive(v, cfg.CheckConcurrency)
		case "check_interval_seconds":
			cfg.CheckIntervalSeconds = parsePositive(v, cfg.CheckIntervalSeconds)
		case "check_schedule_enabled":
			cfg.CheckScheduleEnabled = parseBoolString(v)
		case "check_schedule_scope":
			cfg.CheckScheduleScope = normalizeCheckScope(v)
		case "check_schedule_schemes":
			cfg.CheckScheduleSchemes = normalizeCheckSchemes(v)
		case "fail_threshold":
			cfg.FailThreshold = parsePositive(v, cfg.FailThreshold)
		case "timeout_seconds":
			cfg.TimeoutSeconds = parsePositive(v, cfg.TimeoutSeconds)
		case "tls_insecure_skip_verify":
			cfg.TLSInsecureSkipVerify = parseBoolString(v)
		case "check_success_keywords":
			cfg.CheckSuccessKeywords = v
		case "check_block_keywords":
			cfg.CheckBlockKeywords = v
		case "maintenance_enabled":
			cfg.MaintenanceEnabled = parseBoolString(v)
		case "maintenance_interval_seconds":
			cfg.MaintenanceInterval = parsePositive(v, cfg.MaintenanceInterval)
		case "log_retention_days":
			cfg.LogRetentionDays = parseNonNegative(v, cfg.LogRetentionDays)
		case "source_retention_days":
			cfg.SourceRetentionDays = parseNonNegative(v, cfg.SourceRetentionDays)
		case "backup_dir":
			cfg.BackupDir = v
		case "auto_backup":
			cfg.AutoBackup = parseBoolString(v)
		case "auto_vacuum":
			cfg.AutoVacuum = parseBoolString(v)
		case "auto_cleanup_unavailable":
			cfg.AutoCleanup = parseBoolString(v)
		case "schedule_enabled":
			cfg.ScheduleEnabled = parseBoolString(v)
		case "schedule_interval_minutes":
			cfg.ScheduleInterval = parsePositive(v, cfg.ScheduleInterval)
		case "schedule_run_collector":
			cfg.ScheduleRunCollector = parseBoolString(v)
		case "schedule_run_custom":
			cfg.ScheduleRunCustom = parseBoolString(v)
		case "schedule_check_after_import":
			cfg.ScheduleCheck = parseBoolString(v)
		case "schedule_max_runs_per_day":
			cfg.ScheduleMaxRunsDay = parsePositive(v, cfg.ScheduleMaxRunsDay)
		case "schedule_failure_pause_minutes":
			cfg.SchedulePauseMinutes = parseNonNegative(v, cfg.SchedulePauseMinutes)
		case "log_level":
			cfg.LogLevel = normalizeLogLevel(v)
		case "log_file":
			cfg.LogFile = v
		case "bind_local_only":
			cfg.BindLocalOnly = parseBoolString(v)
		case "web_allowlist":
			cfg.WebAllowlist = v
		case "web_blocklist":
			cfg.WebBlocklist = v
		case "proxy_allowlist":
			cfg.ProxyAllowlist = v
		case "proxy_blocklist":
			cfg.ProxyBlocklist = v
		}
	}
	return cfg, rows.Err()
}

func (s *Store) UpdateSettings(ctx context.Context, cfg Settings) error {
	current, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if cfg.AdminPassword == "" && cfg.AdminPasswordHash == "" {
		cfg.AdminPassword = current.AdminPassword
		cfg.AdminPasswordHash = current.AdminPasswordHash
	}
	if cfg.AdminPassword != "" && cfg.AdminPasswordHash == "" {
		hash, err := HashPassword(cfg.AdminPassword)
		if err != nil {
			return err
		}
		cfg.AdminPasswordHash = hash
		cfg.AdminPassword = ""
	}
	def := defaultSettings()
	if cfg.DomesticHealthURL == "" {
		cfg.DomesticHealthURL = firstNonEmpty(current.DomesticHealthURL, def.DomesticHealthURL)
	}
	if cfg.ForeignHealthURL == "" {
		cfg.ForeignHealthURL = firstNonEmpty(cfg.HealthURL, current.ForeignHealthURL, def.ForeignHealthURL)
	}
	if cfg.ServeReachability == "" {
		cfg.ServeReachability = current.ServeReachability
	}
	if cfg.ServeStrategy == "" {
		cfg.ServeStrategy = current.ServeStrategy
	}
	values := map[string]string{
		"web_addr":                       cfg.WebAddr,
		"http_proxy_addr":                cfg.HTTPProxyAddr,
		"socks_proxy_addr":               cfg.SOCKSProxyAddr,
		"admin_user":                     cfg.AdminUser,
		"admin_password":                 cfg.AdminPassword,
		"admin_password_hash":            cfg.AdminPasswordHash,
		"api_token":                      cfg.APIToken,
		"proxy_username":                 cfg.ProxyUsername,
		"proxy_password":                 cfg.ProxyPassword,
		"health_url":                     cfg.HealthURL,
		"domestic_health_url":            cfg.DomesticHealthURL,
		"foreign_health_url":             cfg.ForeignHealthURL,
		"anonymity_url":                  cfg.AnonymityURL,
		"serve_anonymity":                normalizeAnonymityFilter(cfg.ServeAnonymity),
		"serve_reachability":             normalizeReachabilityFilter(cfg.ServeReachability),
		"serve_strategy":                 normalizeServeStrategy(cfg.ServeStrategy),
		"collector_sources":              cfg.CollectorSources,
		"collector_mode":                 normalizeCollectorMode(cfg.CollectorMode),
		"collector_proxy":                cfg.CollectorProxy,
		"check_concurrency":              strconv.Itoa(maxInt(1, cfg.CheckConcurrency)),
		"check_interval_seconds":         strconv.Itoa(maxInt(10, cfg.CheckIntervalSeconds)),
		"check_schedule_enabled":         boolString(cfg.CheckScheduleEnabled),
		"check_schedule_scope":           normalizeCheckScope(cfg.CheckScheduleScope),
		"check_schedule_schemes":         normalizeCheckSchemes(cfg.CheckScheduleSchemes),
		"fail_threshold":                 strconv.Itoa(maxInt(1, cfg.FailThreshold)),
		"timeout_seconds":                strconv.Itoa(maxInt(1, cfg.TimeoutSeconds)),
		"tls_insecure_skip_verify":       boolString(cfg.TLSInsecureSkipVerify),
		"check_success_keywords":         cfg.CheckSuccessKeywords,
		"check_block_keywords":           cfg.CheckBlockKeywords,
		"maintenance_enabled":            boolString(cfg.MaintenanceEnabled),
		"maintenance_interval_seconds":   strconv.Itoa(maxInt(60, cfg.MaintenanceInterval)),
		"log_retention_days":             strconv.Itoa(maxInt(0, cfg.LogRetentionDays)),
		"source_retention_days":          strconv.Itoa(maxInt(0, cfg.SourceRetentionDays)),
		"backup_dir":                     firstNonEmpty(cfg.BackupDir, current.BackupDir, def.BackupDir),
		"auto_backup":                    boolString(cfg.AutoBackup),
		"auto_vacuum":                    boolString(cfg.AutoVacuum),
		"auto_cleanup_unavailable":       boolString(cfg.AutoCleanup),
		"schedule_enabled":               boolString(cfg.ScheduleEnabled),
		"schedule_interval_minutes":      strconv.Itoa(maxInt(1, cfg.ScheduleInterval)),
		"schedule_run_collector":         boolString(cfg.ScheduleRunCollector),
		"schedule_run_custom":            boolString(cfg.ScheduleRunCustom),
		"schedule_check_after_import":    boolString(cfg.ScheduleCheck),
		"schedule_max_runs_per_day":      strconv.Itoa(maxInt(1, cfg.ScheduleMaxRunsDay)),
		"schedule_failure_pause_minutes": strconv.Itoa(maxInt(0, cfg.SchedulePauseMinutes)),
		"log_level":                      normalizeLogLevel(cfg.LogLevel),
		"log_file":                       cfg.LogFile,
		"bind_local_only":                boolString(cfg.BindLocalOnly),
		"web_allowlist":                  cfg.WebAllowlist,
		"web_blocklist":                  cfg.WebBlocklist,
		"proxy_allowlist":                cfg.ProxyAllowlist,
		"proxy_blocklist":                cfg.ProxyBlocklist,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AddProxy(ctx context.Context, p Proxy) (bool, error) {
	now := time.Now().UTC()
	p.Tags = normalizeTagList(firstNonEmpty(p.Tags, p.Scheme))
	n, err := s.queries.InsertProxy(ctx, storage.InsertProxyParams{
		Scheme:           p.Scheme,
		Host:             p.Host,
		Port:             int64(p.Port),
		Username:         p.Username,
		Password:         p.Password,
		Tags:             p.Tags,
		Source:           p.Source,
		Anonymity:        normalizeAnonymity(p.Anonymity),
		TargetCapability: normalizeTargetCapability(p.TargetCapability),
		CreatedAt:        now,
		UpdatedAt:        now,
	})
	if err != nil {
		return false, err
	}
	if n == 0 {
		if err := s.mergeProxyTags(ctx, p); err != nil {
			return false, err
		}
		return false, nil
	}
	return n > 0, nil
}

func (s *Store) ProxyID(ctx context.Context, p Proxy) (int64, error) {
	return s.queries.GetProxyID(ctx, storage.GetProxyIDParams{
		Scheme:   p.Scheme,
		Host:     p.Host,
		Port:     int64(p.Port),
		Username: p.Username,
		Password: p.Password,
	})
}

func (s *Store) mergeProxyTags(ctx context.Context, p Proxy) error {
	existing, err := s.queries.GetProxyTagsByIdentity(ctx, storage.GetProxyTagsByIdentityParams{
		Scheme:   p.Scheme,
		Host:     p.Host,
		Port:     int64(p.Port),
		Username: p.Username,
		Password: p.Password,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	merged := normalizeTagList(existing + "," + p.Tags + "," + p.Scheme)
	err = s.queries.UpdateProxyTagsByIdentity(ctx, storage.UpdateProxyTagsByIdentityParams{
		Tags:      merged,
		UpdatedAt: time.Now().UTC(),
		Scheme:    p.Scheme,
		Host:      p.Host,
		Port:      int64(p.Port),
		Username:  p.Username,
		Password:  p.Password,
	})
	return err
}

func (s *Store) normalizeStoredProxyTags(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE proxies SET tags = scheme WHERE tags != scheme`)
	return err
}

func (s *Store) ListProxies(ctx context.Context, query, scheme, status string) ([]Proxy, error) {
	return s.ListProxiesFiltered(ctx, query, scheme, status, "", "")
}

func (s *Store) ListProxiesFiltered(ctx context.Context, query, scheme, status, anonymity, reachability string) ([]Proxy, error) {
	return s.ListProxiesFilteredByTargetCapability(ctx, query, scheme, status, anonymity, reachability, "")
}

func (s *Store) ListProxiesFilteredByTargetCapability(ctx context.Context, query, scheme, status, anonymity, reachability, targetCapability string) ([]Proxy, error) {
	anonymityFilter := ""
	if normalizeAnonymityFilter(anonymity) != "any" {
		anonymityFilter = normalizeAnonymityFilter(anonymity)
	}
	reachabilityFilter := ""
	if normalizeReachabilityFilter(reachability) != "any" {
		reachabilityFilter = normalizeReachabilityFilter(reachability)
	}
	targetCapabilityFilter := ""
	if normalizeTargetCapabilityFilter(targetCapability) != "any" {
		targetCapabilityFilter = normalizeTargetCapabilityFilter(targetCapability)
	}
	rows, err := s.queries.ListProxiesFiltered(ctx, storage.ListProxiesFilteredParams{
		Query:                  strings.TrimSpace(query),
		SchemeFilter:           strings.TrimSpace(scheme),
		StatusFilter:           strings.TrimSpace(status),
		AnonymityFilter:        anonymityFilter,
		ReachabilityFilter:     reachabilityFilter,
		TargetCapabilityFilter: targetCapabilityFilter,
		RowOffset:              0,
		RowLimit:               maxUnpagedRows,
	})
	if err != nil {
		return nil, err
	}
	out := []Proxy{}
	for _, row := range rows {
		out = append(out, proxyFromStorage(row))
	}
	return out, nil
}

func (s *Store) ListProxiesFilteredPage(ctx context.Context, query, scheme, status, anonymity, reachability string, page, pageSize int) (Page[Proxy], error) {
	return s.ListProxiesFilteredPageByTargetCapability(ctx, query, scheme, status, anonymity, reachability, "", page, pageSize)
}

func (s *Store) ListProxiesFilteredPageByTargetCapability(ctx context.Context, query, scheme, status, anonymity, reachability, targetCapability string, page, pageSize int) (Page[Proxy], error) {
	page, pageSize = normalizePage(page, pageSize)
	anonymityFilter := ""
	if normalizeAnonymityFilter(anonymity) != "any" {
		anonymityFilter = normalizeAnonymityFilter(anonymity)
	}
	reachabilityFilter := ""
	if normalizeReachabilityFilter(reachability) != "any" {
		reachabilityFilter = normalizeReachabilityFilter(reachability)
	}
	targetCapabilityFilter := ""
	if normalizeTargetCapabilityFilter(targetCapability) != "any" {
		targetCapabilityFilter = normalizeTargetCapabilityFilter(targetCapability)
	}
	countParams := storage.CountProxiesFilteredParams{
		Query:                  strings.TrimSpace(query),
		SchemeFilter:           strings.TrimSpace(scheme),
		StatusFilter:           strings.TrimSpace(status),
		AnonymityFilter:        anonymityFilter,
		ReachabilityFilter:     reachabilityFilter,
		TargetCapabilityFilter: targetCapabilityFilter,
	}
	total, err := s.queries.CountProxiesFiltered(ctx, countParams)
	if err != nil {
		return Page[Proxy]{}, err
	}
	rows, err := s.queries.ListProxiesFiltered(ctx, storage.ListProxiesFilteredParams{
		Query:                  countParams.Query,
		SchemeFilter:           countParams.SchemeFilter,
		StatusFilter:           countParams.StatusFilter,
		AnonymityFilter:        countParams.AnonymityFilter,
		ReachabilityFilter:     countParams.ReachabilityFilter,
		TargetCapabilityFilter: countParams.TargetCapabilityFilter,
		RowOffset:              int64(pageOffset(page, pageSize)),
		RowLimit:               int64(pageSize),
	})
	if err != nil {
		return Page[Proxy]{}, err
	}
	out := []Proxy{}
	for _, row := range rows {
		out = append(out, proxyFromStorage(row))
	}
	return newPage(out, page, pageSize, int(total)), nil
}

func (s *Store) GetProxy(ctx context.Context, id int64) (Proxy, error) {
	row, err := s.queries.GetProxy(ctx, id)
	if err != nil {
		return Proxy{}, err
	}
	return proxyFromStorage(row), nil
}

func (s *Store) DeleteProxy(ctx context.Context, id int64) error {
	return s.queries.DeleteProxy(ctx, id)
}

func (s *Store) DeleteUnavailable(ctx context.Context) (int64, error) {
	return s.queries.DeleteUnavailableProxies(ctx)
}

func (s *Store) DeleteMultiTagProxies(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM proxies WHERE tags LIKE '%,%'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) DeleteProxiesFiltered(ctx context.Context, query, scheme, status, anonymity, reachability string) (int64, error) {
	return s.DeleteProxiesFilteredByTargetCapability(ctx, query, scheme, status, anonymity, reachability, "")
}

func (s *Store) DeleteProxiesFilteredByTargetCapability(ctx context.Context, query, scheme, status, anonymity, reachability, targetCapability string) (int64, error) {
	anonymityFilter := ""
	if normalizeAnonymityFilter(anonymity) != "any" {
		anonymityFilter = normalizeAnonymityFilter(anonymity)
	}
	reachabilityFilter := ""
	if normalizeReachabilityFilter(reachability) != "any" {
		reachabilityFilter = normalizeReachabilityFilter(reachability)
	}
	targetCapabilityFilter := ""
	if normalizeTargetCapabilityFilter(targetCapability) != "any" {
		targetCapabilityFilter = normalizeTargetCapabilityFilter(targetCapability)
	}
	res, err := s.db.ExecContext(ctx, `
DELETE FROM proxies
WHERE
  (? = '' OR host LIKE '%' || ? || '%' OR username LIKE '%' || ? || '%' OR source LIKE '%' || ? || '%')
  AND (? = '' OR scheme = ?)
  AND (? = '' OR status = ?)
  AND (? = '' OR anonymity = ?)
  AND (
    ? = ''
    OR reachability = ?
    OR (? = 'domestic' AND reachability = 'both')
    OR (? = 'foreign' AND reachability = 'both')
  )
  AND (
    ? = ''
    OR target_capability = ?
    OR (? = 'http' AND target_capability = 'both')
    OR (? = 'https' AND target_capability = 'both')
  )
`, strings.TrimSpace(query), strings.TrimSpace(query), strings.TrimSpace(query), strings.TrimSpace(query),
		strings.TrimSpace(scheme), strings.TrimSpace(scheme),
		strings.TrimSpace(status), strings.TrimSpace(status),
		anonymityFilter, anonymityFilter,
		reachabilityFilter, reachabilityFilter, reachabilityFilter, reachabilityFilter,
		targetCapabilityFilter, targetCapabilityFilter, targetCapabilityFilter, targetCapabilityFilter)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) SetProxyEnabled(ctx context.Context, id int64, enabled bool) error {
	return s.queries.SetProxyEnabled(ctx, storage.SetProxyEnabledParams{
		Enabled:   int64(boolToInt(enabled)),
		UpdatedAt: time.Now().UTC(),
		ID:        id,
	})
}

func (s *Store) CandidateProxies(ctx context.Context, limit int, anonymity, reachability, schemes string) ([]Proxy, error) {
	return s.CandidateProxiesForTargetCapability(ctx, limit, anonymity, reachability, schemes, "")
}

// SequentialCandidateProxies returns candidates strictly ordered by last_used
// (oldest first), ignoring status/fail/latency bias so every proxy gets its
// turn in a true sequential rotation.
func (s *Store) SequentialCandidateProxies(ctx context.Context, limit int, anonymity, reachability, schemes, targetCapability string) ([]Proxy, error) {
	filter := normalizeAnonymityFilter(anonymity)
	if filter == "any" {
		filter = ""
	}
	reachabilityFilter := normalizeReachabilityFilter(reachability)
	if reachabilityFilter == "any" {
		reachabilityFilter = ""
	}
	targetCapabilityFilter := normalizeTargetCapabilityFilter(targetCapability)
	if targetCapabilityFilter == "any" {
		targetCapabilityFilter = ""
	}
	rows, err := s.queries.SequentialCandidateProxies(ctx, storage.SequentialCandidateProxiesParams{
		SchemeFilter:           normalizeCandidateSchemeFilter(schemes),
		AnonymityFilter:        filter,
		ReachabilityFilter:     reachabilityFilter,
		TargetCapabilityFilter: targetCapabilityFilter,
		RowLimit:               int64(limit),
	})
	if err != nil {
		return nil, err
	}
	var out []Proxy
	for _, row := range rows {
		out = append(out, proxyFromStorage(row))
	}
	return out, nil
}

func (s *Store) CandidateProxiesForTargetCapability(ctx context.Context, limit int, anonymity, reachability, schemes, targetCapability string) ([]Proxy, error) {
	filter := normalizeAnonymityFilter(anonymity)
	if filter == "any" {
		filter = ""
	}
	reachabilityFilter := normalizeReachabilityFilter(reachability)
	if reachabilityFilter == "any" {
		reachabilityFilter = ""
	}
	targetCapabilityFilter := normalizeTargetCapabilityFilter(targetCapability)
	if targetCapabilityFilter == "any" {
		targetCapabilityFilter = ""
	}
	rows, err := s.queries.CandidateProxies(ctx, storage.CandidateProxiesParams{
		SchemeFilter:           normalizeCandidateSchemeFilter(schemes),
		AnonymityFilter:        filter,
		ReachabilityFilter:     reachabilityFilter,
		TargetCapabilityFilter: targetCapabilityFilter,
		RowLimit:               int64(limit),
	})
	if err != nil {
		return nil, err
	}
	var out []Proxy
	for _, row := range rows {
		out = append(out, proxyFromStorage(row))
	}
	return out, nil
}

func normalizeCandidateSchemeFilter(value string) string {
	return strings.Join(protocolSchemes(value), ",")
}

func (s *Store) MarkUsed(ctx context.Context, id int64) {
	now := time.Now().UTC()
	_ = s.queries.MarkProxyUsed(ctx, storage.MarkProxyUsedParams{
		LastUsed:  sql.NullTime{Time: now, Valid: true},
		UpdatedAt: now,
		ID:        id,
	})
}

func (s *Store) MarkCheck(ctx context.Context, id int64, ok bool, latency time.Duration, cfg Settings) error {
	reachability := "none"
	targetCapability := "none"
	if ok {
		reachability = "unknown"
		targetCapability = "unknown"
	}
	return s.markCheck(ctx, id, ok, latency, reachability, targetCapability, "", cfg)
}

func (s *Store) MarkCheckResult(ctx context.Context, id int64, result ProxyCheckResult, cfg Settings) error {
	errText := ""
	if result.Error != nil {
		errText = result.Error.Error()
	}
	return s.markCheck(ctx, id, result.OK(), result.Latency, result.Reachability, result.TargetCapability, errText, cfg)
}

func (s *Store) markCheck(ctx context.Context, id int64, ok bool, latency time.Duration, reachability, targetCapability, lastErr string, cfg Settings) error {
	now := time.Now().UTC()
	if ok {
		return s.queries.MarkProxyCheckOK(ctx, storage.MarkProxyCheckOKParams{
			LatencyMs:        latency.Milliseconds(),
			Reachability:     normalizeReachability(reachability),
			TargetCapability: normalizeTargetCapability(targetCapability),
			LastChecked:      sql.NullTime{Time: now, Valid: true},
			UpdatedAt:        now,
			ID:               id,
		})
	}
	if err := s.queries.MarkProxyCheckFail(ctx, storage.MarkProxyCheckFailParams{
		Reachability:     normalizeReachability(reachability),
		TargetCapability: normalizeTargetCapability(targetCapability),
		LastChecked:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:        now,
		ID:               id,
	}); err != nil {
		return err
	}
	threshold := maxInt(1, cfg.FailThreshold)
	if p, err := s.GetProxy(ctx, id); err == nil && p.FailCount >= threshold {
		if err := s.ArchiveDeadProxy(ctx, p, p.FailCount, lastErr); err != nil {
			return err
		}
	}
	return s.queries.DeleteProxyAtFailThreshold(ctx, storage.DeleteProxyAtFailThresholdParams{
		ID:            id,
		FailThreshold: int64(threshold),
	})
}

func (s *Store) MarkAnonymity(ctx context.Context, id int64, anonymity string) error {
	return s.queries.MarkProxyAnonymity(ctx, storage.MarkProxyAnonymityParams{
		Anonymity: normalizeAnonymity(anonymity),
		UpdatedAt: time.Now().UTC(),
		ID:        id,
	})
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	row, err := s.queries.ProxyStats(ctx)
	if err != nil {
		return Stats{}, err
	}
	stats := Stats{
		Total:        int(row.Total),
		Available:    sqlValueInt(row.Available),
		Unavailable:  sqlValueInt(row.Unavailable),
		Disabled:     sqlValueInt(row.Disabled),
		Anonymous:    sqlValueInt(row.Anonymous),
		Transparent:  sqlValueInt(row.Transparent),
		Unknown:      sqlValueInt(row.Unknown),
		AverageDelay: sqlValueInt(row.AverageDelay),
	}
	return stats, nil
}

type proxyScanner interface {
	Scan(dest ...any) error
}

func scanCustomScraper(row proxyScanner) (CustomScraperTemplate, error) {
	var tpl CustomScraperTemplate
	var enabled int
	var lastRun sql.NullTime
	err := row.Scan(&tpl.ID, &tpl.Name, &enabled, &tpl.URLTemplate, &tpl.RequestMethod, &tpl.RequestHeaders, &tpl.RequestBody, &tpl.PageMode, &tpl.PageStart, &tpl.PageEnd, &tpl.PageSize, &tpl.MaxPages, &tpl.RequestTimeout, &tpl.Concurrency, &tpl.RetryCount, &tpl.StopFailures, &tpl.PagePattern, &tpl.PageGroup, &tpl.DefaultScheme, &tpl.ExtractPattern, &tpl.ResponseType, &tpl.JSONListPath, &tpl.JSONProxyPath, &tpl.JSONSchemePath, &tpl.JSONHostPath, &tpl.JSONPortPath, &tpl.JSONUserPath, &tpl.JSONPassPath, &tpl.SchemeGroup, &tpl.HostGroup, &tpl.PortGroup, &tpl.UserGroup, &tpl.PasswordGroup, &tpl.Notes, &tpl.CreatedAt, &tpl.UpdatedAt, &lastRun, &tpl.LastResult)
	if err != nil {
		return tpl, err
	}
	tpl.Enabled = enabled == 1
	if lastRun.Valid {
		tpl.LastRun = &lastRun.Time
	}
	return tpl, nil
}

func normalizeCustomScraper(tpl CustomScraperTemplate) CustomScraperTemplate {
	if tpl.Name == "" {
		tpl.Name = "未命名模板"
	}
	if tpl.PageStart <= 0 {
		tpl.PageStart = 1
	}
	tpl.RequestMethod = strings.ToUpper(strings.TrimSpace(tpl.RequestMethod))
	if tpl.RequestMethod != "POST" {
		tpl.RequestMethod = "GET"
	}
	if tpl.PageMode != "auto" {
		tpl.PageMode = "range"
	}
	if tpl.PageEnd < tpl.PageStart {
		tpl.PageEnd = tpl.PageStart
	}
	if tpl.PageSize < 0 {
		tpl.PageSize = 0
	}
	if tpl.MaxPages <= 0 {
		tpl.MaxPages = 50
	}
	if tpl.RequestTimeout < 0 {
		tpl.RequestTimeout = 0
	}
	if tpl.Concurrency <= 0 {
		tpl.Concurrency = 1
	}
	if tpl.Concurrency > 50 {
		tpl.Concurrency = 50
	}
	if tpl.RetryCount < 0 {
		tpl.RetryCount = 0
	}
	if tpl.RetryCount > 10 {
		tpl.RetryCount = 10
	}
	if tpl.StopFailures < 0 {
		tpl.StopFailures = 0
	}
	tpl.DefaultScheme = protocolTagString(tpl.DefaultScheme)
	if tpl.ResponseType != "json" {
		tpl.ResponseType = "text"
	}
	if tpl.ResponseType == "json" && tpl.JSONListPath == "" {
		tpl.JSONListPath = "data.proxies"
	}
	if strings.TrimSpace(tpl.ExtractPattern) == "" {
		tpl.ExtractPattern = defaultCustomExtractPattern
	}
	if tpl.SchemeGroup == "" {
		tpl.SchemeGroup = "scheme"
	}
	if tpl.HostGroup == "" {
		tpl.HostGroup = "host"
	}
	if tpl.PortGroup == "" {
		tpl.PortGroup = "port"
	}
	if tpl.UserGroup == "" {
		tpl.UserGroup = "user"
	}
	if tpl.PasswordGroup == "" {
		tpl.PasswordGroup = "password"
	}
	return tpl
}

func scanProxy(row proxyScanner) (Proxy, error) {
	var p Proxy
	var enabled int
	var lastChecked, lastUsed sql.NullTime
	err := row.Scan(&p.ID, &p.Scheme, &p.Host, &p.Port, &p.Username, &p.Password, &p.Tags, &p.Source, &p.Anonymity, &p.Reachability, &p.TargetCapability, &enabled, &p.Status, &p.FailCount, &p.SuccessCount, &p.LatencyMS, &lastChecked, &lastUsed, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	p.Tags = normalizeTagList(firstNonEmpty(p.Tags, p.Scheme))
	p.Anonymity = normalizeAnonymity(p.Anonymity)
	p.Reachability = normalizeReachability(p.Reachability)
	p.TargetCapability = normalizeTargetCapability(p.TargetCapability)
	p.Enabled = enabled == 1
	if lastChecked.Valid {
		p.LastChecked = &lastChecked.Time
	}
	if lastUsed.Valid {
		p.LastUsed = &lastUsed.Time
	}

	return p, nil
}

func parsePositive(v string, fallback int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func parseNonNegative(v string, fallback int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func notFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func dbPath() string {
	return filepath.Join("data", "proxypool.db")
}

func wrapErr(msg string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", msg, err)
}
