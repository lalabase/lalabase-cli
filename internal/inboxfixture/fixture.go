// Package inboxfixture is a loopback stand-in for a Lalabase server, for the
// automated and the manual runs of the interface: the start, the inbox and
// (C1c) the projects and tickets. One handler serves both: a PTY test
// mounts it on a test server, the tool tools/inbox-fixture listens on
// 127.0.0.1 for a person at a terminal.
//
// Its answers are derived from the contract copy (the recorded initialize,
// tools/list, /me, projects, a recorded notification, ticket row and ticket
// as the templates of every row), so a client that works against it works
// against the shapes the server really sends. It accepts one synthetic
// token, binds to the loopback address only, reads no profile and no
// environment, and offers nothing but the reads of C1: writes and the
// other tools are refused, as a scenario to see them refused.
//
// The shipped lalabase does not import it (a source guard holds that).
package inboxfixture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Token is the one credential the fixture accepts. Synthetic: it opens
// nothing anywhere else.
const Token = "lalabase-inbox-fixture-only" //nolint:gosec // synthetic: it opens nothing anywhere

// Scenario is what the fixture does with the second page of organisation
// A's inbox and of Website's tickets (and, for 401, with ticket #2).
type Scenario string

const (
	// Normal: everything succeeds.
	Normal Scenario = "normal"
	// Older: a server before S1: list_notifications without
	// organisation_id, get_ticket without the paging arguments.
	Older Scenario = "older"
	// Temporary: the first two attempts at page 2 fail with 503.
	Temporary Scenario = "temporary"
	// RateLimited: the first attempt at page 2 is a 429 with a short window.
	RateLimited Scenario = "429"
	// Unauthorized: page 2 answers 401, and so does a read of ticket #2.
	Unauthorized Scenario = "401"
)

// Scenarios lists them, for a tool's usage line.
func Scenarios() []Scenario { return []Scenario{Normal, Older, Temporary, RateLimited, Unauthorized} }

// The two organisations: A as the contract records it, B synthetic.
const (
	OrgA = "00000000-0000-4000-8000-000000000001"
	OrgB = "00000000-0000-4000-8000-000000000002"
	// RowsA and RowsB are the unread rows of each: two pages for A.
	RowsA = 30
	RowsB = 3
)

// Request is one request the fixture saw, for a test's assertions. The
// token never appears in it.
type Request struct {
	Method string
	Path   string
	RPC    string         // the JSON-RPC method, for /api/mcp
	Tool   string         // the tool of a tools/call
	Args   map[string]any // its arguments
	Status int
}

// Fixture is the handler.
type Fixture struct {
	scenario   Scenario
	initialize json.RawMessage
	tools      []json.RawMessage
	me         json.RawMessage
	orgs       []map[string]any
	rows       map[string][]map[string]any
	// C1c: the recorded projects of A, the ticket row and the ticket the
	// synthetic ones are made from (tickets.go).
	projectList []map[string]any
	ticketRow   map[string]any
	ticketTmpl  map[string]any

	mu       sync.Mutex
	log      []Request
	attempts map[string]int
}

// contractFile is the part of the contract copy the fixture reads. It is
// parsed here: internal/contract stays a package only tests import.
type contractFile struct {
	Contract struct {
		MCP struct {
			Initialize struct {
				Response struct {
					Body json.RawMessage `json:"body"`
				} `json:"response"`
			} `json:"initialize"`
			ToolsList struct {
				Response struct {
					Body json.RawMessage `json:"body"`
				} `json:"response"`
			} `json:"tools_list"`
			Calls map[string]map[string]struct {
				Response struct {
					Body json.RawMessage `json:"body"`
				} `json:"response"`
			} `json:"calls"`
		} `json:"mcp"`
		REST map[string]map[string]struct {
			Response struct {
				Status int             `json:"status"`
				Body   json.RawMessage `json:"body"`
			} `json:"response"`
		} `json:"rest"`
	} `json:"contract"`
}

