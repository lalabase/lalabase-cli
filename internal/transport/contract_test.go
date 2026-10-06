package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/contract"
)

// expected is the class the transport must give a recorded answer, once for
// a server that sends the machine field (0.7.0 on) and once for an older one
// that does not (the field stripped). An older server is recognised only by
// its exact, complete texts; whatever else it says is a refusal.
type expected struct {
	class        Class
	reason       string
	legacyClass  Class
	legacyReason string
}

// Every recorded tool answer of the contract, by tool and case. A case the
// contract gains without an entry here turns TestEveryRecordedCaseIsClassified
// red: a new outcome needs a decision, not a default.
var toolCases = map[string]expected{
	"list_tickets/ok":                             {class: Object, legacyClass: Object},
	"list_tickets/empty":                          {class: Object, legacyClass: Object},
	"list_tickets/invalid_input":                  {class: InvalidInput, legacyClass: Refused},
	"list_tickets/invalid_input_missing_argument": {class: InvalidInput, legacyClass: Refused},
	"list_tickets/not_found":                      {class: NotFound, legacyClass: NotFound},

	"get_ticket/ok":            {class: Object, legacyClass: Object},
	"get_ticket/ok_by_number":  {class: Object, legacyClass: Object},
	"get_ticket/empty":         {class: NotFound, legacyClass: NotFound},
	"get_ticket/invalid_input": {class: InvalidInput, legacyClass: Refused},
	"get_ticket/not_found":     {class: NotFound, legacyClass: NotFound},
	"get_ticket/forbidden":     {class: NoAccess, legacyClass: NoAccess},

	"search_knowledge/ok":            {class: Object, legacyClass: Object},
	"search_knowledge/empty":         {class: Object, legacyClass: Object},
	"search_knowledge/invalid_input": {class: InvalidInput, legacyClass: Refused},
	"search_knowledge/not_found":     {class: NotFound, legacyClass: NotFound},

	"post_comment/ok":            {class: Object, legacyClass: Object},
	"post_comment/empty":         {class: NotFound, legacyClass: NotFound},
	"post_comment/invalid_input": {class: InvalidInput, legacyClass: Refused},
	"post_comment/read_only":     {class: ReadOnly, legacyClass: ReadOnly},
	"post_comment/not_found":     {class: NotFound, legacyClass: NotFound},

	"list_notifications/ok":                    {class: Object, legacyClass: Object},
	"list_notifications/empty":                 {class: Object, legacyClass: Object},
	"list_notifications/invalid_input":         {class: InvalidInput, legacyClass: Refused},
	"list_notifications/invalid_input_in_tool": {class: InvalidInput, legacyClass: Refused},
	"list_notifications/not_found":             {class: NotFound, legacyClass: NotFound},

	"mark_notification_read/ok":               {class: Object, legacyClass: Object},
	"mark_notification_read/empty":            {class: NotFound, legacyClass: NotFound},
	"mark_notification_read/invalid_input":    {class: InvalidInput, legacyClass: Refused},
	"mark_notification_read/read_only":        {class: ReadOnly, legacyClass: ReadOnly},
	"mark_notification_read/refused_archived": {class: Refused, reason: "archived", legacyClass: Refused},
}

// The tools that address one record, and the argument naming it. Their empty
// answer is "not found"; for every other tool it is an empty result.
var oneRecord = map[string]string{
	"get_ticket":             "ticket_id",
	"post_comment":           "ticket_id",
	"mark_notification_read": "notification_id",
}

