package proxypool

import "testing"
import (
	"encoding/base64"
	"net/http"
)

func TestAccessAllowed(t *testing.T) {
	if !accessAllowed("192.168.1.10:1234", "192.168.1.0/24", "") {
		t.Fatal("expected CIDR allowlist match")
	}
	if accessAllowed("192.168.2.10:1234", "192.168.1.0/24", "") {
		t.Fatal("expected allowlist miss")
	}
	if accessAllowed("192.168.1.10:1234", "192.168.1.0/24", "192.168.1.10") {
		t.Fatal("blocklist should take precedence")
	}
	if !accessAllowed("203.0.113.9:1234", "", "") {
		t.Fatal("empty allowlist should allow by default")
	}
	if accessAllowed("203.0.113.9:1234", "", "203.0.113.0/24") {
		t.Fatal("expected CIDR blocklist match")
	}
}

func TestProxyBasicAuthAllowed(t *testing.T) {
	cfg := Settings{ProxyUsername: "proxyuser", ProxyPassword: "proxypass", APIToken: "token"}
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.RemoteAddr = "203.0.113.10:5000"
	req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("proxyuser:proxypass")))
	if !proxyRequestAllowed(req, cfg) {
		t.Fatal("expected proxy basic auth to be accepted")
	}
	req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("proxyuser:wrong")))
	if proxyRequestAllowed(req, cfg) {
		t.Fatal("unexpected proxy basic auth success")
	}
	req.Header.Set("Proxy-Authorization", "Bearer token")
	if !proxyRequestAllowed(req, cfg) {
		t.Fatal("expected legacy bearer token to remain accepted")
	}
}

func TestSOCKS5CredentialsAllowed(t *testing.T) {
	cfg := Settings{ProxyUsername: "proxyuser", ProxyPassword: "proxypass", APIToken: "token"}
	if !socks5CredentialsAllowed("proxyuser", "proxypass", cfg) {
		t.Fatal("expected configured SOCKS5 username/password to be accepted")
	}
	if !socks5CredentialsAllowed("token", "", cfg) {
		t.Fatal("expected legacy token username to be accepted")
	}
	if !socks5CredentialsAllowed("", "token", cfg) {
		t.Fatal("expected legacy token password to be accepted")
	}
	if socks5CredentialsAllowed("proxyuser", "wrong", cfg) {
		t.Fatal("unexpected SOCKS5 auth success")
	}
}