// New builds the fixture of a scenario from the contract copy's bytes.
func New(scenario Scenario, contractJSON []byte) (*Fixture, error) {
	if !slices.Contains(Scenarios(), scenario) {
		return nil, fmt.Errorf("inbox fixture: unknown scenario %q", string(scenario))
	}
	var file contractFile
	if err := json.Unmarshal(contractJSON, &file); err != nil {
		return nil, fmt.Errorf("inbox fixture: the contract copy does not read: %w", err)
	}
	f := &Fixture{scenario: scenario, attempts: map[string]int{}, rows: map[string][]map[string]any{}}
	var initialize struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(file.Contract.MCP.Initialize.Response.Body, &initialize); err != nil || len(initialize.Result) == 0 {
		return nil, errors.New("inbox fixture: the contract copy records no initialize")
	}
	f.initialize = initialize.Result
	var tools struct {
		Result struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(file.Contract.MCP.ToolsList.Response.Body, &tools); err != nil || len(tools.Result.Tools) == 0 {
		return nil, errors.New("inbox fixture: the contract copy records no tools/list")
	}
	f.tools = tools.Result.Tools
	if scenario == Older {
		if err := f.dropArgument("list_notifications", "organisation_id"); err != nil {
			return nil, err
		}
		for _, arg := range []string{"posts_page", "posts_per_page", "posts_order", "attachments_page", "full_text", "post_id"} {
			if err := f.dropArgument("get_ticket", arg); err != nil {
				return nil, err
			}
		}
	}
	me, ok := file.Contract.REST["GET /api/v1/me"]["ok"]
	if !ok || len(me.Response.Body) == 0 {
		return nil, errors.New("inbox fixture: the contract copy records no /me")
	}
	f.me = me.Response.Body
	var orgs struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(file.Contract.REST["GET /api/v1/organisations"]["ok"].Response.Body, &orgs); err != nil || len(orgs.Data) == 0 {
		return nil, errors.New("inbox fixture: the contract copy records no organisations")
	}
	a := orgs.Data[0]
	if a["id"] != OrgA {
		return nil, fmt.Errorf("inbox fixture: the recorded organisation is %v, not %s", a["id"], OrgA)
	}
	b := map[string]any{}
	for k, v := range a {
		b[k] = v
	}
	b["id"], b["name"], b["subdomain"] = OrgB, "Südwind AG", "suedwind"
	f.orgs = []map[string]any{a, b}
	template, err := recordedRow(file)
	if err != nil {
		return nil, err
	}
	f.rows[OrgA] = synthesise(template, a, 1, RowsA)
	f.rows[OrgB] = synthesise(template, b, 101, RowsB)
	if f.projectList, f.ticketRow, f.ticketTmpl, err = recordedTickets(file); err != nil {
		return nil, err
	}
	return f, nil
}

// recordedRow is the one notification the contract records, as the
// template of every synthetic row.
func recordedRow(file contractFile) (map[string]any, error) {
	ex, ok := file.Contract.MCP.Calls["list_notifications"]["ok"]
	if !ok {
		return nil, errors.New("inbox fixture: the contract copy records no list_notifications")
	}
	var body struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(ex.Response.Body, &body); err != nil || len(body.Result.Content) == 0 {
		return nil, errors.New("inbox fixture: the recorded list_notifications has no text")
	}
	var page struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body.Result.Content[0].Text), &page); err != nil || len(page.Data) == 0 {
		return nil, errors.New("inbox fixture: the recorded list_notifications has no row")
	}
	return page.Data[0], nil
}

