package proxypool

import "testing"

func TestParseProxyFormats(t *testing.T) {
	tests := []struct {
		in     string
		scheme string
		host   string
		port   int
		user   string
	}{
		{"socks5://user:pass@127.0.0.1:1080", "socks5", "127.0.0.1", 1080, "user"},
		{"http://example.com:8080", "http", "example.com", 8080, ""},
		{"user:pass@proxy.local:3128", "http", "proxy.local", 3128, "user"},
		{"https,proxy.local,8443,alice,secret", "https", "proxy.local", 8443, "alice"},
	}
	for _, tt := range tests {
		p, err := parseProxyLine(tt.in)
		if err != nil {
			t.Fatalf("parse %q: %v", tt.in, err)
		}
		if p.Scheme != tt.scheme || p.Host != tt.host || p.Port != tt.port || p.Username != tt.user {
			t.Fatalf("parse %q got %#v", tt.in, p)
		}
	}
}

func TestParseRejectsBadProxy(t *testing.T) {
	if _, err := parseProxyLine("ftp://127.0.0.1:21"); err == nil {
		t.Fatal("expected unsupported scheme error")
	}
	if _, err := parseProxyLine("127.0.0.1:notaport"); err == nil {
		t.Fatal("expected invalid port error")
	}
}
