package proxypool

import "time"

type Proxy struct {
	ID               int64      `json:"id"`
	Scheme           string     `json:"scheme"`
	Host             string     `json:"host"`
	Port             int        `json:"port"`
	Username         string     `json:"username,omitempty"`
	Password         string     `json:"password,omitempty"`
	Tags             string     `json:"tags,omitempty"`
	Source           string     `json:"source,omitempty"`
	Anonymity        string     `json:"anonymity"`
	Reachability     string     `json:"reachability"`
	TargetCapability string     `json:"target_capability"`
	Enabled          bool       `json:"enabled"`
	Status           string     `json:"status"`
	FailCount        int        `json:"fail_count"`
	SuccessCount     int        `json:"success_count"`
	LatencyMS        int64      `json:"latency_ms"`
	LastChecked      *time.Time `json:"last_checked,omitempty"`
	LastUsed         *time.Time `json:"last_used,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func (p Proxy) Addr() string {
	return p.Host + ":" + itoa(p.Port)
}

type Settings struct {
	WebAddr               string `json:"web_addr"`
	HTTPProxyAddr         string `json:"http_proxy_addr"`
	SOCKSProxyAddr        string `json:"socks_proxy_addr"`
	AdminUser             string `json:"admin_user"`
	AdminPassword         string `json:"admin_password,omitempty"`
	AdminPasswordHash     string `json:"admin_password_hash,omitempty"`
	APIToken              string `json:"api_token"`
	ProxyUsername         string `json:"proxy_username"`
	ProxyPassword         string `json:"proxy_password,omitempty"`
	HealthURL             string `json:"health_url"`
	DomesticHealthURL     string `json:"domestic_health_url"`
	ForeignHealthURL      string `json:"foreign_health_url"`
	AnonymityURL          string `json:"anonymity_url"`
	ServeAnonymity        string `json:"serve_anonymity"`
	ServeReachability     string `json:"serve_reachability"`
	ServeStrategy         string `json:"serve_strategy"`
	CollectorSources      string `json:"collector_sources"`
	CollectorMode         string `json:"collector_mode"`
	CollectorProxy        string `json:"collector_proxy"`
	CheckConcurrency      int    `json:"check_concurrency"`
	CheckIntervalSeconds  int    `json:"check_interval_seconds"`
	CheckScheduleEnabled  bool   `json:"check_schedule_enabled"`
	CheckScheduleScope    string `json:"check_schedule_scope"`
	CheckScheduleSchemes  string `json:"check_schedule_schemes"`
	FailThreshold         int    `json:"fail_threshold"`
	TimeoutSeconds        int    `json:"timeout_seconds"`
	TLSInsecureSkipVerify bool   `json:"tls_insecure_skip_verify"`
	CheckSuccessKeywords  string `json:"check_success_keywords"`
	CheckBlockKeywords    string `json:"check_block_keywords"`
	MaintenanceEnabled    bool   `json:"maintenance_enabled"`
	MaintenanceInterval   int    `json:"maintenance_interval_seconds"`
	LogRetentionDays      int    `json:"log_retention_days"`
	SourceRetentionDays   int    `json:"source_retention_days"`
	DeadRetentionDays     int    `json:"dead_retention_days"`
	MaxDBSizeMB           int    `json:"max_db_size_mb"`
	BackupDir             string `json:"backup_dir"`
	AutoBackup            bool   `json:"auto_backup"`
	AutoVacuum            bool   `json:"auto_vacuum"`
	AutoCleanup           bool   `json:"auto_cleanup_unavailable"`
	ScheduleEnabled       bool   `json:"schedule_enabled"`
	ScheduleInterval      int    `json:"schedule_interval_minutes"`
	ScheduleRunCollector  bool   `json:"schedule_run_collector"`
	ScheduleRunCustom     bool   `json:"schedule_run_custom"`
	ScheduleCheck         bool   `json:"schedule_check_after_import"`
	ScheduleMaxRunsDay    int    `json:"schedule_max_runs_per_day"`
	SchedulePauseMinutes  int    `json:"schedule_failure_pause_minutes"`
	LogLevel              string `json:"log_level"`
	LogFile               string `json:"log_file"`
	BindLocalOnly         bool   `json:"bind_local_only"`
	WebAllowlist          string `json:"web_allowlist"`
	WebBlocklist          string `json:"web_blocklist"`
	ProxyAllowlist        string `json:"proxy_allowlist"`
	ProxyBlocklist        string `json:"proxy_blocklist"`
}

type ImportResult struct {
	Imported   int      `json:"imported"`
	Duplicates int      `json:"duplicates"`
	Skipped    int      `json:"skipped"`
	Failed     int      `json:"failed"`
	Checked    int      `json:"checked"`
	Errors     []string `json:"errors"`
}

type Stats struct {
	Total        int `json:"total"`
	Available    int `json:"available"`
	Unavailable  int `json:"unavailable"`
	Disabled     int `json:"disabled"`
	Anonymous    int `json:"anonymous"`
	Transparent  int `json:"transparent"`
	Unknown      int `json:"unknown"`
	AverageDelay int `json:"average_delay"`
}

type MaintenanceResult struct {
	DeletedUnavailable int64    `json:"deleted_unavailable"`
	DeletedSources     int64    `json:"deleted_sources"`
	DeletedDead        int64    `json:"deleted_dead"`
	LogLinesRemoved    int      `json:"log_lines_removed"`
	BackupPath         string   `json:"backup_path,omitempty"`
	Vacuumed           bool     `json:"vacuumed"`
	WALCheckpointed    bool     `json:"wal_checkpointed"`
	OverLimitCleanup   bool     `json:"over_limit_cleanup,omitempty"`
	Errors             []string `json:"errors,omitempty"`
}

type RuntimeStatus struct {
	Proxy           Stats                   `json:"proxy"`
	Outbound        PoolMetrics             `json:"outbound"`
	Scheduler       SchedulerSnapshot       `json:"scheduler"`
	HealthScheduler HealthSchedulerSnapshot `json:"health_scheduler"`
	Maintenance     MaintenanceSnapshot     `json:"maintenance"`
	RunningJobs     int                     `json:"running_jobs"`
	StoredJobs      int                     `json:"stored_jobs"`
	HealthCheckBusy bool                    `json:"health_check_busy"`
	DBSizeBytes     int64                   `json:"db_size_bytes"`
	WALSizeBytes    int64                   `json:"wal_size_bytes"`
	LogSizeBytes    int64                   `json:"log_size_bytes"`
	SourceTotal     int                     `json:"source_total"`
	SourceFailing   int                     `json:"source_failing"`
	CustomTotal     int                     `json:"custom_total"`
	CustomEnabled   int                     `json:"custom_enabled"`
	GeneratedAt     time.Time               `json:"generated_at"`
}

type RuntimeTasks struct {
	HealthScheduler    HealthSchedulerSnapshot `json:"health_scheduler"`
	CollectorScheduler SchedulerSnapshot       `json:"collector_scheduler"`
	Maintenance        MaintenanceSnapshot     `json:"maintenance"`
	CollectorJobs      []CollectorJob          `json:"collector_jobs"`
	GeneratedAt        time.Time               `json:"generated_at"`
}

type SourceStat struct {
	URL          string     `json:"url"`
	FetchOK      int        `json:"fetch_ok"`
	FetchFail    int        `json:"fetch_fail"`
	Imported     int        `json:"imported"`
	Duplicates   int        `json:"duplicates"`
	Checked      int        `json:"checked"`
	LastError    string     `json:"last_error,omitempty"`
	LastRun      *time.Time `json:"last_run,omitempty"`
	LastImported int        `json:"last_imported"`
}

type DeadProxy struct {
	ID        int64      `json:"id"`
	Scheme    string     `json:"scheme"`
	Host      string     `json:"host"`
	Port      int        `json:"port"`
	Username  string     `json:"username,omitempty"`
	Password  string     `json:"password,omitempty"`
	Tags      string     `json:"tags,omitempty"`
	Source    string     `json:"source,omitempty"`
	FailCount int        `json:"fail_count"`
	LastError string     `json:"last_error,omitempty"`
	Disabled  bool       `json:"disabled"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type Page[T any] struct {
	Items      []T `json:"items"`
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

type CustomScraperTemplate struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Enabled        bool       `json:"enabled"`
	URLTemplate    string     `json:"url_template"`
	RequestMethod  string     `json:"request_method"`
	RequestHeaders string     `json:"request_headers"`
	RequestBody    string     `json:"request_body"`
	PageMode       string     `json:"page_mode"`
	PageStart      int        `json:"page_start"`
	PageEnd        int        `json:"page_end"`
	PageSize       int        `json:"page_size"`
	MaxPages       int        `json:"max_pages"`
	RequestTimeout int        `json:"request_timeout_seconds"`
	Concurrency    int        `json:"concurrency"`
	RetryCount     int        `json:"retry_count"`
	StopFailures   int        `json:"stop_after_failures"`
	PagePattern    string     `json:"page_pattern"`
	PageGroup      string     `json:"page_group"`
	DefaultScheme  string     `json:"default_scheme"`
	ExtractPattern string     `json:"extract_pattern"`
	ResponseType   string     `json:"response_type"`
	JSONListPath   string     `json:"json_list_path"`
	JSONProxyPath  string     `json:"json_proxy_path"`
	JSONSchemePath string     `json:"json_scheme_path"`
	JSONHostPath   string     `json:"json_host_path"`
	JSONPortPath   string     `json:"json_port_path"`
	JSONUserPath   string     `json:"json_user_path"`
	JSONPassPath   string     `json:"json_password_path"`
	SchemeGroup    string     `json:"scheme_group"`
	HostGroup      string     `json:"host_group"`
	PortGroup      string     `json:"port_group"`
	UserGroup      string     `json:"user_group"`
	PasswordGroup  string     `json:"password_group"`
	Notes          string     `json:"notes"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	LastRun        *time.Time `json:"last_run,omitempty"`
	LastResult     string     `json:"last_result,omitempty"`
}

type CustomScrapeRequest struct {
	TemplateID    int64  `json:"template_id"`
	Mode          string `json:"mode"`
	SpecificProxy string `json:"specific_proxy"`
	Check         *bool  `json:"check"`
}

type CustomScrapePreview struct {
	URLs    []string `json:"urls"`
	Matches []Proxy  `json:"matches"`
	Error   string   `json:"error,omitempty"`
}

const defaultDomesticHealthURL = "http://117.72.157.151:8880/"
const defaultForeignHealthURL = "https://www.gstatic.com/generate_204\nhttps://www.cloudflare.com/cdn-cgi/trace\nhttps://httpbin.org/status/204"

func defaultSettings() Settings {
	return Settings{
		WebAddr:               "127.0.0.1:8080",
		HTTPProxyAddr:         "127.0.0.1:18080",
		SOCKSProxyAddr:        "127.0.0.1:18081",
		AdminUser:             "admin",
		AdminPassword:         "",
		APIToken:              randomToken(),
		ProxyUsername:         "",
		ProxyPassword:         "",
		HealthURL:             "https://www.gstatic.com/generate_204\nhttps://www.cloudflare.com/cdn-cgi/trace\nhttps://httpbin.org/status/204\nhttp://www.msftconnecttest.com/connecttest.txt",
		DomesticHealthURL:     defaultDomesticHealthURL,
		ForeignHealthURL:      defaultForeignHealthURL,
		AnonymityURL:          "https://httpbin.org/get",
		ServeAnonymity:        "any",
		ServeReachability:     "any",
		ServeStrategy:         "sequential",
		CollectorSources:      defaultCollectorSources(),
		CollectorMode:         "direct",
		CollectorProxy:        "",
		CheckConcurrency:      20,
		CheckIntervalSeconds:  300,
		CheckScheduleEnabled:  true,
		CheckScheduleScope:    "all",
		CheckScheduleSchemes:  "",
		FailThreshold:         3,
		TimeoutSeconds:        8,
		TLSInsecureSkipVerify: false,
		MaintenanceEnabled:    true,
		MaintenanceInterval:   3600,
		LogRetentionDays:      7,
		SourceRetentionDays:   30,
		DeadRetentionDays:     30,
		MaxDBSizeMB:           512,
		BackupDir:             "data/backups",
		AutoBackup:            false,
		AutoVacuum:            true,
		AutoCleanup:           false,
		ScheduleEnabled:       false,
		ScheduleInterval:      60,
		ScheduleRunCollector:  true,
		ScheduleRunCustom:     false,
		ScheduleCheck:         true,
		ScheduleMaxRunsDay:    24,
		SchedulePauseMinutes:  30,
		LogLevel:              "info",
		LogFile:               "logs/proxypool.log",
		BindLocalOnly:         true,
		WebAllowlist:          "",
		WebBlocklist:          "",
		ProxyAllowlist:        "",
		ProxyBlocklist:        "",
	}
}
