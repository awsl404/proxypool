package proxypool

import (
	"context"
	"encoding/csv"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func ImportProxies(ctx context.Context, store *Store, text string) ImportResult {
	result := ImportResult{Errors: []string{}}
	for _, raw := range splitImportLines(text) {
		p, err := parseProxyLine(raw)
		if err != nil {
			result.Failed++
			if len(result.Errors) < 50 {
				result.Errors = append(result.Errors, raw+": "+err.Error())
			}
			continue
		}
		added, err := store.AddProxy(ctx, p)
		if err != nil {
			result.Failed++
			if len(result.Errors) < 50 {
				result.Errors = append(result.Errors, raw+": "+err.Error())
			}
			continue
		}
		if added {
			result.Imported++
		} else {
			result.Duplicates++
		}
	}
	return result
}

func splitImportLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func parseProxyLine(line string) (Proxy, error) {
	if strings.Contains(line, ",") {
		if p, err := parseCSVProxy(line); err == nil {
			return p, nil
		}
	}
	if strings.Contains(line, "://") {
		return parseProxyURL(line)
	}
	return parseLooseProxy(line)
}

func parseProxyLinesWithDefault(line, defaultScheme string) ([]Proxy, error) {
	if strings.Contains(line, "://") || strings.Contains(line, ",") {
		p, err := parseProxyLine(line)
		if err != nil {
			return nil, err
		}
		return []Proxy{p}, nil
	}
	schemes := protocolSchemes(defaultScheme)
	var out []Proxy
	for _, scheme := range schemes {
		p, err := parseSourceProxyLine(line, scheme)
		if err == nil {
			p.Tags = scheme
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("expected host:port")
	}
	return out, nil
}

func parseCSVProxy(line string) (Proxy, error) {
	records, err := csv.NewReader(strings.NewReader(line)).Read()
	if err != nil {
		return Proxy{}, err
	}
	for i := range records {
		records[i] = strings.TrimSpace(records[i])
	}
	if len(records) == 1 {
		return parseProxyLine(records[0])
	}
	if len(records) < 3 {
		return Proxy{}, errors.New("CSV requires at least scheme,host,port")
	}
	port, err := strconv.Atoi(records[2])
	if err != nil {
		return Proxy{}, errors.New("invalid port")
	}
	p := Proxy{
		Scheme: strings.ToLower(records[0]),
		Host:   records[1],
		Port:   port,
	}
	if len(records) > 3 {
		p.Username = records[3]
	}
	if len(records) > 4 {
		p.Password = records[4]
	}
	return normalizeProxy(p)
}

func parseProxyURL(line string) (Proxy, error) {
	u, err := url.Parse(strings.TrimSpace(line))
	if err != nil {
		return Proxy{}, err
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return Proxy{}, errors.New("invalid or missing port")
	}
	p := Proxy{
		Scheme: strings.ToLower(u.Scheme),
		Host:   u.Hostname(),
		Port:   port,
	}
	if u.User != nil {
		p.Username = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	return normalizeProxy(p)
}

func parseLooseProxy(line string) (Proxy, error) {
	line = strings.TrimSpace(line)
	scheme := "http"
	if first, rest, ok := strings.Cut(line, " "); ok && validScheme(first) {
		scheme = strings.ToLower(first)
		line = strings.TrimSpace(rest)
	}
	var auth string
	hostport := line
	if a, hp, ok := strings.Cut(line, "@"); ok {
		auth = a
		hostport = hp
	}
	host, portText, err := net.SplitHostPort(hostport)
	if err != nil {
		parts := strings.Split(hostport, ":")
		if len(parts) != 2 {
			return Proxy{}, errors.New("expected host:port")
		}
		host, portText = parts[0], parts[1]
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return Proxy{}, errors.New("invalid port")
	}
	p := Proxy{Scheme: scheme, Host: host, Port: port}
	if auth != "" {
		p.Username, p.Password, _ = strings.Cut(auth, ":")
	}
	return normalizeProxy(p)
}

func normalizeProxy(p Proxy) (Proxy, error) {
	p.Scheme = strings.ToLower(strings.TrimSpace(p.Scheme))
	p.Host = strings.TrimSpace(p.Host)
	p.Username = strings.TrimSpace(p.Username)
	p.Password = strings.TrimSpace(p.Password)
	p.Tags = normalizeTagList(firstNonEmpty(p.Tags, p.Scheme))
	if !validScheme(p.Scheme) {
		return p, errors.New("unsupported scheme")
	}
	if p.Host == "" {
		return p, errors.New("missing host")
	}
	if p.Port <= 0 || p.Port > 65535 {
		return p, errors.New("invalid port")
	}
	return p, nil
}

func normalizeTagList(tags string) string {
	seen := map[string]bool{}
	for _, raw := range strings.FieldsFunc(tags, func(r rune) bool {
		return r == ',' || r == ' ' || r == '|' || r == ';' || r == '\n' || r == '\t'
	}) {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			continue
		}
		if tag == "all" || tag == "auto" {
			for _, expanded := range []string{"http", "https", "socks5"} {
				seen[expanded] = true
			}
			continue
		}
		if tag == "socks4" {
			continue
		}
		if validScheme(tag) || tag == "unknown" {
			seen[tag] = true
		}
	}
	var out []string
	for _, tag := range []string{"http", "https", "socks5", "unknown"} {
		if seen[tag] {
			out = append(out, tag)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, ",")
}

func protocolSchemes(value string) []string {
	schemes := parseProtocolSchemes(value)
	if len(schemes) == 0 {
		return []string{"http"}
	}
	return schemes
}

func protocolTagString(value string) string {
	return strings.Join(protocolSchemes(value), ",")
}

func validProtocolSpec(value string) bool {
	return len(parseProtocolSchemes(value)) > 0
}

func parseProtocolSchemes(value string) []string {
	seen := map[string]bool{}
	for _, raw := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '|' || r == ';' || r == '\n' || r == '\t'
	}) {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			continue
		}
		if tag == "all" || tag == "auto" {
			for _, expanded := range []string{"http", "https", "socks5"} {
				seen[expanded] = true
			}
			continue
		}
		if validScheme(tag) {
			seen[tag] = true
		}
	}
	var out []string
	for _, scheme := range []string{"http", "https", "socks5"} {
		if seen[scheme] {
			out = append(out, scheme)
		}
	}
	return out
}

func validScheme(s string) bool {
	switch strings.ToLower(s) {
	case "http", "https", "socks5":
		return true
	default:
		return false
	}
}
