package proxypool

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func DialViaProxy(ctx context.Context, p Proxy, target string, timeout time.Duration) (net.Conn, error) {
	switch p.Scheme {
	case "http", "https":
		return dialViaHTTPProxy(ctx, p, target, timeout)
	case "socks5":
		return dialViaSOCKS5(ctx, p, target, timeout)
	default:
		return nil, fmt.Errorf("unsupported upstream scheme %q", p.Scheme)
	}
}

func dialViaHTTPProxy(ctx context.Context, p Proxy, target string, timeout time.Duration) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", p.Addr())
	if err != nil {
		return nil, err
	}
	if p.Scheme == "https" {
		conn = tls.Client(conn, &tls.Config{ServerName: p.Host, MinVersion: tls.VersionTLS12})
		if err := conn.(*tls.Conn).HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\nProxy-Connection: Keep-Alive\r\n"
	if p.Username != "" {
		secret := base64.StdEncoding.EncodeToString([]byte(p.Username + ":" + p.Password))
		req += "Proxy-Authorization: Basic " + secret + "\r\n"
	}
	req += "\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = conn.Close()
		return nil, fmt.Errorf("upstream CONNECT failed: %s", resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: br}, nil
	}
	return conn, nil
}

func dialViaSOCKS5(ctx context.Context, p Proxy, target string, timeout time.Duration) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", p.Addr())
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := socks5ClientHandshake(conn, p, target); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func socks5ClientHandshake(conn net.Conn, p Proxy, target string) error {
	if p.Username != "" {
		if _, err := conn.Write([]byte{0x05, 0x02, 0x00, 0x02}); err != nil {
			return err
		}
	} else if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	method := []byte{0, 0}
	if _, err := io.ReadFull(conn, method); err != nil {
		return err
	}
	if method[0] != 0x05 {
		return errors.New("invalid socks5 version")
	}
	if method[1] == 0x02 {
		if len(p.Username) > 255 || len(p.Password) > 255 {
			return errors.New("socks5 credentials too long")
		}
		auth := []byte{0x01, byte(len(p.Username))}
		auth = append(auth, []byte(p.Username)...)
		auth = append(auth, byte(len(p.Password)))
		auth = append(auth, []byte(p.Password)...)
		if _, err := conn.Write(auth); err != nil {
			return err
		}
		resp := []byte{0, 0}
		if _, err := io.ReadFull(conn, resp); err != nil {
			return err
		}
		if resp[1] != 0 {
			return errors.New("socks5 authentication failed")
		}
	} else if method[1] != 0x00 {
		return errors.New("socks5 server rejected auth methods")
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return err
	}
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("target host too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, []byte(host)...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}
	head := []byte{0, 0, 0, 0}
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}
	if head[1] != 0 {
		return fmt.Errorf("socks5 connect failed: code %d", head[1])
	}
	var skip int
	switch head[3] {
	case 0x01:
		skip = 4
	case 0x03:
		l := []byte{0}
		if _, err := io.ReadFull(conn, l); err != nil {
			return err
		}
		skip = int(l[0])
	case 0x04:
		skip = 16
	default:
		return errors.New("invalid socks5 address type")
	}
	if skip > 0 {
		if _, err := io.CopyN(io.Discard, conn, int64(skip)); err != nil {
			return err
		}
	}
	_, err = io.CopyN(io.Discard, conn, 2)
	return err
}

func targetFromRequest(r *http.Request) (string, error) {
	if r.Method == http.MethodConnect {
		return addDefaultPort(r.Host, "443"), nil
	}
	if r.URL != nil && r.URL.Host != "" {
		port := "80"
		if strings.EqualFold(r.URL.Scheme, "https") {
			port = "443"
		}
		return addDefaultPort(r.URL.Host, port), nil
	}
	if r.Host != "" {
		return addDefaultPort(r.Host, "80"), nil
	}
	return "", errors.New("missing target host")
}

func addDefaultPort(host, port string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, port)
}

func upstreamProxyURL(p Proxy) *url.URL {
	u := &url.URL{
		Scheme: p.Scheme,
		Host:   p.Addr(),
	}
	if p.Username != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}
