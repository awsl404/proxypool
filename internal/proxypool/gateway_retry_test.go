package proxypool

import (
	"net"
	"testing"
)

func TestGatewayPreludeSplitAcrossReads(t *testing.T) {
	upstream, sender := net.Pipe()
	defer upstream.Close()
	defer sender.Close()
	client, receiver := net.Pipe()
	defer client.Close()
	defer receiver.Close()
	go func() {
		defer sender.Close()
		// Each pipe write must be consumed separately: deterministically model TCP fragmentation.
		for _, part := range []string{"HT", "TP/1.1 502 Bad ", "Gateway\r\n", "Server: test\r\n\r\n"} {
			if _, err := sender.Write([]byte(part)); err != nil {
				return
			}
		}
	}()
	gateway, prelude, err := inspectUpstreamPreludeAndRelay(client, upstream, Settings{TimeoutSeconds: 1})
	if err != nil || !gateway {
		t.Fatalf("fragmented error was not classified: gateway=%t prelude=%q err=%v", gateway, prelude, err)
	}
}

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
