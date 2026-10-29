package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// What the contract cannot hold: a code the client does not know, an
// answer in another form than the API's, a status that decides alone.
func TestTheCodeOfARestErrorDecidesItsClass(t *testing.T) {
	cases := []struct {
		status int
		body   string
		class  Class
		code   string
		reason string
	}{
		// A code the client does not know is a refusal, never temporary
		// and never "not found", whatever the status says.
		{404, `{"error":"not_found","code":"gone_forever"}`, Refused, "gone_forever", ""},
		{400, `{"error":"bad_request","code":"try_later","reason":"busy"}`, Refused, "try_later", "busy"},
		{422, `{"error":"validation_failed","code":"new_code"}`, Refused, "new_code", ""},
		// The code decides over the status and over "error".
		{403, `{"error":"forbidden","code":"read_only"}`, ReadOnly, "read_only", ""},
		{409, `{"error":"stale_object","code":"refused","reason":"stale_object"}`, Refused, "refused", "stale_object"},
		{403, `{"error":"forbidden","code":"unauthorized"}`, Unauthenticated, "unauthorized", ""},
		// A reason names a refusal only.
		{404, `{"error":"not_found","code":"not_found","reason":"x"}`, NotFound, "not_found", ""},
		// Not the API's form: no "error" beside the code, or a field of
		// another type. The status and "error" decide as before; a 403 or
		// 404 that is not the API's own answer drops nothing.
		{404, `{"code":"not_found"}`, Protocol, "", ""},
		{403, `{"code":"forbidden","message":"proxy"}`, Protocol, "", ""},
		{404, `{"error":"not_found","code":404}`, Protocol, "", ""},
		{404, `{"error":"not_found","code":""}`, NotFound, "", ""},
		{404, `["not_found"]`, Protocol, "", ""},
		{404, `<html>not found</html>`, Protocol, "", ""},
		// These statuses decide alone, whatever the body names.
		{503, `{"error":"x","code":"not_found"}`, Temporary, "not_found", ""},
		{500, `{"error":"x","code":"invalid_input"}`, Temporary, "invalid_input", ""},
		{401, `{"error":"unauthorized","code":"forbidden"}`, Unauthenticated, "forbidden", ""},
		{302, `{"error":"x","code":"not_found"}`, Protocol, "not_found", ""},
		// A success is the object, never an error.
		{200, `{"data":{"error":"x","code":"not_found"}}`, Object, "", ""},
	}
	for _, tc := range cases {
		srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		got := clientAt(t, srv.URL).Get(context.Background(), "/api/v1/tickets/x")
		if got.Class != tc.class || got.Code != tc.code || got.Reason != tc.reason {
			t.Errorf("%d %s: got %v/%q/%q, want %v/%q/%q", tc.status, tc.body, got.Class, got.Code, got.Reason, tc.class, tc.code, tc.reason)
		}
	}
}

// A 429 with a code still pauses: the status decides, the window is kept.
func TestA429WithACodeKeepsItsWindow(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"x","code":"refused"}`))
	})
	got := clientAt(t, srv.URL).Write(context.Background(), http.MethodPost, "/api/v1/tickets/x/posts", json.RawMessage(`{}`), testKey)
	if got.Class != Temporary || !got.RetryAt.Equal(fixedNow.Add(30*time.Second)) || !got.Sent {
		t.Fatalf("got %v %v sent=%v", got.Class, got.RetryAt, got.Sent)
	}
}

func TestAWriteSendsItsKeyBodyAndMethod(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		var got *http.Request
		var body []byte
		srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
			got, body = r, nil
			body, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"p1"}}`))
		})
		ans := clientAt(t, srv.URL, WithVersion("1.2.3")).Write(context.Background(), method, "/api/v1/tickets/x/posts?a=1", json.RawMessage(`{"post":{"body":"Hallo"}}`), "key-0001")
		if !ans.OK() || string(ans.Payload) != `{"data":{"id":"p1"}}` || ans.Status != http.StatusCreated || !ans.Sent {
			t.Fatalf("%s: %v %s %d", method, ans.Class, ans.Payload, ans.Status)
		}
		if got.Method != method || got.URL.RequestURI() != "/api/v1/tickets/x/posts?a=1" || string(body) != `{"post":{"body":"Hallo"}}` {
			t.Fatalf("%s: sent %s %s %s", method, got.Method, got.URL.RequestURI(), body)
		}
		for header, want := range map[string]string{
			"Idempotency-Key": "key-0001", "Content-Type": "application/json", "Accept": "application/json",
			"Authorization": "Bearer lalab_pat_test", "User-Agent": "lalabase/1.2.3",
		} {
			if v := got.Header.Get(header); v != want {
				t.Errorf("%s: %s %q, want %q", method, header, v, want)
			}
		}
	}
}

