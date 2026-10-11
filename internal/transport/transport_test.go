package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// A fixed clock, so retry times are exact.
var fixedNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func clientAt(t *testing.T, base string, opts ...Option) *Client {
	t.Helper()
	opts = append([]Option{WithClock(func() time.Time { return fixedNow })}, opts...)
	c, err := New(base, "lalab_pat_test", opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestABaseAddressWithoutTLSIsOnlyAllowedForThisMachine(t *testing.T) {
	ok := []string{"https://lalabase.com", "https://example.com/sub/", "http://localhost:3000", "http://127.0.0.1:3000", "http://[::1]:3000"}
	refused := []string{"http://lalabase.com", "http://10.0.0.5:3000", "http://localhost.evil.example", "http://127.0.0.1.evil.example", "ftp://lalabase.com", "https://user:pw@lalabase.com",
		"https://lalabase.com/?x=1", "https://lalabase.com/#f", "lalabase.com", "", "https://"}
	for _, base := range ok {
		if _, err := New(base, "lalab_pat_test"); err != nil {
			t.Errorf("%q refused: %v", base, err)
		}
	}
	for _, base := range refused {
		if _, err := New(base, "lalab_pat_test"); err == nil {
			t.Errorf("%q accepted", base)
		}
	}
}

func TestATokenWithLineBreaksOrSpacesIsRefused(t *testing.T) {
	for _, token := range []string{"", "lalab pat", "lalab_pat_x\r\nX-Evil: 1", "lalab_pat_\x7f"} {
		if _, err := New("https://lalabase.com", token); err == nil {
			t.Errorf("token %q accepted", token)
		}
	}
}

func TestRequestsCarryTokenAgentAndAcceptHeaders(t *testing.T) {
	var got http.Header
	var path string
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got, path = r.Header.Clone(), r.URL.Path
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	c := clientAt(t, srv.URL+"/sub", WithVersion("v0.1.0"))
	if ans := c.Get(context.Background(), "/api/v1/me"); ans.Class != Object {
		t.Fatalf("got %v %s", ans.Class, ans.Message)
	}
	if path != "/sub/api/v1/me" {
		t.Errorf("path %q, want the base path kept", path)
	}
	if got.Get("Authorization") != "Bearer lalab_pat_test" || got.Get("Accept") != "application/json" || got.Get("User-Agent") != "lalabase/v0.1.0" {
		t.Errorf("headers %v", got)
	}
}

func TestAPathOutsideTheAPIIsRefusedBeforeAnyRequest(t *testing.T) {
	hits := 0
	srv := serve(t, func(http.ResponseWriter, *http.Request) { hits++ })
	c := clientAt(t, srv.URL)
	for _, p := range []string{"/users/sign_in", "api/v1/me", "/api/../admin", "https://evil.example/api/v1/me", "//evil.example/api"} {
		if ans := c.Get(context.Background(), p); ans.Class != Protocol {
			t.Errorf("%q: got %v", p, ans.Class)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests were sent", hits)
	}
}

func TestA429CarriesTheTimeTheWindowOpensAgain(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    time.Time
	}{
		{"reset as epoch", map[string]string{"RateLimit-Reset": strconv.FormatInt(fixedNow.Add(42*time.Second).Unix(), 10)}, fixedNow.Add(42 * time.Second)},
		{"reset in the past", map[string]string{"RateLimit-Reset": strconv.FormatInt(fixedNow.Add(-10*time.Second).Unix(), 10)}, fixedNow},
		{"reset as seconds", map[string]string{"RateLimit-Reset": "17"}, fixedNow.Add(17 * time.Second)},
		{"retry-after seconds", map[string]string{"Retry-After": "9"}, fixedNow.Add(9 * time.Second)},
		{"retry-after first", map[string]string{"Retry-After": "3", "RateLimit-Reset": "50"}, fixedNow.Add(3 * time.Second)},
		{"retry-after as a date", map[string]string{"Retry-After": fixedNow.Add(90 * time.Second).Format(http.TimeFormat)}, fixedNow.Add(90 * time.Second)},
		{"retry-after as a past date", map[string]string{"Retry-After": fixedNow.Add(-90 * time.Second).Format(http.TimeFormat)}, fixedNow},
		{"nothing said", nil, fixedNow.Add(5 * time.Second)},
		{"garbage", map[string]string{"RateLimit-Reset": "soon"}, fixedNow.Add(5 * time.Second)},
		{"absurd retry-after", map[string]string{"Retry-After": "99999999999"}, fixedNow.Add(5 * time.Minute)},
		{"retry-after that overflows a duration", map[string]string{"Retry-After": "9223372037"}, fixedNow.Add(5 * time.Minute)},
		{"reset beyond the epoch range, in seconds", map[string]string{"RateLimit-Reset": "922337203685"}, fixedNow.Add(5 * time.Minute)},
		{"reset at the int64 limit", map[string]string{"RateLimit-Reset": "9223372036854775807"}, fixedNow.Add(5 * time.Minute)},
		{"reset as a far epoch", map[string]string{"RateLimit-Reset": "9223372037"}, fixedNow.Add(5 * time.Minute)},
		{"reset far in the future", map[string]string{"RateLimit-Reset": strconv.FormatInt(fixedNow.Add(30*24*time.Hour).Unix(), 10)}, fixedNow.Add(5 * time.Minute)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte("Zu viele Anfragen. / Too many requests.\n"))
			})
			for _, ans := range []Answer{
				clientAt(t, srv.URL).Get(context.Background(), "/api/v1/me"),
				clientAt(t, srv.URL).CallTool(context.Background(), Call{Tool: "list_tickets"}),
			} {
				if ans.Class != Temporary || !ans.RetryAt.Equal(tc.want) || ans.Status != 429 {
					t.Fatalf("got %v retry %v status %d, want Temporary retry %v", ans.Class, ans.RetryAt, ans.Status, tc.want)
				}
			}
		})
	}
}

