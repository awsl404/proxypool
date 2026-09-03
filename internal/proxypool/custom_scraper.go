package proxypool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultCustomExtractPattern = `(?:(?P<scheme>https?|socks5)://)?(?:(?P<user>[^:@\s/]+)(?::(?P<password>[^@\s/]*))?@)?(?P<host>(?:\d{1,3}\.){3}\d{1,3}|[A-Za-z0-9.-]+)\s*[:：]\s*(?P<port>\d{2,5})`

func CustomScraperSample() CustomScraperTemplate {
	return CustomScraperTemplate{
		Name:           "proxy.scdn.io 示例",
		Enabled:        true,
		PageMode:       "range",
		PageStart:      1,
		PageEnd:        100,
		PageSize:       100,
		MaxPages:       100,
		Concurrency:    1,
		RetryCount:     1,
		StopFailures:   1,
		DefaultScheme:  "socks5",
		ResponseType:   "text",
		URLTemplate:    "https://proxy.scdn.io/get_proxies.php?protocol=SOCKS5&country=&per_page={size}&page={page}",
		ExtractPattern: defaultCustomExtractPattern,
		SchemeGroup:    "scheme",
		HostGroup:      "host",
		PortGroup:      "port",
		UserGroup:      "user",
		PasswordGroup:  "password",
		Notes:          "使用通用正则从响应文本中提取 host:port。",
	}
}

