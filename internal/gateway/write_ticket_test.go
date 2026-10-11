package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// The writes of C2 through the gateway's one write path: the recorded
// answers of update_ticket and post_comment (format plain), replayed by a
// server that counts attempts and keeps the keys and arguments it saw.

// The recorded tickets: #1 of Website (the recorded get_ticket and the
// plain comment), #2 (every recorded update_ticket), and the person the
// write token belongs to.
const (
	recordedProject = "00000000-0000-4000-8000-000000000031"
	recordedLogin   = "00000000-0000-4000-8000-000000000041"
	recordedExport  = "00000000-0000-4000-8000-000000000042"
	recordedAnna    = "00000000-0000-4000-8000-000000000011"
)

type groupServer struct {
	mu       sync.Mutex
	attempts int
	keys     []string
	args     []map[string]any
	tools    []string
	// answer picks the recorded case of the group for an attempt (1 the
	// first); "" drops the connection after the request was read, "429"
	// answers a rate limit whose window is already open.
	answer func(attempt int) string
	api    *api.API
}

// replayGroup serves the recorded answers of one contract group
// (update_ticket, post_comment_formats).
func replayGroup(t *testing.T, group string, answer func(attempt int) string) *groupServer {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := &groupServer{answer: answer}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.attempts++
		n := s.attempts
		key, _ := req.Params.Arguments["idempotency_key"].(string)
		s.keys, s.args, s.tools = append(s.keys, key), append(s.args, req.Params.Arguments), append(s.tools, req.Params.Name)
		s.mu.Unlock()
		switch name := answer(n); name {
		case "":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		case "429":
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
		default:
			ex, ok := file.Contract.MCP.Calls[group][name]
			if !ok {
				t.Errorf("no recorded case %s/%s", group, name)
				return
			}
			var body map[string]any
			_ = json.Unmarshal(ex.Response.Body, &body)
			body["id"] = req.ID
			out, _ := json.Marshal(body)
			w.WriteHeader(ex.Response.Status)
			_, _ = w.Write(out)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_synthetic")
	if err != nil {
		t.Fatal(err)
	}
	s.api = api.New(c)
	return s
}

func (s *groupServer) seen() (int, []string, []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts, append([]string(nil), s.keys...), append([]map[string]any(nil), s.args...)
}

// jumpingClock is time as the queue sees it in these tests: every wait
// passes at once, so the pause after a 429 does not hold the test.
type jumpingClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *jumpingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *jumpingClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.now = c.now.Add(d)
	}
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

func writeGateway(t *testing.T, a *api.API) *Gateway {
	t.Helper()
	sup := worker.New(context.Background())
	t.Cleanup(func() { sup.Stop(wall) })
	q := queue.New(queue.WithLifetime(sup), queue.WithRetryDelay(0), queue.WithClock(&jumpingClock{now: time.Now()}))
	return New(sup, q, a, WithEvents(NewEvents(sup)))
}

func writeResult(t *testing.T, cmd tea.Cmd) WriteResult {
	t.Helper()
	select {
	case msg := <-run(cmd):
		r, ok := msg.(WriteResult)
		if !ok {
			t.Fatalf("%T", msg)
		}
		return r
	case <-time.After(wall):
		t.Fatal("no result")
	}
	return WriteResult{}
}

func ticketWrite(kind WriteKind, ticket, value string, op uint64) WriteTicket {
	return WriteTicket{Session: 7, Op: op, Kind: kind, Key: "lalabase-" + kind.String() + "-7-1-1", Project: recordedProject, Ticket: ticket, Value: value}
}

