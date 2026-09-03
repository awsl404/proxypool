package proxypool

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type identityResponse struct {
	Origin    string            `json:"origin"`
	Headers   map[string]string `json:"headers"`
	ProxyType string
}

var ipLikePattern = regexp.MustCompile(`(?i)(?:\d{1,3}\.){3}\d{1,3}|[0-9a-f]{0,4}:[0-9a-f:.]+`)

func DetectAnonymity(ctx context.Context, p Proxy, cfg Settings) string {
	testURL := cfg.AnonymityURL
	if testURL == "" {
		testURL = defaultSettings().AnonymityURL
	}
	direct, _ := fetchIdentity(ctx, nil, testURL, cfg)
	viaProxy, err := fetchIdentity(ctx, &p, testURL, cfg)
	if err != nil {
		return "unknown"
	}
	switch viaProxy.ProxyType {
	case "transparent":
		return "transparent"
	case "anonymous":
		return "anonymous"
	}
	if hasForwardingHeaders(viaProxy.Headers) {
		return "transparent"
	}
	if direct.Origin != "" && originContains(viaProxy.Origin, direct.Origin) {
		return "transparent"
	}
	if viaProxy.Origin == "" {
		return "unknown"
	}
	return "anonymous"
}

func fetchIdentity(ctx context.Context, p *Proxy, testURL string, cfg Settings) (identityResponse, error) {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	tr := &http.Transport{
		TLSClientConfig:       tlsConfig(cfg),
		ResponseHeaderTimeout: timeout,
		TLSHandshakeTimeout:   timeout,
		IdleConnTimeout:       30 * time.Second,
	}
	if p != nil {
		if p.Scheme == "http" || p.Scheme == "https" {
			tr.Proxy = http.ProxyURL(upstreamProxyURL(*p))
		} else {
			tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return DialViaProxy(ctx, *p, addr, timeout)
			}
		}
	}
	client := http.Client{Transport: tr, Timeout: timeout + 5*time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return identityResponse{}, err
	}
	req.Header.Set("User-Agent", "proxypool-anonymity/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return identityResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return identityResponse{}, err
	}
	var parsed identityResponse
	if err := json.Unmarshal(body, &parsed); err == nil && (parsed.Origin != "" || len(parsed.Headers) > 0) {
		return parsed, nil
	}
	return parseIdentityText(string(body)), nil
}

func parseIdentityText(text string) identityResponse {
	resp := identityResponse{Headers: map[string]string{}}
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			key, value, ok = strings.Cut(line, "：")
		}
		if ok {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			lower := strings.ToLower(key)
			switch {
			case strings.Contains(key, "IP") || strings.Contains(key, "ip"):
				if ip := firstIPLike(value); ip != "" {
					resp.Origin = ip
				}
			case strings.Contains(key, "代理类型"):
				resp.ProxyType = normalizeIdentityProxyType(value)
			default:
				resp.Headers[key] = value
			}
			if isForwardingHeader(lower) {
				resp.Headers[key] = value
			}
			continue
		}
		if resp.Origin == "" {
			resp.Origin = firstIPLike(line)
		}
	}
	if resp.Origin == "" {
		resp.Origin = strings.TrimSpace(text)
	}
	return resp
}

func hasForwardingHeaders(headers map[string]string) bool {
	for k, v := range headers {
		key := strings.ToLower(k)
		if v == "" {
			continue
		}
		if isForwardingHeader(key) {
			return true
		}
	}
	return false
}

func isForwardingHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "x-forwarded-for", "forwarded", "via", "x-real-ip", "x-original-forwarded-for", "cf-connecting-ip", "true-client-ip", "proxy-connection":
		return true
	default:
		return false
	}
}

func normalizeIdentityProxyType(value string) string {
	switch {
	case strings.Contains(value, "透明") || strings.Contains(strings.ToLower(value), "transparent"):
		return "transparent"
	case strings.Contains(value, "匿名") || strings.Contains(strings.ToLower(value), "anonymous"):
		return "anonymous"
	default:
		return ""
	}
}

func firstIPLike(value string) string {
	match := ipLikePattern.FindString(value)
	if match == "" {
		return ""
	}
	if ip := net.ParseIP(match); ip != nil {
		return ip.String()
	}
	return match
}

func originContains(origin, ip string) bool {
	origin = strings.ReplaceAll(origin, " ", "")
	for _, part := range strings.Split(origin, ",") {
		if part == ip {
			return true
		}
	}
	return origin == ip
}

func normalizeAnonymity(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "anonymous", "transparent":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "unknown"
	}
}

func normalizeAnonymityFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "anonymous", "transparent", "unknown":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "any"
	}
}