func RunCustomScraper(ctx context.Context, store *Store, pool *ProxyPool, cfg Settings, req CustomScrapeRequest, loggers ...CollectorLogger) CollectorResult {
	tpl, err := store.GetCustomScraper(ctx, req.TemplateID)
	if err != nil {
		collectorLog(loggers, "读取模板失败：%v", err)
		return CollectorResult{ImportResult: ImportResult{Failed: 1, Errors: []string{err.Error()}}, SourceErrors: []string{}}
	}
	mode := normalizeCollectorMode(firstNonEmpty(req.Mode, cfg.CollectorMode))
	specific := firstNonEmpty(req.SpecificProxy, cfg.CollectorProxy)
	checkAfterImport := true
	if req.Check != nil {
		checkAfterImport = *req.Check
	}
	runCfg := cfg
	if tpl.RequestTimeout > 0 {
		runCfg.TimeoutSeconds = tpl.RequestTimeout
	}
	collectorLog(loggers, "自定义抓取配置：模板=%s，方法=%s，出口=%s，页码模式=%s，超时=%ds，并发=%d，重试=%d，失败停止=%d，导入后验证=%t", tpl.Name, tpl.RequestMethod, mode, tpl.PageMode, runCfg.TimeoutSeconds, tpl.Concurrency, tpl.RetryCount, tpl.StopFailures, checkAfterImport)
	client, err := collectorHTTPClient(ctx, pool, runCfg, mode, specific)
	if err != nil {
		collectorLog(loggers, "创建 HTTP 客户端失败：%v", err)
		return CollectorResult{ImportResult: ImportResult{Failed: 1, Errors: []string{err.Error()}}, SourceErrors: []string{}}
	}
	result := CollectorResult{ImportResult: ImportResult{Errors: []string{}}, SourceErrors: []string{}}
	pageRequests, firstBody, firstErr := resolveCustomScraperRequests(ctx, client, tpl, loggers...)
	if firstErr != nil {
		collectorLog(loggers, "解析抓取 URL 失败：%v", firstErr)
		result.SourcesFailed++
		result.SourceErrors = appendLimited(result.SourceErrors, firstErr.Error(), 30)
		_ = store.MarkCustomScraperRun(ctx, tpl.ID, firstErr.Error())
		return result
	}
	collectorLog(loggers, "待抓取页面数：%d", len(pageRequests))
	type pageJob struct {
		index int
		req   FetchRequest
	}
	jobs := make(chan pageJob, len(pageRequests))
	for i, pageReq := range pageRequests {
		jobs <- pageJob{index: i, req: pageReq}
	}
	close(jobs)
	var mu sync.Mutex
	failures := 0
	stopped := false
	workers := minInt(maxInt(1, tpl.Concurrency), len(pageRequests))
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				mu.Lock()
				shouldStop := stopped
				mu.Unlock()
				if shouldStop {
					continue
				}
				if err := ctx.Err(); err != nil {
					mu.Lock()
					stopped = true
					result.SourceErrors = appendLimited(result.SourceErrors, err.Error(), 30)
					mu.Unlock()
					collectorLog(loggers, "任务已停止：%v", err)
					continue
				}
				body, err := fetchCustomPage(ctx, client, job.req, job.index, len(pageRequests), firstBody, tpl.RetryCount, loggers...)
				if err != nil {
					collectorLog(loggers, "页面请求失败：%s -> %v", job.req.URL, err)
					_ = store.RecordSourceStat(ctx, job.req.URL, false, 0, 0, 0, err.Error())
					mu.Lock()
					result.SourcesFailed++
					failures++
					result.SourceErrors = appendLimited(result.SourceErrors, job.req.URL+": "+err.Error(), 30)
					if tpl.StopFailures > 0 && failures >= tpl.StopFailures {
						stopped = true
						result.SourceErrors = appendLimited(result.SourceErrors, fmt.Sprintf("页面失败达到阈值 %d，自动停止剩余页面", tpl.StopFailures), 30)
						collectorLog(loggers, "页面失败达到阈值 %d，自动停止剩余页面", tpl.StopFailures)
					}
					mu.Unlock()
					continue
				}
				proxies, err := ExtractCustomProxies(tpl, body)
				if err != nil {
					collectorLog(loggers, "提取正则失败：%v", err)
					mu.Lock()
					result.Failed++
					result.Errors = appendLimited(result.Errors, job.req.URL+": "+err.Error(), 50)
					mu.Unlock()
					continue
				}
				collectorLog(loggers, "提取到候选代理：%d 个", len(proxies))
				sourceImported, sourceDuplicates, sourceChecked, sourceFailed := 0, 0, 0, 0
				for _, p := range proxies {
					if err := ctx.Err(); err != nil {
						mu.Lock()
						stopped = true
						result.SourceErrors = appendLimited(result.SourceErrors, err.Error(), 30)
						mu.Unlock()
						collectorLog(loggers, "任务已停止，跳过剩余导入：%v", err)
						break
					}
					p.Source = job.req.URL
					p.Anonymity = "unknown"
					added, err := store.AddProxy(ctx, p)
					if err != nil {
						sourceFailed++
						mu.Lock()
						result.Failed++
						result.Errors = appendLimited(result.Errors, job.req.URL+": "+err.Error(), 50)
						mu.Unlock()
						continue
					}
					if !added {
						sourceDuplicates++
						mu.Lock()
						result.Duplicates++
						mu.Unlock()
						continue
					}
					sourceImported++
					mu.Lock()
					result.Imported++
					mu.Unlock()
					if checkAfterImport {
						id, err := store.ProxyID(ctx, p)
						if err == nil {
							checkCtx, cancel := context.WithTimeout(ctx, proxyCheckTimeout(runCfg))
							checkResult := CheckProxyReachability(checkCtx, p, runCfg)
							cancel()
							_ = store.MarkCheckResult(ctx, id, checkResult, runCfg)
							sourceChecked++
							mu.Lock()
							result.Checked++
							mu.Unlock()
						}
					}
				}
				_ = store.RecordSourceStat(ctx, job.req.URL, true, sourceImported, sourceDuplicates, sourceChecked, "")
				mu.Lock()
				result.SourcesFetched++
				mu.Unlock()
				collectorLog(loggers, "页面处理完成：导入=%d，重复=%d，验证=%d，失败=%d", sourceImported, sourceDuplicates, sourceChecked, sourceFailed)
			}
		}()
	}
	wg.Wait()
	summary := fmt.Sprintf("pages=%d imported=%d duplicates=%d failed=%d", result.SourcesFetched, result.Imported, result.Duplicates, result.Failed)
	_ = store.MarkCustomScraperRun(ctx, tpl.ID, summary)
	collectorLog(loggers, "任务汇总：%s", summary)
	return result
}

