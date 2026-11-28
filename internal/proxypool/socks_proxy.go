package proxypool

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"time"
)

func StartSOCKS5Proxy(ctx context.Context, addr string, store *Store, pool *ProxyPool) net.Listener {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		appError("SOCKS5 proxy listen failed on %s: %v", addr, err)
		return nil
	}
	appInfo("SOCKS5 proxy listening on %s", addr)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					appWarn("SOCKS5 accept failed: %v", err)
					continue
				}
			}
			go handleSOCKS5Client(ctx, conn, store, pool)
		}
	}()
	return ln
}

func handleSOCKS5Client(parent context.Context, client net.Conn, store *Store, pool *ProxyPool) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(20 * time.Second))
	cfg, err := store.Settings(parent)
	if err != nil {
		_ = writeSOCKS5Reply(client, 0x01)
		return
	}
	if !proxyAccessAllowed(client.RemoteAddr().String(), cfg) {
		_ = writeSOCKS5Reply(client, 0x02)
		return
	}
	target, err := readSOCKS5Connect(client, cfg, isRemoteLocal(client.RemoteAddr().String()))
	if err != nil {
		return
	}
	limit := maxServeProxyAttempts
	candidates, err := pool.CandidatesForSchemes(parent, target, cfg, limit, "socks5")
	if err != nil || len(candidates) == 0 {
		_ = writeSOCKS5Reply(client, 0x04)
		return
	}
	next := 0
	var initial []byte
	replied := false
	for attempt := 0; attempt < limit && next < len(candidates); attempt++ {
		ctx, cancel := context.WithTimeout(parent, time.Duration(cfg.TimeoutSeconds)*time.Second)
		upstream, used, newNext, err := dialServingCandidate(ctx, pool, target, cfg, candidates, next)
		cancel()
		next = newNext
		if err != nil {
			if !replied {
				_ = writeSOCKS5Reply(client, 0x04)
			}
			return
		}
		if !replied {
			// Upstream dialing may legitimately outlast the client handshake deadline.
			_ = client.SetDeadline(time.Now().Add(20 * time.Second))
			if err := writeSOCKS5Reply(client, 0x00); err != nil {
				_ = upstream.Close()
				return
			}
			replied = true
			_ = client.SetDeadline(time.Time{})
			initial, err = readInitialClientBytes(client, cfg)
			if err != nil {
				_ = upstream.Close()
				return
			}
		}
		replayable := replayableTunnelInitial(initial)
		if !replayable {
			limit = attempt + 1
		}
		gatewayFailure, _, err := writeInitialAndRelay(client, upstream, initial, cfg)
		_ = upstream.Close()
		if gatewayFailure && replayable && attempt+1 < limit && next < len(candidates) {
			markServingGatewayFailure(store, used, cfg)
			continue
		}
		if gatewayFailure {
			markServingGatewayFailure(store, used, cfg)
			_, _ = io.WriteString(client, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
		}
		_ = err
		return
	}
}

func readSOCKS5Connect(conn net.Conn, cfg Settings, remoteLocal bool) (string, error) {
	head := []byte{0, 0}
	if _, err := io.ReadFull(conn, head); err != nil {
		return "", err
	}
	if head[0] != 0x05 {
		return "", errors.New("not socks5")
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return "", err
	}
	needAuth := !cfg.BindLocalOnly || !remoteLocal
	if needAuth {
		if !hasSOCKSMethod(methods, 0x02) {
			_, _ = conn.Write([]byte{0x05, 0xff})
			return "", errors.New("socks5 auth required")
		}
		if _, err := conn.Write([]byte{0x05, 0x02}); err != nil {
			return "", err
		}
		if err := readSOCKS5UserPass(conn, cfg); err != nil {
			return "", err
		}
	} else {
		if !hasSOCKSMethod(methods, 0x00) {
			_, _ = conn.Write([]byte{0x05, 0xff})
			return "", errors.New("no supported socks5 auth method")
		}
		if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
			return "", err
		}
	}
	req := []byte{0, 0, 0, 0}
	if _, err := io.ReadFull(conn, req); err != nil {
		return "", err
	}
	if req[0] != 0x05 || req[1] != 0x01 {
		_ = writeSOCKS5Reply(conn, 0x07)
		return "", errors.New("only connect is supported")
	}
	host, err := readSOCKS5Addr(conn, req[3])
	if err != nil {
		_ = writeSOCKS5Reply(conn, 0x08)
		return "", err
	}
	portBytes := []byte{0, 0}
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBytes)
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func hasSOCKSMethod(methods []byte, method byte) bool {
	for _, m := range methods {
		if m == method {
			return true
		}
	}
	return false
}

func readSOCKS5UserPass(conn net.Conn, cfg Settings) error {
	head := []byte{0, 0}
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}
	if head[0] != 0x01 {
		return errors.New("invalid socks5 auth version")
	}
	user := make([]byte, int(head[1]))
	if _, err := io.ReadFull(conn, user); err != nil {
		return err
	}
	passLen := []byte{0}
	if _, err := io.ReadFull(conn, passLen); err != nil {
		return err
	}
	pass := make([]byte, int(passLen[0]))
	if _, err := io.ReadFull(conn, pass); err != nil {
		return err
	}
	username := string(user)
	password := string(pass)
	if !socks5CredentialsAllowed(username, password, cfg) {
		_, _ = conn.Write([]byte{0x01, 0x01})
		return errors.New("invalid socks5 token")
	}
	_, err := conn.Write([]byte{0x01, 0x00})
	return err
}

func socks5CredentialsAllowed(username, password string, cfg Settings) bool {
	if (cfg.ProxyUsername != "" || cfg.ProxyPassword != "") && username == cfg.ProxyUsername && password == cfg.ProxyPassword {
		return true
	}
	return cfg.APIToken != "" && (username == cfg.APIToken || password == cfg.APIToken)
}

func readSOCKS5Addr(conn net.Conn, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	case 0x03:
		l := []byte{0}
		if _, err := io.ReadFull(conn, l); err != nil {
			return "", err
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(conn, b); err != nil {
			return "", err
		}
		return string(b), nil
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(conn, b); err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	default:
		return "", errors.New("unsupported address type")
	}
}

func writeSOCKS5Reply(conn net.Conn, code byte) error {
	return writeAll(conn, []byte{0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}
