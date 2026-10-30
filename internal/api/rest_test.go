package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K0: the ground of the REST writes and the capabilities in /me. No
// adapter of the interface writes over REST yet; the generic write is
// tested against the contract's idempotency cases with a spec of the
// tests' own.

// postSpec stands for the REST adapter of a comment that K1 brings.
var postSpec = Spec{
	Name: "POST /api/v1/tickets/:id/posts", Path: "/api/v1/tickets/:id/posts", Method: http.MethodPost,
	Required: []string{"data.id", "data.ticket_id", "data.body_text", "data.created_at"},
}

// restWriteCase serves one recorded REST answer of the contract and
// records what the client sent.
func restWriteCase(t *testing.T, group, name string) (*API, *[]recordedWrite) {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	ex, ok := file.Contract.REST[group][name]
	if !ok {
		t.Fatalf("the contract has no %s/%s", group, name)
	}
	var sent []recordedWrite
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = append(sent, recordedWrite{method: r.Method, path: r.URL.RequestURI(), key: r.Header.Get("Idempotency-Key"), body: string(body)})
		w.WriteHeader(ex.Response.Status)
		_, _ = w.Write(ex.Response.Body)
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_test")
	if err != nil {
		t.Fatal(err)
	}
	return New(c), &sent
}

type recordedWrite struct{ method, path, key, body string }

type postBody struct {
	Post struct {
		Body string `json:"body"`
	} `json:"post"`
}

type createdPost struct {
	Data struct {
		ID       string `json:"id"`
		TicketID string `json:"ticket_id"`
		BodyText string `json:"body_text"`
	} `json:"data"`
}

func TestAWriteSendsItsBodyOnceEncodedWithTheKey(t *testing.T) {
	path := "/api/v1/tickets/" + login + "/posts"
	var body postBody
	body.Post.Body = "Per REST, einmal."
	for _, name := range []string{"first", "replay"} {
		api, sent := restWriteCase(t, "idempotency", name)
		var out createdPost
		ans := api.write(context.Background(), postSpec, path, body, "contract-rest-1", &out)
		if !ans.OK() || out.Data.TicketID != login || out.Data.BodyText != "Per REST, einmal." || out.Data.ID == "" || !ans.Sent {
			t.Fatalf("%s: %v %q %+v", name, ans.Class, ans.Message, out)
		}
		want := recordedWrite{method: http.MethodPost, path: path, key: "contract-rest-1", body: `{"post":{"body":"Per REST, einmal."}}`}
		if len(*sent) != 1 || (*sent)[0] != want {
			t.Fatalf("%s: sent %+v", name, *sent)
		}
	}
}

// Another request under a used key is an invalid input with its code: the
// action's key was reused for something else, and nothing was written.
func TestAMismatchedRepeatIsAnInvalidInput(t *testing.T) {
	api, _ := restWriteCase(t, "idempotency", "mismatch")
	var body postBody
	body.Post.Body = "Anderer Text"
	var out createdPost
	ans := api.write(context.Background(), postSpec, "/api/v1/tickets/"+login+"/posts", body, "contract-rest-1", &out)
	if ans.Class != transport.InvalidInput || ans.Code != "invalid_input" || !ans.Sent || out.Data.ID != "" {
		t.Fatalf("got %v %q sent=%v %+v", ans.Class, ans.Code, ans.Sent, out)
	}
}

// The recorded refusals of a write keep their class, code and reason
// through the generic write.
func TestTheRecordedRefusalsOfAWrite(t *testing.T) {
	cases := map[string]struct {
		class  transport.Class
		reason string
	}{
		"read_only":     {transport.ReadOnly, ""},
		"forbidden":     {transport.NoAccess, ""},
		"invalid_input": {transport.InvalidInput, ""},
		"refused":       {transport.Refused, "stale_object"},
	}
	for name, want := range cases {
		api, sent := restWriteCase(t, "errors", name)
		var out createdPost
		ans := api.write(context.Background(), postSpec, "/api/v1/tickets/"+login+"/posts", postBody{}, "key-0001", &out)
		if ans.Class != want.class || ans.Reason != want.reason || ans.Code == "" || !ans.Sent || len(*sent) != 1 {
			t.Errorf("%s: got %v/%q/%q sent=%v", name, ans.Class, ans.Code, ans.Reason, ans.Sent)
		}
	}
}