// A write that cannot carry a usable key, a method that is no write, a
// body that is no JSON object or a path outside the API is refused before
// anything is sent.
func TestAWriteIsRefusedBeforeSending(t *testing.T) {
	hits := 0
	srv := serve(t, func(http.ResponseWriter, *http.Request) { hits++ })
	c := clientAt(t, srv.URL)
	body := json.RawMessage(`{"post":{"body":"Hallo"}}`)
	cases := []struct {
		method, path string
		body         json.RawMessage
		key          string
	}{
		{http.MethodPost, "/api/v1/x", body, ""},
		{http.MethodPost, "/api/v1/x", body, "short"},
		{http.MethodPost, "/api/v1/x", body, " \t\n"},
		{http.MethodPost, "/api/v1/x", body, "key with spaces"},
		{http.MethodPost, "/api/v1/x", body, "key-0001\r\nX-Other: 1"},
		{http.MethodPost, "/api/v1/x", body, "key-0001\x00"},
		{http.MethodPost, "/api/v1/x", body, strings.Repeat("k", 129)},
		{http.MethodGet, "/api/v1/x", body, "key-0001"},
		{http.MethodDelete, "/api/v1/x", body, "key-0001"},
		{http.MethodPut, "/api/v1/x", body, "key-0001"},
		{"post", "/api/v1/x", body, "key-0001"},
		{http.MethodPost, "/api/v1/x", nil, "key-0001"},
		{http.MethodPost, "/api/v1/x", json.RawMessage(`not json`), "key-0001"},
		{http.MethodPost, "/api/v1/x", json.RawMessage(`[1]`), "key-0001"},
		{http.MethodPost, "/api/v1/x", json.RawMessage(`null`), "key-0001"},
		{http.MethodPost, "/other", body, "key-0001"},
		{http.MethodPost, "/api/v1/../mcp", body, "key-0001"},
	}
	for _, tc := range cases {
		got := c.Write(context.Background(), tc.method, tc.path, tc.body, tc.key)
		if got.Class != InvalidInput && got.Class != Protocol || got.Sent || got.Status != 0 {
			t.Errorf("%s %s %s %q: got %v sent=%v", tc.method, tc.path, tc.body, tc.key, got.Class, got.Sent)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests sent", hits)
	}
	// The key, the method and the body are the caller's mistake: an
	// invalid input, so the caller does not read it as the server's.
	if got := c.Write(context.Background(), http.MethodPost, "/api/v1/x", body, "short"); got.Class != InvalidInput {
		t.Fatalf("a short key: got %v", got.Class)
	}
}

// The key form: what the server keeps unchanged after it strips a key,
// within its 255 bytes.
func TestTheKeyForm(t *testing.T) {
	for _, key := range []string{"key-0001", "a.b_c:d-e", strings.Repeat("k", 128), "12345678"} {
		if !ValidKey(key) {
			t.Errorf("%q refused", key)
		}
	}
	for _, key := range []string{"", "1234567", strings.Repeat("k", 129), " key-0001", "key-0001 ", "key/0001", "k\u00e9y-0001", "key-0001\n"} {
		if ValidKey(key) {
			t.Errorf("%q accepted", key)
		}
	}
}

// Sent says whether the server may have acted: nothing left the client on
// a refused connection, the request left when the answer does not come.
func TestAWritesSentSaysWhetherTheRequestLeft(t *testing.T) {
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	body := json.RawMessage(`{}`)
	if got := clientAt(t, "http://"+addr).Write(context.Background(), http.MethodPost, "/api/v1/x", body, testKey); got.Class != Temporary || got.Sent {
		t.Fatalf("refused connection: %v sent=%v", got.Class, got.Sent)
	}

	release := make(chan struct{})
	srv := serve(t, func(http.ResponseWriter, *http.Request) { <-release })
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if got := clientAt(t, srv.URL).Write(ctx, http.MethodPost, "/api/v1/x", body, testKey); got.Class != Temporary || !got.Sent {
		t.Fatalf("no answer after sending: %v sent=%v", got.Class, got.Sent)
	}

	// An answer that arrived broken: the server acted, or may have.
	srv = serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":`))
	})
	if got := clientAt(t, srv.URL).Write(context.Background(), http.MethodPost, "/api/v1/x", body, testKey); got.Class != Protocol || !got.Sent {
		t.Fatalf("a broken answer: %v sent=%v", got.Class, got.Sent)
	}
}