func TestStatusCodesOfTheRestReaders(t *testing.T) {
	cases := []struct {
		status int
		body   string
		class  Class
	}{
		{200, `{"data":[]}`, Object},
		{401, `{"error":"unauthorized"}`, Unauthenticated},
		{403, `{"error":"forbidden"}`, NoAccess},
		{404, `{"error":"not_found"}`, NotFound},
		{422, `{"error":"x"}`, InvalidInput},
		{500, `x`, Temporary},
		{502, `x`, Temporary},
		{503, `x`, Temporary},
		{400, `{"error":"bad_request"}`, Protocol},
		{409, `{"error":"x"}`, Protocol},
		// A 403 or 404 that is not the API's own answer (a proxy, a firewall,
		// a wrong base address, Rails' HTML 404) says nothing about the
		// record: it must not make the cache drop it.
		{404, `<html>The page you were looking for doesn't exist</html>`, Protocol},
		{403, `<html>Forbidden</html>`, Protocol},
		{404, `{"error":"forbidden"}`, Protocol},
		{403, `{"error":"not_found"}`, Protocol},
	}
	for _, tc := range cases {
		srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		if got := clientAt(t, srv.URL).Get(context.Background(), "/api/v1/me"); got.Class != tc.class {
			t.Errorf("%d %s: got %v, want %v", tc.status, tc.body, got.Class, tc.class)
		}
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	elsewhere := 0
	target := serve(t, func(http.ResponseWriter, *http.Request) { elsewhere++ })
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/me", http.StatusFound)
	})
	if got := clientAt(t, srv.URL).Get(context.Background(), "/api/v1/me"); got.Class != Protocol {
		t.Fatalf("got %v", got.Class)
	}
	if elsewhere != 0 {
		t.Fatal("the redirect was followed, with the token")
	}
}

func TestAnAnswerThatIsNoJSONIsAProtocolError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>Wartung</html>"))
	})
	c := clientAt(t, srv.URL)
	if got := c.Get(context.Background(), "/api/v1/me"); got.Class != Protocol {
		t.Errorf("REST: got %v", got.Class)
	}
	if got := c.CallTool(context.Background(), Call{Tool: "get_ticket"}); got.Class != Protocol {
		t.Errorf("MCP: got %v", got.Class)
	}
}