// Each kind confirms its recorded success: the summary names the ticket
// with the value sent (the person among the assignees for Assign), the
// comment names the ticket; one attempt, the key and the one argument of
// the change on the wire, "me" for the assignment.
func TestWriteConfirmsTheRecordedSuccesses(t *testing.T) {
	cases := []struct {
		group, name string
		ticket      WriteTicket
		arg         string
	}{
		{"update_ticket", "ok_status", ticketWrite(Status, recordedExport, "in_progress", 1), "status"},
		{"update_ticket", "ok_title", ticketWrite(Title, recordedExport, "Export als CSV und XLSX", 2), "title"},
		{"update_ticket", "ok_assign", ticketWrite(Assign, recordedExport, recordedAnna, 3), "add_assignee_ids"},
		{"post_comment_formats", "plain", ticketWrite(Comment, recordedLogin, "line one\nline two\n\nsee <file.txt> &lt;tag&gt;", 4), "body"},
	}
	for _, c := range cases {
		s := replayGroup(t, c.group, func(int) string { return c.name })
		r := writeResult(t, writeGateway(t, s.api).Write(context.Background(), c.ticket))
		if !r.Answer.OK() || r.WriteTicket != c.ticket {
			t.Errorf("%s: %v %s", c.name, r.Answer.Class, r.Answer.Message)
			continue
		}
		switch c.ticket.Kind {
		case Comment:
			if r.Comment.ID == "" || r.Comment.TicketID != recordedLogin || r.Summary.ID != "" {
				t.Errorf("%s: %+v", c.name, r.Comment)
			}
		default:
			if r.Summary.ID != recordedExport || r.Summary.Number != 2 || r.Comment.ID != "" {
				t.Errorf("%s: %+v", c.name, r.Summary)
			}
		}
		n, keys, args := s.seen()
		if n != 1 || keys[0] != c.ticket.Key || args[0]["ticket_id"] != c.ticket.Ticket || args[0]["project_id"] != recordedProject {
			t.Errorf("%s: %d attempts, keys %v, args %v", c.name, n, keys, args)
		}
		if _, ok := args[0][c.arg]; !ok {
			t.Errorf("%s: %s not sent: %v", c.name, c.arg, args[0])
		}
		if c.ticket.Kind == Assign {
			if ids, _ := args[0]["add_assignee_ids"].([]any); len(ids) != 1 || ids[0] != "me" {
				t.Errorf("assign sent %v, want [me]", args[0]["add_assignee_ids"])
			}
		}
		if c.ticket.Kind == Comment && args[0]["format"] != "plain" {
			t.Errorf("comment sent format %v", args[0]["format"])
		}
	}
}

// A success that does not carry the write (another ticket, another status
// or title, the person not among the assignees, a comment of another
// ticket) is no confirmation: a protocol answer, still sent, so the model
// treats it as unknown and reloads instead of resending.
func TestASuccessWithoutTheWriteInItIsNoConfirmation(t *testing.T) {
	cases := []struct {
		group, name string
		ticket      WriteTicket
	}{
		{"update_ticket", "ok_status", ticketWrite(Status, recordedExport, "resolved", 1)},
		{"update_ticket", "ok_status", ticketWrite(Status, recordedLogin, "in_progress", 2)},
		{"update_ticket", "ok_title", ticketWrite(Title, recordedExport, "Export", 3)},
		{"update_ticket", "ok_status", ticketWrite(Assign, recordedExport, recordedAnna, 4)},
		{"update_ticket", "ok_assign", ticketWrite(Assign, recordedExport, "00000000-0000-4000-8000-000000000012", 5)},
		{"post_comment_formats", "plain", ticketWrite(Comment, recordedExport, "x", 6)},
	}
	for _, c := range cases {
		s := replayGroup(t, c.group, func(int) string { return c.name })
		r := writeResult(t, writeGateway(t, s.api).Write(context.Background(), c.ticket))
		if r.Answer.Class != transport.Protocol || !r.Answer.Sent || r.Summary.ID != "" || r.Comment.ID != "" {
			t.Errorf("%s as %s: %v sent %v, summary %+v, comment %+v", c.name, c.ticket.Kind, r.Answer.Class, r.Answer.Sent, r.Summary, r.Comment)
		}
	}
	status := "in_progress"
	row := api.TicketRow{ID: recordedExport, Title: "T", Status: &status, Assignees: []api.Ref{{ID: recordedAnna}}}
	for kind, value := range map[WriteKind]string{Status: "in_progress", Title: "T", Assign: recordedAnna} {
		if err := CheckUpdatedTicket(ticketWrite(kind, recordedExport, value, 1), row); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
	}
	if err := CheckUpdatedTicket(ticketWrite(Status, recordedExport, "in_progress", 1), api.TicketRow{ID: recordedExport}); err == nil {
		t.Error("a summary without a status confirmed a status")
	}
	if err := CheckUpdatedTicket(ticketWrite(Comment, recordedExport, "x", 1), row); err == nil {
		t.Error("a summary confirmed a comment")
	}
	if err := CheckPostedComment(ticketWrite(Comment, recordedExport, "x", 1), api.Comment{ID: "c", TicketID: recordedExport}); err != nil {
		t.Error(err)
	}
	if err := CheckPostedComment(ticketWrite(Comment, recordedExport, "x", 1), api.Comment{TicketID: recordedExport}); err == nil {
		t.Error("a comment without an id confirmed")
	}
	if err := CheckPostedComment(ticketWrite(Status, recordedExport, "x", 1), api.Comment{ID: "c", TicketID: recordedExport}); err == nil {
		t.Error("a comment confirmed a status")
	}
}