// A read takes only 200 as its object; a write takes 200 and 201. Any
// other success is no answer the client can place.
func TestTheSuccessesOfAReadAndAWrite(t *testing.T) {
	for _, tc := range []struct {
		status      int
		read, write Class
	}{
		{200, Object, Object},
		{201, Protocol, Object},
		{202, Protocol, Protocol},
		{204, Protocol, Protocol},
	} {
		srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			if tc.status != http.StatusNoContent {
				_, _ = w.Write([]byte(`{"data":{}}`))
			}
		})
		c := clientAt(t, srv.URL)
		if got := c.Get(context.Background(), "/api/v1/x"); got.Class != tc.read {
			t.Errorf("read %d: got %v", tc.status, got.Class)
		}
		if got := c.Write(context.Background(), http.MethodPost, "/api/v1/x", json.RawMessage(`{}`), testKey); got.Class != tc.write {
			t.Errorf("write %d: got %v", tc.status, got.Class)
		}
	}
}

// net/http sends a request with an Idempotency-Key again on its own when a
// reused connection breaks after the request went out. A write is sent
// once per attempt of the queue, never again behind its back: the second
// request on a kept-alive connection that the server closes unanswered
// reaches the server once, and its answer says it was sent.
func TestAWriteIsNotSentAgainByTheHTTPClient(t *testing.T) {
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	var mu sync.Mutex
	requests := 0
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				r := bufio.NewReader(conn)
				for {
					req, err := http.ReadRequest(r)
					if err != nil {
						return
					}
					_, _ = io.ReadAll(req.Body)
					mu.Lock()
					requests++
					n := requests
					mu.Unlock()
					if n != 1 {
						return // the request was read; no answer comes
					}
					_, _ = conn.Write([]byte("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: 11\r\n\r\n{\"data\":{}}"))
				}
			}()
		}
	}()
	c := clientAt(t, "http://"+l.Addr().String())
	body := json.RawMessage(`{"post":{"body":"Hallo"}}`)
	if got := c.Write(context.Background(), http.MethodPost, "/api/v1/x", body, testKey); !got.OK() {
		t.Fatalf("first: %v %s", got.Class, got.Message)
	}
	got := c.Write(context.Background(), http.MethodPost, "/api/v1/x", body, "key-contract-0002")
	if got.Class != Temporary || !got.Sent {
		t.Fatalf("second: %v sent=%v", got.Class, got.Sent)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("the server read %d requests, want 2", requests)
	}
}
