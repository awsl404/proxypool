package proxypool

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPlainHTTPFailedUpstreamReturns502(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	// A bound but closed local port fails without contacting any remote service.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if _, err := store.AddProxy(ctx, Proxy{Scheme: "http", Host: "127.0.0.1", Port: port}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(&HTTPProxy{store: store, pool: NewProxyPool(store)})
	defer srv.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "GET http://example.test/ HTTP/1.1\r\nHost: example.test\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("expected valid HTTP error response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("got %s", resp.Status)
	}
}

func TestSOCKS5EmptyPoolReturnsFailure(t *testing.T) {
	store := newTestStore(t)
	ln := StartSOCKS5Proxy(contextForTest(t), "127.0.0.1:0", store, NewProxyPool(store))
	if ln == nil {
		t.Fatal("listen failed")
	}
	defer ln.Close()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "\x05\x01\x00")
	method := make([]byte, 2)
	if _, err := io.ReadFull(conn, method); err != nil {
		t.Fatal(err)
	}
	if method[1] != 0 {
		t.Fatalf("unexpected auth: %v", method)
	}
	fmt.Fprint(conn, "\x05\x01\x00\x03\x0cexample.test\x00\x50")
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] == 0 {
		t.Fatal("empty pool must not report success")
	}
}

func contextForTest(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

func TestSOCKS5RetriesWithoutSecondHandshake(t *testing.T) {
	store := newTestStore(t)
	cfg := defaultSettings()
	// Two local upstream SOCKS servers: first returns a gateway failure, second succeeds.
	for i := 0; i < 2; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		response := "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n"
		if i == 1 {
			response = "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"
		}
		go func() {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := readSOCKS5Connect(c, cfg, true); err != nil {
				return
			}
			if err := writeSOCKS5Reply(c, 0); err != nil {
				return
			}
			if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
				return
			}
			io.WriteString(c, response)
		}()
		_, portText, _ := net.SplitHostPort(ln.Addr().String())
		port, _ := strconv.Atoi(portText)
		if _, err := store.AddProxy(context.Background(), Proxy{Scheme: "socks5", Host: "127.0.0.1", Port: port}); err != nil {
			t.Fatal(err)
		}
	}
	ln := StartSOCKS5Proxy(contextForTest(t), "127.0.0.1:0", store, NewProxyPool(store))
	if ln == nil {
		t.Fatal("listen failed")
	}
	defer ln.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(c, "\x05\x01\x00")
	method := make([]byte, 2)
	if _, err := io.ReadFull(c, method); err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(c, "\x05\x01\x00\x03\x0cexample.test\x00\x50")
	reply := make([]byte, 10)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0 {
		t.Fatalf("connect failed: %v", reply)
	}
	fmt.Fprint(c, "GET / HTTP/1.1\r\nHost: example.test\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("retry must not insert another SOCKS reply: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("unexpected response %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "ok" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}