// The padding keeps the first bytes valid JSON, so the answer fails on its
// size and not by accident on a cut-off object.
func TestAnOversizedAnswerIsAProtocolError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{}}` + strings.Repeat(" ", 2048)))
	})
	got := clientAt(t, srv.URL, WithMaxBody(1024)).Get(context.Background(), "/api/v1/me")
	if got.Class != Protocol || !strings.Contains(got.Message, "larger than") {
		t.Fatalf("got %v %q", got.Class, got.Message)
	}
	if got := clientAt(t, srv.URL, WithMaxBody(4096)).Get(context.Background(), "/api/v1/me"); got.Class != Object {
		t.Fatalf("within the limit: got %v", got.Class)
	}
}

func TestAConnectionThatBreaksIsTemporary(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte(`{"data":`))
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	})
	if got := clientAt(t, srv.URL).Get(context.Background(), "/api/v1/me"); got.Class != Temporary || got.Err == nil || !got.Sent {
		t.Fatalf("got %v %v sent=%v", got.Class, got.Err, got.Sent)
	}

	// Nothing listens: connection refused.
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	if got := clientAt(t, "http://"+addr).Get(context.Background(), "/api/v1/me"); got.Class != Temporary {
		t.Fatalf("refused connection: got %v", got.Class)
	}
}

func TestATimeoutIsTemporary(t *testing.T) {
	release := make(chan struct{})
	srv := serve(t, func(http.ResponseWriter, *http.Request) { <-release })
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if got := clientAt(t, srv.URL).Get(ctx, "/api/v1/me"); got.Class != Temporary {
		t.Fatalf("got %v", got.Class)
	}
}

func rpcServer(t *testing.T, answer func(id json.RawMessage) string) *httptest.Server {
	t.Helper()
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_, _ = w.Write([]byte(answer(req.ID)))
	})
}

func TestJSONRPCErrors(t *testing.T) {
	cases := map[string]struct {
		body  func(id json.RawMessage) string
		class Class
	}{
		"invalid params": {func(id json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":` + string(id) + `,"error":{"code":-32602,"message":"Tool not found: x"}}`
		}, InvalidInput},
		"internal error": {func(id json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":` + string(id) + `,"error":{"code":-32603,"message":"boom"}}`
		}, Protocol},
		"wrong id": {func(json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":999999,"result":{"content":[{"type":"text","text":"{}"}]}}`
		}, Protocol},
		"no result": {func(id json.RawMessage) string { return `{"jsonrpc":"2.0","id":` + string(id) + `}` }, Protocol},
		"no text content": {func(id json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"content":[]}}`
		}, Protocol},
		"unknown code is a refusal": {func(id json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"isError":true,"content":[{"type":"text","text":"nope"}],"_meta":{"lalabase/code":"quota"}}}`
		}, Refused},
		"unknown legacy text is a refusal": {func(id json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"isError":true,"content":[{"type":"text","text":"Datenbank voll"}]}}`
		}, Refused},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := clientAt(t, rpcServer(t, tc.body).URL).CallTool(context.Background(), Call{Tool: "get_ticket"})
			if got.Class != tc.class {
				t.Fatalf("got %v (%s), want %v", got.Class, got.Message, tc.class)
			}
		})
	}
}

// "Empty" is "not found" only for a tool that addresses one record; for a
// list it is a result like any other.
func TestEmptyMeansNotFoundOnlyForOneRecord(t *testing.T) {
	body := func(id json.RawMessage) string {
		return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"content":[{"type":"text","text":"No symbols matched."}],"_meta":{"lalabase/kind":"empty"}}}`
	}
	srv := rpcServer(t, body)
	if got := clientAt(t, srv.URL).CallTool(context.Background(), Call{Tool: "x", OneRecord: true}); got.Class != NotFound {
		t.Errorf("one record: got %v", got.Class)
	}
	if got := clientAt(t, srv.URL).CallTool(context.Background(), Call{Tool: "x"}); got.Class != Object {
		t.Errorf("a list: got %v", got.Class)
	}
}

