package proxypool

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ProxyPool struct {
	store     *Store
	mu        sync.Mutex
	totalDial int64
	okDial    int64
	failDial  int64
	lastError string
	lastProxy int64
}

type PoolMetrics struct {
	TotalDial int64  `json:"total_dial"`
	OKDial    int64  `json:"ok_dial"`
	FailDial  int64  `json:"fail_dial"`
	LastError string `json:"last_error,omitempty"`
	LastProxy int64  `json:"last_proxy_id,omitempty"`
}

type ProxyCheckResult struct {
	Latency          time.Duration
	DomesticOK       bool
	ForeignOK        bool
	HTTPOK           bool
	HTTPSOK          bool
	Reachability     string
	TargetCapability string
	Error            error
}

func (r ProxyCheckResult) OK() bool {
	return r.DomesticOK || r.ForeignOK
}

func NewProxyPool(store *Store) *ProxyPool {
	return &ProxyPool{store: store}
}

func (p *ProxyPool) Dial(ctx context.Context, target string, cfg Settings) (net.Conn, Proxy, error) {
	candidates, err := p.Candidates(ctx, target, cfg, 30)
	if err != nil {
		p.recordDial(false, Proxy{}, err)
		return nil, Proxy{}, err
	}
	return p.dialCandidates(ctx, target, cfg, candidates)
}

func (p *ProxyPool) DialForSchemes(ctx context.Context, target string, cfg Settings, schemes string) (net.Conn, Proxy, error) {
	candidates, err := p.CandidatesForSchemes(ctx, target, cfg, 30, schemes)
	if err != nil {
		p.recordDial(false, Proxy{}, err)
		return nil, Proxy{}, err
	}
	return p.dialCandidates(ctx, target, cfg, candidates)
}

