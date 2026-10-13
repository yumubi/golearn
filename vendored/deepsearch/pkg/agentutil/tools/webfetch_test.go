package tools

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/fantasy"
)

func TestBlockedFetchAddrClassifiesNetworks(t *testing.T) {
	cases := []struct {
		address string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"0.0.0.0", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"198.18.0.1", true},
		{"192.0.2.1", true},
		{"203.0.113.1", true},
		{"224.0.0.1", true},
		{"255.255.255.255", true},
		{"::", true},
		{"::1", true},
		{"fc00::1", true},
		{"fe80::1", true},
		{"ff02::1", true},
		{"2001:db8::1", true},
		{"64:ff9b::7f00:1", true},
		{"2002:7f00:1::1", true},
		{"::ffff:127.0.0.1", true},
		{"8.8.8.8", false},
		{"93.184.216.34", false},
		{"2606:4700:4700::1111", false},
	}

	for _, tc := range cases {
		got := blockedFetchAddr(netip.MustParseAddr(tc.address))
		if got != tc.blocked {
			t.Errorf("blockedFetchAddr(%s) = %v, want %v", tc.address, got, tc.blocked)
		}
	}

	if !blockedFetchAddr(netip.Addr{}) {
		t.Error("an invalid address must be blocked")
	}
	if !blockedFetchAddr(netip.MustParseAddr("fe80::1%eth0")) {
		t.Error("a zoned address must be blocked")
	}
}

func TestWebFetchControlChecksResolvedAddress(t *testing.T) {
	allowed := []string{"93.184.216.34:443", "8.8.8.8:80", "[2606:4700:4700::1111]:443"}
	for _, address := range allowed {
		if e := webFetchControl("tcp", address, nil); e != nil {
			t.Errorf("webFetchControl(%q) = %v, want nil", address, e)
		}
	}

	blocked := []string{"127.0.0.1:80", "10.1.2.3:8080", "169.254.169.254:80", "[::1]:443", "[fe80::1%eth0]:80", "not-an-address"}
	for _, address := range blocked {
		if e := webFetchControl("tcp", address, nil); e == nil {
			t.Errorf("webFetchControl(%q) = nil, want a blocked error", address)
		}
	}
}

func TestParseFetchURLRejectsUnsafeInput(t *testing.T) {
	valid := []string{"https://example.com/a?b=c#d", "http://sub.example.org:8080/path", "https://xn--fiqs8s.example/path"}
	for _, raw := range valid {
		if _, e := parseFetchURL(raw); e != nil {
			t.Errorf("parseFetchURL(%q) = %v, want it accepted", raw, e)
		}
	}

	invalid := []string{
		"",
		"   ",
		"example.com/a",
		"file:///etc/passwd",
		"ftp://example.com/",
		"http://user:pw@example.com/",
		"http:///path",
		"http://127.0.0.1/x",
		"http://[::1]/x",
		"http://[2001:db8::1]/x",
		"http://localhost/x",
		"http://LOCALHOST./x",
		"http://sub.localhost/x",
	}
	for _, raw := range invalid {
		if _, e := parseFetchURL(raw); e == nil {
			t.Errorf("parseFetchURL(%q) = nil, want it rejected", raw)
		}
	}
}

func TestWebFetchToolRefusesLiteralAddress(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("<html><body>internal</body></html>"))
	}))
	defer srv.Close()

	got := runWebFetch(t, NewWebFetchTool(WebFetchConfig{}), `{"url":"`+srv.URL+`/"}`)
	if !got.IsError {
		t.Fatalf("fetching a literal address = %q, want an error", got.Content)
	}
	if !strings.Contains(got.Content, "blocked") {
		t.Fatalf("error = %q, want it to say why the address is refused", got.Content)
	}
	if hits.Load() != 0 {
		t.Fatal("the refused request still reached the server")
	}
}