// An older server is recognised by the complete text naming the id that was
// asked for, never by a text that only starts like it.
func TestALegacyNotFoundMustMatchExactly(t *testing.T) {
	answer := func(text string) func(json.RawMessage) string {
		return func(id json.RawMessage) string {
			b, _ := json.Marshal(text)
			return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"content":[{"type":"text","text":` + string(b) + `}]}}`
		}
	}
	call := Call{Tool: "get_ticket", OneRecord: true, Args: map[string]any{"ticket_id": "t-1"}}
	if got := clientAt(t, rpcServer(t, answer("No ticket found for t-1.")).URL).CallTool(context.Background(), call); got.Class != NotFound {
		t.Errorf("exact text: got %v", got.Class)
	}
	if got := clientAt(t, rpcServer(t, answer("No ticket found for t-2.")).URL).CallTool(context.Background(), call); got.Class == NotFound {
		t.Error("another id counted as not found")
	}
}

func TestToolCallsSendTheirArgumentsAndNumberedRequests(t *testing.T) {
	var ids []string
	var args map[string]any
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/mcp" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.JSONRPC != "2.0" || req.Method != "tools/call" || req.Params.Name != "post_comment" {
			t.Errorf("request %+v", req)
		}
		ids, args = append(ids, string(req.ID)), req.Params.Arguments
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"content":[{"type":"text","text":"{}"}],"_meta":{"lalabase/kind":"ok"}}}`))
	})
	c := clientAt(t, srv.URL)
	call := Call{Tool: "post_comment", Args: map[string]any{"ticket_id": "t-1", "body": "Hallo", "idempotency_key": "k-1"}}
	for range 2 {
		if got := c.CallTool(context.Background(), call); got.Class != Object {
			t.Fatalf("got %v %s", got.Class, got.Message)
		}
	}
	if args["idempotency_key"] != "k-1" || args["body"] != "Hallo" {
		t.Errorf("arguments %v", args)
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Errorf("request ids %v", ids)
	}
}

func TestToolsListFollowsTheCursor(t *testing.T) {
	page := 0
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Params struct {
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		page++
		if page == 1 {
			if req.Params.Cursor != "" {
				t.Errorf("first page with cursor %q", req.Params.Cursor)
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"tools":[{"name":"a","inputSchema":{"type":"object"}}],"nextCursor":"c2"}}`))
			return
		}
		if req.Params.Cursor != "c2" {
			t.Errorf("second page with cursor %q", req.Params.Cursor)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"tools":[{"name":"b","inputSchema":{"type":"object"}}]}}`))
	})
	tools, ans := clientAt(t, srv.URL).ListTools(context.Background())
	if ans.Class != Object || len(tools) != 2 || tools[1].Name != "b" {
		t.Fatalf("%v %v", ans.Class, tools)
	}
}

