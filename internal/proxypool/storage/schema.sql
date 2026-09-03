CREATE TABLE IF NOT EXISTS proxies (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  scheme TEXT NOT NULL,
  host TEXT NOT NULL,
  port INTEGER NOT NULL,
  username TEXT NOT NULL DEFAULT '',
  password TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  anonymity TEXT NOT NULL DEFAULT 'unknown',
  reachability TEXT NOT NULL DEFAULT 'unknown',
  target_capability TEXT NOT NULL DEFAULT 'unknown',
  enabled INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL DEFAULT 'unchecked',
  fail_count INTEGER NOT NULL DEFAULT 0,
  success_count INTEGER NOT NULL DEFAULT 0,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  last_checked DATETIME,
  last_used DATETIME,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(scheme, host, port, username, password)
);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS source_stats (
  url TEXT PRIMARY KEY,
  fetch_ok INTEGER NOT NULL DEFAULT 0,
  fetch_fail INTEGER NOT NULL DEFAULT 0,
  imported INTEGER NOT NULL DEFAULT 0,
  duplicates INTEGER NOT NULL DEFAULT 0,
  checked INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  last_run DATETIME,
  last_imported INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS dead_proxies (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  scheme TEXT NOT NULL,
  host TEXT NOT NULL,
  port INTEGER NOT NULL,
  username TEXT NOT NULL DEFAULT '',
  password TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  fail_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  disabled INTEGER NOT NULL DEFAULT 0,
  last_seen DATETIME,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(scheme, host, port, username, password)
);

CREATE TABLE IF NOT EXISTS custom_scrapers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  url_template TEXT NOT NULL,
  request_method TEXT NOT NULL DEFAULT 'GET',
  request_headers TEXT NOT NULL DEFAULT '',
  request_body TEXT NOT NULL DEFAULT '',
  page_mode TEXT NOT NULL DEFAULT 'range',
  page_start INTEGER NOT NULL DEFAULT 1,
  page_end INTEGER NOT NULL DEFAULT 1,
  page_size INTEGER NOT NULL DEFAULT 0,
  max_pages INTEGER NOT NULL DEFAULT 50,
  request_timeout_seconds INTEGER NOT NULL DEFAULT 0,
  concurrency INTEGER NOT NULL DEFAULT 1,
  retry_count INTEGER NOT NULL DEFAULT 0,
  stop_after_failures INTEGER NOT NULL DEFAULT 0,
  page_pattern TEXT NOT NULL DEFAULT '',
  page_group TEXT NOT NULL DEFAULT '',
  default_scheme TEXT NOT NULL DEFAULT 'http',
  extract_pattern TEXT NOT NULL,
  response_type TEXT NOT NULL DEFAULT 'text',
  json_list_path TEXT NOT NULL DEFAULT '',
  json_proxy_path TEXT NOT NULL DEFAULT '',
  json_scheme_path TEXT NOT NULL DEFAULT '',
  json_host_path TEXT NOT NULL DEFAULT '',
  json_port_path TEXT NOT NULL DEFAULT '',
  json_user_path TEXT NOT NULL DEFAULT '',
  json_password_path TEXT NOT NULL DEFAULT '',
  scheme_group TEXT NOT NULL DEFAULT '',
  host_group TEXT NOT NULL DEFAULT 'host',
  port_group TEXT NOT NULL DEFAULT 'port',
  user_group TEXT NOT NULL DEFAULT '',
  password_group TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  last_run DATETIME,
  last_result TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_proxies_filter
ON proxies(enabled, status, scheme, anonymity, reachability, target_capability, latency_ms, id);

CREATE INDEX IF NOT EXISTS idx_proxies_candidate
ON proxies(enabled, status, fail_count, latency_ms, last_used);

CREATE INDEX IF NOT EXISTS idx_proxies_check_scope
ON proxies(status, enabled, last_checked);

CREATE INDEX IF NOT EXISTS idx_dead_proxies_list
ON dead_proxies(disabled, fail_count DESC, updated_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_source_stats_list
ON source_stats(last_run DESC, imported DESC);

CREATE INDEX IF NOT EXISTS idx_source_stats_failed
ON source_stats(fetch_fail, last_run DESC);