// TestWebFetchClientBlocksResolvedLoopback checks the guarded transport
// itself: a hostname that resolves to a local address must never connect,
// even when the caller bypasses the URL checks.
func TestWebFetchClientBlocksResolvedLoopback(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	address := strings.TrimPrefix(srv.URL, "http://")
	resp, e := newWebFetchClient(2 * time.Second).Get("http://localhost:" + portOf(t, address) + "/")
	if e == nil {
		_ = resp.Body.Close()
		t.Fatal("the guarded client connected to a loopback address")
	}
	if !errors.Is(e, errWebFetchBlocked) {
		t.Fatalf("error = %v, want the blocked error", e)
	}
	if hits.Load() != 0 {
		t.Fatal("the blocked dial still reached the server")
	}
}

func portOf(t *testing.T, address string) string {
	t.Helper()

	_, port, e := net.SplitHostPort(address)
	if e != nil {
		t.Fatalf("split %q: %v", address, e)
	}

	return port
}

func TestWebFetchToolReadsHTMLPage(t *testing.T) {
	fixture := `<!doctype html><html><head>
<title> Release notes </title>
<style>body { color: red }</style>
</head><body>
<script>window.secret = "must-not-leak"</script>
<h1>Release 1.2</h1>
<p>First   paragraph.</p>
<p>Second paragraph with a <a href="/docs/next">next step</a>.</p>
<a href="https://other.example/x">External</a>
<a href="mailto:team@example.com">Mail us</a>
</body></html>`

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	got := runWebFetch(t, newWebFetchTestTool(t, srv, WebFetchConfig{}), `{"url":"https://example.com/releases/1.2"}`)
	if got.IsError {
		t.Fatalf("unexpected error: %s", got.Content)
	}
	for _, want := range []string{
		"URL: https://example.com/releases/1.2",
		"Title: Release notes",
		"Release 1.2",
		"First paragraph.",
		"Second paragraph with a next step.",
		"Links:",
		"- https://example.com/docs/next — next step",
		"- https://other.example/x — External",
	} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("page misses %q:\n%s", want, got.Content)
		}
	}
	for _, banned := range []string{"must-not-leak", "color: red", "mailto:"} {
		if strings.Contains(got.Content, banned) {
			t.Fatalf("page leaks %q:\n%s", banned, got.Content)
		}
	}
}

func TestWebFetchToolReadsPlainText(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello from a text file"))
	}))
	defer srv.Close()

	got := runWebFetch(t, newWebFetchTestTool(t, srv, WebFetchConfig{}), `{"url":"https://example.com/notes.txt"}`)
	if got.IsError {
		t.Fatalf("unexpected error: %s", got.Content)
	}
	if !strings.Contains(got.Content, "hello from a text file") || strings.Contains(got.Content, "Links:") {
		t.Fatalf("plain text page = %q", got.Content)
	}
}

func TestWebFetchToolFollowsRedirectsToFinalURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "http://example.org/new", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>Moved</title></head><body>Final page</body></html>"))
	}))
	defer srv.Close()

	got := runWebFetch(t, newWebFetchTestTool(t, srv, WebFetchConfig{}), `{"url":"http://example.com/old"}`)
	if got.IsError {
		t.Fatalf("unexpected error: %s", got.Content)
	}
	for _, want := range []string{"URL: http://example.org/new", "Title: Moved", "Final page"} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("page misses %q:\n%s", want, got.Content)
		}
	}
}

func TestWebFetchToolRefusesRedirectToLocalAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:9/admin", http.StatusFound)
	}))
	defer srv.Close()

	got := runWebFetch(t, newWebFetchTestTool(t, srv, WebFetchConfig{}), `{"url":"http://example.com/redirect"}`)
	if !got.IsError || !strings.Contains(got.Content, "blocked") {
		t.Fatalf("redirect to a local address = %q, want a blocked error", got.Content)
	}
}