func PreviewCustomScraper(ctx context.Context, pool *ProxyPool, cfg Settings, tpl CustomScraperTemplate, mode, specific string) CustomScrapePreview {
	tpl = normalizeCustomScraper(tpl)
	preview := CustomScrapePreview{}
	if tpl.RequestTimeout > 0 {
		cfg.TimeoutSeconds = tpl.RequestTimeout
	}
	client, err := collectorHTTPClient(ctx, pool, cfg, normalizeCollectorMode(mode), specific)
	if err != nil {
		preview.Error = err.Error()
		return preview
	}
	requests, firstBody, err := resolveCustomScraperRequests(ctx, client, tpl)
	preview.URLs = fetchRequestURLs(requests)
	if err != nil {
		preview.Error = err.Error()
		return preview
	}
	if len(requests) == 0 {
		preview.Error = "没有可抓取 URL"
		return preview
	}
	body := firstBody
	if body == "" {
		fetched, fetchErr := fetchRequestDetailed(ctx, client, requests[0])
		if fetchErr == nil {
			body = fetched.Body
		}
		err = fetchErr
	}
	if err != nil {
		preview.Error = err.Error()
		return preview
	}
	matches, err := ExtractCustomProxies(tpl, body)
	if err != nil {
		preview.Error = err.Error()
		return preview
	}
	if len(matches) > 20 {
		matches = matches[:20]
	}
	preview.Matches = matches
	return preview
}

func fetchCustomPage(ctx context.Context, client *http.Client, spec FetchRequest, index, total int, firstBody string, retryCount int, loggers ...CollectorLogger) (string, error) {
	if index == 0 && firstBody != "" {
		collectorLog(loggers, "复用自动分页起始页响应：%s", spec.URL)
		return firstBody, nil
	}
	var lastErr error
	attempts := maxInt(1, retryCount+1)
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if attempts > 1 {
			collectorLog(loggers, "请求页面 %d/%d：%s（第 %d/%d 次）", index+1, total, fetchRequestSummary(spec), attempt, attempts)
		} else {
			collectorLog(loggers, "请求页面 %d/%d：%s", index+1, total, fetchRequestSummary(spec))
		}
		fetched, err := fetchRequestDetailed(ctx, client, spec)
		if err == nil {
			collectorLog(loggers, "页面响应：%s，大小=%d bytes", fetched.Status, fetched.Bytes)
			if preview := responsePreview(fetched.Body); preview != "" {
				collectorLog(loggers, "页面响应预览：%s", preview)
			}
			return fetched.Body, nil
		}
		lastErr = err
		if attempt < attempts {
			collectorLog(loggers, "页面请求失败，准备重试：%v", err)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
			}
		}
	}
	return "", lastErr
}

func ExtractCustomProxies(tpl CustomScraperTemplate, body string) ([]Proxy, error) {
	tpl = normalizeCustomScraper(tpl)
	if tpl.ResponseType == "json" {
		return extractCustomJSONProxies(tpl, body)
	}
	re, err := regexp.Compile(tpl.ExtractPattern)
	if err != nil {
		return nil, err
	}
	names := re.SubexpNames()
	var out []Proxy
	for _, match := range re.FindAllStringSubmatch(body, -1) {
		host := groupValue(match, names, tpl.HostGroup)
		portText := groupValue(match, names, tpl.PortGroup)
		if host == "" || portText == "" {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			continue
		}
		scheme := firstNonEmpty(groupValue(match, names, tpl.SchemeGroup), tpl.DefaultScheme)
		user := groupValue(match, names, tpl.UserGroup)
		pass := groupValue(match, names, tpl.PasswordGroup)
		out = append(out, proxyCandidatesFromParts(scheme, host, port, user, pass)...)
	}
	return out, nil
}

