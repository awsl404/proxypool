package proxypool

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailFileFiltersQueryAndLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	text := "2026-01-01 00:00:01 [INFO] collector started\n" +
		"2026-01-01 00:00:02 [ERROR] collector failed\n" +
		"2026-01-01 00:00:03 [WARN] health failed\n"
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	lines, err := tailFile(path, 10, "collector", "error")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "2026-01-01 00:00:02 [ERROR] collector failed" {
		t.Fatalf("unexpected filtered lines: %#v", lines)
	}
}

func TestLoginFailureLimit(t *testing.T) {
	store := newTestStore(t)
	server := NewWebServer(store, NewProxyPool(store), "")
	remote := "127.0.0.1"
	for i := 0; i < loginFailureLimit; i++ {
		server.recordLoginFailure(remote)
	}
	if !server.loginRateLimited(remote) {
		t.Fatal("expected remote to be rate limited")
	}
	server.clearLoginFailures(remote)
	if server.loginRateLimited(remote) {
		t.Fatal("successful login reset should clear failures")
	}
	server.mu.Lock()
	server.loginFails[remote] = loginAttempt{windowStarted: time.Now().Add(-loginFailureWindow), failures: loginFailureLimit}
	server.mu.Unlock()
	if server.loginRateLimited(remote) {
		t.Fatal("expired failure window should no longer be limited")
	}
}

func TestRegisterCollectorJobLimit(t *testing.T) {
	store := newTestStore(t)
	server := NewWebServer(store, NewProxyPool(store), "")
	for i := 0; i < maxRunningJobs; i++ {
		if !server.registerCollectorJob(string(rune('a'+i)), &CollectorJob{Status: "running"}) {
			t.Fatalf("job %d unexpectedly rejected", i)
		}
	}
	if server.registerCollectorJob("overflow", &CollectorJob{Status: "running"}) {
		t.Fatal("expected running-job limit to reject overflow")
	}
}

func TestDraftCustomScraperPreviewDoesNotSaveTemplate(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1.2.3.4:8080"))
	}))
	defer target.Close()

	store := newTestStore(t)
	server := NewWebServer(store, NewProxyPool(store), filepath.Join(t.TempDir(), "config.json"))
	body := map[string]any{
		"template": CustomScraperTemplate{
			Name:           "draft",
			URLTemplate:    target.URL,
			DefaultScheme:  "http",
			ExtractPattern: `(?P<host>\d+\.\d+\.\d+\.\d+):(?P<port>\d+)`,
			HostGroup:      "host",
			PortGroup:      "port",
		},
		"mode": "direct",
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/custom-scrapers/preview", bytes.NewReader(data))
	rec := httptest.NewRecorder()
	server.handleCustomScraperItem(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	var preview CustomScrapePreview
	if err := json.NewDecoder(rec.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Matches) != 1 || preview.Matches[0].Host != "1.2.3.4" {
		t.Fatalf("unexpected preview: %#v", preview)
	}
	items, err := store.ListCustomScrapers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("preview should not save templates: %#v", items)
	}
}
