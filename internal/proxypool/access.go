package proxypool

import (
	"net"
	"strings"
)

func webAccessAllowed(remote string, cfg Settings) bool {
	return accessAllowed(remote, cfg.WebAllowlist, cfg.WebBlocklist)
}

func proxyAccessAllowed(remote string, cfg Settings) bool {
	return accessAllowed(remote, cfg.ProxyAllowlist, cfg.ProxyBlocklist)
}

func accessAllowed(remote, allowlist, blocklist string) bool {
	host := remoteHost(remote)
	if host == "" {
		return false
	}
	if accessListMatches(blocklist, host) {
		return false
	}
	if strings.TrimSpace(allowlist) == "" {
		return true
	}
	return accessListMatches(allowlist, host)
}

func accessListMatches(list, host string) bool {
	ip := net.ParseIP(host)
	for _, item := range splitAccessList(list) {
		if item == "*" {
			return true
		}
		if _, network, err := net.ParseCIDR(item); err == nil {
			if ip != nil && network.Contains(ip) {
				return true
			}
			continue
		}
		if parsed := net.ParseIP(item); parsed != nil {
			if ip != nil && parsed.Equal(ip) {
				return true
			}
			continue
		}
		if strings.EqualFold(item, host) {
			return true
		}
	}
	return false
}

func splitAccessList(list string) []string {
	var out []string
	for _, item := range strings.FieldsFunc(list, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' ' || r == '\t'
	}) {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func remoteHost(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	return strings.Trim(host, "[]")
}
