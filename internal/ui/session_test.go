package ui

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// The harness of the session tests: fakes for the four startup readers and
// the inbox source, fed with the contract's recorded answers and explicit
// changes of them; a real queue and gateway; commands run on the test's
// goroutine (the fakes answer at once), so the tests stay synchronous. The
// mailbox's Wait is bound to a stopped supervisor, so it returns at once.

const wall = 3 * time.Second

var (
	orgA = "00000000-0000-4000-8000-000000000001" // Nordlicht GmbH, as recorded
	orgB = "00000000-0000-4000-8000-000000000002"
)

// recordedData is what the contract records for the startup and the inbox.
type recordedData struct {
	me      api.Me
	orgs    []api.Ref
	server  transport.ServerInfo
	tools   []transport.Tool
	rowTmpl api.Notification
}

func recorded(t *testing.T) recordedData {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	var d recordedData
	var me struct {
		Data api.Me `json:"data"`
	}
	if err := json.Unmarshal(file.Contract.REST["GET /api/v1/me"]["ok"].Response.Body, &me); err != nil {
		t.Fatal(err)
	}
	d.me = me.Data
	var orgs struct {
		Data []api.Ref `json:"data"`
	}
	if err := json.Unmarshal(file.Contract.REST["GET /api/v1/organisations"]["ok"].Response.Body, &orgs); err != nil {
		t.Fatal(err)
	}
	d.orgs = append(orgs.Data, api.Ref{ID: orgB, Name: "Südwind AG"})
	d.server = transport.ServerInfo{Name: "lalabase", Version: file.ServerVersion()}
	tools, err := file.Tools()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		d.tools = append(d.tools, transport.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}
	var body struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	_ = json.Unmarshal(file.Contract.MCP.Calls["list_notifications"]["ok"].Response.Body, &body)
	page, ans := api.DecodeNotifications(transport.Answer{Class: transport.Object, Payload: []byte(body.Result.Content[0].Text), Status: 200, Sent: true})
	if !ans.OK() || len(page.Data) != 1 {
		t.Fatalf("recorded inbox: %v", ans.Message)
	}
	d.rowTmpl = page.Data[0]
	return d
}

// withoutArgument removes one argument from one tool's schema: an older
// server.
func withoutArgument(tools []transport.Tool, tool, arg string) []transport.Tool {
	out := slices.Clone(tools)
	for i, tl := range out {
		if tl.Name != tool {
			continue
		}
		var schema map[string]any
		_ = json.Unmarshal(tl.InputSchema, &schema)
		delete(schema["properties"].(map[string]any), arg)
		out[i].InputSchema, _ = json.Marshal(schema)
	}
	return out
}

func withoutTool(tools []transport.Tool, tool string) []transport.Tool {
	return slices.DeleteFunc(slices.Clone(tools), func(tl transport.Tool) bool { return tl.Name == tool })
}

// rows makes n rows of org from the recorded template, ids numbered from
// first.
func (d recordedData) rows(org string, first, n int) []api.Notification {
	var out []api.Notification
	for i := range n {
		row := d.rowTmpl
		row.ID = idOf(first + i)
		row.Organisation = api.Ref{ID: org, Name: "Org " + org[len(org)-1:]}
		ticket := *d.rowTmpl.Ticket
		ticket.Number = first + i
		row.Ticket = &ticket
		row.Message = "Message " + row.ID[len(row.ID)-4:]
		out = append(out, row)
	}
	return out
}

