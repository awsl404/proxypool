package proxypool

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestImportDeduplicates(t *testing.T) {
	store := newTestStore(t)
	result := ImportProxies(context.Background(), store, "http://127.0.0.1:8080\nhttp://127.0.0.1:8080")
	if result.Imported != 1 || result.Duplicates != 1 || result.Failed != 0 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	items, err := store.ListProxies(context.Background(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one proxy, got %d", len(items))
	}
}

func TestStatsEmptyStoreReturnsZeros(t *testing.T) {
	store := newTestStore(t)
	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 0 || stats.Available != 0 || stats.Anonymous != 0 || stats.AverageDelay != 0 {
		t.Fatalf("expected zero stats, got %#v", stats)
	}
}

func TestProxyProtocolTagsStayPerScheme(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if _, err := store.AddProxy(ctx, Proxy{Scheme: "http", Host: "127.0.0.1", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProxy(ctx, Proxy{Scheme: "socks5", Host: "127.0.0.1", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListProxies(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected two protocol rows, got %#v", items)
	}
	for _, item := range items {
		if item.Tags != item.Scheme {
			t.Fatalf("expected per-scheme protocol tags, got %#v", items)
		}
	}
}

func TestDeleteMultiTagProxies(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if _, err := store.AddProxy(ctx, Proxy{Scheme: "http", Host: "127.0.0.1", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE proxies SET tags = 'http,socks5' WHERE host = '127.0.0.1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProxy(ctx, Proxy{Scheme: "http", Host: "127.0.0.2", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteMultiTagProxies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected one deleted multi-tag proxy, got %d", deleted)
	}
	items, err := store.ListProxies(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Host != "127.0.0.2" {
		t.Fatalf("unexpected remaining proxies: %#v", items)
	}
}

func TestSourceStatsQueryAndDelete(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.RecordSourceStat(ctx, "https://ok.example/list.txt", true, 2, 1, 2, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSourceStat(ctx, "https://bad.example/list.txt", false, 0, 0, 0, "timeout"); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListSourceStats(ctx, "timeout", false, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].URL != "https://bad.example/list.txt" {
		t.Fatalf("unexpected query result: %#v", items)
	}
	items, err = store.ListSourceStats(ctx, "", true, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].FetchFail != 1 {
		t.Fatalf("unexpected failed-only result: %#v", items)
	}
	deleted, err := store.DeleteSourceStat(ctx, "https://bad.example/list.txt")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected one deleted source stat, got %d", deleted)
	}
	deleted, err = store.ClearSourceStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected one remaining source stat deleted, got %d", deleted)
	}
}

func TestFailureThresholdDeletesProxy(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	cfg, err := store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg.FailThreshold = 2
	if err := store.UpdateSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	result := ImportProxies(ctx, store, "http://127.0.0.1:8080")
	if result.Imported != 1 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	items, _ := store.ListProxies(ctx, "", "", "")
	if err := store.MarkCheck(ctx, items[0].ID, false, 0, cfg); err != nil {
		t.Fatal(err)
	}
	items, _ = store.ListProxies(ctx, "", "", "")
	if len(items) != 1 {
		t.Fatal("proxy deleted before threshold")
	}
	if err := store.MarkCheck(ctx, items[0].ID, false, 0, cfg); err != nil {
		t.Fatal(err)
	}
	items, _ = store.ListProxies(ctx, "", "", "")
	if len(items) != 0 {
		t.Fatalf("expected deletion at threshold, got %d proxies", len(items))
	}
}

func TestFailureThresholdArchivesAndSkipsDeadProxy(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	cfg, err := store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg.FailThreshold = 1
	result := ImportProxies(ctx, store, "http://127.0.0.1:8080")
	if result.Imported != 1 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	items, _ := store.ListProxies(ctx, "", "", "")
	if err := store.MarkCheckResult(ctx, items[0].ID, ProxyCheckResult{Reachability: "none", Error: context.DeadlineExceeded}, cfg); err != nil {
		t.Fatal(err)
	}
	items, _ = store.ListProxies(ctx, "", "", "")
	if len(items) != 0 {
		t.Fatalf("expected proxy to be removed from active pool, got %#v", items)
	}
	page, err := store.ListDeadProxiesPage(ctx, "", "false", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].FailCount != 1 {
		t.Fatalf("unexpected dead proxy page: %#v", page)
	}
	skip, dead, err := store.ShouldSkipDeadProxy(ctx, Proxy{Scheme: "http", Host: "127.0.0.1", Port: 8080}, cfg.FailThreshold)
	if err != nil {
		t.Fatal(err)
	}
	if !skip || dead.Host != "127.0.0.1" {
		t.Fatalf("expected dead proxy skip, got skip=%t dead=%#v", skip, dead)
	}
	if err := store.SetDeadProxyDisabled(ctx, dead.ID, true); err != nil {
		t.Fatal(err)
	}
	skip, _, err = store.ShouldSkipDeadProxy(ctx, Proxy{Scheme: "http", Host: "127.0.0.1", Port: 8080}, cfg.FailThreshold)
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("disabled dead proxy should not be skipped")
	}
}

func TestStatsAverageLatency(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	ImportProxies(ctx, store, "http://127.0.0.1:8080\nhttp://127.0.0.2:8080")
	cfg, _ := store.Settings(ctx)
	items, _ := store.ListProxies(ctx, "", "", "")
	_ = store.MarkCheck(ctx, items[0].ID, true, 100*time.Millisecond, cfg)
	_ = store.MarkCheck(ctx, items[1].ID, true, 300*time.Millisecond, cfg)
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Available != 2 || stats.AverageDelay != 200 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
}

func TestReachabilityFilterIncludesBoth(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	cfg, _ := store.Settings(ctx)
	ImportProxies(ctx, store, "http://127.0.0.1:8080\nhttp://127.0.0.2:8080")
	items, _ := store.ListProxies(ctx, "", "", "")
	_ = store.MarkCheckResult(ctx, items[0].ID, ProxyCheckResult{Latency: 80 * time.Millisecond, DomesticOK: true, Reachability: "domestic"}, cfg)
	_ = store.MarkCheckResult(ctx, items[1].ID, ProxyCheckResult{Latency: 90 * time.Millisecond, DomesticOK: true, ForeignOK: true, Reachability: "both"}, cfg)

	domestic, err := store.ListProxiesFiltered(ctx, "", "", "", "", "domestic")
	if err != nil {
		t.Fatal(err)
	}
	if len(domestic) != 2 {
		t.Fatalf("expected domestic filter to include both, got %#v", domestic)
	}
	foreign, err := store.CandidateProxies(ctx, 10, "any", "foreign", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(foreign) != 1 || foreign[0].Reachability != "both" {
		t.Fatalf("expected foreign candidates to include only both-capable proxy, got %#v", foreign)
	}
}

func TestCandidateProxiesSchemeFilter(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	cfg, _ := store.Settings(ctx)
	proxies := []Proxy{
		{Scheme: "http", Host: "127.0.0.1", Port: 8080},
		{Scheme: "https", Host: "127.0.0.2", Port: 8443},
		{Scheme: "socks5", Host: "127.0.0.3", Port: 1080},
	}
	for _, p := range proxies {
		if _, err := store.AddProxy(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListProxies(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if err := store.MarkCheckResult(ctx, item.ID, ProxyCheckResult{Latency: 80 * time.Millisecond, DomesticOK: true, ForeignOK: true, Reachability: "both"}, cfg); err != nil {
			t.Fatal(err)
		}
	}
	httpItems, err := store.CandidateProxies(ctx, 10, "any", "any", "http,https")
	if err != nil {
		t.Fatal(err)
	}
	if len(httpItems) != 2 {
		t.Fatalf("expected 2 http candidates, got %#v", httpItems)
	}
	for _, item := range httpItems {
		if item.Scheme == "socks5" {
			t.Fatalf("unexpected socks candidate for http entry: %#v", httpItems)
		}
	}
	socksItems, err := store.CandidateProxies(ctx, 10, "any", "any", "socks5")
	if err != nil {
		t.Fatal(err)
	}
	if len(socksItems) != 1 || socksItems[0].Scheme != "socks5" {
		t.Fatalf("expected socks5 candidate, got %#v", socksItems)
	}
}

func TestTargetCapabilityFilters(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	cfg, _ := store.Settings(ctx)
	proxies := []Proxy{
		{Scheme: "http", Host: "127.0.0.1", Port: 8080},
		{Scheme: "http", Host: "127.0.0.2", Port: 8080},
		{Scheme: "socks5", Host: "127.0.0.3", Port: 1080},
	}
	for _, p := range proxies {
		if _, err := store.AddProxy(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListProxies(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, item := range items {
		ids[item.Host] = item.ID
	}
	if err := store.MarkCheckResult(ctx, ids["127.0.0.1"], ProxyCheckResult{Latency: 80 * time.Millisecond, DomesticOK: true, HTTPOK: true, Reachability: "domestic", TargetCapability: "http"}, cfg); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCheckResult(ctx, ids["127.0.0.2"], ProxyCheckResult{Latency: 90 * time.Millisecond, ForeignOK: true, HTTPSOK: true, Reachability: "foreign", TargetCapability: "https"}, cfg); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCheckResult(ctx, ids["127.0.0.3"], ProxyCheckResult{Latency: 100 * time.Millisecond, DomesticOK: true, ForeignOK: true, HTTPOK: true, HTTPSOK: true, Reachability: "both", TargetCapability: "both"}, cfg); err != nil {
		t.Fatal(err)
	}

	httpsItems, err := store.CandidateProxiesForTargetCapability(ctx, 10, "any", "any", "all", "https")
	if err != nil {
		t.Fatal(err)
	}
	if len(httpsItems) != 2 {
		t.Fatalf("expected https-capable candidates to include https and both, got %#v", httpsItems)
	}
	for _, item := range httpsItems {
		if item.TargetCapability == "http" {
			t.Fatalf("http-only proxy should not be a https candidate: %#v", httpsItems)
		}
	}

	httpItems, err := store.ListProxiesFilteredByTargetCapability(ctx, "", "", "", "", "", "http")
	if err != nil {
		t.Fatal(err)
	}
	if len(httpItems) != 2 {
		t.Fatalf("expected http-capable filter to include http and both, got %#v", httpItems)
	}
}

func TestHTTPSCandidateFallsBackToUnknownOnly(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	cfg, _ := store.Settings(ctx)
	if _, err := store.AddProxy(ctx, Proxy{Scheme: "http", Host: "127.0.0.1", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProxy(ctx, Proxy{Scheme: "http", Host: "127.0.0.2", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListProxies(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, item := range items {
		ids[item.Host] = item.ID
	}
	if err := store.MarkCheckResult(ctx, ids["127.0.0.1"], ProxyCheckResult{Latency: 80 * time.Millisecond, DomesticOK: true, HTTPOK: true, Reachability: "domestic", TargetCapability: "http"}, cfg); err != nil {
		t.Fatal(err)
	}

	pool := NewProxyPool(store)
	candidates, err := pool.CandidatesForSchemes(ctx, "example.com:443", cfg, 10, "http,https")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Host != "127.0.0.2" || candidates[0].TargetCapability != "unknown" {
		t.Fatalf("expected https target to fall back only to unknown candidates, got %#v", candidates)
	}
}

func TestDeleteProxiesFiltered(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	cfg, _ := store.Settings(ctx)
	proxies := []Proxy{
		{Scheme: "http", Host: "127.0.0.1", Port: 8080, Source: "alpha"},
		{Scheme: "http", Host: "127.0.0.2", Port: 8080, Source: "alpha"},
		{Scheme: "socks5", Host: "127.0.0.3", Port: 1080, Source: "beta"},
	}
	for _, p := range proxies {
		if _, err := store.AddProxy(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListProxies(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, item := range items {
		ids[item.Host] = item.ID
	}
	if err := store.MarkCheckResult(ctx, ids["127.0.0.1"], ProxyCheckResult{Latency: 80 * time.Millisecond, DomesticOK: true, ForeignOK: true, Reachability: "both"}, cfg); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCheckResult(ctx, ids["127.0.0.2"], ProxyCheckResult{Latency: 90 * time.Millisecond, DomesticOK: true, Reachability: "domestic"}, cfg); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCheckResult(ctx, ids["127.0.0.3"], ProxyCheckResult{Latency: 100 * time.Millisecond, DomesticOK: true, ForeignOK: true, Reachability: "both"}, cfg); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAnonymity(ctx, ids["127.0.0.3"], "anonymous"); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.DeleteProxiesFiltered(ctx, "alpha", "http", "available", "unknown", "foreign")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected one filtered proxy deleted, got %d", deleted)
	}
	remaining, err := store.ListProxies(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected two proxies remaining, got %#v", remaining)
	}
	for _, item := range remaining {
		if item.Host == "127.0.0.1" {
			t.Fatalf("filtered proxy was not deleted: %#v", remaining)
		}
	}
}

func TestPaginatedLists(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	var proxyLines []string
	for i := 0; i < 11; i++ {
		proxyLines = append(proxyLines, "http://127.0.0."+itoa(i+1)+":8001")
	}
	ImportProxies(ctx, store, strings.Join(proxyLines, "\n"))
	proxyPage, err := store.ListProxiesFilteredPage(ctx, "", "", "", "", "", 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if proxyPage.Total != 11 || proxyPage.TotalPages != 2 || len(proxyPage.Items) != 1 || proxyPage.Page != 2 {
		t.Fatalf("unexpected proxy page: %#v", proxyPage)
	}

	for i := 0; i < 11; i++ {
		if err := store.RecordSourceStat(ctx, "https://example.test/"+itoa(i), true, 1, 0, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	sourcePage, err := store.ListSourceStatsPage(ctx, "", false, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if sourcePage.Total != 11 || sourcePage.TotalPages != 2 || len(sourcePage.Items) != 1 {
		t.Fatalf("unexpected source page: %#v", sourcePage)
	}

	for i := 0; i < 11; i++ {
		if _, err := store.SaveCustomScraper(ctx, CustomScraperTemplate{
			Name:           "tpl-" + itoa(i),
			URLTemplate:    "https://example.test",
			DefaultScheme:  "http",
			ExtractPattern: `(?P<host>\S+):(?P<port>\d+)`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	customPage, err := store.ListCustomScrapersPage(ctx, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if customPage.Total != 11 || customPage.TotalPages != 2 || len(customPage.Items) != 1 {
		t.Fatalf("unexpected custom page: %#v", customPage)
	}
}
