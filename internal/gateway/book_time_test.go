package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K6: a booking through the gateway's one write path, the recorded answers
// of POST .../time_sessions replayed by a server that counts every attempt
// that reaches it and keeps what each one carried.

const bookGroup = "POST /api/v1/tickets/:ticket_id/time_sessions"

type bookAttempt struct{ method, path, key, body string }

type bookServer struct {
	mu       sync.Mutex
	attempts []bookAttempt
	api      *api.API
}

// replayBooking serves the recorded case answer names for an attempt (1 the
// first); "" drops the connection after the request was read, "429" answers
// a rate limit whose window is already open, anything starting with { is
// that body as a 201.
func replayBooking(t *testing.T, answer func(attempt int) string) *bookServer {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := &bookServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.attempts = append(s.attempts, bookAttempt{method: r.Method, path: r.URL.RequestURI(), key: r.Header.Get("Idempotency-Key"), body: string(body)})
		n := len(s.attempts)
		s.mu.Unlock()
		name := answer(n)
		switch {
		case name == "":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		case name == "429":
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
		case name[0] == '{':
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(name))
		default:
			ex, ok := file.Contract.REST[bookGroup][name]
			if !ok {
				t.Errorf("no recorded case %s", name)
				return
			}
			w.WriteHeader(ex.Response.Status)
			_, _ = w.Write(ex.Response.Body)
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

func (s *bookServer) seen() []bookAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]bookAttempt(nil), s.attempts...)
}

func bookWrite(duration, note string, op uint64) WriteTicket {
	return WriteTicket{Session: 7, Op: op, Kind: BookTime, Key: "lalabase-book-7-1-1", Project: recordedProject, Ticket: recordedLogin, Value: duration, Note: note}
}

const bookPath = "/api/v1/tickets/" + recordedLogin + "/time_sessions"

// The recorded success confirms: one attempt, on the ticket asked for, with
// the key, the duration as typed and no note; the result carries the
// booking and the minutes the server read.
func TestABookingConfirmsTheRecordedSuccess(t *testing.T) {
	s := replayBooking(t, func(int) string { return "ok_minutes_text" })
	w := bookWrite("30", "", 1)
	r := writeResult(t, writeGateway(t, s.api).Write(context.Background(), w))
	if !r.Answer.OK() || r.WriteTicket != w || r.Booking.TicketID != recordedLogin || r.Booking.ID == "" || r.Booking.Minutes != 30 {
		t.Fatalf("%v %s, %+v", r.Answer.Class, r.Answer.Message, r.Booking)
	}
	want := bookAttempt{method: http.MethodPost, path: bookPath, key: w.Key, body: `{"time_session":{"duration_string":"30"}}`}
	if got := s.seen(); len(got) != 1 || got[0] != want {
		t.Fatalf("sent %+v", got)
	}
}

// The note travels with the duration, and the kind's name says what failed.
func TestABookingCarriesItsNote(t *testing.T) {
	s := replayBooking(t, func(int) string { return "ok_hours_text" })
	w := bookWrite("1:30", "Vertrag", 1)
	w.Key = "lalabase-book-7-1-2"
	if r := writeResult(t, writeGateway(t, s.api).Write(context.Background(), w)); !r.Answer.OK() || r.Booking.Minutes != 90 {
		t.Fatalf("%v %+v", r.Answer.Class, r.Booking)
	}
	if got := s.seen(); len(got) != 1 || got[0].body != `{"time_session":{"duration_string":"1:30","description":"Vertrag"}}` {
		t.Fatalf("sent %+v", got)
	}
	if BookTime.String() != "book time" {
		t.Fatalf("named %q", BookTime)
	}
}

// A success counts only when the answer belongs to the booking asked for:
// the ticket, an id, minutes above zero. Anything else is no confirmation:
// a protocol answer, still sent, so the model reloads and never resends.
// What the contract cannot hold: the only hand-written answers here.
func TestASuccessThatDoesNotConfirmIsUnclear(t *testing.T) {
	booking := func(id, ticket string, minutes int) string {
		out, _ := json.Marshal(map[string]any{"data": map[string]any{"id": id, "ticket_id": ticket, "duration_minutes": minutes}})
		return string(out)
	}
	for name, body := range map[string]string{
		"another ticket": booking("x", recordedExport, 30),
		"no ticket":      booking("x", "", 30),
		"an empty id":    booking("", recordedLogin, 30),
		"no minutes":     booking("x", recordedLogin, 0),
		"minus minutes":  booking("x", recordedLogin, -30),
	} {
		s := replayBooking(t, func(int) string { return body })
		r := writeResult(t, writeGateway(t, s.api).Write(context.Background(), bookWrite("30", "", 1)))
		if r.Answer.Class != transport.Protocol || !r.Answer.Sent || r.Booking != (api.TimeBooking{}) {
			t.Errorf("%s: %v sent %v, %+v", name, r.Answer.Class, r.Answer.Sent, r.Booking)
		}
		if n := len(s.seen()); n != 1 {
			t.Errorf("%s: %d attempts", name, n)
		}
	}
	good := api.TimeBooking{ID: "x", TicketID: recordedLogin, Minutes: 1}
	if err := CheckBooking(bookWrite("1", "", 1), good); err != nil {
		t.Errorf("one minute: %v", err)
	}
	if err := CheckBooking(ticketWrite(Comment, recordedLogin, "x", 1), good); err == nil {
		t.Error("a booking confirmed a comment")
	}
}