// synthesise makes n rows for org from the template, numbered from first.
// Deterministic variations cover what the interface must fall back on: a
// row without a project, one without a ticket, one without an actor, a
// deleted actor (name null), a ticket without a title. Times are fixed,
// in UTC.
func synthesise(template, org map[string]any, first, n int) []map[string]any {
	var rows []map[string]any
	for i := range n {
		row := map[string]any{}
		for k, v := range template {
			row[k] = v
		}
		number := first + i
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", 100000+number)
		title := fmt.Sprintf("Ticket %d of %s", number, org["name"])
		actorName := "Ben Berger"
		row["id"] = id
		row["read"], row["read_at"], row["state"] = false, nil, "queued"
		row["organisation"] = map[string]any{"id": org["id"], "name": org["name"]}
		row["project"] = map[string]any{"id": "00000000-0000-4000-8000-000000000031", "name": "Website"}
		row["ticket"] = map[string]any{"id": fmt.Sprintf("00000000-0000-4000-8000-%012d", 200000+number), "number": number, "title": title, "project_id": "00000000-0000-4000-8000-000000000031"}
		row["actor"] = map[string]any{"id": "00000000-0000-4000-8000-000000000012", "name": actorName}
		row["message"] = fmt.Sprintf("Benachrichtigung: %s hat eine Aktion zu #%d %s durchgeführt", actorName, number, title)
		stamp := fmt.Sprintf("2026-01-05T%02d:%02d:00Z", 9+i/60, i%60)
		row["first_event_at"], row["last_event_at"], row["created_at"] = stamp, stamp, stamp
		switch {
		case i%4 == 3:
			row["project"] = nil
		case i%5 == 4:
			row["ticket"] = nil
		case i%7 == 6:
			row["actor"] = nil
		}
		if i == 1 {
			row["actor"] = map[string]any{"id": "00000000-0000-4000-8000-000000000099", "name": nil}
		}
		if i == 2 {
			row["ticket"].(map[string]any)["title"] = ""
		}
		rows = append(rows, row)
	}
	return rows
}

// dropArgument removes an argument from a tool's input schema.
func (f *Fixture) dropArgument(tool, arg string) error {
	for i, raw := range f.tools {
		var t map[string]any
		if err := json.Unmarshal(raw, &t); err != nil {
			return err
		}
		if t["name"] != tool {
			continue
		}
		schema, _ := t["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		delete(props, arg)
		out, err := json.Marshal(t)
		if err != nil {
			return err
		}
		f.tools[i] = out
		return nil
	}
	return fmt.Errorf("inbox fixture: no tool %s in the contract copy", tool)
}

// Requests is what the fixture saw so far.
func (f *Fixture) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

func (f *Fixture) record(r Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, r)
}

// Listen opens the loopback address alone, on a numeric port (0 for one
// the system picks).
func Listen(port int) (net.Listener, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("inbox fixture: port %d is not a port", port)
	}
	var lc net.ListenConfig
	return lc.Listen(context.Background(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

func (f *Fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req := Request{Method: r.Method, Path: r.URL.Path}
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+Token {
		req.Status = http.StatusUnauthorized
		f.record(req)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me":
		req.Status = http.StatusOK
		f.record(req)
		_, _ = w.Write(f.me)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/organisations":
		req.Status = http.StatusOK
		f.record(req)
		out, _ := json.Marshal(map[string]any{"data": f.orgs})
		_, _ = w.Write(out)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects":
		f.projects(w, r, req)
	case r.Method == http.MethodPost && r.URL.Path == "/api/mcp":
		f.rpc(w, r, req)
	default:
		// Everything else: not here.
		req.Status = http.StatusNotFound
		f.record(req)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
	}
}

// maxBody bounds a request body.
const maxBody = 1 << 20

func (f *Fixture) rpc(w http.ResponseWriter, r *http.Request, req Request) {
	var call struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&call); err != nil {
		req.Status = http.StatusBadRequest
		f.record(req)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad_request"}`))
		return
	}
	req.RPC, req.Tool, req.Args = call.Method, call.Params.Name, call.Params.Arguments
	reply := func(status int, result any, rpcError any) {
		req.Status = status
		f.record(req)
		msg := map[string]any{"jsonrpc": "2.0", "id": call.ID}
		if rpcError != nil {
			msg["error"] = rpcError
		} else {
			msg["result"] = result
		}
		out, _ := json.Marshal(msg)
		w.WriteHeader(status)
		_, _ = w.Write(out)
	}
	switch call.Method {
	case "initialize":
		reply(http.StatusOK, f.initialize, nil)
	case "tools/list":
		reply(http.StatusOK, map[string]any{"tools": f.tools}, nil)
	case "tools/call":
		switch call.Params.Name {
		case "list_notifications":
			f.notifications(w, call.Params.Arguments, reply, req)
		case "list_tickets":
			f.tickets(w, call.Params.Arguments, reply, req)
		case "get_ticket":
			f.ticket(w, call.Params.Arguments, reply, req)
		default:
			reply(http.StatusOK, toolError("refused", "not available in the inbox fixture: "+call.Params.Name), nil)
		}
	default:
		reply(http.StatusOK, nil, map[string]any{"code": -32601, "message": "method not found"})
	}
}

// toolError is a tool answer with the machine field, as the server sends it.
func toolError(code, text string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": true,
		"_meta":   map[string]any{"lalabase/code": code},
	}
}

