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
	// Since 0.7.1 a ticket without read access answers like a missing one.
	"get_ticket/hidden": {class: NotFound, legacyClass: NotFound},
	// S1 (0.9.0). A server older than 0.7.0 never gets these arguments
	// (feature detection), so its class only says the text is not one it
	// knows: "No comment found" names a comment, not the ticket.
	"get_ticket/page_newest":    {class: Object, legacyClass: Object},
	"get_ticket/one_post":       {class: Object, legacyClass: Object},
	"get_ticket/post_not_found": {class: NotFound, legacyClass: Refused},
	"get_ticket/invalid_paging": {class: InvalidInput, legacyClass: Refused},

	"search_knowledge/ok":            {class: Object, legacyClass: Object},
	"search_knowledge/empty":         {class: Object, legacyClass: Object},
	"search_knowledge/invalid_input": {class: InvalidInput, legacyClass: Refused},
	"search_knowledge/not_found":     {class: NotFound, legacyClass: NotFound},

	"post_comment/ok":            {class: Object, legacyClass: Object},
	"post_comment/empty":         {class: NotFound, legacyClass: NotFound},
	"post_comment/invalid_input": {class: InvalidInput, legacyClass: Refused},
	"post_comment/read_only":     {class: ReadOnly, legacyClass: ReadOnly},
	"post_comment/not_found":     {class: NotFound, legacyClass: NotFound},
	"post_comment/forbidden":     {class: NoAccess, legacyClass: NoAccess},

	"list_notifications/ok":                    {class: Object, legacyClass: Object},
	"list_notifications/empty":                 {class: Object, legacyClass: Object},
	"list_notifications/invalid_input":         {class: InvalidInput, legacyClass: Refused},
	"list_notifications/invalid_input_in_tool": {class: InvalidInput, legacyClass: Refused},
	"list_notifications/not_found":             {class: NotFound, legacyClass: NotFound},
	// S1 (0.8.0): organisation_id. An older server never gets it.
	"list_notifications/by_organisation":        {class: Object, legacyClass: Object},
	"list_notifications/organisation_not_found": {class: NotFound, legacyClass: Refused},

	"mark_notification_read/ok":               {class: Object, legacyClass: Object},
	"mark_notification_read/empty":            {class: NotFound, legacyClass: NotFound},
	"mark_notification_read/invalid_input":    {class: InvalidInput, legacyClass: Refused},
	"mark_notification_read/read_only":        {class: ReadOnly, legacyClass: ReadOnly},
	"mark_notification_read/refused_archived": {class: Refused, reason: "archived", legacyClass: Refused},

	// S2 (0.10.0): update_ticket, one change per call. Its refusals carry
	// a reason. An older server never gets the call (feature detection),
	// so its class only says whether the text is one the client knows.
	"update_ticket/ok_status":              {class: Object, legacyClass: Object},
	"update_ticket/ok_assign":              {class: Object, legacyClass: Object},
	"update_ticket/ok_title":               {class: Object, legacyClass: Object},
	"update_ticket/ok_assign_beside_stale": {class: Object, legacyClass: Object},
	"update_ticket/mixed":                  {class: InvalidInput, legacyClass: Refused},
	"update_ticket/status_not_allowed":     {class: Refused, reason: "value_not_allowed", legacyClass: Refused},
	"update_ticket/value_not_allowed":      {class: Refused, reason: "value_not_allowed", legacyClass: Refused},
	"update_ticket/no_status_field":        {class: Refused, reason: "no_status_field", legacyClass: Refused},
	"update_ticket/empty":                  {class: NotFound, legacyClass: NotFound},
	"update_ticket/hidden":                 {class: NotFound, legacyClass: NotFound},
	"update_ticket/forbidden":              {class: NoAccess, legacyClass: NoAccess},
	"update_ticket/not_found":              {class: NotFound, legacyClass: NotFound},
	"update_ticket/read_only":              {class: ReadOnly, legacyClass: ReadOnly},

	// S2b (0.11.0): post_comment with format. The SDK refuses a format it
	// does not know before the tool runs ("Invalid arguments: ..."), and
	// the dispatcher gives that the machine code of an invalid input.
	"post_comment_formats/plain":             {class: Object, legacyClass: Object},
	"post_comment_formats/html":              {class: Object, legacyClass: Object},
	"post_comment_formats/private":           {class: Object, legacyClass: Object},
	"post_comment_formats/invalid_format":    {class: InvalidInput, legacyClass: Refused},
	"post_comment_formats/format_not_string": {class: InvalidInput, legacyClass: Refused},
	"post_comment_formats/nul":               {class: InvalidInput, legacyClass: Refused},
	"post_comment_formats/whitespace_only":   {class: InvalidInput, legacyClass: Refused},
	"post_comment_formats/read_only":         {class: ReadOnly, legacyClass: ReadOnly},
	"post_comment_formats/hidden":            {class: NotFound, legacyClass: NotFound},
	"post_comment_formats/forbidden":         {class: NoAccess, legacyClass: NoAccess},

	// S6 (0.12.0): the booked time of a ticket. A server before 0.12.0
	// has no such tool and is never asked (feature detection), so the
	// legacy classes only say how its texts would read without the
	// machine field. A ticket the token cannot read, or none, is "not
	// found", as for get_ticket.
	"summarize_time_sessions/ok_own":      {class: Object, legacyClass: Object},
	"summarize_time_sessions/ok_total":    {class: Object, legacyClass: Object},
	"summarize_time_sessions/ok_own_tier": {class: Object, legacyClass: Object},
	// Without the machine field its text is not one the client knows.
	"summarize_time_sessions/empty":        {class: NotFound, legacyClass: Refused},
	"summarize_time_sessions/refused":      {class: Refused, legacyClass: Refused},
	"summarize_time_sessions/invalid_date": {class: Refused, legacyClass: Refused},
	"summarize_time_sessions/not_found":    {class: NotFound, legacyClass: NotFound},

	// 0.13.0: the people of an organisation. The client does not use the
	// two tools yet; their cases are classified all the same, so a tool it
	// adopts later starts from a decision. A server before 0.13.0 has
	// neither tool, so the legacy classes only say how the texts read
	// without the machine field: none of them is one the client knows
	// ("Organisation not found ..." is not the project's sentence).
	"find_people/ok":                             {class: Object, legacyClass: Object},
	"find_people/ok_query":                       {class: Object, legacyClass: Object},
	"find_people/ok_kind":                        {class: Object, legacyClass: Object},
	"find_people/empty":                          {class: Object, legacyClass: Object},
	"find_people/invalid_input":                  {class: InvalidInput, legacyClass: Refused},
	"find_people/invalid_input_kind":             {class: InvalidInput, legacyClass: Refused},
	"find_people/invalid_input_missing_argument": {class: InvalidInput, legacyClass: Refused},
	"find_people/not_found":                      {class: NotFound, legacyClass: Refused},
	"find_people/not_found_project_token":        {class: NotFound, legacyClass: Refused},
	"find_people/refused":                        {class: Refused, reason: "people_directory", legacyClass: Refused},

	"get_person/ok":                      {class: Object, legacyClass: Object},
	"get_person/ok_member":               {class: Object, legacyClass: Object},
	"get_person/empty":                   {class: NotFound, legacyClass: Refused},
	"get_person/hidden":                  {class: NotFound, legacyClass: Refused},
	"get_person/invalid_input":           {class: InvalidInput, legacyClass: Refused},
	"get_person/not_found":               {class: NotFound, legacyClass: Refused},
	"get_person/not_found_project_token": {class: NotFound, legacyClass: Refused},
	"get_person/refused":                 {class: Refused, reason: "people_directory", legacyClass: Refused},
}

// The tools that address one record, and the argument naming it. Their empty
// answer is "not found"; for every other tool it is an empty result.
var oneRecord = map[string]string{
	"get_ticket":             "ticket_id",
	"post_comment":           "ticket_id",
	"update_ticket":          "ticket_id",
	"mark_notification_read": "notification_id",
	// The booked time is asked of one ticket: no ticket is not found.
	"summarize_time_sessions": "ticket",
	"get_person":              "id",
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

func TestTheRecordedServerAnswersInitializeAndToolsList(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	srv := replay(t, 200, file.Contract.MCP.Initialize.Response.Body)
	info, ans := newClient(t, srv.URL).Initialize(context.Background())
	if ans.Class != Object || info.Name != "lalabase" || info.Version != "0.13.0" {
		t.Fatalf("initialize: %v %+v", ans.Class, info)
	}

	srv = replay(t, 200, file.Contract.MCP.ToolsList.Response.Body)
	tools, ans := newClient(t, srv.URL).ListTools(context.Background())
	if ans.Class != Object || len(tools) != 17 || tools[0].Name != "search_code_symbols" {
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