func extractCustomJSONProxies(tpl CustomScraperTemplate, body string) ([]Proxy, error) {
	var root any
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return nil, err
	}
	listValue, ok := jsonPathValue(root, tpl.JSONListPath)
	if !ok {
		return nil, fmt.Errorf("JSON 列表路径不存在: %s", tpl.JSONListPath)
	}
	items, ok := listValue.([]any)
	if !ok {
		return nil, fmt.Errorf("JSON 列表路径不是数组: %s", tpl.JSONListPath)
	}
	var out []Proxy
	for _, item := range items {
		if text, ok := jsonScalarString(item); ok {
			proxies, err := parseProxyLinesWithDefault(text, tpl.DefaultScheme)
			if err == nil {
				out = append(out, proxies...)
			}
			continue
		}
		if tpl.JSONProxyPath != "" {
			if v, ok := jsonPathValue(item, tpl.JSONProxyPath); ok {
				if text, ok := jsonScalarString(v); ok {
					proxies, err := parseProxyLinesWithDefault(text, tpl.DefaultScheme)
					if err == nil {
						out = append(out, proxies...)
						continue
					}
				}
			}
		}
		host := jsonPathString(item, tpl.JSONHostPath)
		portText := jsonPathString(item, tpl.JSONPortPath)
		if host == "" || portText == "" {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			continue
		}
		scheme := firstNonEmpty(jsonPathString(item, tpl.JSONSchemePath), tpl.DefaultScheme)
		user := jsonPathString(item, tpl.JSONUserPath)
		pass := jsonPathString(item, tpl.JSONPassPath)
		out = append(out, proxyCandidatesFromParts(scheme, host, port, user, pass)...)
	}
	return out, nil
}

func proxyCandidatesFromParts(scheme, host string, port int, user, pass string) []Proxy {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme == "" {
		scheme = "auto"
	}
	schemes := protocolSchemes(scheme)
	var out []Proxy
	for _, candidate := range schemes {
		p, err := normalizeProxy(Proxy{Scheme: candidate, Host: host, Port: port, Username: user, Password: pass, Tags: candidate})
		if err == nil {
			out = append(out, p)
		}
	}
	return out
}

func customScraperRequests(tpl CustomScraperTemplate) []FetchRequest {
	tpl = normalizeCustomScraper(tpl)
	headers := parseCustomHeaders(tpl.RequestHeaders)
	if !customScraperHasPagePlaceholder(tpl) {
		return []FetchRequest{{
			Method:  tpl.RequestMethod,
			URL:     applyCustomScraperPlaceholders(tpl.URLTemplate, tpl.PageStart, tpl.PageSize),
			Headers: headers,
			Body:    applyCustomScraperPlaceholders(tpl.RequestBody, tpl.PageStart, tpl.PageSize),
		}}
	}
	var requests []FetchRequest
	for page := tpl.PageStart; page <= tpl.PageEnd; page++ {
		u := applyCustomScraperPlaceholders(tpl.URLTemplate, page, tpl.PageSize)
		body := applyCustomScraperPlaceholders(tpl.RequestBody, page, tpl.PageSize)
		requests = append(requests, FetchRequest{Method: tpl.RequestMethod, URL: u, Headers: headers, Body: body})
	}
	return requests
}

func resolveCustomScraperRequests(ctx context.Context, client *http.Client, tpl CustomScraperTemplate, loggers ...CollectorLogger) ([]FetchRequest, string, error) {
	tpl = normalizeCustomScraper(tpl)
	firstRequest := FetchRequest{
		Method:  tpl.RequestMethod,
		URL:     applyCustomScraperPlaceholders(tpl.URLTemplate, tpl.PageStart, tpl.PageSize),
		Headers: parseCustomHeaders(tpl.RequestHeaders),
		Body:    applyCustomScraperPlaceholders(tpl.RequestBody, tpl.PageStart, tpl.PageSize),
	}
	if tpl.PageMode != "auto" {
		return customScraperRequests(tpl), "", nil
	}
	collectorLog(loggers, "自动识别全部页：请求起始页 %s", firstRequest.URL)
	body, err := fetchCustomPage(ctx, client, firstRequest, 0, 1, "", tpl.RetryCount, loggers...)
	if err != nil {
		return nil, "", err
	}
	maxPage := detectMaxPage(tpl, body)
	if maxPage < tpl.PageStart {
		maxPage = tpl.PageStart
	}
	limitEnd := tpl.PageStart + tpl.MaxPages - 1
	if maxPage > limitEnd {
		collectorLog(loggers, "自动页数超过最大页数限制：识别=%d，限制=%d", maxPage, limitEnd)
		maxPage = limitEnd
	}
	tpl.PageEnd = maxPage
	collectorLog(loggers, "自动识别页码范围：%d-%d", tpl.PageStart, tpl.PageEnd)
	return customScraperRequests(tpl), body, nil
}

