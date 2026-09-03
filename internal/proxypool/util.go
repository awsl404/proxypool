package proxypool

import (
	"crypto/rand"
	"encoding/hex"
	"hash/fnv"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func itoa(v int) string {
	return strconv.Itoa(v)
}

func randomToken() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "change-me-" + strconv.FormatInt(int64(len(b)), 10)
	}
	return hex.EncodeToString(b[:])
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampInt(v, minValue, maxValue int) int {
	if v < minValue {
		return minValue
	}
	if v > maxValue {
		return maxValue
	}
	return v
}

func normalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	switch pageSize {
	case 10, 20, 50, 100:
	default:
		pageSize = 20
	}
	return page, pageSize
}

func pageOffset(page, pageSize int) int {
	return (page - 1) * pageSize
}

func totalPages(total, pageSize int) int {
	if total <= 0 {
		return 1
	}
	return (total + pageSize - 1) / pageSize
}

func newPage[T any](items []T, page, pageSize, total int) Page[T] {
	page, pageSize = normalizePage(page, pageSize)
	return Page[T]{
		Items:      items,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages(total, pageSize),
	}
}

func parseBoolString(v string) bool {
	return strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "yes")
}

func normalizeCheckScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "unavailable", "unchecked", "available":
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return "all"
	}
}

func normalizeCheckSchemes(value string) string {
	raw := strings.ToLower(strings.TrimSpace(value))
	if raw == "" || raw == "all" || raw == "any" {
		return ""
	}
	return strings.Join(parseProtocolSchemes(raw), ",")
}

func proxyMatchesCheckSchemes(p Proxy, schemes string) bool {
	filter := normalizeCheckSchemes(schemes)
	if filter == "" {
		return true
	}
	for _, scheme := range strings.Split(filter, ",") {
		if p.Scheme == scheme {
			return true
		}
	}
	return false
}

func normalizeReachability(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "domestic", "cn", "china":
		return "domestic"
	case "foreign", "overseas", "global", "intl", "international":
		return "foreign"
	case "both", "all":
		return "both"
	case "none":
		return "none"
	default:
		return "unknown"
	}
}

func normalizeReachabilityFilter(v string) string {
	raw := strings.ToLower(strings.TrimSpace(v))
	if raw == "" || raw == "any" {
		return "any"
	}
	normalized := normalizeReachability(raw)
	switch normalized {
	case "domestic", "foreign", "both", "none", "unknown":
		return normalized
	default:
		return "any"
	}
}

func reachabilityFromBools(domesticOK, foreignOK bool) string {
	switch {
	case domesticOK && foreignOK:
		return "both"
	case domesticOK:
		return "domestic"
	case foreignOK:
		return "foreign"
	default:
		return "none"
	}
}

func normalizeTargetCapability(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "http", "http_only", "http-only":
		return "http"
	case "https", "tls", "ssl", "connect", "https_only", "https-only":
		return "https"
	case "both", "all":
		return "both"
	case "none":
		return "none"
	default:
		return "unknown"
	}
}

func normalizeTargetCapabilityFilter(v string) string {
	raw := strings.ToLower(strings.TrimSpace(v))
	if raw == "" || raw == "any" {
		return "any"
	}
	normalized := normalizeTargetCapability(raw)
	switch normalized {
	case "http", "https", "both", "none", "unknown":
		return normalized
	default:
		return "any"
	}
}

func targetCapabilityFromBools(httpOK, httpsOK bool) string {
	switch {
	case httpOK && httpsOK:
		return "both"
	case httpOK:
		return "http"
	case httpsOK:
		return "https"
	default:
		return "none"
	}
}

func targetCapabilityFromTarget(target string) string {
	raw := strings.TrimSpace(strings.ToLower(target))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "https://") {
		return "https"
	}
	if strings.HasPrefix(raw, "http://") {
		return "http"
	}
	host := target
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		host = u.Host
	}
	_, port, err := netSplitHostPort(host)
	if err != nil {
		return "http"
	}
	if port == "443" {
		return "https"
	}
	return "http"
}

func normalizeServeStrategy(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "round_robin", "round-robin", "rr":
		return "round_robin"
	case "weighted", "weight":
		return "weighted"
	case "latency", "fast":
		return "latency"
	case "sticky", "target_sticky":
		return "sticky"
	case "quality", "score", "balanced":
		return "quality"
	default:
		return "quality"
	}
}

func proxyQualityScore(p Proxy) int {
	if !p.Enabled {
		return 0
	}
	score := 45
	switch p.Status {
	case "available":
		score += 25
	case "unchecked":
		score += 5
	case "unavailable":
		score -= 35
	}
	attempts := p.SuccessCount + p.FailCount
	if attempts > 0 {
		score += int(float64(p.SuccessCount) / float64(attempts) * 20)
		score -= int(float64(p.FailCount) / float64(attempts) * 12)
	}
	score -= p.FailCount * 6
	if p.LatencyMS > 0 {
		switch {
		case p.LatencyMS <= 300:
			score += 18
		case p.LatencyMS <= 800:
			score += 12
		case p.LatencyMS <= 1500:
			score += 6
		case p.LatencyMS > 4000:
			score -= 10
		}
	}
	switch normalizeAnonymity(p.Anonymity) {
	case "anonymous":
		score += 5
	case "transparent":
		score -= 5
	}
	switch normalizeReachability(p.Reachability) {
	case "both":
		score += 8
	case "domestic", "foreign":
		score += 4
	case "none":
		score -= 25
	}
	switch normalizeTargetCapability(p.TargetCapability) {
	case "both":
		score += 8
	case "https":
		score += 5
	case "http":
		score += 3
	case "none":
		score -= 25
	}
	if p.LastChecked == nil {
		score -= 3
	} else if time.Since(*p.LastChecked) > 24*time.Hour {
		score -= 6
	}
	return clampInt(score, 0, 100)
}

func netSplitHostPort(hostport string) (string, string, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err == nil {
		return host, port, nil
	}
	if strings.Count(hostport, ":") == 1 {
		parts := strings.SplitN(hostport, ":", 2)
		if parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], nil
		}
	}
	return "", "", err
}

func stickyScore(target string, proxyID int64) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(target))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(strconv.FormatInt(proxyID, 10)))
	return h.Sum64()
}

func proxyURI(p Proxy) string {
	hostport := p.Host + ":" + itoa(p.Port)
	if p.Username == "" {
		return p.Scheme + "://" + hostport
	}
	auth := url.UserPassword(p.Username, p.Password).String()
	return p.Scheme + "://" + auth + "@" + hostport
}
