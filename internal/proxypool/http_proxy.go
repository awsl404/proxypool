package proxypool

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

type HTTPProxy struct {
	store *Store
	pool  *ProxyPool
}

func (h *HTTPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.store.Settings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !proxyAccessAllowed(r.RemoteAddr, cfg) {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	if !proxyRequestAllowed(r, cfg) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="proxypool"`)
		w.Header().Add("Proxy-Authenticate", `Bearer realm="proxypool"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	target, err := targetFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodConnect {
		if err := h.handlePlainHTTPWithRetry(w, r, target, cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(cfg.TimeoutSeconds)*time.Second)
	upstream, _, err := h.pool.DialForSchemes(ctx, target, cfg, "http,https")
	cancel()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	h.handleConnect(w, r, upstream)
}

func (h *HTTPProxy) handleConnect(w http.ResponseWriter, r *http.Request, upstream net.Conn) {
	defer upstream.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	copyBoth(client, upstream)
}

func (h *HTTPProxy) handlePlainHTTP(w http.ResponseWriter, r *http.Request, upstream net.Conn) error {
	defer upstream.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return errors.New("hijacking not supported")
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return err
	}
	defer client.Close()
	cleanProxyRequest(r)
	if err := r.Write(upstream); err != nil {
		return err
	}
	_, _ = io.Copy(client, upstream)
	return nil
}

func (h *HTTPProxy) handlePlainHTTPWithRetry(w http.ResponseWriter, r *http.Request, target string, cfg Settings) error {
	replayable := isReplayableHTTPRequest(r)
	limit := maxServeProxyAttempts
	if !replayable {
		limit = 1
	}
	candidates, err := h.pool.CandidatesForSchemes(r.Context(), target, cfg, limit, "http,https")
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return errors.New("no available upstream proxies")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return errors.New("hijacking not supported")
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return err
	}
	defer client.Close()
	cleanProxyRequest(r)
	next := 0
	for attempt := 0; attempt < limit && next < len(candidates); attempt++ {
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(cfg.TimeoutSeconds)*time.Second)
		upstream, used, newNext, err := dialServingCandidate(ctx, h.pool, target, cfg, candidates, next)
		cancel()
		next = newNext
		if err != nil {
			_, _ = io.WriteString(client, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
			return nil
		}
		if err := r.Write(upstream); err != nil {
			_ = upstream.Close()
			markServingGatewayFailure(h.store, used, cfg)
			if replayable && attempt+1 < limit && next < len(candidates) {
				continue
			}
			_, _ = io.WriteString(client, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
			return nil
		}
		gatewayFailure, _, err := inspectUpstreamPreludeAndRelay(client, upstream, cfg)
		_ = upstream.Close()
		if gatewayFailure && replayable && attempt+1 < limit && next < len(candidates) {
			markServingGatewayFailure(h.store, used, cfg)
			continue
		}
		if gatewayFailure {
			markServingGatewayFailure(h.store, used, cfg)
			_, _ = io.WriteString(client, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
		}
		_ = err
		return nil
	}
	return nil
}

func cleanProxyRequest(r *http.Request) {
	r.RequestURI = ""
	r.Header.Del("Proxy-Authorization")
	r.Header.Del("Proxy-Connection")
	r.Header.Del("Connection")
	if r.URL != nil && r.URL.IsAbs() {
		r.URL.Scheme = ""
		r.URL.Host = ""
	}
}

func copyBoth(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go proxyCopy(a, b, done)
	go proxyCopy(b, a, done)
	<-done
	_ = a.SetDeadline(time.Now())
	_ = b.SetDeadline(time.Now())
	<-done
}

func proxyCopy(dst, src net.Conn, done chan<- struct{}) {
	_, _ = io.Copy(dst, src)
	if tcp, ok := dst.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
	done <- struct{}{}
}

func StartHTTPProxy(ctx context.Context, addr string, store *Store, pool *ProxyPool) *http.Server {
	srv := &http.Server{
		Addr:              addr,
		Handler:           &HTTPProxy{store: store, pool: pool},
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	go func() {
		appInfo("HTTP proxy listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			appError("HTTP proxy stopped: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	return srv
}

func isLocalAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1" || strings.HasPrefix(host, "[::1]")
}

func proxyRequestAllowed(r *http.Request, cfg Settings) bool {
	if cfg.BindLocalOnly && isRemoteLocal(r.RemoteAddr) {
		return true
	}
	auth := r.Header.Get("Proxy-Authorization")
	if proxyBasicAuthAllowed(auth, cfg) {
		return true
	}
	if auth == "Bearer "+cfg.APIToken {
		return true
	}
	return false
}

func proxyBasicAuthAllowed(auth string, cfg Settings) bool {
	if cfg.ProxyUsername == "" && cfg.ProxyPassword == "" {
		return false
	}
	if !strings.HasPrefix(auth, "Basic ") {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(auth, "Basic ")))
	if err != nil {
		return false
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok {
		return false
	}
	return user == cfg.ProxyUsername && pass == cfg.ProxyPassword
}

func isRemoteLocal(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