func customScraperHasPagePlaceholder(tpl CustomScraperTemplate) bool {
	return strings.Contains(tpl.URLTemplate, "{page}") ||
		strings.Contains(tpl.RequestBody, "{page}")
}

func applyCustomScraperPlaceholders(value string, page, size int) string {
	value = strings.ReplaceAll(value, "{page}", strconv.Itoa(page))
	sizeText := ""
	if size > 0 {
		sizeText = strconv.Itoa(size)
	}
	return strings.ReplaceAll(value, "{size}", sizeText)
}

func fetchRequestURLs(requests []FetchRequest) []string {
	urls := make([]string, 0, len(requests))
	for _, req := range requests {
		urls = append(urls, req.URL)
	}
	return urls
}

func parseCustomHeaders(text string) map[string]string {
	headers := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var key, value string
		if left, right, ok := strings.Cut(line, ":"); ok {
			key, value = left, right
		} else if left, right, ok := strings.Cut(line, "="); ok {
			key, value = left, right
		} else {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" {
			headers[key] = value
		}
	}
	return headers
}

func detectMaxPage(tpl CustomScraperTemplate, body string) int {
	tpl = normalizeCustomScraper(tpl)
	if tpl.PagePattern != "" {
		if max := maxPageByPattern(tpl.PagePattern, tpl.PageGroup, body); max > 0 {
			return max
		}
	}
	patterns := []string{
		`[?&]page=(?P<page>\d+)`,
		`/page/(?P<page>\d+)`,
		`page[=/](?P<page>\d+)`,
		`第\s*(?P<page>\d+)\s*页`,
	}
	max := 0
	for _, pattern := range patterns {
		if n := maxPageByPattern(pattern, "page", body); n > max {
			max = n
		}
	}
	if max == 0 {
		max = tpl.PageStart
	}
	return max
}

func maxPageByPattern(pattern, group, body string) int {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return 0
	}
	names := re.SubexpNames()
	max := 0
	for _, match := range re.FindAllStringSubmatch(body, -1) {
		value := groupValue(match, names, group)
		if value == "" && len(match) > 1 {
			value = match[1]
		}
		n, err := strconv.Atoi(value)
		if err == nil && n > max {
			max = n
		}
	}
	return max
}

func jsonPathValue(root any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "$.")
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return root, true
	}
	current := root
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[part]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(typed) {
				return nil, false
			}
			current = typed[idx]
		default:
			return nil, false
		}
	}
	return current, true
}

func jsonPathString(root any, path string) string {
	if path == "" {
		return ""
	}
	value, ok := jsonPathValue(root, path)
	if !ok {
		return ""
	}
	text, _ := jsonScalarString(value)
	return strings.TrimSpace(text)
}

func jsonScalarString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10), true
		}
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(typed), true
	case nil:
		return "", false
	default:
		return "", false
	}
}

func groupValue(match, names []string, group string) string {
	if group == "" {
		return ""
	}
	if idx, err := strconv.Atoi(group); err == nil {
		if idx >= 0 && idx < len(match) {
			return strings.TrimSpace(match[idx])
		}
		return ""
	}
	for i, name := range names {
		if name == group && i < len(match) {
			return strings.TrimSpace(match[i])
		}
	}
	return ""
}

func appendLimited(items []string, item string, limit int) []string {
	if len(items) >= limit {
		return items
	}
	return append(items, item)
}