// Every recorded refusal keeps its class and reason, one attempt, sent.
func TestWritePassesTheRecordedRefusalsOnUnretried(t *testing.T) {
	type want struct {
		class  transport.Class
		reason string
	}
	updates := map[string]want{
		"mixed": {transport.InvalidInput, ""}, "status_not_allowed": {transport.Refused, "value_not_allowed"},
		"value_not_allowed": {transport.Refused, "value_not_allowed"}, "no_status_field": {transport.Refused, "no_status_field"},
		"empty": {transport.NotFound, ""}, "hidden": {transport.NotFound, ""}, "forbidden": {transport.NoAccess, ""},
		"not_found": {transport.NotFound, ""}, "read_only": {transport.ReadOnly, ""},
	}
	for name, w := range updates {
		s := replayGroup(t, "update_ticket", func(int) string { return name })
		r := writeResult(t, writeGateway(t, s.api).Write(context.Background(), ticketWrite(Status, recordedExport, "new", 1)))
		if r.Answer.Class != w.class || r.Answer.Reason != w.reason || !r.Answer.Sent || r.Summary.ID != "" {
			t.Errorf("update_ticket/%s: %v %q sent %v", name, r.Answer.Class, r.Answer.Reason, r.Answer.Sent)
		}
		if n, _, _ := s.seen(); n != 1 {
			t.Errorf("update_ticket/%s: %d attempts", name, n)
		}
	}
	comments := map[string]want{
		"invalid_format": {transport.InvalidInput, ""}, "format_not_string": {transport.InvalidInput, ""}, "nul": {transport.InvalidInput, ""},
		"whitespace_only": {transport.InvalidInput, ""}, "read_only": {transport.ReadOnly, ""}, "hidden": {transport.NotFound, ""}, "forbidden": {transport.NoAccess, ""},
	}
	for name, w := range comments {
		s := replayGroup(t, "post_comment_formats", func(int) string { return name })
		r := writeResult(t, writeGateway(t, s.api).Write(context.Background(), ticketWrite(Comment, recordedLogin, "x", 1)))
		if r.Answer.Class != w.class || r.Answer.Reason != w.reason || !r.Answer.Sent || r.Comment.ID != "" {
			t.Errorf("post_comment_formats/%s: %v %q sent %v", name, r.Answer.Class, r.Answer.Reason, r.Answer.Sent)
		}
		if n, _, _ := s.seen(); n != 1 {
			t.Errorf("post_comment_formats/%s: %d attempts", name, n)
		}
	}
}

