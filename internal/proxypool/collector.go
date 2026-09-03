package proxypool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type CollectorRequest struct {
	Mode          string `json:"mode"`
	SpecificProxy string `json:"specific_proxy"`
	Sources       string `json:"sources"`
	Check         *bool  `json:"check"`
}

type CollectorResult struct {
	ImportResult
	SourcesFetched int      `json:"sources_fetched"`
	SourcesFailed  int      `json:"sources_failed"`
	SourceErrors   []string `json:"source_errors"`
}

type collectorSource struct {
	Scheme string
	URL    string
}

type collectorProxyCheck struct {
	ID    int64
	Proxy Proxy
}

type CollectorLogger func(format string, args ...any)

type FetchResponse struct {
	Body       string
	Status     string
	StatusCode int
	Bytes      int
}

type FetchRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}

func defaultCollectorSources() string {
	return strings.Join([]string{
		"http|https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/http.txt",
		"socks5|https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/socks5.txt",
		"http|https://raw.githubusercontent.com/monosans/proxy-list/main/proxies/http.txt",
		"https|https://raw.githubusercontent.com/monosans/proxy-list/main/proxies/https.txt",
		"socks5|https://raw.githubusercontent.com/monosans/proxy-list/main/proxies/socks5.txt",
		"http|https://raw.githubusercontent.com/proxifly/free-proxy-list/main/proxies/protocols/http/data.txt",
		"https|https://raw.githubusercontent.com/proxifly/free-proxy-list/main/proxies/protocols/https/data.txt",
		"socks5|https://raw.githubusercontent.com/proxifly/free-proxy-list/main/proxies/protocols/socks5/data.txt",
	}, "\n")
}

func CollectFreeProxies(ctx context.Context, store *Store, pool *ProxyPool, cfg Settings, req CollectorRequest, loggers ...CollectorLogger) CollectorResult {
	mode := normalizeCollectorMode(firstNonEmpty(req.Mode, cfg.CollectorMode))
	specific := firstNonEmpty(req.SpecificProxy, cfg.CollectorProxy)
	sourceText := firstNonEmpty(req.Sources, cfg.CollectorSources)
	checkAfterImport := true
	if req.Check != nil {
		checkAfterImport = *req.Check
	}
	sources := parseCollectorSources(sourceText)
	collectorLog(loggers, "采集配置：出口=%s，源数量=%d，导入后验证=%t", mode, len(sources), checkAfterImport)
	client, err := collectorHTTPClient(ctx, pool, cfg, mode, specific)
	if err != nil {
		collectorLog(loggers, "创建 HTTP 客户端失败：%v", err)
		return CollectorResult{ImportResult: ImportResult{Failed: 1, Errors: []string{err.Error()}}, SourceErrors: []string{}}
	}
	result := CollectorResult{ImportResult: ImportResult{Errors: []string{}}, SourceErrors: []string{}}
	for i, source := range sources {
		if err := ctx.Err(); err != nil {
			collectorLog(loggers, "任务已停止：%v", err)
			result.SourceErrors = appendLimited(result.SourceErrors, err.Error(), 30)
			break
		}
		collectorLog(loggers, "请求源 %d/%d：%s（默认协议=%s）", i+1, len(sources), source.URL, source.Scheme)
		fetched, err := fetchSourceDetailed(ctx, client, source.URL)
		if err != nil {
			collectorLog(loggers, "请求失败：%s -> %v", source.URL, err)
			result.SourcesFailed++
			_ = store.RecordSourceStat(ctx, source.URL, false, 0, 0, 0, err.Error())
			result.SourceErrors = appendLimited(result.SourceErrors, source.URL+": "+err.Error(), 30)
			continue
		}
		collectorLog(loggers, "响应完成：%s，大小=%d bytes", fetched.Status, fetched.Bytes)
		if preview := responsePreview(fetched.Body); preview != "" {
			collectorLog(loggers, "响应预览：%s", preview)
		}
		result.SourcesFetched++
		sourceImported := 0
		sourceDuplicates := 0
		sourceChecked := 0
		sourceSkipped := 0
		toCheck := []collectorProxyCheck{}
		lines := splitImportLines(fetched.Body)
		collectorLog(loggers, "解析候选代理：%d 行", len(lines))
		stopped := false
		for lineIndex, line := range lines {
			if err := ctx.Err(); err != nil {
				collectorLog(loggers, "任务已停止，跳过剩余导入：%v", err)
				result.SourceErrors = appendLimited(result.SourceErrors, err.Error(), 30)
				stopped = true
				break
			}
			proxies, err := parseProxyLinesWithDefault(line, source.Scheme)
			if err != nil {
				result.Failed++
				result.Errors = appendLimited(result.Errors, source.URL+": "+line+": "+err.Error(), 50)
				continue
			}
			for _, p := range proxies {
				p.Source = source.URL
				p.Anonymity = "unknown"
				skip, dead, err := store.ShouldSkipDeadProxy(ctx, p, cfg.FailThreshold)
				if err != nil {
					result.Failed++
					result.Errors = appendLimited(result.Errors, source.URL+": "+line+": "+err.Error(), 50)
					continue
				}
				if skip {
					result.Skipped++
					sourceSkipped++
					if sourceSkipped <= 3 {
						collectorLog(loggers, "跳过失效档案命中：%s://%s:%d（失败=%d）", dead.Scheme, dead.Host, dead.Port, dead.FailCount)
					}
					continue
				}
				added, err := store.AddProxy(ctx, p)
				if err != nil {
					result.Failed++
					result.Errors = appendLimited(result.Errors, source.URL+": "+line+": "+err.Error(), 50)
					continue
				}
				if !added {
					result.Duplicates++
					sourceDuplicates++
					continue
				}
				result.Imported++
				sourceImported++
				if checkAfterImport {
					id, err := store.ProxyID(ctx, p)
					if err == nil {
						toCheck = append(toCheck, collectorProxyCheck{ID: id, Proxy: p})
					}
				}
			}
			if (lineIndex+1)%200 == 0 || lineIndex+1 == len(lines) {
				collectorLog(loggers, "导入进度：%d/%d，导入=%d，重复=%d，跳过=%d，累计失败=%d", lineIndex+1, len(lines), sourceImported, sourceDuplicates, sourceSkipped, result.Failed)
			}
		}
		if checkAfterImport && len(toCheck) > 0 && !stopped {
			sourceChecked = validateCollectorProxies(ctx, store, cfg, toCheck, loggers)
			result.Checked += sourceChecked
		}
		_ = store.RecordSourceStat(ctx, source.URL, true, sourceImported, sourceDuplicates, sourceChecked, "")
		collectorLog(loggers, "源处理完成：导入=%d，重复=%d，跳过=%d，验证=%d，累计失败=%d", sourceImported, sourceDuplicates, sourceSkipped, sourceChecked, result.Failed)
	}
	collectorLog(loggers, "任务汇总：源成功=%d，源失败=%d，导入=%d，重复=%d，跳过=%d，验证=%d，失败=%d", result.SourcesFetched, result.SourcesFailed, result.Imported, result.Duplicates, result.Skipped, result.Checked, result.Failed)
	return result
}