// A server that hands out cursors forever does not keep the client busy.
func TestToolsListStopsAfterAPageLimit(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"tools":[],"nextCursor":"again"}}`))
	})
	if _, ans := clientAt(t, srv.URL).ListTools(context.Background()); ans.Class != Protocol || !ans.Sent || ans.Status != 200 {
		t.Fatalf("got %v sent=%v status=%d", ans.Class, ans.Sent, ans.Status)
	}
}

// A certificate that does not check out is no network hiccup: retrying it
// forever hides an attack or a broken setup. It is a protocol error.
func TestACertificateThatDoesNotCheckOutIsAProtocolError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()
	got := clientAt(t, srv.URL).Get(context.Background(), "/api/v1/me")
	if got.Class != Protocol || !strings.Contains(got.Message, "certificate") {
		t.Fatalf("got %v %q", got.Class, got.Message)
	}
	// The same server, trusted: an object.
	if got := clientAt(t, srv.URL, WithTransport(srv.Client().Transport)).Get(context.Background(), "/api/v1/me"); got.Class != Object {
		t.Fatalf("trusted: got %v %q", got.Class, got.Message)
	}
}

func TestAPlainHTTPServerBehindAnHTTPSAddressIsAProtocolError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	got := clientAt(t, strings.Replace(srv.URL, "http://", "https://", 1)).Get(context.Background(), "/api/v1/me")
	if got.Class != Protocol {
		t.Fatalf("got %v %q", got.Class, got.Message)
	}
}

func TestAVersionWithControlCharactersIsRefused(t *testing.T) {
	if _, err := New("https://lalabase.com", "lalab_pat_test", WithVersion("v1\r\nX-Evil: 1")); err == nil {
		t.Fatal("a version with a line break was accepted")
	}
}

// The client's own cancellation (the selection moved on) is no failure of
// the server: the answer is dropped, not marked stale and not retried.
func TestTheClientsOwnCancellationIsCanceled(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	srv := serve(t, func(http.ResponseWriter, *http.Request) {
		close(arrived)
		<-release
	})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-arrived; cancel() }()
	got := clientAt(t, srv.URL).Get(ctx, "/api/v1/me")
	if got.Class != Canceled || !got.Sent {
		t.Fatalf("got %v sent=%v", got.Class, got.Sent)
	}
}

// Whether the request left the client: a write with an unclear outcome is
// reloaded, never sent again (plan, Writes). A refused connection sent
// nothing; a request that got no answer may have been carried out.
func TestSentSaysWhetherTheRequestLeft(t *testing.T) {
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	if got := clientAt(t, "http://"+addr).CallTool(context.Background(), Call{Tool: "post_comment"}); got.Class != Temporary || got.Sent {
		t.Fatalf("refused connection: %v sent=%v", got.Class, got.Sent)
	}

	release := make(chan struct{})
	srv := serve(t, func(http.ResponseWriter, *http.Request) { <-release })
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if got := clientAt(t, srv.URL).CallTool(ctx, Call{Tool: "post_comment"}); got.Class != Temporary || !got.Sent {
		t.Fatalf("no answer after sending: %v sent=%v", got.Class, got.Sent)
	}
}

func TestInitializeSendsTheVersionAndNeedsAServerName(t *testing.T) {
	var clientVersion string
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Params struct {
				ClientInfo struct {
					Version string `json:"version"`
				} `json:"clientInfo"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		clientVersion = req.Params.ClientInfo.Version
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"serverInfo":{"name":"","version":"0.7.0"}}}`))
	})
	_, ans := clientAt(t, srv.URL, WithVersion("v0.2.0")).Initialize(context.Background())
	if ans.Class != Protocol {
		t.Errorf("a server without a name: got %v", ans.Class)
	}
	if clientVersion != "v0.2.0" {
		t.Errorf("clientInfo.version %q", clientVersion)
	}
}

func TestAResultOfNullIsAProtocolError(t *testing.T) {
	srv := rpcServer(t, func(id json.RawMessage) string { return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":null}` })
	if _, ans := clientAt(t, srv.URL).ListTools(context.Background()); ans.Class != Protocol {
		t.Fatalf("got %v", ans.Class)
	}
}

func TestMCPKeepsTheBasePath(t *testing.T) {
	var path string
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"tools":[]}}`))
	})
	if _, ans := clientAt(t, srv.URL+"/sub/").ListTools(context.Background()); ans.Class != Object {
		t.Fatalf("got %v", ans.Class)
	}
	if path != "/sub/api/mcp" {
		t.Fatalf("path %q", path)
	}
}

// A machine code other than "refused" carries no reason, even if the server
// sends one.
func TestAReasonOnlyNamesARefusal(t *testing.T) {
	srv := rpcServer(t, func(id json.RawMessage) string {
		return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"isError":true,"content":[{"type":"text","text":"no"}],"_meta":{"lalabase/code":"forbidden","lalabase/reason":"x"}}}`
	})
	if got := clientAt(t, srv.URL).CallTool(context.Background(), Call{Tool: "get_ticket"}); got.Class != NoAccess || got.Reason != "" {
		t.Fatalf("got %v %q", got.Class, got.Reason)
	}
}

// HTTP/2 can return from a canceled request before the whole body is
// written. Once the headers are out, the server may act on the request, so it
// counts as sent: a write is then reloaded, not sent again.
func TestAnHTTP2RequestCanceledMidBodyCountsAsSent(t *testing.T) {
	for i := range 20 {
		readFirst := make(chan struct{})
		release := make(chan struct{})
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor != 2 {
				t.Errorf("protocol %s", r.Proto)
			}
			one := make([]byte, 1)
			_, _ = r.Body.Read(one)
			close(readFirst)
			<-release
		}))
		srv.EnableHTTP2 = true
		srv.StartTLS()

		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-readFirst; cancel() }()
		big := strings.Repeat("x", 8<<20)
		got := clientAt(t, srv.URL, WithTransport(srv.Client().Transport)).CallTool(ctx, Call{Tool: "post_comment", Args: map[string]any{"body": big}})
		close(release)
		srv.Close()
		if got.Class != Canceled || !got.Sent {
			t.Fatalf("run %d: got %v sent=%v", i, got.Class, got.Sent)
		}
	}
}