// replay answers every request with one recorded response.
func replay(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer lalab_pat_test" {
			t.Errorf("Authorization header %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T, base string) *Client {
	t.Helper()
	c, err := New(base, "lalab_pat_test")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// withoutMeta removes the machine field from a recorded body, which is what a
// server before 0.7.0 answers.
func withoutMeta(t *testing.T, body json.RawMessage) []byte {
	t.Helper()
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatal(err)
	}
	if result, ok := msg["result"].(map[string]any); ok {
		delete(result, "_meta")
	}
	out, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func callArgs(t *testing.T, ex contract.Example) (string, map[string]any) {
	t.Helper()
	var req struct {
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(ex.Request, &req); err != nil {
		t.Fatal(err)
	}
	return req.Params.Name, req.Params.Arguments
}

func TestEveryRecordedCaseIsClassified(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	var recorded []string
	for tool, cases := range file.Contract.MCP.Calls {
		for name := range cases {
			recorded = append(recorded, tool+"/"+name)
		}
	}
	sort.Strings(recorded)
	for _, key := range recorded {
		if _, ok := toolCases[key]; !ok {
			t.Errorf("the contract records %s, the table here has no class for it", key)
		}
	}
	for key := range toolCases {
		tool, name, _ := strings.Cut(key, "/")
		if _, ok := file.Contract.MCP.Calls[tool][name]; !ok {
			t.Errorf("the table classifies %s, the contract does not record it", key)
		}
	}
}

func TestRecordedToolAnswersAreClassified(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for tool, cases := range file.Contract.MCP.Calls {
		for name, ex := range cases {
			want, ok := toolCases[tool+"/"+name]
			if !ok {
				continue // reported by TestEveryRecordedCaseIsClassified
			}
			callName, args := callArgs(t, ex)
			call := Call{Tool: callName, Args: args, OneRecord: oneRecord[callName] != ""}

			t.Run(tool+"/"+name, func(t *testing.T) {
				srv := replay(t, ex.Response.Status, ex.Response.Body)
				got := newClient(t, srv.URL).CallTool(context.Background(), call)
				if got.Class != want.class || got.Reason != want.reason {
					t.Fatalf("got %v/%q (%s), want %v/%q", got.Class, got.Reason, got.Message, want.class, want.reason)
				}
				if got.Class == Object && len(got.Payload) == 0 {
					t.Fatal("an object without payload")
				}
				if got.Class != Refused && got.Reason != "" {
					t.Fatalf("a reason on a %v answer: %q", got.Class, got.Reason)
				}
				if !got.Sent {
					t.Fatal("an answer that arrived does not count as sent")
				}
			})
			t.Run(tool+"/"+name+"/older-server", func(t *testing.T) {
				srv := replay(t, ex.Response.Status, withoutMeta(t, ex.Response.Body))
				got := newClient(t, srv.URL).CallTool(context.Background(), call)
				if got.Class != want.legacyClass || got.Reason != want.legacyReason {
					t.Fatalf("got %v/%q (%s), want %v/%q", got.Class, got.Reason, got.Message, want.legacyClass, want.legacyReason)
				}
			})
		}
	}
}

func TestRecordedRestAnswersAreClassified(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Class{"ok": Object, "ok_read_only": Object, "ok_one_organisation": Object, "empty": Object, "unauthorized": Unauthenticated}
	for path, cases := range file.Contract.REST {
		for name, ex := range cases {
			class, ok := want[name]
			if !ok {
				t.Errorf("%s/%s: no class in the table", path, name)
				continue
			}
			t.Run(path+"/"+name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(ex.Response.Status)
					_, _ = w.Write(ex.Response.Body)
				}))
				defer srv.Close()
				got := newClient(t, srv.URL).Get(context.Background(), strings.TrimPrefix(path, "GET "))
				if got.Class != class {
					t.Fatalf("got %v (%s), want %v", got.Class, got.Message, class)
				}
			})
		}
	}
}

func TestTheRecordedServerAnswersInitializeAndToolsList(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	srv := replay(t, 200, file.Contract.MCP.Initialize.Response.Body)
	info, ans := newClient(t, srv.URL).Initialize(context.Background())
	if ans.Class != Object || info.Name != "lalabase" || info.Version != "0.7.0" {
		t.Fatalf("initialize: %v %+v", ans.Class, info)
	}

	srv = replay(t, 200, file.Contract.MCP.ToolsList.Response.Body)
	tools, ans := newClient(t, srv.URL).ListTools(context.Background())
	if ans.Class != Object || len(tools) != 13 || tools[0].Name != "search_code_symbols" {
		t.Fatalf("tools/list: %v, %d tools", ans.Class, len(tools))
	}
	if _, ok := tools[6].Properties()["ticket_id"]; !ok || tools[6].Name != "get_ticket" {
		t.Fatalf("schema of %s not read: %v", tools[6].Name, tools[6].Properties())
	}

	srv = replay(t, file.Contract.MCP.Unauthorized.Response.Status, file.Contract.MCP.Unauthorized.Response.Body)
	if _, ans := newClient(t, srv.URL).ListTools(context.Background()); ans.Class != Unauthenticated {
		t.Fatalf("401: got %v", ans.Class)
	}
}