// Every recorded refusal keeps its class: one attempt, sent, no booking.
func TestABookingPassesTheRecordedRefusalsOnUnretried(t *testing.T) {
	for name, class := range map[string]transport.Class{
		"invalid_input_no_duration": transport.InvalidInput, "invalid_input_over_a_day": transport.InvalidInput,
		"idempotency_mismatch": transport.InvalidInput, "forbidden": transport.NoAccess, "read_only": transport.ReadOnly,
		"not_found": transport.NotFound, "hidden": transport.NotFound,
	} {
		s := replayBooking(t, func(int) string { return name })
		r := writeResult(t, writeGateway(t, s.api).Write(context.Background(), bookWrite("30", "", 1)))
		if r.Answer.Class != class || !r.Answer.Sent || r.Booking != (api.TimeBooking{}) {
			t.Errorf("%s: %v sent %v, %+v", name, r.Answer.Class, r.Answer.Sent, r.Booking)
		}
		if n := len(s.seen()); n != 1 {
			t.Errorf("%s: %d attempts", name, n)
		}
	}
}

// A lost answer is one attempt, temporary and sent: the server may have
// booked, and nothing goes out again. A server error says as little.
func TestALostBookingIsNotSentAgain(t *testing.T) {
	s := replayBooking(t, func(int) string { return "" })
	r := writeResult(t, writeGateway(t, s.api).Write(context.Background(), bookWrite("30", "", 1)))
	if r.Answer.Class != transport.Temporary || !r.Answer.Sent {
		t.Fatalf("%v sent %v: %s", r.Answer.Class, r.Answer.Sent, r.Answer.Message)
	}
	if n := len(s.seen()); n != 1 {
		t.Fatalf("%d attempts", n)
	}
}

// A 429 is the one case the queue sends again: the server has not acted.
// The retry is the same request under the same key, once; a second 429
// ends it, and that outcome says it was the rate limit.
func TestA429IsRetriedOnceWithTheSameRequest(t *testing.T) {
	s := replayBooking(t, func(attempt int) string {
		if attempt == 1 {
			return "429"
		}
		return "ok_minutes_text"
	})
	r := writeResult(t, writeGateway(t, s.api).Write(context.Background(), bookWrite("30", "x", 1)))
	got := s.seen()
	if !r.Answer.OK() || len(got) != 2 || got[0] != got[1] {
		t.Fatalf("%v, attempts %+v", r.Answer.Class, got)
	}
	s = replayBooking(t, func(int) string { return "429" })
	r = writeResult(t, writeGateway(t, s.api).Write(context.Background(), bookWrite("30", "x", 2)))
	if got := s.seen(); r.Answer.Class != transport.Temporary || r.Answer.Status != 429 || len(got) != 2 || got[0] != got[1] {
		t.Fatalf("%v %d, attempts %+v", r.Answer.Class, r.Answer.Status, got)
	}
}

// A booking without its number, its ticket, its project, a duration or a
// key of the right form is refused before anything is sent. The note may be
// empty.
func TestABadBookingIsRefusedBeforeSending(t *testing.T) {
	s := replayBooking(t, func(int) string { return "ok_minutes_text" })
	g := writeGateway(t, s.api)
	for _, edit := range []func(*WriteTicket){
		func(w *WriteTicket) { w.Op = 0 },
		func(w *WriteTicket) { w.Project = "" },
		func(w *WriteTicket) { w.Ticket = "" },
		func(w *WriteTicket) { w.Value = "" },
		func(w *WriteTicket) { w.Key = "" },
		func(w *WriteTicket) { w.Key = "short" },
		func(w *WriteTicket) { w.Ticket = "a/b" },
	} {
		w := bookWrite("30", "note", 1)
		edit(&w)
		r := writeResult(t, g.Write(context.Background(), w))
		if r.Answer.Class != transport.InvalidInput || r.Answer.Sent || r.WriteTicket != w {
			t.Errorf("%+v: %v sent %v", w, r.Answer.Class, r.Answer.Sent)
		}
	}
	if n := len(s.seen()); n != 0 {
		t.Fatalf("%d attempts", n)
	}
	if r := writeResult(t, g.Write(context.Background(), bookWrite("30", "", 1))); !r.Answer.OK() {
		t.Fatalf("the good booking: %v", r.Answer.Class)
	}
}

// A 401 on a booking revokes the session like on every write, and the
// result is observed for the mailbox whatever the model does with it.
func TestA401OnABookingRevokesTheSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized","code":"unauthorized"}`))
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_synthetic")
	if err != nil {
		t.Fatal(err)
	}
	g := writeGateway(t, api.New(c))
	w := bookWrite("30", "", 1)
	w.Session = g.events.Session()
	if r := writeResult(t, g.Write(context.Background(), w)); r.Answer.Class != transport.Unauthenticated || !g.events.State().Revoked {
		t.Fatalf("%v revoked %v", r.Answer.Class, g.events.State().Revoked)
	}
}