func idOf(n int) string {
	s := "00000000-0000-4000-8000-"
	digits := []byte("000000000000")
	for i := len(digits) - 1; n > 0 && i >= 0; i-- {
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return s + string(digits)
}

// pageAnswer is the answer of one page: rows, totals and counts, as the
// server would send them, marshalled through the client's own types.
func pageAnswer(page, perPage, total, unreadTotal int, rows []api.Notification, counts map[string]int) transport.Answer {
	var p api.NotificationPage
	p.Data = rows
	if p.Data == nil {
		p.Data = []api.Notification{} // an empty list, never null: the spec requires the list
	}
	p.Meta.Page, p.Meta.PerPage, p.Meta.Total, p.Meta.UnreadTotal = page, perPage, total, unreadTotal
	p.Meta.ByOrganisation = []api.OrganisationCount{}
	for id, n := range counts {
		p.Meta.ByOrganisation = append(p.Meta.ByOrganisation, api.OrganisationCount{ID: id, Name: "Org " + id[len(id)-1:], Unread: n})
	}
	slices.SortFunc(p.Meta.ByOrganisation, func(a, b api.OrganisationCount) int { return compareStrings(a.ID, b.ID) })
	payload, _ := json.Marshal(p)
	return transport.Answer{Class: transport.Object, Payload: payload, Status: 200, Sent: true}
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func failing(class transport.Class, status int) transport.Answer {
	return transport.Answer{Class: class, Status: status, Message: "synthetic " + class.String(), Sent: true}
}

// fakes answer the startup readers and the inbox source; every answer is
// a function the test replaces, every call is counted.
type fakes struct {
	mu       sync.Mutex
	me       func() (api.Me, transport.Answer)
	orgs     func() ([]api.Ref, transport.Answer)
	init     func() (transport.ServerInfo, transport.Answer)
	tools    func() ([]transport.Tool, transport.Answer)
	inbox    func(q api.NotificationQuery) transport.Answer
	meCalls  int
	orgCalls int
	initCall int
	toolCall int
	queries  []api.NotificationQuery
	// gate, when set, holds the inbox source for queries of gateOrg (and of
	// gatePage, when that is set); with
	// ignoreCancel the source answers after the gate even when the request
	// was canceled meanwhile (a server whose answer is already on its way).
	gate         chan struct{}
	gateOrg      string
	gatePage     int
	ignoreCancel bool
}

func (f *fakes) Me(context.Context) (api.Me, transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.meCalls++
	return f.me()
}

func (f *fakes) Organisations(context.Context) ([]api.Ref, transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orgCalls++
	return f.orgs()
}

func (f *fakes) Initialize(context.Context) (transport.ServerInfo, transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initCall++
	return f.init()
}

func (f *fakes) ListTools(context.Context) ([]transport.Tool, transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolCall++
	return f.tools()
}

func (f *fakes) NotificationsAnswer(ctx context.Context, q api.NotificationQuery) transport.Answer {
	f.mu.Lock()
	gate, gateOrg, gatePage, ignoreCancel := f.gate, f.gateOrg, f.gatePage, f.ignoreCancel
	f.queries = append(f.queries, q)
	answer := f.inbox
	f.mu.Unlock()
	if gate != nil && q.OrganisationID == gateOrg && (gatePage == 0 || q.Page == gatePage) {
		if ignoreCancel {
			<-gate
		} else {
			select {
			case <-gate:
			case <-ctx.Done():
				return transport.Answer{Class: transport.Canceled, Message: "canceled"}
			}
		}
	}
	return answer(q)
}

func (f *fakes) inboxCalls() []api.NotificationQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.queries)
}

func (f *fakes) set(fn func(q api.NotificationQuery) transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inbox = fn
}

