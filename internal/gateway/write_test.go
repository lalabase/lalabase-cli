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

// The write of C1f through the gateway: the recorded answers of
// mark_notification_read, replayed by a server that counts attempts and
// keeps the keys it saw.

type writeServer struct {
	mu       sync.Mutex
	attempts int
	keys     []string
	ids      []string
	// answer picks the recorded case for an attempt (1 the first); "" drops
	// the connection after the request was read.
	answer func(attempt int) string
	api    *api.API
}

func replayWrites(t *testing.T, answer func(attempt int) string) *writeServer {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := &writeServer{answer: answer}
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
		id, _ := req.Params.Arguments["notification_id"].(string)
		s.keys, s.ids = append(s.keys, key), append(s.ids, id)
		s.mu.Unlock()
		name := answer(n)
		if name == "" {
			// The request was read, the answer is lost: the client sees a
			// request that left and no answer.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		ex, ok := file.Contract.MCP.Calls["mark_notification_read"][name]
		if !ok {
			t.Errorf("no recorded case %s", name)
			return
		}
		var body map[string]any
		_ = json.Unmarshal(ex.Response.Body, &body)
		body["id"] = req.ID
		out, _ := json.Marshal(body)
		w.WriteHeader(ex.Response.Status)
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_synthetic")
	if err != nil {
		t.Fatal(err)
	}
	s.api = api.New(c)
	return s
}

func (s *writeServer) seen() (int, []string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts, append([]string(nil), s.keys...), append([]string(nil), s.ids...)
}

// The recorded ids of the contract's mark_notification_read cases.
const (
	recordedUnread  = "00000000-0000-4000-8000-000000000071"
	recordedMissing = "00000000-0000-4000-8000-000000000999"
)

func markGateway(t *testing.T, s *writeServer) (*Gateway, *worker.Supervisor) {
	t.Helper()
	sup := worker.New(context.Background())
	t.Cleanup(func() { sup.Stop(wall) })
	q := queue.New(queue.WithLifetime(sup), queue.WithRetryDelay(0))
	return New(sup, q, s.api, WithEvents(NewEvents(sup))), sup
}

func markResult(t *testing.T, cmd tea.Cmd) MarkReadResult {
	t.Helper()
	select {
	case msg := <-run(cmd):
		r, ok := msg.(MarkReadResult)
		if !ok {
			t.Fatalf("%T", msg)
		}
		return r
	case <-time.After(wall):
		t.Fatal("no result")
	}
	return MarkReadResult{}
}

// A confirmed write: the recorded success names the notification as read;
// the key of the ticket is what the server saw.
func TestMarkReadConfirmsTheRecordedSuccess(t *testing.T) {
	s := replayWrites(t, func(int) string { return "ok" })
	g, _ := markGateway(t, s)
	ticket := MarkReadTicket{Session: 7, Op: 1, Notification: recordedUnread, Key: "lalabase-mark-7-1-1"}
	r := markResult(t, g.MarkRead(context.Background(), ticket))
	if !r.Answer.OK() || r.State.ID != recordedUnread || r.State.State != "read" || r.State.ReadAt == nil || r.MarkReadTicket != ticket {
		t.Fatalf("%+v %v %s", r.State, r.Answer.Class, r.Answer.Message)
	}
	if n, keys, ids := s.seen(); n != 1 || keys[0] != ticket.Key || ids[0] != recordedUnread {
		t.Fatalf("%d attempts, keys %v, ids %v", n, keys, ids)
	}
}

// A success that names another notification, or a state other than read,
// is no confirmation: a protocol answer, still sent, so the model treats
// it as unknown.
func TestASuccessForAnotherNotificationIsNoConfirmation(t *testing.T) {
	s := replayWrites(t, func(int) string { return "ok" })
	g, _ := markGateway(t, s)
	other := "00000000-0000-4000-8000-000000000072"
	r := markResult(t, g.MarkRead(context.Background(), MarkReadTicket{Session: 7, Op: 2, Notification: other, Key: "lalabase-mark-7-2-1"}))
	if r.Answer.Class != transport.Protocol || !r.Answer.Sent || r.State != (api.NotificationState{}) {
		t.Fatalf("%v sent %v state %+v", r.Answer.Class, r.Answer.Sent, r.State)
	}
	if err := CheckMarkedState(MarkReadTicket{Notification: recordedUnread}, api.NotificationState{ID: recordedUnread, State: "queued"}); err == nil {
		t.Fatal("a state other than read confirmed")
	}
	if err := CheckMarkedState(MarkReadTicket{Notification: recordedUnread}, api.NotificationState{ID: recordedUnread, State: "read"}); err != nil {
		t.Fatal(err)
	}
}

// The recorded refusals keep their class and are not retried.
func TestMarkReadPassesTheRefusalsOnUnretried(t *testing.T) {
	for name, class := range map[string]transport.Class{"empty": transport.NotFound, "read_only": transport.ReadOnly, "refused_archived": transport.Refused, "invalid_input": transport.InvalidInput} {
		s := replayWrites(t, func(int) string { return name })
		g, _ := markGateway(t, s)
		r := markResult(t, g.MarkRead(context.Background(), MarkReadTicket{Session: 7, Op: 3, Notification: recordedMissing, Key: "lalabase-mark-7-3-1"}))
		if r.Answer.Class != class || !r.Answer.Sent {
			t.Errorf("%s: %v sent %v", name, r.Answer.Class, r.Answer.Sent)
		}
		if n, _, _ := s.seen(); n != 1 {
			t.Errorf("%s: %d attempts", name, n)
		}
		if name == "refused_archived" && r.Answer.Reason != "archived" {
			t.Errorf("reason %q", r.Answer.Reason)
		}
	}
}

// An answer lost after the request left: one attempt, no retry, the
// answer temporary and sent.
func TestALostAnswerIsNotRetried(t *testing.T) {
	s := replayWrites(t, func(int) string { return "" })
	g, _ := markGateway(t, s)
	r := markResult(t, g.MarkRead(context.Background(), MarkReadTicket{Session: 7, Op: 4, Notification: recordedUnread, Key: "lalabase-mark-7-4-1"}))
	if r.Answer.Class != transport.Temporary || !r.Answer.Sent {
		t.Fatalf("%v sent %v: %s", r.Answer.Class, r.Answer.Sent, r.Answer.Message)
	}
	if n, _, _ := s.seen(); n != 1 {
		t.Fatalf("%d attempts", n)
	}
}

// A ticket without a target or a number is refused before anything is
// sent; an empty key is refused by the adapter, unsent too.
func TestMarkReadRefusesABadTicketBeforeSending(t *testing.T) {
	s := replayWrites(t, func(int) string { return "ok" })
	g, _ := markGateway(t, s)
	for _, ticket := range []MarkReadTicket{
		{Session: 7, Op: 0, Notification: recordedUnread, Key: "lalabase-mark-7-0-1"},
		{Session: 7, Op: 5, Notification: "", Key: "lalabase-mark-7-5-1"},
		{Session: 7, Op: 6, Notification: recordedUnread, Key: ""},
		{Session: 7, Op: 7, Notification: recordedUnread, Key: "short"},
	} {
		r := markResult(t, g.MarkRead(context.Background(), ticket))
		if r.Answer.Class != transport.InvalidInput || r.Answer.Sent || r.MarkReadTicket != ticket {
			t.Errorf("%+v: %v sent %v", ticket, r.Answer.Class, r.Answer.Sent)
		}
	}
	if n, _, _ := s.seen(); n != 0 {
		t.Fatalf("%d attempts", n)
	}
}

// A 401 on the write is recorded in the mailbox, whatever the result's
// class.
func TestA401OnAWriteRevokesTheSession(t *testing.T) {
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
	r := markResult(t, g.MarkRead(context.Background(), MarkReadTicket{Session: ev.Session(), Op: 8, Notification: recordedUnread, Key: "lalabase-mark-7-8-1"}))
	if r.Answer.Class != transport.Unauthenticated || !ev.State().Revoked {
		t.Fatalf("%v revoked %v", r.Answer.Class, ev.State().Revoked)
	}
	// Nothing goes out of a revoked session, not even a write.
	r = markResult(t, g.MarkRead(context.Background(), MarkReadTicket{Session: ev.Session(), Op: 9, Notification: recordedUnread, Key: "lalabase-mark-7-9-1"}))
	if r.Answer.Class != transport.Canceled || r.Answer.Sent {
		t.Fatalf("after the revocation: %v sent %v", r.Answer.Class, r.Answer.Sent)
	}
}