func toolObject(payload any) map[string]any {
	text, _ := json.Marshal(payload)
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
		"isError": false,
		"_meta":   map[string]any{"lalabase/kind": "ok"},
	}
}

func (f *Fixture) notifications(w http.ResponseWriter, args map[string]any, reply func(int, any, any), req Request) {
	status, _ := args["status"].(string)
	if status != "" && status != "unread" {
		reply(http.StatusOK, toolError("invalid_input", "the inbox fixture serves unread rows only"), nil)
		return
	}
	if _, ok := args["project_id"]; ok {
		reply(http.StatusOK, toolError("refused", "the inbox fixture has no projects"), nil)
		return
	}
	org, _ := args["organisation_id"].(string)
	if org != "" && f.scenario == Older {
		reply(http.StatusOK, toolError("invalid_input", "unknown argument organisation_id"), nil)
		return
	}
	page, perPage := intArg(args, "page", 1), intArg(args, "per_page", 25)
	if page < 1 || perPage < 1 || perPage > 100 {
		reply(http.StatusOK, toolError("invalid_input", "page or per_page out of range"), nil)
		return
	}
	var rows []map[string]any
	counts := map[string]int{}
	switch org {
	case "":
		rows = append(append(rows, f.rows[OrgA]...), f.rows[OrgB]...)
		counts[OrgA], counts[OrgB] = RowsA, RowsB
	case OrgA, OrgB:
		rows = f.rows[org]
		counts[org] = len(rows)
	default:
		reply(http.StatusOK, toolError("not_found", "Organisation not found or not accessible."), nil)
		return
	}
	// The scenario acts on A's page 2, attempt by attempt.
	if org == OrgA && page == 2 && f.scenarioOn(w, "A2", req) {
		return
	}
	from := (page - 1) * perPage
	var data []map[string]any
	if from < len(rows) {
		data = rows[from:min(len(rows), from+perPage)]
	}
	if data == nil {
		data = []map[string]any{}
	}
	var by []map[string]any
	for _, id := range slices.Sorted(mapKeys(counts)) {
		name := "Nordlicht GmbH"
		if id == OrgB {
			name = "Südwind AG"
		}
		by = append(by, map[string]any{"id": id, "name": name, "unread": counts[id]})
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	reply(http.StatusOK, toolObject(map[string]any{
		"data": data,
		"meta": map[string]any{"page": page, "per_page": perPage, "total": total, "unread_total": total, "since": nil, "by_organisation": by},
	}), nil)
}

func mapKeys(m map[string]int) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// intArg reads a JSON number argument.
func intArg(args map[string]any, name string, fallback int) int {
	v, ok := args[name]
	if !ok {
		return fallback
	}
	n, ok := v.(float64)
	if !ok || n != float64(int(n)) {
		return -1
	}
	return int(n)
}

// Describe is one line for a tool's output: the scenario and the address.
func Describe(scenario Scenario, addr net.Addr) string {
	return "inbox fixture: scenario " + string(scenario) + " at http://" + strings.TrimPrefix(addr.String(), "tcp/")
}
