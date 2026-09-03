package proxypool

import "testing"

func TestProxyGatewayHTTPPreludeDetection(t *testing.T) {
	body := "HTTP/1.1 501 Gateway error\r\n" +
		"Transfer-Encoding: chunked\r\n" +
		"Server: Jetty/4.2.x (VxWorks/WIND version 2.9 ppc java/1.1-rr-std-b12)\r\n" +
		"Content-Type: text/html\r\n\r\n" +
		"<H1>Could not connect</H1>"
	if !isProxyGatewayHTTPPrelude([]byte(body)) {
		t.Fatal("expected gateway error response to be detected")
	}
	okResponse := "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n当前 IP：14.112.3.9\n"
	if isProxyGatewayHTTPPrelude([]byte(okResponse)) {
		t.Fatal("normal HTTP response should not be detected as gateway failure")
	}
}

func TestReplayableTunnelInitial(t *testing.T) {
	getRequest := "GET / HTTP/1.1\r\nHost: 117.72.157.151:8880\r\n\r\n"
	if !replayableTunnelInitial([]byte(getRequest)) {
		t.Fatal("plain GET request should be replayable")
	}
	postRequest := "POST / HTTP/1.1\r\nHost: example.test\r\nContent-Length: 4\r\n\r\ndata"
	if replayableTunnelInitial([]byte(postRequest)) {
		t.Fatal("request with body should not be replayable")
	}
}