// A write without a usable key, or of a spec that is no REST write, is
// refused before anything is sent.
func TestAWriteIsCheckedBeforeSending(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	api := New(c)
	var out createdPost
	// The adapter refuses, not the transport behind it: the refusal
	// names the adapter.
	for _, key := range []string{"", "short", " \t\n", "key with spaces"} {
		if ans := api.write(context.Background(), postSpec, "/api/v1/tickets/x/posts", postBody{}, key, &out); ans.Class != transport.InvalidInput || ans.Sent || !strings.HasPrefix(ans.Message, postSpec.Name+": ") {
			t.Errorf("key %q: got %v sent=%v %q", key, ans.Class, ans.Sent, ans.Message)
		}
	}
	toolWithMethod := CommentSpec
	toolWithMethod.Method = http.MethodPost
	for _, spec := range []Spec{MeSpec, CommentSpec, toolWithMethod, {Name: "PUT x", Path: "/api/v1/x", Method: http.MethodPut}} {
		if ans := api.write(context.Background(), spec, "/api/v1/tickets/x/posts", postBody{}, "key-0001", &out); ans.Class != transport.InvalidInput || ans.Sent || !strings.HasPrefix(ans.Message, spec.Name+": ") {
			t.Errorf("%s: got %v sent=%v %q", spec.Name, ans.Class, ans.Sent, ans.Message)
		}
	}
	// A body that cannot be encoded.
	if ans := api.write(context.Background(), postSpec, "/api/v1/tickets/x/posts", map[string]any{"x": make(chan int)}, "key-0001", &out); ans.Class != transport.InvalidInput || ans.Sent {
		t.Errorf("a body that is no JSON: got %v sent=%v", ans.Class, ans.Sent)
	}
	if hits != 0 {
		t.Fatalf("%d requests sent", hits)
	}
}