// A server that only speaks an outdated TLS version will not change its mind
// on a retry: a protocol error, not a temporary one.
func TestAnIncompatibleTLSVersionIsAProtocolError(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11} //nolint:gosec // the outdated server under test
	srv.StartTLS()
	defer srv.Close()
	got := clientAt(t, srv.URL, WithTransport(srv.Client().Transport)).Get(context.Background(), "/api/v1/me")
	if got.Class != Protocol {
		t.Fatalf("got %v %q (%v)", got.Class, got.Message, got.Err)
	}
}

// A machine code means an error, whether or not isError is set; a code next
// to a kind, or a kind the client does not know, is a refusal. None of them
// may turn into "not found", which would drop the entry.
func TestContradictoryMachineFieldsAreARefusal(t *testing.T) {
	cases := map[string]string{
		"code without isError":    `"_meta":{"lalabase/code":"quota"}`,
		"code next to kind":       `"_meta":{"lalabase/kind":"empty","lalabase/code":"quota"}`,
		"known code next to kind": `"_meta":{"lalabase/kind":"empty","lalabase/code":"not_found"}`,
		"unknown kind":            `"_meta":{"lalabase/kind":"partial"}`,
	}
	for name, meta := range cases {
		t.Run(name, func(t *testing.T) {
			srv := rpcServer(t, func(id json.RawMessage) string {
				return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"content":[{"type":"text","text":"new refusal"}],` + meta + `}}`
			})
			got := clientAt(t, srv.URL).CallTool(context.Background(), Call{Tool: "get_ticket", OneRecord: true, Args: map[string]any{"ticket_id": "t-1"}})
			if got.Class != Refused {
				t.Fatalf("got %v", got.Class)
			}
		})
	}
	// A known code without isError keeps its meaning.
	srv := rpcServer(t, func(id json.RawMessage) string {
		return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"content":[{"type":"text","text":"x"}],"_meta":{"lalabase/code":"read_only"}}}`
	})
	if got := clientAt(t, srv.URL).CallTool(context.Background(), Call{Tool: "post_comment"}); got.Class != ReadOnly {
		t.Fatalf("read_only without isError: got %v", got.Class)
	}
}

// The older server answered "not found" with the address it had normalised:
// "#187" and " 187 " became 187, an id lost its outer spaces.
func TestALegacyNotFoundUsesTheServersNormalisedAddress(t *testing.T) {
	answer := func(text string) *httptest.Server {
		return rpcServer(t, func(id json.RawMessage) string {
			b, _ := json.Marshal(text)
			return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"content":[{"type":"text","text":` + string(b) + `}]}}`
		})
	}
	cases := []struct {
		args map[string]any
		text string
	}{
		{map[string]any{"number": "#187"}, "No ticket found for number 187."},
		{map[string]any{"number": " 187 "}, "No ticket found for number 187."},
		{map[string]any{"number": "0187"}, "No ticket found for number 187."},
		{map[string]any{"ticket_id": " t-1 "}, "No ticket found for t-1."},
	}
	for _, tc := range cases {
		call := Call{Tool: "get_ticket", OneRecord: true, Args: tc.args}
		if got := clientAt(t, answer(tc.text).URL).CallTool(context.Background(), call); got.Class != NotFound {
			t.Errorf("%v: got %v", tc.args, got.Class)
		}
	}
	notification := Call{Tool: "mark_notification_read", OneRecord: true, Args: map[string]any{"notification_id": " n-1 "}}
	if got := clientAt(t, answer("No notification found for n-1.").URL).CallTool(context.Background(), notification); got.Class != NotFound {
		t.Errorf("notification id with spaces: got %v", got.Class)
	}
	// update_ticket (S2) answers a missing ticket with the sentence of
	// get_ticket, so the answer is classified like get_ticket's. A server
	// with update_ticket (0.10.0 on) sends the machine field, so this
	// sentence alone never decides on the real thing; and a write's "not
	// found" keeps the cached ticket (cache.Decide), only a read's removes.
	update := Call{Tool: "update_ticket", OneRecord: true, Args: map[string]any{"ticket_id": " t-1 ", "status": "new"}}
	if got := clientAt(t, answer("No ticket found for t-1.").URL).CallTool(context.Background(), update); got.Class != NotFound {
		t.Errorf("update_ticket: got %v", got.Class)
	}
}

// A catalogue of three valid pages is read whole; the page limit only stops
// a server that never ends.
func TestToolsListReadsThreePages(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Params struct {
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		next := map[string]string{"": "p2", "p2": "p3", "p3": ""}[req.Params.Cursor]
		name := map[string]string{"": "a", "p2": "b", "p3": "c"}[req.Params.Cursor]
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"tools":[{"name":"` + name + `"}],"nextCursor":"` + next + `"}}`))
	})
	tools, ans := clientAt(t, srv.URL).ListTools(context.Background())
	if ans.Class != Object || len(tools) != 3 {
		t.Fatalf("%v %d tools", ans.Class, len(tools))
	}
}