func (p *ProxyPool) dialCandidates(ctx context.Context, target string, cfg Settings, candidates []Proxy) (net.Conn, Proxy, error) {
	if len(candidates) == 0 {
		err := errors.New("no available upstream proxies")
		p.recordDial(false, Proxy{}, err)
		return nil, Proxy{}, err
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	var lastErr error
	for _, candidate := range candidates {
		conn, err := DialViaProxy(ctx, candidate, target, timeout)
		if err == nil {
			p.store.MarkUsed(ctx, candidate.ID)
			p.recordDial(true, candidate, nil)
			return conn, candidate, nil
		}
		lastErr = err
		_ = p.store.MarkCheck(ctx, candidate.ID, false, 0, cfg)
	}
	p.recordDial(false, Proxy{}, lastErr)
	return nil, Proxy{}, lastErr
}

func (p *ProxyPool) Candidates(ctx context.Context, target string, cfg Settings, limit int) ([]Proxy, error) {
	return p.CandidatesForSchemes(ctx, target, cfg, limit, "")
}

func (p *ProxyPool) CandidatesForSchemes(ctx context.Context, target string, cfg Settings, limit int, schemes string) ([]Proxy, error) {
	if limit <= 0 {
		limit = 1
	}
	fetchLimit := maxInt(limit*8, 50)
	if fetchLimit > 300 {
		fetchLimit = 300
	}
	strategy := normalizeServeStrategy(cfg.ServeStrategy)
	targetCapability := targetCapabilityFromTarget(target)
	if strategy == "sequential" {
		candidates, err := p.store.SequentialCandidateProxies(ctx, fetchLimit, cfg.ServeAnonymity, cfg.ServeReachability, schemes, targetCapability)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 && targetCapability != "" {
			candidates, err = p.store.SequentialCandidateProxies(ctx, fetchLimit, cfg.ServeAnonymity, cfg.ServeReachability, schemes, "unknown")
			if err != nil {
				return nil, err
			}
		}
		if len(candidates) > limit {
			candidates = candidates[:limit]
		}
		return candidates, nil
	}
	candidates, err := p.store.CandidateProxiesForTargetCapability(ctx, fetchLimit, cfg.ServeAnonymity, cfg.ServeReachability, schemes, targetCapability)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 && targetCapability != "" {
		candidates, err = p.store.CandidateProxiesForTargetCapability(ctx, fetchLimit, cfg.ServeAnonymity, cfg.ServeReachability, schemes, "unknown")
		if err != nil {
			return nil, err
		}
	}
	return orderCandidateProxies(candidates, strategy, target, limit), nil
}

func (p *ProxyPool) Metrics() PoolMetrics {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PoolMetrics{
		TotalDial: p.totalDial,
		OKDial:    p.okDial,
		FailDial:  p.failDial,
		LastError: p.lastError,
		LastProxy: p.lastProxy,
	}
}

func (p *ProxyPool) recordDial(ok bool, used Proxy, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.totalDial++
	if ok {
		p.okDial++
		p.lastProxy = used.ID
		p.lastError = ""
		return
	}
	p.failDial++
	if err != nil {
		p.lastError = err.Error()
	}
}

func orderCandidateProxies(candidates []Proxy, strategy, target string, limit int) []Proxy {
	items := append([]Proxy(nil), candidates...)
	switch strategy {
	case "latency":
		sort.SliceStable(items, func(i, j int) bool {
			if statusRank(items[i]) != statusRank(items[j]) {
				return statusRank(items[i]) < statusRank(items[j])
			}
			li, lj := latencyRank(items[i]), latencyRank(items[j])
			if li != lj {
				return li < lj
			}
			return lastUsedBefore(items[i], items[j])
		})
	case "round_robin":
		sort.SliceStable(items, func(i, j int) bool {
			if statusRank(items[i]) != statusRank(items[j]) {
				return statusRank(items[i]) < statusRank(items[j])
			}
			return lastUsedBefore(items[i], items[j])
		})
	case "sticky":
		if target == "" {
			return orderCandidateProxies(items, "round_robin", target, limit)
		}
		sort.SliceStable(items, func(i, j int) bool {
			if statusRank(items[i]) != statusRank(items[j]) {
				return statusRank(items[i]) < statusRank(items[j])
			}
			return stickyScore(target, items[i].ID) < stickyScore(target, items[j].ID)
		})
	default:
		// sequential：保持 SQL 返回的 last_used 升序，严格依次轮询。
		sort.SliceStable(items, func(i, j int) bool {
			return lastUsedBefore(items[i], items[j])
		})
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func statusRank(p Proxy) int {
	if p.Status == "available" {
		return 0
	}
	if p.Status == "unchecked" {
		return 1
	}
	return 2
}

func latencyRank(p Proxy) int64 {
	if p.LatencyMS <= 0 {
		return 1<<62 - 1
	}
	return p.LatencyMS
}

func lastUsedBefore(a, b Proxy) bool {
	if a.LastUsed == nil && b.LastUsed == nil {
		return a.ID < b.ID
	}
	if a.LastUsed == nil {
		return true
	}
	if b.LastUsed == nil {
		return false
	}
	if a.LastUsed.Equal(*b.LastUsed) {
		return a.ID < b.ID
	}
	return a.LastUsed.Before(*b.LastUsed)
}

func CheckProxy(ctx context.Context, p Proxy, cfg Settings) (time.Duration, error) {
	result := CheckProxyReachability(ctx, p, cfg)
	if result.OK() {
		return result.Latency, nil
	}
	return 0, result.Error
}

func CheckProxyReachability(ctx context.Context, p Proxy, cfg Settings) ProxyCheckResult {
	var domestic, foreign urlCheckResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		domestic = checkProxyAnyURL(ctx, p, cfg, domesticHealthCheckURLs(cfg))
	}()
	go func() {
		defer wg.Done()
		foreign = checkProxyAnyURL(ctx, p, cfg, foreignHealthCheckURLs(cfg))
	}()
	wg.Wait()

	result := ProxyCheckResult{
		DomesticOK: domestic.err == nil,
		ForeignOK:  foreign.err == nil,
		HTTPOK:     domestic.httpOK || foreign.httpOK,
		HTTPSOK:    domestic.httpsOK || foreign.httpsOK,
	}
	result.Reachability = reachabilityFromBools(result.DomesticOK, result.ForeignOK)
	result.TargetCapability = targetCapabilityFromBools(result.HTTPOK, result.HTTPSOK)
	switch {
	case result.DomesticOK && result.ForeignOK:
		if domestic.latency <= foreign.latency {
			result.Latency = domestic.latency
		} else {
			result.Latency = foreign.latency
		}
	case result.DomesticOK:
		result.Latency = domestic.latency
	case result.ForeignOK:
		result.Latency = foreign.latency
	default:
		result.Error = combineCheckErrors(domestic.err, foreign.err)
	}
	return result
}

type urlCheckResult struct {
	latency time.Duration
	err     error
	httpOK  bool
	httpsOK bool
}

func checkProxyAnyURL(ctx context.Context, p Proxy, cfg Settings, urls []string) urlCheckResult {
	var result urlCheckResult
	var lastErr error
	needHTTP, needHTTPS := healthURLSchemes(urls)
	for _, healthURL := range urls {
		checkCtx, cancel := context.WithTimeout(ctx, time.Duration(maxInt(1, cfg.TimeoutSeconds)+1)*time.Second)
		latency, err := checkProxyURL(checkCtx, p, cfg, healthURL)
		cancel()
		if err == nil {
			if result.latency == 0 || latency < result.latency {
				result.latency = latency
			}
			switch healthURLScheme(healthURL) {
			case "http":
				result.httpOK = true
			case "https":
				result.httpsOK = true
			}
			if (!needHTTP || result.httpOK) && (!needHTTPS || result.httpsOK) {
				return result
			}
			continue
		}
		lastErr = err
	}
	if result.latency > 0 {
		return result
	}
	if lastErr == nil {
		lastErr = errors.New("no health check URL configured")
	}
	result.err = lastErr
	return result
}

func healthURLSchemes(urls []string) (bool, bool) {
	var needHTTP, needHTTPS bool
	for _, healthURL := range urls {
		switch healthURLScheme(healthURL) {
		case "http":
			needHTTP = true
		case "https":
			needHTTPS = true
		}
	}
	return needHTTP, needHTTPS
}

func healthURLScheme(healthURL string) string {
	u, err := url.Parse(healthURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Scheme)
}

func combineCheckErrors(domesticErr, foreignErr error) error {
	switch {
	case domesticErr != nil && foreignErr != nil:
		return errors.New("domestic check failed: " + domesticErr.Error() + "; foreign check failed: " + foreignErr.Error())
	case domesticErr != nil:
		return domesticErr
	case foreignErr != nil:
		return foreignErr
	default:
		return errors.New("proxy check failed")
	}
}

func checkProxyURL(ctx context.Context, p Proxy, cfg Settings, healthURL string) (time.Duration, error) {
	u, err := url.Parse(healthURL)
	if err != nil {
		return 0, err
	}
	if p.Scheme == "http" || p.Scheme == "https" {
		return checkHTTPProxyURL(ctx, p, cfg, healthURL)
	}
	port := "80"
	if u.Scheme == "https" {
		port = "443"
	}
	target := addDefaultPort(u.Host, port)
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	start := time.Now()
	conn, err := DialViaProxy(ctx, p, target, timeout)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if u.Scheme == "https" {
		tlsConf := tlsConfig(cfg)
		tlsConf.ServerName = u.Hostname()
		tlsConn := tls.Client(conn, tlsConf)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return 0, err
		}
		conn = tlsConn
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	req := "GET " + path + " HTTP/1.1\r\nHost: " + u.Host + "\r\nUser-Agent: proxypool-health/1.0\r\nConnection: close\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		return 0, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 500 {
		return 0, errors.New(resp.Status)
	}
	body, err := readCheckBody(resp.Body)
	if err != nil {
		return 0, err
	}
	if err := validateCheckBody(body, checkKeywords(cfg.CheckSuccessKeywords), checkKeywords(cfg.CheckBlockKeywords)); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

const checkBodyReadLimit = 256 * 1024

// checkKeywords splits the configured keyword text into lines; empty lines
// and # comments are ignored.
func checkKeywords(text string) []string {
	return splitImportLines(text)
}

// validateCheckBody applies the configured content signatures to a health
// check response body. successKeywords must ALL be present; any blockKeyword
// marks the response as unusable (hijacked portal pages, captive errors...).
func validateCheckBody(body string, successKeywords, blockKeywords []string) error {
	for _, keyword := range blockKeywords {
		if keyword != "" && strings.Contains(body, keyword) {
			return fmt.Errorf("响应内容命中禁止关键词: %q", keyword)
		}
	}
	for _, keyword := range successKeywords {
		if keyword != "" && !strings.Contains(body, keyword) {
			return fmt.Errorf("响应内容缺少必需关键词: %q", keyword)
		}
	}
	return nil
}

func readCheckBody(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, checkBodyReadLimit))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func checkHTTPProxyURL(ctx context.Context, p Proxy, cfg Settings, healthURL string) (time.Duration, error) {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	tr := &http.Transport{
		Proxy:                 http.ProxyURL(upstreamProxyURL(p)),
		TLSClientConfig:       tlsConfig(cfg),
		ResponseHeaderTimeout: timeout,
		TLSHandshakeTimeout:   timeout,
		IdleConnTimeout:       30 * time.Second,
	}
	client := http.Client{Transport: tr, Timeout: timeout + 5*time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "proxypool-health/1.0")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 500 {
		return 0, errors.New(resp.Status)
	}
	body, err := readCheckBody(resp.Body)
	if err != nil {
		return 0, err
	}
	if err := validateCheckBody(body, checkKeywords(cfg.CheckSuccessKeywords), checkKeywords(cfg.CheckBlockKeywords)); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

func tlsConfig(cfg Settings) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.TLSInsecureSkipVerify}
}

