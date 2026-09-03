package proxypool

import "testing"

func TestCustomScraperURLs(t *testing.T) {
	tpl := CustomScraperTemplate{URLTemplate: "https://example.test/page/{page}", PageStart: 2, PageEnd: 4, DefaultScheme: "http", ExtractPattern: `(?P<host>\S+):(?P<port>\d+)`}
	requests := customScraperRequests(tpl)
	if len(requests) != 3 || requests[0].URL != "https://example.test/page/2" || requests[2].URL != "https://example.test/page/4" {
		t.Fatalf("unexpected requests: %#v", requests)
	}
}

func TestCustomScraperRequestsPostBodyPagination(t *testing.T) {
	tpl := CustomScraperTemplate{
		URLTemplate:    "https://example.test/api",
		RequestMethod:  "POST",
		RequestHeaders: "Content-Type: application/x-www-form-urlencoded\nX-Test=yes",
		RequestBody:    "page={page}&limit=20",
		PageStart:      2,
		PageEnd:        3,
	}
	requests := customScraperRequests(tpl)
	if len(requests) != 2 || requests[0].Method != "POST" || requests[1].Body != "page=3&limit=20" {
		t.Fatalf("unexpected requests: %#v", requests)
	}
	if requests[0].Headers["X-Test"] != "yes" {
		t.Fatalf("unexpected headers: %#v", requests[0].Headers)
	}
}

func TestCustomScraperRequestsSizePlaceholder(t *testing.T) {
	tpl := CustomScraperTemplate{
		URLTemplate:    "https://example.test/api?limit={size}&page={page}",
		RequestBody:    "size={size}",
		PageStart:      1,
		PageEnd:        2,
		PageSize:       100,
		DefaultScheme:  "http",
		ExtractPattern: `(?P<host>\S+):(?P<port>\d+)`,
	}
	requests := customScraperRequests(tpl)
	if len(requests) != 2 || requests[0].URL != "https://example.test/api?limit=100&page=1" || requests[1].Body != "size=100" {
		t.Fatalf("unexpected requests: %#v", requests)
	}
}

func TestCustomScraperRequestsEmptySizePlaceholder(t *testing.T) {
	tpl := CustomScraperTemplate{
		URLTemplate:    "https://example.test/api?limit={size}",
		PageStart:      1,
		PageEnd:        3,
		PageSize:       0,
		DefaultScheme:  "http",
		ExtractPattern: `(?P<host>\S+):(?P<port>\d+)`,
	}
	requests := customScraperRequests(tpl)
	if len(requests) != 1 || requests[0].URL != "https://example.test/api?limit=" {
		t.Fatalf("unexpected requests: %#v", requests)
	}
}

func TestExtractCustomProxiesNamedGroups(t *testing.T) {
	tpl := CustomScraperTemplate{
		DefaultScheme:  "http",
		ExtractPattern: `(?P<host>(?:\d{1,3}\.){3}\d{1,3})[:：](?P<port>\d{2,5})`,
		HostGroup:      "host",
		PortGroup:      "port",
	}
	proxies, err := ExtractCustomProxies(tpl, "1.2.3.4:8080\nbad\n5.6.7.8：3128")
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 2 || proxies[0].Host != "1.2.3.4" || proxies[1].Port != 3128 {
		t.Fatalf("unexpected proxies: %#v", proxies)
	}
}