// The TLS text decides only before the handshake succeeded: a handshake the
// two sides cannot agree on stays so. A TLS error after it (a broken record
// in the answer, headers or body) is the network and may be retried.
func TestATLSErrorCountsAsPermanentOnlyBeforeTheHandshake(t *testing.T) {
	ctx := context.Background()
	for _, text := range []string{"remote error: tls: protocol version not supported", "tls: server selected unsupported protocol version 302"} {
		if got := failure(ctx, errors.New(text), false, false, "x"); got.Class != Protocol {
			t.Errorf("before the handshake, %q: got %v", text, got.Class)
		}
	}
	if got := failure(ctx, errors.New("local error: tls: bad record MAC"), true, true, "x"); got.Class != Temporary {
		t.Errorf("after the handshake: got %v", got.Class)
	}
}

// corruptingConn flips the last byte of every write once corrupt is set: a
// TLS record whose authentication tag no longer matches.
type corruptingConn struct {
	net.Conn
	corrupt atomic.Bool
}

func (c *corruptingConn) Write(b []byte) (int, error) {
	if c.corrupt.Load() && len(b) > 0 {
		b = append([]byte(nil), b...)
		b[len(b)-1] ^= 0xff
	}
	return c.Conn.Write(b)
}

// The case of the review: the handshake succeeds, the request arrives, the
// first record of the answer is broken. http.Client.Do reads the answer's
// headers itself, so the error comes out of Do; it must still be temporary.
func TestABrokenRecordAfterTheHandshakeIsTemporary(t *testing.T) {
	ctx := context.Background()
	trusted := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	trusted.StartTLS()
	defer trusted.Close()

	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		raw, err := l.Accept()
		if err != nil {
			return
		}
		conn := &corruptingConn{Conn: raw}
		server := tls.Server(conn, &tls.Config{Certificates: trusted.TLS.Certificates, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
		defer func() { _ = server.Close() }()
		if server.HandshakeContext(ctx) != nil {
			return
		}
		req, err := http.ReadRequest(bufio.NewReader(server))
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, req.Body)
		conn.corrupt.Store(true)
		_, _ = server.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}"))
		time.Sleep(200 * time.Millisecond)
	}()

	got := clientAt(t, "https://"+l.Addr().String(), WithTransport(trusted.Client().Transport)).Get(ctx, "/api/v1/me")
	if got.Class != Temporary || !got.Sent || got.Err == nil || !strings.Contains(got.Err.Error(), "tls") {
		t.Fatalf("got %v sent=%v err=%v", got.Class, got.Sent, got.Err)
	}
}

