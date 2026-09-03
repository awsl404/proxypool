package proxypool

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckProxyURLUsesHTTPProxyRequest(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer health.Close()

	seenAbsoluteURL := make(chan bool, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAbsoluteURL <- r.URL.IsAbs()
		outReq := r.Clone(r.Context())
		outReq.RequestURI = ""
		resp, err := http.DefaultTransport.RoundTrip(outReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxy.Close()

	proxyURL := mustParseProxyLine(t, proxy.URL)
	_, err := checkProxyURL(context.Background(), proxyURL, Settings{TimeoutSeconds: 3}, health.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !<-seenAbsoluteURL {
		t.Fatal("expected health check to use normal HTTP proxy request")
	}
}

func mustParseProxyLine(t *testing.T, line string) Proxy {
	t.Helper()
	p, err := parseProxyLine(line)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