// harness is one session under test.
type harness struct {
	t      *testing.T
	d      recordedData
	f      *fakes
	live   *worker.Supervisor
	q      *queue.Queue
	events *gateway.Events
	now    time.Time
	m      Model
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	d := recorded(t)
	f := &fakes{
		me: func() (api.Me, transport.Answer) {
			return d.me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		},
		orgs: func() ([]api.Ref, transport.Answer) {
			return d.orgs, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		},
		init: func() (transport.ServerInfo, transport.Answer) {
			return d.server, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		},
		tools: func() ([]transport.Tool, transport.Answer) {
			return d.tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		},
	}
	f.inbox = func(q api.NotificationQuery) transport.Answer {
		return d.defaultInbox(q)
	}
	live := worker.New(context.Background())
	t.Cleanup(func() { live.Stop(wall) })
	// The mailbox's commands wait on a lifetime that is over, so a test can
	// run every command of a batch without blocking.
	dead := worker.New(context.Background())
	dead.Stop(0)
	ev := gateway.NewEvents(dead)
	q := queue.New(queue.WithLifetime(live), queue.WithRetryDelay(0), queue.WithOnRateLimit(ev.RateLimit))
	h := &harness{t: t, d: d, f: f, live: live, q: q, events: ev, now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	h.m = New(WithSession(Session{
		Startup: gateway.NewStartup(live, q, f, f, ev),
		Gateway: gateway.New(live, q, f, gateway.WithEvents(ev)),
		Events:  ev,
		Profile: "default",
		Server:  "https://example.invalid",
	}), WithClock(func() time.Time { return h.now }))
	h.m.width, h.m.height = 120, 24
	return h
}

// defaultInbox is a server with 30 unread rows in A (two pages) and 3 in B.
func (d recordedData) defaultInbox(q api.NotificationQuery) transport.Answer {
	counts := map[string]int{orgA: 30, orgB: 3}
	per := q.PerPage
	if per == 0 {
		per = 25
	}
	switch q.OrganisationID {
	case "":
		all := append(d.rows(orgA, 1, 30), d.rows(orgB, 101, 3)...)
		return pageAnswer(q.Page, per, 33, 33, slice(all, q.Page, per), counts)
	case orgA:
		return pageAnswer(q.Page, per, 30, 30, slice(d.rows(orgA, 1, 30), q.Page, per), map[string]int{orgA: 30})
	case orgB:
		return pageAnswer(q.Page, per, 3, 3, slice(d.rows(orgB, 101, 3), q.Page, per), map[string]int{orgB: 3})
	}
	return transport.Answer{Class: transport.NotFound, Message: "Organisation not found or not accessible.", Sent: true, Status: 200}
}

func slice(rows []api.Notification, page, per int) []api.Notification {
	from := (page - 1) * per
	if from >= len(rows) {
		return nil
	}
	return rows[from:min(len(rows), from+per)]
}

// messages runs a command (a batch, a sequence or one) on the test's
// goroutine and returns every message it produced, in order.
func messages(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch batch := msg.(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, messages(t, c)...)
		}
		return out
	case nil:
		return nil
	}
	return []tea.Msg{msg}
}

// feed gives the model every message and collects the commands it issues.
func (h *harness) feed(msgs ...tea.Msg) []tea.Cmd {
	h.t.Helper()
	var cmds []tea.Cmd
	for _, msg := range msgs {
		var cmd tea.Cmd
		h.m, cmd = step(h.t, h.m, msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// drive runs the commands and feeds their messages, until nothing is left.
func (h *harness) drive(cmds ...tea.Cmd) {
	h.t.Helper()
	for len(cmds) > 0 {
		var next []tea.Cmd
		for _, cmd := range cmds {
			next = append(next, h.feed(messages(h.t, cmd)...)...)
		}
		cmds = next
	}
}

// start runs the whole start: /me, initialize and organisations, tools,
// the global counters.
func (h *harness) start() {
	h.t.Helper()
	h.drive(h.m.Init())
}

func (h *harness) press(name string) tea.Cmd {
	h.t.Helper()
	var cmd tea.Cmd
	h.m, cmd = step(h.t, h.m, press(name))
	return cmd
}

// choose highlights an organisation in the chooser and opens its inbox.
func (h *harness) choose(org string) tea.Cmd {
	h.t.Helper()
	h.m.focus = view.Navigation
	h.m.app.highlight = org
	return h.press("enter")
}

// background runs a command on a supervised goroutine (the only kind the
// interface's tests may start) and delivers its message.
func (h *harness) background(cmd tea.Cmd) <-chan tea.Msg {
	out := make(chan tea.Msg, 1)
	if !h.live.Start("test: command", func(context.Context) { out <- cmd() }) {
		h.t.Fatal("the lifetime is over")
	}
	return out
}

func (h *harness) receive(ch <-chan tea.Msg) tea.Msg {
	h.t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(wall):
		h.t.Fatal("no message")
	}
	return nil
}

func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(wall)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("%s did not happen", what)
		}
		time.Sleep(time.Millisecond)
	}
}
