package transport

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
		{"nothing said", nil, fixedNow.Add(5 * time.Second)},
		{"garbage", map[string]string{"RateLimit-Reset": "soon"}, fixedNow.Add(5 * time.Second)},
		{"absurd retry-after", map[string]string{"Retry-After": "99999999999"}, fixedNow.Add(5 * time.Minute)},
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
	if got := clientAt(t, srv.URL).Get(context.Background(), "/api/v1/me"); got.Class != Temporary || got.Err == nil {
		t.Fatalf("got %v %v", got.Class, got.Err)
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
	if _, ans := clientAt(t, srv.URL).ListTools(context.Background()); ans.Class != Protocol {
		t.Fatalf("got %v", ans.Class)
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