func validateCollectorProxies(ctx context.Context, store *Store, cfg Settings, items []collectorProxyCheck, loggers []CollectorLogger) int {
	total := len(items)
	if total == 0 {
		return 0
	}
	workers := maxInt(1, cfg.CheckConcurrency)
	if workers > total {
		workers = total
	}
	collectorLog(loggers, "开始并发验证：%d 个，并发=%d", total, workers)
	jobs := make(chan collectorProxyCheck)
	var wg sync.WaitGroup
	var mu sync.Mutex
	checked := 0
	okCount := 0
	failCount := 0
	logProgress := func() {
		collectorLog(loggers, "验证进度：%d/%d，成功=%d，失败=%d", checked, total, okCount, failCount)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					return
				}
				checkCtx, cancel := context.WithTimeout(ctx, proxyCheckTimeout(cfg))
				checkResult := CheckProxyReachability(checkCtx, item.Proxy, cfg)
				cancel()
				_ = store.MarkCheckResult(ctx, item.ID, checkResult, cfg)
				if checkResult.OK() {
					anonCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds+4)*time.Second)
					anon := DetectAnonymity(anonCtx, item.Proxy, cfg)
					cancel()
					_ = store.MarkAnonymity(ctx, item.ID, anon)
				}
				mu.Lock()
				checked++
				if checkResult.OK() {
					okCount++
				} else {
					failCount++
				}
				shouldLog := checked == total || checked%25 == 0
				mu.Unlock()
				if shouldLog {
					mu.Lock()
					logProgress()
					mu.Unlock()
				}
			}
		}()
	}
sendLoop:
	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		select {
		case jobs <- item:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		collectorLog(loggers, "验证已停止：%v", err)
	}
	return checked
}

func parseCollectorSources(text string) []collectorSource {
	var out []collectorSource
	for _, line := range splitImportLines(text) {
		scheme := inferSourceScheme(line)
		sourceURL := line
		if left, right, ok := strings.Cut(line, "|"); ok && validCollectorSourceScheme(strings.TrimSpace(left)) {
			scheme = strings.ToLower(strings.TrimSpace(left))
			sourceURL = strings.TrimSpace(right)
		}
		if sourceURL != "" {
			out = append(out, collectorSource{Scheme: scheme, URL: sourceURL})
		}
	}
	return out
}

