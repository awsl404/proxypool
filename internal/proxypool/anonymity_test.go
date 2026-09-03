package proxypool

import "testing"

func TestParseIdentityTextMyIPResponse(t *testing.T) {
	resp := parseIdentityText("当前 IP：203.0.113.10\n代理类型：匿名代理（未暴露真实IP）\n")
	if resp.Origin != "203.0.113.10" {
		t.Fatalf("unexpected origin: %#v", resp)
	}
	if resp.ProxyType != "anonymous" {
		t.Fatalf("expected anonymous proxy type, got %#v", resp.ProxyType)
	}

	resp = parseIdentityText("当前 IP：198.51.100.2\n代理类型：透明代理（真实IP已暴露）\n\n相关代理头部信息：\nX-Forwarded-For: 198.51.100.1\n")
	if resp.ProxyType != "transparent" {
		t.Fatalf("expected transparent proxy type, got %#v", resp.ProxyType)
	}
	if !hasForwardingHeaders(resp.Headers) {
		t.Fatalf("expected forwarding headers, got %#v", resp.Headers)
	}
}