// A written object without a field the client relies on is a protocol
// error that stays sent: the server wrote, and the caller reloads.
func TestAWriteAnswerWithoutARequiredFieldStaysSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"p1"}}`))
	}))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	var out createdPost
	ans := New(c).write(context.Background(), postSpec, "/api/v1/tickets/x/posts", postBody{}, "key-0001", &out)
	if ans.Class != transport.Protocol || !ans.Sent || ans.Status != http.StatusCreated {
		t.Fatalf("got %v sent=%v status=%d", ans.Class, ans.Sent, ans.Status)
	}
}

// /me of a server since R0 names its API version and capabilities; one
// before it names neither and stays valid.
func TestMeReadsTheCapabilitiesWhenTheServerHasThem(t *testing.T) {
	me, ans := restServer(t, "GET /api/v1/me", "ok").Me(context.Background())
	if !ans.OK() || me.APIVersion == "" || !slices.Contains(me.Capabilities, "idempotency") || !slices.Contains(me.Capabilities, "me.show") {
		t.Fatalf("%v %q %+v", ans.Class, me.APIVersion, me.Capabilities)
	}

	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	older := withoutFields(t, file.Contract.REST["GET /api/v1/me"]["ok"].Response.Body, "api_version", "capabilities")
	me, ans = serveBody(t, http.StatusOK, older).Me(context.Background())
	if !ans.OK() || me.APIVersion != "" || me.Capabilities != nil || me.Token.ID == "" {
		t.Fatalf("an older server: %v %q %+v", ans.Class, ans.Message, me)
	}
}

// Present, the two fields follow the rules of a required field: a null or
// a list of the wrong shape is a protocol error, never an empty list read
// as "no capabilities".
func TestNewerFieldsThatArePresentAreChecked(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	recorded := file.Contract.REST["GET /api/v1/me"]["ok"].Response.Body
	for _, edit := range []map[string]any{
		{"capabilities": nil},
		{"capabilities": "tickets.list"},
		{"capabilities": []any{"tickets.list", nil}},
		{"capabilities": []any{1}},
		{"api_version": nil},
		{"api_version": 1},
	} {
		body := withFields(t, recorded, edit)
		if _, ans := serveBody(t, http.StatusOK, body).Me(context.Background()); ans.Class != transport.Protocol {
			t.Errorf("%v: got %v", edit, ans.Class)
		}
	}
}

// A newer field may be absent: the last segment of its path. Present, it
// follows the rules of a required field; what leads to it must be there.
func TestMissingAllowsANewerFieldToBeAbsent(t *testing.T) {
	spec := Spec{Required: []string{"data.id"}, Newer: []string{"data.caps[]", "data.version", "data.extra?"}}
	for payload, want := range map[string][]string{
		`{"data":{"id":"x"}}`:                                          nil,
		`{"data":{"id":"x","caps":[],"version":"1"}}`:                  nil,
		`{"data":{"id":"x","extra":null}}`:                             nil,
		`{"data":{"id":"x","caps":null}}`:                              {"data.caps[]"},
		`{"data":{"id":"x","caps":["a",null]}}`:                        {"data.caps[1]"},
		`{"data":{"id":"x","version":null}}`:                           {"data.version"},
		`{"data":{}}`:                                                  {"data.id"},
		`{"data":{"id":"x","caps":{"a":1}}}`:                           {"data.caps[]"},
		`{"data":{"id":"x","caps":["a"],"version":null,"extra":null}}`: {"data.version"},
	} {
		got, err := spec.Missing([]byte(payload))
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: got %v %v, want %v", payload, got, err, want)
		}
	}
	if got, _ := spec.Missing([]byte(`{}`)); !slices.Equal(got, []string{"data", "data", "data", "data"}) {
		t.Errorf("no data: got %v", got)
	}
	if _, err := spec.Missing([]byte(`no json`)); err == nil {
		t.Error("no JSON was accepted")
	}
}

func withoutFields(t *testing.T, body json.RawMessage, names ...string) []byte {
	t.Helper()
	edit := map[string]any{}
	for _, name := range names {
		edit[name] = deleteField{}
	}
	return withFields(t, body, edit)
}

// deleteField in an edit removes the field.
type deleteField struct{}

// withFields changes fields of the "data" object of a recorded body.
func withFields(t *testing.T, body json.RawMessage, edit map[string]any) []byte {
	t.Helper()
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatal(err)
	}
	data := msg["data"].(map[string]any)
	for name, value := range edit {
		if _, ok := value.(deleteField); ok {
			delete(data, name)
			continue
		}
		data[name] = value
	}
	out, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func serveBody(t *testing.T, status int, body []byte) *API {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_test")
	if err != nil {
		t.Fatal(err)
	}
	return New(c)
}

// TestAttachmentIDIsReadOnBothForms: the id of a ticket's file is what
// opens it (C10); it is a string or nothing, and an entry without one
// stays an entry.
func TestAttachmentIDIsReadOnBothForms(t *testing.T) {
	var a Attachment
	if err := json.Unmarshal([]byte(`{"id":"00000000-0000-4000-8000-000000000001","filename":"a.png"}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.ID == nil || *a.ID != "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("id = %v", a.ID)
	}
	for _, body := range []string{`{"filename":"a.png"}`, `{"id":null,"filename":"a.png"}`, `{"id":7,"filename":"a.png"}`} {
		var b Attachment
		if err := json.Unmarshal([]byte(body), &b); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if b.ID != nil || b.Filename != "a.png" {
			t.Fatalf("%s: %+v", body, b)
		}
	}
}

// TestTheAttachmentIDIsNoRequiredField: an older server sends no id and
// its ticket reads as before. Like the file's metadata the id is read
// leniently, so no spec names it (a Newer field would still report a list
// that is missing as a whole, twice).
func TestTheAttachmentIDIsNoRequiredField(t *testing.T) {
	for _, spec := range []Spec{TicketSpec, TicketRESTSpec, TicketByNumberRESTSpec} {
		if slices.ContainsFunc(spec.Required, func(f string) bool { return strings.HasSuffix(f, "attachments[].id") }) {
			t.Fatalf("%s requires the id", spec.Name)
		}
	}
}