// A lost answer is one attempt, temporary and sent: the server may have
// acted. A 429 on the first attempt is retried once with the same key.
func TestALostAnswerToATicketWriteIsNotRetriedAndA429IsRetriedWithTheSameKey(t *testing.T) {
	for _, kind := range []WriteKind{Status, Comment} {
		group, ticket := "update_ticket", ticketWrite(Status, recordedExport, "in_progress", 1)
		if kind == Comment {
			group, ticket = "post_comment_formats", ticketWrite(Comment, recordedLogin, "x", 2)
		}
		s := replayGroup(t, group, func(int) string { return "" })
		r := writeResult(t, writeGateway(t, s.api).Write(context.Background(), ticket))
		if r.Answer.Class != transport.Temporary || !r.Answer.Sent {
			t.Errorf("%s lost: %v sent %v: %s", kind, r.Answer.Class, r.Answer.Sent, r.Answer.Message)
		}
		if n, _, _ := s.seen(); n != 1 {
			t.Errorf("%s lost: %d attempts", kind, n)
		}
		ok := map[WriteKind]string{Status: "ok_status", Comment: "plain"}[kind]
		s = replayGroup(t, group, func(attempt int) string {
			if attempt == 1 {
				return "429"
			}
			return ok
		})
		r = writeResult(t, writeGateway(t, s.api).Write(context.Background(), ticket))
		if !r.Answer.OK() {
			t.Errorf("%s after a 429: %v %s", kind, r.Answer.Class, r.Answer.Message)
		}
		if n, keys, _ := s.seen(); n != 2 || keys[0] != ticket.Key || keys[1] != ticket.Key {
			t.Errorf("%s after a 429: %d attempts, keys %v", kind, n, keys)
		}
	}
}

// A write without a number, a target, the payload of its kind, a known
// kind or a key of the right form is refused before anything is sent.
func TestWriteRefusesABadTicketBeforeSending(t *testing.T) {
	s := replayGroup(t, "update_ticket", func(int) string { return "ok_status" })
	g := writeGateway(t, s.api)
	good := ticketWrite(Status, recordedExport, "in_progress", 1)
	bad := []WriteTicket{}
	for _, edit := range []func(*WriteTicket){
		func(w *WriteTicket) { w.Op = 0 },
		func(w *WriteTicket) { w.Project = "" },
		func(w *WriteTicket) { w.Ticket = "" },
		func(w *WriteTicket) { w.Value = "" },
		func(w *WriteTicket) { w.Kind = Assign; w.Value = "" },
		func(w *WriteTicket) { w.Kind = Comment; w.Value = "" },
		func(w *WriteTicket) { w.Kind = 0 },
		func(w *WriteTicket) { w.Kind = 99 },
		func(w *WriteTicket) { w.Key = "" },
		func(w *WriteTicket) { w.Key = "short" },
	} {
		w := good
		edit(&w)
		bad = append(bad, w)
	}
	for _, w := range bad {
		r := writeResult(t, g.Write(context.Background(), w))
		if r.Answer.Class != transport.InvalidInput || r.Answer.Sent || r.WriteTicket != w {
			t.Errorf("%+v: %v sent %v", w, r.Answer.Class, r.Answer.Sent)
		}
	}
	if n, _, _ := s.seen(); n != 0 {
		t.Fatalf("%d attempts", n)
	}
	if r := writeResult(t, g.Write(context.Background(), good)); !r.Answer.OK() {
		t.Fatalf("the good ticket: %v", r.Answer.Class)
	}
}

// A 401 on a ticket write revokes the session; nothing goes out of it
// afterwards.
func TestA401OnATicketWriteRevokesTheSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_synthetic")
	if err != nil {
		t.Fatal(err)
	}
	sup := worker.New(context.Background())
	t.Cleanup(func() { sup.Stop(wall) })
	ev := NewEvents(sup)
	g := New(sup, queue.New(queue.WithLifetime(sup), queue.WithRetryDelay(0)), api.New(c), WithEvents(ev))
	w := ticketWrite(Title, recordedExport, "T", 1)
	w.Session = ev.Session()
	if r := writeResult(t, g.Write(context.Background(), w)); r.Answer.Class != transport.Unauthenticated || !ev.State().Revoked {
		t.Fatalf("%v revoked %v", r.Answer.Class, ev.State().Revoked)
	}
	w.Op = 2
	if r := writeResult(t, g.Write(context.Background(), w)); r.Answer.Class != transport.Canceled || r.Answer.Sent {
		t.Fatalf("after the revocation: %v sent %v", r.Answer.Class, r.Answer.Sent)
	}
}