// An older server (no machine field) answers objects as JSON and missing
// records with its known sentences. A plain text that is neither is a
// refusal, shown and never retried. A newer server that marks the same text
// "ok" is believed: the code tools answer Markdown on purpose.
func TestAnUnknownPlainTextFromAnOlderServerIsARefusal(t *testing.T) {
	plain := func(meta string) *httptest.Server {
		return rpcServer(t, func(id json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"content":[{"type":"text","text":"Service retired permanently."}]` + meta + `}}`
		})
	}
	call := Call{Tool: "get_ticket", OneRecord: true, Args: map[string]any{"ticket_id": "t-1"}}
	if got := clientAt(t, plain("").URL).CallTool(context.Background(), call); got.Class != Refused || got.Message != "Service retired permanently." {
		t.Errorf("older server: got %v %q", got.Class, got.Message)
	}
	if got := clientAt(t, plain(`,"_meta":{"lalabase/kind":"ok"}`).URL).CallTool(context.Background(), call); got.Class != Object {
		t.Errorf("newer server, kind ok: got %v", got.Class)
	}
}

// A 429 whose body breaks off still carries its window: the headers arrived,
// and without RetryAt the queue would retry at once into the closed window.
func TestA429WithABrokenBodyKeepsItsWindow(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("Zu vi"))
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	})
	for _, got := range []Answer{
		clientAt(t, srv.URL).Get(context.Background(), "/api/v1/me"),
		clientAt(t, srv.URL).CallTool(context.Background(), Call{Tool: "get_ticket"}),
	} {
		if got.Class != Temporary || got.Status != 429 || !got.RetryAt.Equal(fixedNow.Add(60*time.Second)) {
			t.Fatalf("got %v status %d retry %v", got.Class, got.Status, got.RetryAt)
		}
	}
}

// Every 429 whose headers arrived carries its window, whatever became of the
// rest of the answer: a body too large, or the caller leaving while it was
// read. The class stays what happened (no retry after a cancel), the window
// is still the server's.
func TestEvery429KeepsItsWindow(t *testing.T) {
	want := fixedNow.Add(60 * time.Second)
	t.Run("body too large", func(t *testing.T) {
		srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
		})
		for _, got := range []Answer{
			clientAt(t, srv.URL, WithMaxBody(1024)).Get(context.Background(), "/api/v1/me"),
			clientAt(t, srv.URL, WithMaxBody(1024)).CallTool(context.Background(), Call{Tool: "get_ticket"}),
		} {
			if got.Class != Protocol || got.Status != 429 || !got.RetryAt.Equal(want) {
				t.Fatalf("got %v status %d retry %v", got.Class, got.Status, got.RetryAt)
			}
			// The window adds to the answer, it does not change it.
			if !got.Sent || got.Err == nil {
				t.Fatalf("sent %v, err %v", got.Sent, got.Err)
			}
		}
	})
	t.Run("canceled while the body is read", func(t *testing.T) {
		headersSent := make(chan struct{})
		release := make(chan struct{})
		srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "60")
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("Zu vi"))
			w.(http.Flusher).Flush()
			headersSent <- struct{}{}
			<-release
		})
		t.Cleanup(func() { close(release) })
		for _, call := range []func(ctx context.Context, c *Client) Answer{
			func(ctx context.Context, c *Client) Answer { return c.Get(ctx, "/api/v1/me") },
			func(ctx context.Context, c *Client) Answer { return c.CallTool(ctx, Call{Tool: "get_ticket"}) },
		} {
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan Answer, 1)
			go func() { done <- call(ctx, clientAt(t, srv.URL)) }()
			select {
			case <-headersSent:
			case <-time.After(2 * time.Second):
				t.Fatal("the server never sent its headers")
			}
			time.Sleep(20 * time.Millisecond) // the client is now in the body
			cancel()
			select {
			case got := <-done:
				if got.Class != Canceled || got.Status != 429 || !got.RetryAt.Equal(want) {
					t.Fatalf("got %v status %d retry %v", got.Class, got.Status, got.RetryAt)
				}
				if !got.Sent || got.Err == nil {
					t.Fatalf("sent %v, err %v", got.Sent, got.Err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the canceled request did not return")
			}
		}
	})
}

// Only a 429 carries a window: another status with a Retry-After header, its
// body broken off or too large, has none (the queue would pause on it).
func TestOnlyA429CarriesAWindow(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusOK} {
		srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
		})
		for _, got := range []Answer{
			clientAt(t, srv.URL, WithMaxBody(1024)).Get(context.Background(), "/api/v1/me"),
			clientAt(t, srv.URL, WithMaxBody(1024)).CallTool(context.Background(), Call{Tool: "get_ticket"}),
		} {
			if !got.RetryAt.IsZero() {
				t.Fatalf("status %d: retry %v", status, got.RetryAt)
			}
		}
	}
}