func TestWebFetchToolStopsRedirectLoops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/again", http.StatusFound)
	}))
	defer srv.Close()

	got := runWebFetch(t, newWebFetchTestTool(t, srv, WebFetchConfig{}), `{"url":"http://example.com/loop"}`)
	if !got.IsError || !strings.Contains(got.Content, "too many redirects") {
		t.Fatalf("redirect loop = %q, want a too many redirects error", got.Content)
	}
}

func TestWebFetchToolRefusesBinaryContent(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7"))
	}))
	defer srv.Close()

	got := runWebFetch(t, newWebFetchTestTool(t, srv, WebFetchConfig{}), `{"url":"https://example.com/report.pdf"}`)
	if !got.IsError || !strings.Contains(got.Content, "not text") {
		t.Fatalf("binary page = %q, want it refused", got.Content)
	}
}

func TestWebFetchToolReportsErrorStatus(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	got := runWebFetch(t, newWebFetchTestTool(t, srv, WebFetchConfig{}), `{"url":"https://example.com/missing"}`)
	if !got.IsError || !strings.Contains(got.Content, "404") {
		t.Fatalf("missing page = %q, want a 404 error", got.Content)
	}
}

func TestWebFetchToolReportsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	got := runWebFetch(t, newWebFetchTestTool(t, srv, WebFetchConfig{Timeout: 200 * time.Millisecond}), `{"url":"http://example.com/slow"}`)
	if !got.IsError || !strings.Contains(got.Content, "timed out") {
		t.Fatalf("slow page = %q, want a timeout error", got.Content)
	}
}

func TestWebFetchToolTruncatesLongPages(t *testing.T) {
	body := "<html><head><title>Long</title></head><body><p>" + strings.Repeat("lorem ipsum dolor sit amet ", 200) + "</p></body></html>"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got := runWebFetch(t, newWebFetchTestTool(t, srv, WebFetchConfig{MaxChars: 200}), `{"url":"https://example.com/long"}`)
	if got.IsError {
		t.Fatalf("unexpected error: %s", got.Content)
	}
	if !strings.Contains(got.Content, "[content truncated]") {
		t.Fatalf("long page = %q, want a truncation marker", got.Content)
	}
	if runes := len([]rune(got.Content)); runes > 200+len([]rune("\n[content truncated]")) {
		t.Fatalf("page has %d runes, want at most the configured limit", runes)
	}

	got = runWebFetch(t, newWebFetchTestTool(t, srv, WebFetchConfig{MaxBytes: 256}), `{"url":"https://example.com/long"}`)
	if got.IsError {
		t.Fatalf("unexpected error: %s", got.Content)
	}
	if !strings.Contains(got.Content, "only its beginning is shown") {
		t.Fatalf("page = %q, want a size truncation note", got.Content)
	}
}

func TestWebFetchToolInfo(t *testing.T) {
	info := NewWebFetchTool(WebFetchConfig{}).Info()
	if info.Name != "WebFetch" {
		t.Fatalf("name = %q, want WebFetch", info.Name)
	}
	if !info.Parallel {
		t.Fatal("WebFetch must be safe to run in parallel")
	}
}

// newWebFetchTestTool serves every request from the test server while keeping
// the production redirect policy, so the logical URLs stay public-looking and
// the test server's TLS trust still applies to https URLs.
func newWebFetchTestTool(t *testing.T, srv *httptest.Server, cfg WebFetchConfig) fantasy.AgentTool {
	t.Helper()

	client := newWebFetchClient(cfg.Timeout)
	base, ok := srv.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatal("unexpected test server transport")
	}
	transport := base.Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, srv.Listener.Addr().String())
	}
	client.Transport = transport

	return newWebFetchTool(cfg, client)
}

func runWebFetch(t *testing.T, tool fantasy.AgentTool, input string) fantasy.ToolResponse {
	t.Helper()

	got, e := tool.Run(context.Background(), fantasy.ToolCall{Name: "WebFetch", Input: input})
	if e != nil {
		t.Fatalf("WebFetch: %v", e)
	}

	return got
}
