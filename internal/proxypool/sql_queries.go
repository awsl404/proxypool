package proxypool

import (
	_ "embed"
	"strings"
)

//go:embed storage/schema.sql
var schemaSQL string

func migrationStatements() []string {
	stmts := []string{"PRAGMA journal_mode=WAL"}
	for _, stmt := range strings.Split(schemaSQL, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt != "" {
			stmts = append(stmts, stmt)
		}
	}
	return stmts
}

var customScraperColumnMigrations = []struct {
	name string
	decl string
}{
	{"request_method", "TEXT NOT NULL DEFAULT 'GET'"},
	{"request_headers", "TEXT NOT NULL DEFAULT ''"},
	{"request_body", "TEXT NOT NULL DEFAULT ''"},
	{"page_mode", "TEXT NOT NULL DEFAULT 'range'"},
	{"page_size", "INTEGER NOT NULL DEFAULT 0"},
	{"max_pages", "INTEGER NOT NULL DEFAULT 50"},
	{"request_timeout_seconds", "INTEGER NOT NULL DEFAULT 0"},
	{"concurrency", "INTEGER NOT NULL DEFAULT 1"},
	{"retry_count", "INTEGER NOT NULL DEFAULT 0"},
	{"stop_after_failures", "INTEGER NOT NULL DEFAULT 0"},
	{"page_pattern", "TEXT NOT NULL DEFAULT ''"},
	{"page_group", "TEXT NOT NULL DEFAULT ''"},
	{"response_type", "TEXT NOT NULL DEFAULT 'text'"},
	{"json_list_path", "TEXT NOT NULL DEFAULT ''"},
	{"json_proxy_path", "TEXT NOT NULL DEFAULT ''"},
	{"json_scheme_path", "TEXT NOT NULL DEFAULT ''"},
	{"json_host_path", "TEXT NOT NULL DEFAULT ''"},
	{"json_port_path", "TEXT NOT NULL DEFAULT ''"},
	{"json_user_path", "TEXT NOT NULL DEFAULT ''"},
	{"json_password_path", "TEXT NOT NULL DEFAULT ''"},
}