func validCollectorSourceScheme(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return validProtocolSpec(s)
}

func parseSourceProxyLine(line, defaultScheme string) (Proxy, error) {
	if strings.Contains(line, "://") || strings.Contains(line, ",") {
		return parseProxyLine(line)
	}
	return parseProxyLine(defaultScheme + " " + line)
}

func inferSourceScheme(source string) string {
	lower := strings.ToLower(source)
	if u, err := url.Parse(source); err == nil {
		lower = strings.ToLower(u.Path)
	}
	switch {
	case strings.Contains(lower, "socks5"):
		return "socks5"
	case strings.Contains(lower, "https"):
		return "https"
	default:
		return "http"
	}
}

func fetchSource(ctx context.Context, client *http.Client, sourceURL string) (string, error) {
	fetched, err := fetchSourceDetailed(ctx, client, sourceURL)
	if err != nil {
		return "", err
	}
	return fetched.Body, nil
}

func fetchSourceDetailed(ctx context.Context, client *http.Client, sourceURL string) (FetchResponse, error) {
	return fetchRequestDetailed(ctx, client, FetchRequest{Method: http.MethodGet, URL: sourceURL})
}

func fetchRequestDetailed(ctx context.Context, client *http.Client, spec FetchRequest) (FetchResponse, error) {
	method := strings.ToUpper(strings.TrimSpace(spec.Method))
	if method == "" {
		method = http.MethodGet
	}
	var requestBody io.Reader
	if spec.Body != "" {
		requestBody = strings.NewReader(spec.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, spec.URL, requestBody)
	if err != nil {
		return FetchResponse{}, err
	}
	req.Header.Set("User-Agent", "proxypool-collector/1.0")
	for key, value := range spec.Headers {
		if strings.TrimSpace(key) != "" {
			req.Header.Set(strings.TrimSpace(key), value)
		}
	}
	if spec.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return FetchResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return FetchResponse{Status: resp.Status, StatusCode: resp.StatusCode}, errors.New(resp.Status)
	}
	var b strings.Builder
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 8<<20))
	for scanner.Scan() {
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
	}
	responseBody := b.String()
	return FetchResponse{Body: responseBody, Status: resp.Status, StatusCode: resp.StatusCode, Bytes: len(responseBody)}, scanner.Err()
}

func fetchRequestSummary(spec FetchRequest) string {
	keys := make([]string, 0, len(spec.Headers))
	for key := range spec.Headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	bodyInfo := ""
	if spec.Body != "" {
		bodyInfo = fmt.Sprintf("，body=%d bytes", len(spec.Body))
	}
	if len(keys) == 0 {
		return fmt.Sprintf("%s %s%s", spec.Method, spec.URL, bodyInfo)
	}
	return fmt.Sprintf("%s %s，headers=%s%s", spec.Method, spec.URL, strings.Join(keys, ","), bodyInfo)
}

func collectorHTTPClient(ctx context.Context, pool *ProxyPool, cfg Settings, mode, specific string) (*http.Client, error) {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	dialer := &net.Dialer{Timeout: timeout}
	tr := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       tlsConfig(cfg),
		ResponseHeaderTimeout: timeout,
		TLSHandshakeTimeout:   timeout,
		IdleConnTimeout:       30 * time.Second,
	}
	switch mode {
	case "direct":
	case "pool":
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if shouldBypassCollectorProxy(addr) {
				return dialer.DialContext(ctx, network, addr)
			}
			conn, _, err := pool.Dial(ctx, addr, cfg)
			return conn, err
		}
	case "specific":
		p, err := parseProxyLine(specific)
		if err != nil {
			return nil, err
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if shouldBypassCollectorProxy(addr) {
				return dialer.DialContext(ctx, network, addr)
			}
			return DialViaProxy(ctx, p, addr, timeout)
		}
	default:
		return nil, errors.New("invalid collector mode")
	}
	_ = ctx
	return &http.Client{Transport: tr, Timeout: timeout + 5*time.Second}, nil
}

func shouldBypassCollectorProxy(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func normalizeCollectorMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "pool":
		return "pool"
	case "specific":
		return "specific"
	default:
		return "direct"
	}
}

func collectorLog(loggers []CollectorLogger, format string, args ...any) {
	if len(loggers) == 0 || loggers[0] == nil {
		return
	}
	loggers[0](fmt.Sprintf(format, args...))
}

func responsePreview(body string) string {
	text := strings.Join(strings.Fields(body), " ")
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 240 {
		return string(runes[:240]) + "..."
	}
	return text
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func sourceHost(sourceURL string) string {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return sourceURL
	}
	return u.Host
}
