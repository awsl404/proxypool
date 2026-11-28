package proxypool

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestLoginHandlerLimitsConcurrentFailuresAndBoundsMap(t *testing.T) {
	store := newTestStore(t)
	server := NewWebServer(store, NewProxyPool(store), "")
	handler := server.Routes()
	for i := 0; i < loginFailureLimit; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"bad","password":"bad"}`))
		req.RemoteAddr = "192.0.2.10:1234"
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"bad","password":"bad"}`))
	req.RemoteAddr = "192.0.2.10:9999"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("expected throttled response, status=%d body=%s", rec.Code, rec.Body.String())
	}

	server.mu.Lock()
	server.loginFails = make(map[string]loginAttempt, maxLoginEntries)
	for i := 0; i < maxLoginEntries; i++ {
		server.loginFails[fmt.Sprintf("ip-%d", i)] = loginAttempt{windowStarted: time.Now()}
	}
	server.mu.Unlock()
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"bad","password":"bad"}`))
	req.RemoteAddr = "198.51.100.1:1234"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("full limiter map should fail closed, status=%d", rec.Code)
	}
}

func TestCollectorRunEntrypointsShareConcurrentLimit(t *testing.T) {
	store := newTestStore(t)
	server := NewWebServer(store, NewProxyPool(store), "")
	for i := 0; i < maxRunningJobs; i++ {
		if !server.registerCollectorJob(fmt.Sprint("running-", i), &CollectorJob{Status: "running"}) {
			t.Fatal("failed to seed running job")
		}
	}
	server.mu.Lock()
	server.sessions["test-session"] = time.Now().Add(time.Hour)
	server.mu.Unlock()
	handler := server.Routes()
	for _, path := range []string{"/api/collector/run", "/api/custom-scrapers/1/run"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{}`))
		req.AddCookie(&http.Cookie{Name: "pp_session", Value: "test-session"})
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("%s: expected shared running-job limit, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestSessionExpiryAndHardCapacity(t *testing.T) {
	store := newTestStore(t)
	cfg, err := store.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg.AdminPassword = "secret"
	if err := store.UpdateSettings(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	server := NewWebServer(store, NewProxyPool(store), "")
	server.sessions["expired"] = time.Now().Add(-time.Second)
	server.sessions["live"] = time.Now().Add(time.Hour)
	if server.hasSession(sessionRequest("expired")) {
		t.Fatal("expired session unexpectedly authenticated")
	}
	if _, ok := server.sessions["expired"]; ok {
		t.Fatal("expired session was not removed")
	}
	if !server.hasSession(sessionRequest("live")) {
		t.Fatal("live session rejected")
	}
	server.mu.Lock()
	server.sessions = make(map[string]time.Time, maxSessions)
	for i := 0; i < maxSessions; i++ {
		server.sessions[fmt.Sprintf("session-%d", i)] = time.Now().Add(time.Hour)
	}
	server.mu.Unlock()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	req.RemoteAddr = "203.0.113.20:1"
	server.handleLogin(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected capacity response, got %d: %s", rec.Code, rec.Body.String())
	}
}

func sessionRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "pp_session", Value: token})
	return req
}

func TestConcurrentCollectorJobRegistrationLimit(t *testing.T) {
	store := newTestStore(t)
	server := NewWebServer(store, NewProxyPool(store), "")
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if server.registerCollectorJob(fmt.Sprintf("job-%d", i), &CollectorJob{Status: "running"}) {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if accepted != maxRunningJobs {
		t.Fatalf("accepted %d concurrent jobs; want %d", accepted, maxRunningJobs)
	}
	if got := len(server.jobs); got != maxRunningJobs {
		t.Fatalf("stored %d jobs; want %d", got, maxRunningJobs)
	}
}

func TestCollectorJobHardRetentionIncludesStopped(t *testing.T) {
	store := newTestStore(t)
	server := NewWebServer(store, NewProxyPool(store), "")
	for i := 0; i < maxCollectorJobs; i++ {
		server.jobs[fmt.Sprintf("old-%d", i)] = &CollectorJob{ID: fmt.Sprint(i), Status: "stopped", StartedAt: time.Now().Add(-time.Duration(i+1) * time.Minute)}
	}
	if !server.registerCollectorJob("new", &CollectorJob{Status: "done", StartedAt: time.Now()}) {
		t.Fatal("new terminal job should register after pruning")
	}
	if got := len(server.jobs); got > maxCollectorJobs {
		t.Fatalf("job history exceeded hard cap: %d", got)
	}
	if _, ok := server.jobs["old-99"]; ok {
		t.Fatal("oldest stopped record should have been pruned")
	}
	if _, ok := server.jobs["new"]; !ok {
		t.Fatal("new job should be retained")
	}
}