func TestExtractCustomJSONProxiesStringList(t *testing.T) {
	tpl := CustomScraperTemplate{
		DefaultScheme: "http",
		ResponseType:  "json",
		JSONListPath:  "data.proxies",
	}
	body := `{"code":200,"data":{"proxies":["1.2.3.4:8080","socks5://5.6.7.8:1080"]}}`
	proxies, err := ExtractCustomProxies(tpl, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 2 || proxies[0].Scheme != "http" || proxies[1].Scheme != "socks5" {
		t.Fatalf("unexpected proxies: %#v", proxies)
	}
}

func TestExtractCustomJSONProxiesAutoDefault(t *testing.T) {
	tpl := CustomScraperTemplate{
		DefaultScheme: "auto",
		ResponseType:  "json",
		JSONListPath:  "data.proxies",
	}
	body := `{"data":{"proxies":["1.2.3.4:8080"]}}`
	proxies, err := ExtractCustomProxies(tpl, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 3 || proxies[0].Scheme != "http" || proxies[1].Scheme != "https" || proxies[2].Scheme != "socks5" {
		t.Fatalf("unexpected proxies: %#v", proxies)
	}
	for _, p := range proxies {
		if p.Tags != p.Scheme {
			t.Fatalf("unexpected tags: %#v", proxies)
		}
	}
}

func TestExtractCustomJSONProxiesObjectList(t *testing.T) {
	tpl := CustomScraperTemplate{
		DefaultScheme:  "http",
		ResponseType:   "json",
		JSONListPath:   "items",
		JSONSchemePath: "type",
		JSONHostPath:   "ip",
		JSONPortPath:   "port",
	}
	body := `{"items":[{"type":"https","ip":"1.2.3.4","port":8443}]}`
	proxies, err := ExtractCustomProxies(tpl, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 1 || proxies[0].Scheme != "https" || proxies[0].Port != 8443 {
		t.Fatalf("unexpected proxies: %#v", proxies)
	}
}

func TestExtractCustomProxiesAutoDefaultNamedGroups(t *testing.T) {
	tpl := CustomScraperTemplate{
		DefaultScheme:  "auto",
		ExtractPattern: `(?P<host>1\.2\.3\.4):(?P<port>\d+)`,
		HostGroup:      "host",
		PortGroup:      "port",
	}
	proxies, err := ExtractCustomProxies(tpl, "1.2.3.4:8080")
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 3 || proxies[0].Scheme != "http" || proxies[1].Scheme != "https" || proxies[2].Scheme != "socks5" {
		t.Fatalf("unexpected proxies: %#v", proxies)
	}
}

func TestExtractCustomProxiesMultiDefaultNamedGroups(t *testing.T) {
	tpl := CustomScraperTemplate{
		DefaultScheme:  "http,socks5",
		ExtractPattern: `(?P<host>1\.2\.3\.4):(?P<port>\d+)`,
		HostGroup:      "host",
		PortGroup:      "port",
	}
	proxies, err := ExtractCustomProxies(tpl, "1.2.3.4:8080")
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

func TestNormalizeCustomScraperMultiDefault(t *testing.T) {
	tpl := normalizeCustomScraper(CustomScraperTemplate{DefaultScheme: "socks5 http"})
	if tpl.DefaultScheme != "http,socks5" {
		t.Fatalf("unexpected default scheme: %q", tpl.DefaultScheme)
	}
}

func TestNormalizeCustomScraperDefaultExtractPattern(t *testing.T) {
	tpl := normalizeCustomScraper(CustomScraperTemplate{})
	if tpl.ExtractPattern == "" || tpl.SchemeGroup != "scheme" || tpl.HostGroup != "host" || tpl.PortGroup != "port" || tpl.UserGroup != "user" || tpl.PasswordGroup != "password" {
		t.Fatalf("unexpected template defaults: %#v", tpl)
	}
}

func TestDefaultCustomExtractPatternAuth(t *testing.T) {
	tpl := normalizeCustomScraper(CustomScraperTemplate{DefaultScheme: "http"})
	proxies, err := ExtractCustomProxies(tpl, "socks5://alice:secret@1.2.3.4:1080\nbob:pwd@example.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 2 || proxies[0].Scheme != "socks5" || proxies[0].Username != "alice" || proxies[0].Password != "secret" {
		t.Fatalf("unexpected first proxy: %#v", proxies)
	}
	if proxies[1].Scheme != "http" || proxies[1].Host != "example.com" || proxies[1].Username != "bob" || proxies[1].Password != "pwd" {
		t.Fatalf("unexpected second proxy: %#v", proxies)
	}
}