func healthCheckURLs(cfg Settings) []string {
	text := cfg.HealthURL
	if strings.TrimSpace(text) == "" {
		text = defaultSettings().HealthURL
	}
	var out []string
	for _, line := range splitImportLines(text) {
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		out = []string{"https://www.gstatic.com/generate_204"}
	}
	return out
}

func domesticHealthCheckURLs(cfg Settings) []string {
	return healthURLsFromText(cfg.DomesticHealthURL, defaultDomesticHealthURL)
}

func foreignHealthCheckURLs(cfg Settings) []string {
	text := cfg.ForeignHealthURL
	if strings.TrimSpace(text) == "" {
		text = cfg.HealthURL
	}
	return healthURLsFromText(text, defaultForeignHealthURL)
}

func healthURLsFromText(text, fallback string) []string {
	if strings.TrimSpace(text) == "" {
		text = fallback
	}
	var out []string
	for _, line := range splitImportLines(text) {
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			out = append(out, line)
		}
	}
	if len(out) == 0 && strings.TrimSpace(fallback) != strings.TrimSpace(text) {
		return healthURLsFromText(fallback, "")
	}
	return out
}

func proxyCheckTimeout(cfg Settings) time.Duration {
	attempts := maxInt(len(domesticHealthCheckURLs(cfg)), len(foreignHealthCheckURLs(cfg)))
	if attempts <= 0 {
		attempts = 1
	}
	return time.Duration((maxInt(1, cfg.TimeoutSeconds)+1)*attempts+2) * time.Second
}

