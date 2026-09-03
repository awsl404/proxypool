package proxypool

import "testing"

func TestParseCollectorSources(t *testing.T) {
	sources := parseCollectorSources("socks5|https://example.test/socks5.txt\nauto|https://example.test/mixed.txt\nhttp,socks5|https://example.test/unknown.txt\nhttps://example.test/http.txt")
	if len(sources) != 4 {
		t.Fatalf("expected 4 sources, got %d", len(sources))
	}
	if sources[0].Scheme != "socks5" || sources[0].URL != "https://example.test/socks5.txt" {
		t.Fatalf("unexpected explicit source: %#v", sources[0])
	}
	if sources[1].Scheme != "auto" {
		t.Fatalf("expected auto source, got %#v", sources[1])
	}
	if sources[2].Scheme != "http,socks5" {
		t.Fatalf("expected multi-protocol source, got %#v", sources[2])
	}
	if sources[3].Scheme != "http" {
		t.Fatalf("expected inferred http source, got %#v", sources[3])
	}
}

func TestParseSourceProxyLineUsesDefaultScheme(t *testing.T) {
	p, err := parseSourceProxyLine("127.0.0.1:1080", "socks5")
	if err != nil {
		t.Fatal(err)
	}
	if p.Scheme != "socks5" || p.Host != "127.0.0.1" || p.Port != 1080 {
		t.Fatalf("unexpected proxy: %#v", p)
	}
}

func TestParseProxyLinesWithAutoDefault(t *testing.T) {
	proxies, err := parseProxyLinesWithDefault("127.0.0.1:8080", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 3 || proxies[0].Scheme != "http" || proxies[1].Scheme != "https" || proxies[2].Scheme != "socks5" {
		t.Fatalf("unexpected proxies: %#v", proxies)
	}
	for _, p := range proxies {
		if p.Tags != p.Scheme {
			t.Fatalf("expected per-proxy tags, got %#v", proxies)
		}
	}
}

func TestParseProxyLinesWithMultiDefault(t *testing.T) {
	proxies, err := parseProxyLinesWithDefault("127.0.0.1:8080", "http,socks5")
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 2 || proxies[0].Scheme != "http" || proxies[1].Scheme != "socks5" {
		t.Fatalf("unexpected proxies: %#v", proxies)
	}
	for _, p := range proxies {
		if p.Tags != p.Scheme {
			t.Fatalf("unexpected tags: %#v", proxies)
		}
	}
}

func TestShouldBypassCollectorProxy(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8080":  true,
		"localhost:8080":  true,
		"[::1]:8080":      true,
		"192.168.1.2:80":  false,
		"example.com:443": false,
	}
	for addr, want := range cases {
		if got := shouldBypassCollectorProxy(addr); got != want {
			t.Fatalf("shouldBypassCollectorProxy(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestHealthCheckURLs(t *testing.T) {
	urls := healthCheckURLs(Settings{HealthURL: "https://a.test/ok\n# ignored\nnot-url\nhttp://b.test/ping"})
	if len(urls) != 2 || urls[0] != "https://a.test/ok" || urls[1] != "http://b.test/ping" {
		t.Fatalf("unexpected health urls: %#v", urls)
	}
}
