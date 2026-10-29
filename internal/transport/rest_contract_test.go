package transport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/contract"
)

// restExpected is the class, code and reason the transport must give a
// recorded REST answer, and the class for a server before the machine
// field (R0): the same answer with code and reason stripped. Such a
// server is placed as before: 403 and 404 by their "error", 422 as an
// invalid input, the rest of the client errors a protocol error.
type restExpected struct {
	class       Class
	code        string
	reason      string
	legacyClass Class
}

// Every recorded REST answer of the contract, by group and case. A case the
// contract gains without an entry here turns TestEveryRecordedRestCaseIsClassified
// red, as for the tools.
var restCases = map[string]restExpected{
	"GET /api/v1/me/ok":           {class: Object, legacyClass: Object},
	"GET /api/v1/me/ok_read_only": {class: Object, legacyClass: Object},
	"GET /api/v1/me/unauthorized": {class: Unauthenticated, code: "unauthorized", legacyClass: Unauthenticated},

	"GET /api/v1/organisations/ok":           {class: Object, legacyClass: Object},
	"GET /api/v1/organisations/empty":        {class: Object, legacyClass: Object},
	"GET /api/v1/organisations/unauthorized": {class: Unauthenticated, code: "unauthorized", legacyClass: Unauthenticated},

	"GET /api/v1/projects/ok":                  {class: Object, legacyClass: Object},
	"GET /api/v1/projects/ok_one_organisation": {class: Object, legacyClass: Object},
	"GET /api/v1/projects/empty":               {class: Object, legacyClass: Object},
	"GET /api/v1/projects/unauthorized":        {class: Unauthenticated, code: "unauthorized", legacyClass: Unauthenticated},

	// The attachments of a missing ticket and of one the token cannot
	// read (S5) answer alike, 404.
	"GET /api/v1/tickets/:id/attachments/ok":           {class: Object, legacyClass: Object},
	"GET /api/v1/tickets/:id/attachments/ok_read_only": {class: Object, legacyClass: Object},
	"GET /api/v1/tickets/:id/attachments/empty":        {class: Object, legacyClass: Object},
	"GET /api/v1/tickets/:id/attachments/not_found":    {class: NotFound, code: "not_found", legacyClass: NotFound},
	"GET /api/v1/tickets/:id/attachments/hidden":       {class: NotFound, code: "not_found", legacyClass: NotFound},

	// R0: the error codes. A read-only token was "no access" before the
	// code; a missing parameter and a refusal of the server were no
	// class the client could place.
	"errors/read_only":                {class: ReadOnly, code: "read_only", legacyClass: NoAccess},
	"errors/forbidden":                {class: NoAccess, code: "forbidden", legacyClass: NoAccess},
	"errors/not_found":                {class: NotFound, code: "not_found", legacyClass: NotFound},
	"errors/invalid_input":            {class: InvalidInput, code: "invalid_input", legacyClass: Protocol},
	"errors/invalid_input_validation": {class: InvalidInput, code: "invalid_input", legacyClass: InvalidInput},
	"errors/refused":                  {class: Refused, code: "refused", reason: "stale_object", legacyClass: Protocol},

	// R0: a write's idempotency key. The repeat answers the first answer
	// again; another request under the same key is an invalid input.
	"idempotency/first":    {class: Object, legacyClass: Object},
	"idempotency/replay":   {class: Object, legacyClass: Object},
	"idempotency/mismatch": {class: InvalidInput, code: "invalid_input", legacyClass: InvalidInput},

	// The people of an organisation (core #77). Not read by the client
	// yet; classified so that adopting them starts from a decision.
	"GET /api/v1/organisations/:organisation_id/people/ok":                      {class: Object, legacyClass: Object},
	"GET /api/v1/organisations/:organisation_id/people/ok_query":                {class: Object, legacyClass: Object},
	"GET /api/v1/organisations/:organisation_id/people/ok_paged":                {class: Object, legacyClass: Object},
	"GET /api/v1/organisations/:organisation_id/people/empty":                   {class: Object, legacyClass: Object},
	"GET /api/v1/organisations/:organisation_id/people/invalid_input":           {class: InvalidInput, code: "invalid_input", legacyClass: Protocol},
	"GET /api/v1/organisations/:organisation_id/people/invalid_input_kind":      {class: InvalidInput, code: "invalid_input", legacyClass: Protocol},
	"GET /api/v1/organisations/:organisation_id/people/forbidden":               {class: NoAccess, code: "forbidden", legacyClass: NoAccess},
	"GET /api/v1/organisations/:organisation_id/people/not_found":               {class: NotFound, code: "not_found", legacyClass: NotFound},
	"GET /api/v1/organisations/:organisation_id/people/not_found_project_token": {class: NotFound, code: "not_found", legacyClass: NotFound},
	"GET /api/v1/organisations/:organisation_id/people/unauthorized":            {class: Unauthenticated, code: "unauthorized", legacyClass: Unauthenticated},

	"GET /api/v1/organisations/:organisation_id/people/:id/ok":                      {class: Object, legacyClass: Object},
	"GET /api/v1/organisations/:organisation_id/people/:id/ok_member":               {class: Object, legacyClass: Object},
	"GET /api/v1/organisations/:organisation_id/people/:id/not_found":               {class: NotFound, code: "not_found", legacyClass: NotFound},
	"GET /api/v1/organisations/:organisation_id/people/:id/hidden":                  {class: NotFound, code: "not_found", legacyClass: NotFound},
	"GET /api/v1/organisations/:organisation_id/people/:id/not_found_organisation":  {class: NotFound, code: "not_found", legacyClass: NotFound},
	"GET /api/v1/organisations/:organisation_id/people/:id/not_found_project_token": {class: NotFound, code: "not_found", legacyClass: NotFound},
	"GET /api/v1/organisations/:organisation_id/people/:id/invalid_input":           {class: InvalidInput, code: "invalid_input", legacyClass: Protocol},
	"GET /api/v1/organisations/:organisation_id/people/:id/forbidden":               {class: NoAccess, code: "forbidden", legacyClass: NoAccess},
}