func runHealthPass(ctx context.Context, store *Store, scope string) {
	_ = runHealthPassFiltered(ctx, store, scope, "")
}

func runHealthPassFiltered(ctx context.Context, store *Store, scope, schemes string) HealthPassResult {
	result := HealthPassResult{
		Scope:   normalizeCheckScope(scope),
		Schemes: normalizeCheckSchemes(schemes),
	}
	cfg, err := store.Settings(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	status := ""
	switch result.Scope {
	case "unavailable":
		status = "unavailable"
	case "unchecked":
		status = "unchecked"
	case "available":
		status = "available"
	}
	proxies, err := store.ListProxies(ctx, "", "", status)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	workers := maxInt(1, cfg.CheckConcurrency)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var checked atomic.Int64
	for _, p := range proxies {
		if !p.Enabled {
			continue
		}
		if !proxyMatchesCheckSchemes(p, result.Schemes) {
			continue
		}
		result.Candidates++
		p := p
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer checked.Add(1)
			checkCtx, cancel := context.WithTimeout(ctx, proxyCheckTimeout(cfg))
			checkResult := CheckProxyReachability(checkCtx, p, cfg)
			cancel()
			_ = store.MarkCheckResult(ctx, p.ID, checkResult, cfg)
			if checkResult.OK() {
				checkCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds+4)*time.Second)
				anonymity := DetectAnonymity(checkCtx, p, cfg)
				cancel()
				_ = store.MarkAnonymity(ctx, p.ID, anonymity)
			}
		}()
	}
	wg.Wait()
	result.Checked = int(checked.Load())
	return result
}
