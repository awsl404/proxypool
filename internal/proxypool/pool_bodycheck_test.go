package proxypool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateCheckBody(t *testing.T) {
	cases := []struct {
		name            string
		body            string
		successKeywords []string
		blockKeywords   []string
		wantErr         bool
		wantContains    string
	}{
		{
			name:            "no keywords configured always passes",
			body:            "anything at all",
			successKeywords: nil,
			blockKeywords:   nil,
			wantErr:         false,
		},
		{
			name:            "all success keywords present",
			body:            `{"origin": "1.2.3.4", "url": "https://httpbin.org/get"}`,
			successKeywords: []string{"origin", "httpbin.org"},
			blockKeywords:   nil,
			wantErr:         false,
		},
		{
			name:            "missing one success keyword fails",
			body:            `{"origin": "1.2.3.4"}`,
			successKeywords: []string{"origin", "httpbin.org"},
			blockKeywords:   nil,
			wantErr:         true,
			wantContains:    "缺少必需关键词",
		},
		{
			name:            "block keyword hit fails",
			body:            "<html>请登录后继续使用</html>",
			successKeywords: nil,
			blockKeywords:   []string{"请登录", "流量已用尽"},
			wantErr:         true,
			wantContains:    "命中禁止关键词",
		},
		{
			name:            "block keyword absent passes",
			body:            "<html>normal content</html>",
			successKeywords: nil,
			blockKeywords:   []string{"请登录"},
			wantErr:         false,
		},
		{
			name:            "block takes precedence over success",
			body:            "origin=1.2.3.4 请登录",
			successKeywords: []string{"origin"},
			blockKeywords:   []string{"请登录"},
			wantErr:         true,
			wantContains:    "命中禁止关键词",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCheckBody(tc.body, tc.successKeywords, tc.blockKeywords)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr && tc.wantContains != "" && !strings.Contains(err.Error(), tc.wantContains) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantContains)
			}
		})
	}
}

func TestCheckKeywordsSkipsCommentsAndBlanks(t *testing.T) {
	got := checkKeywords("# comment\n\nip\n  origin  \n")
	if len(got) != 2 || got[0] != "ip" || got[1] != "origin" {
		t.Fatalf("unexpected keywords: %#v", got)
	}
}

func TestCheckProxyURLValidatesBody(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"origin": "203.0.113.7"}`))
	}))
	defer health.Close()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outReq := r.Clone(r.Context())
		outReq.RequestURI = ""
		resp, err := http.DefaultTransport.RoundTrip(outReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write([]byte(`{"origin": "203.0.113.7"}`))
	}))
	defer proxy.Close()

	proxyLine := mustParseProxyLine(t, proxy.URL)
	ctx := context.Background()

	// 无关键词配置：保持原有行为，成功。
	if _, err := checkProxyURL(ctx, proxyLine, Settings{TimeoutSeconds: 3}, health.URL); err != nil {
		t.Fatalf("plain check should pass: %v", err)
	}

	// 必须命中关键词全部存在：成功。
	cfg := Settings{TimeoutSeconds: 3, CheckSuccessKeywords: "origin"}
	if _, err := checkProxyURL(ctx, proxyLine, cfg, health.URL); err != nil {
		t.Fatalf("success keyword present should pass: %v", err)
	}

	// 缺少必须命中的关键词：失败。
	cfg = Settings{TimeoutSeconds: 3, CheckSuccessKeywords: "definitely-not-in-body"}
	if _, err := checkProxyURL(ctx, proxyLine, cfg, health.URL); err == nil {
		t.Fatal("missing success keyword should fail")
	} else if !strings.Contains(err.Error(), "缺少必需关键词") {
		t.Fatalf("unexpected error: %v", err)
	}

	// 命中禁止关键词：失败。
	cfg = Settings{TimeoutSeconds: 3, CheckBlockKeywords: "origin"}
	if _, err := checkProxyURL(ctx, proxyLine, cfg, health.URL); err == nil {
		t.Fatal("block keyword hit should fail")
	} else if !strings.Contains(err.Error(), "命中禁止关键词") {
		t.Fatalf("unexpected error: %v", err)
	}
}