// restRequest is a recorded REST request: GET without a method, a write
// with its method, its parameters as the JSON body and its key.
type restRequest struct {
	Method         string          `json:"method"`
	Path           string          `json:"path"`
	Params         json.RawMessage `json:"params"`
	IdempotencyKey string          `json:"idempotency_key"`
}

func recordedRequest(t *testing.T, ex contract.Example) restRequest {
	t.Helper()
	var req restRequest
	if err := json.Unmarshal(ex.Request, &req); err != nil || req.Path == "" {
		t.Fatalf("recorded request %s: %v", ex.Request, err)
	}
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	return req
}

// testKey is the key a recorded write without one is sent with: the
// recorded refusals were asked without a key, and the client never
// writes without one.
const testKey = "key-contract-0001"

// replayREST serves one recorded REST answer and checks the request the
// client sent: method, path and query, and for a write the JSON body
// and the idempotency key.
func replayREST(t *testing.T, req restRequest, key string, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer lalab_pat_test" {
			t.Errorf("Authorization header %q", got)
		}
		if r.Method != req.Method || r.URL.RequestURI() != req.Path {
			t.Errorf("sent %s %s, recorded %s %s", r.Method, r.URL.RequestURI(), req.Method, req.Path)
		}
		sent, _ := io.ReadAll(r.Body)
		if req.Method == http.MethodGet {
			if len(sent) != 0 || r.Header.Get("Idempotency-Key") != "" {
				t.Errorf("a read sent a body %q or a key %q", sent, r.Header.Get("Idempotency-Key"))
			}
		} else {
			if got := r.Header.Get("Idempotency-Key"); got != key {
				t.Errorf("Idempotency-Key %q, want %q", got, key)
			}
			if r.Header.Get("Content-Type") != "application/json" || !sameJSON(sent, req.Params) {
				t.Errorf("sent %s (%s), recorded %s", sent, r.Header.Get("Content-Type"), req.Params)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return string(ja) == string(jb)
}

// sendRecorded sends a recorded request the way the client would: a read
// through Get, a write through Write with the recorded key (or testKey).
func sendRecorded(t *testing.T, c *Client, req restRequest) Answer {
	t.Helper()
	if req.Method == http.MethodGet {
		return c.Get(context.Background(), req.Path)
	}
	return c.Write(context.Background(), req.Method, req.Path, req.Params, keyOf(req))
}

func keyOf(req restRequest) string {
	if req.IdempotencyKey != "" {
		return req.IdempotencyKey
	}
	return testKey
}

// withoutCode strips the machine field of R0 from a recorded error body,
// which is what a server before R0 answers.
func withoutCode(t *testing.T, body json.RawMessage) []byte {
	t.Helper()
	var msg any
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatal(err)
	}
	if obj, ok := msg.(map[string]any); ok {
		delete(obj, "code")
		delete(obj, "reason")
	}
	out, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEveryRecordedRestCaseIsClassified(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	var recorded []string
	for group, cases := range file.Contract.REST {
		for name := range cases {
			recorded = append(recorded, group+"/"+name)
		}
	}
	sort.Strings(recorded)
	for _, key := range recorded {
		if _, ok := restCases[key]; !ok {
			t.Errorf("the contract records %s, the table here has no class for it", key)
		}
	}
	for key := range restCases {
		i := strings.LastIndex(key, "/")
		if _, ok := file.Contract.REST[key[:i]][key[i+1:]]; !ok {
			t.Errorf("the table classifies %s, the contract does not record it", key)
		}
	}
}

func TestRecordedRestAnswersAreClassified(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for group, cases := range file.Contract.REST {
		for name, ex := range cases {
			want, ok := restCases[group+"/"+name]
			if !ok {
				continue // reported by TestEveryRecordedRestCaseIsClassified
			}
			req := recordedRequest(t, ex)
			t.Run(group+"/"+name, func(t *testing.T) {
				srv := replayREST(t, req, keyOf(req), ex.Response.Status, ex.Response.Body)
				got := sendRecorded(t, newClient(t, srv.URL), req)
				if got.Class != want.class || got.Code != want.code || got.Reason != want.reason {
					t.Fatalf("got %v/%q/%q (%s), want %v/%q/%q", got.Class, got.Code, got.Reason, got.Message, want.class, want.code, want.reason)
				}
				if got.Class == Object && len(got.Payload) == 0 {
					t.Fatal("an object without payload")
				}
				if got.Status != ex.Response.Status || !got.Sent {
					t.Fatalf("status %d, sent %v", got.Status, got.Sent)
				}
			})
			t.Run(group+"/"+name+"/older-server", func(t *testing.T) {
				srv := replayREST(t, req, keyOf(req), ex.Response.Status, withoutCode(t, ex.Response.Body))
				got := sendRecorded(t, newClient(t, srv.URL), req)
				if got.Class != want.legacyClass || got.Code != "" || got.Reason != "" {
					t.Fatalf("got %v/%q/%q (%s), want %v", got.Class, got.Code, got.Reason, got.Message, want.legacyClass)
				}
			})
		}
	}
}

// The repeat of a write under its key answers the first answer again,
// byte for byte, and the client sends the repeat with the same bytes
// (the core compares the request).
func TestARepeatedWriteGivesTheSameObject(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	first, replay := file.Contract.REST["idempotency"]["first"], file.Contract.REST["idempotency"]["replay"]
	req := recordedRequest(t, first)
	if again := recordedRequest(t, replay); again.IdempotencyKey != req.IdempotencyKey || !sameJSON(again.Params, req.Params) {
		t.Fatalf("the recorded repeat is another request: %+v", again)
	}
	var bodies []string
	answers := []json.RawMessage{first.Response.Body, replay.Response.Body}
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		bodies = append(bodies, r.Header.Get("Idempotency-Key")+" "+string(sent))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(answers[i])
		i++
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	a := c.Write(context.Background(), req.Method, req.Path, req.Params, req.IdempotencyKey)
	b := c.Write(context.Background(), req.Method, req.Path, req.Params, req.IdempotencyKey)
	if !a.OK() || !b.OK() || string(a.Payload) != string(b.Payload) {
		t.Fatalf("first %v %s, repeat %v %s", a.Class, a.Payload, b.Class, b.Payload)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || !strings.HasPrefix(bodies[0], req.IdempotencyKey+" ") {
		t.Fatalf("sent %q", bodies)
	}
}
