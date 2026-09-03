package proxypool

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxServeProxyAttempts = 3

func dialServingCandidate(ctx context.Context, pool *ProxyPool, target string, cfg Settings, candidates []Proxy, start int) (net.Conn, Proxy, int, error) {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	var lastErr error
	for i := start; i < len(candidates); i++ {
		candidate := candidates[i]
		conn, err := DialViaProxy(ctx, candidate, target, timeout)
		if err == nil {
			pool.store.MarkUsed(ctx, candidate.ID)
			pool.recordDial(true, candidate, nil)
			return conn, candidate, i + 1, nil
		}
		lastErr = err
		pool.recordDial(false, Proxy{}, err)
		_ = pool.store.MarkCheck(context.Background(), candidate.ID, false, 0, cfg)
	}
	if lastErr == nil {
		lastErr = errors.New("no available upstream proxies")
	}
	return nil, Proxy{}, len(candidates), lastErr
}

func markServingGatewayFailure(store *Store, p Proxy, cfg Settings) {
	if p.ID <= 0 {
		return
	}
	_ = store.MarkCheck(context.Background(), p.ID, false, 0, cfg)
}

func readInitialClientBytes(conn net.Conn, cfg Settings) ([]byte, error) {
	timeout := time.Duration(maxInt(1, cfg.TimeoutSeconds)) * time.Second
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})
	buf := make([]byte, 64*1024)
	n, err := conn.Read(buf)
	if n > 0 {
		return append([]byte(nil), buf[:n]...), nil
	}
	if isTimeoutErr(err) {
		return nil, nil
	}
	return nil, err
}

func replayableTunnelInitial(data []byte) bool {
	text := string(data)
	headerEnd := strings.Index(text, "\r\n\r\n")
	if headerEnd < 0 {
		return false
	}
	lineEnd := strings.Index(text, "\r\n")
	if lineEnd <= 0 {
		return false
	}
	fields := strings.Fields(text[:lineEnd])
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "HTTP/") {
		return false
	}
	if !isReplayableHTTPMethod(fields[0]) {
		return false
	}
	for _, line := range strings.Split(text[:headerEnd], "\r\n")[1:] {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "content-length":
			n, _ := strconv.Atoi(strings.TrimSpace(value))
			if n > 0 {
				return false
			}
		case "transfer-encoding":
			if strings.TrimSpace(value) != "" && !strings.EqualFold(strings.TrimSpace(value), "identity") {
				return false
			}
		}
	}
	return true
}

func isReplayableHTTPRequest(r *http.Request) bool {
	if r == nil || !isReplayableHTTPMethod(r.Method) {
		return false
	}
	if r.ContentLength > 0 || r.Body != nil && r.Body != http.NoBody {
		return false
	}
	return true
}

func isReplayableHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func writeInitialAndRelay(client, upstream net.Conn, initial []byte, cfg Settings) (bool, []byte, error) {
	if len(initial) > 0 {
		if err := writeAll(upstream, initial); err != nil {
			return false, nil, err
		}
	}
	return inspectUpstreamPreludeAndRelay(client, upstream, cfg)
}

func inspectUpstreamPreludeAndRelay(client, upstream net.Conn, cfg Settings) (bool, []byte, error) {
	timeout := time.Duration(maxInt(1, cfg.TimeoutSeconds)) * time.Second
	_ = upstream.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 64*1024)
	n, err := upstream.Read(buf)
	_ = upstream.SetReadDeadline(time.Time{})
	if n > 0 {
		prelude := append([]byte(nil), buf[:n]...)
		if isProxyGatewayHTTPPrelude(prelude) {
			return true, prelude, nil
		}
		if writeErr := writeAll(client, prelude); writeErr != nil {
			return false, prelude, writeErr
		}
		if err != nil && !errors.Is(err, io.EOF) && !isTimeoutErr(err) {
			return false, prelude, err
		}
		copyBoth(client, upstream)
		return false, prelude, nil
	}
	if err != nil && !isTimeoutErr(err) {
		return false, nil, err
	}
	copyBoth(client, upstream)
	return false, nil, nil
}

func isProxyGatewayHTTPPrelude(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	text := strings.ToLower(string(data))
	if !strings.HasPrefix(text, "http/") {
		return false
	}
	firstLine := text
	if idx := strings.Index(firstLine, "\r\n"); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	fields := strings.Fields(firstLine)
	code := 0
	if len(fields) >= 2 {
		code, _ = strconv.Atoi(fields[1])
	}
	if strings.Contains(text, "could not connect") {
		return true
	}
	if code >= 500 && (strings.Contains(text, "sentinelprotectionserver") || strings.Contains(text, "jetty/4.2.x")) {
		return true
	}
	if code == http.StatusNotImplemented && strings.Contains(firstLine, "gateway") {
		return true
	}
	if (code == http.StatusBadGateway || code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout) && strings.Contains(text, "gateway") {
		return true
	}
	return false
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	return false
}
