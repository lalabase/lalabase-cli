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

// K6: time booked on a ticket over REST (the core's R6), the first write the
// client sends there. Every answer is replayed from the contract copy.

const bookGroup = "POST /api/v1/tickets/:ticket_id/time_sessions"

// bookPath is the recorded ticket's booking route.
const bookPath = "/api/v1/tickets/" + login + "/time_sessions"

// Every recorded case of a booking, with what the adapter makes of it. A
// case the contract gains without an entry here fails the test.
var recordedBookings = map[string]struct {
	class  transport.Class
	status int
}{
	"ok_minutes_text":              {transport.Object, 201},
	"ok_hours_text":                {transport.Object, 201},
	"ok_minutes":                   {transport.Object, 201},
	"idempotency_first":            {transport.Object, 201},
	"idempotency_replay":           {transport.Object, 201},
	"idempotency_mismatch":         {transport.InvalidInput, 422},
	"invalid_input_no_duration":    {transport.InvalidInput, 422},
	"invalid_input_both_durations": {transport.InvalidInput, 422},
	"invalid_input_date":           {transport.InvalidInput, 422},
	"invalid_input_minutes":        {transport.InvalidInput, 422},
	"invalid_input_over_a_day":     {transport.InvalidInput, 422},
	"forbidden":                    {transport.NoAccess, 403},
	"read_only":                    {transport.ReadOnly, 403},
	"not_found":                    {transport.NotFound, 404},
	"hidden":                       {transport.NotFound, 404},
}

func recordedBooking(t *testing.T, name string) (TimeBooking, bool) {
	t.Helper()
	var body struct {
		Data *TimeBooking `json:"data"`
	}
	if err := json.Unmarshal(recordedREST(t, bookGroup, name), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data == nil {
		return TimeBooking{}, false
	}
	return *body.Data, true
}

// Every recorded answer gets its class through the adapter, sent, and a
// booking only from a success.
func TestEveryRecordedBookingGetsItsClass(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for name := range file.Contract.REST[bookGroup] {
		if _, ok := recordedBookings[name]; !ok {
			t.Errorf("the contract records %s, the table here has no class for it", name)
		}
	}
	for name, want := range recordedBookings {
		api, sent := restWriteCase(t, bookGroup, name)
		booking, ans := api.BookTime(context.Background(), login, "30", "", "key-0001")
		if ans.Class != want.class || ans.Status != want.status || !ans.Sent || len(*sent) != 1 {
			t.Errorf("%s: got %v %d sent %v, %d requests", name, ans.Class, ans.Status, ans.Sent, len(*sent))
		}
		recorded, ok := recordedBooking(t, name)
		if want.class == transport.Object && (!ok || booking != recorded || booking.ID == "" || booking.TicketID != login || booking.Minutes <= 0) {
			t.Errorf("%s: booking %+v, recorded %+v", name, booking, recorded)
		}
		if want.class != transport.Object && booking != (TimeBooking{}) {
			t.Errorf("%s: a booking out of a refusal: %+v", name, booking)
		}
	}
}

// The spec is one of the adapters, a REST write, and the recorded successes
// carry its fields.
func TestTheBookingSpecFitsTheRecordedSuccesses(t *testing.T) {
	if !slices.ContainsFunc(Specs, func(s Spec) bool { return s.Name == BookTimeSpec.Name }) {
		t.Error("the booking is not among Specs")
	}
	if BookTimeSpec.Name != bookGroup || BookTimeSpec.Tool != "" || BookTimeSpec.Method != http.MethodPost || !strings.HasSuffix(bookGroup, BookTimeSpec.Path) {
		t.Errorf("no REST write of the recorded route: %+v", BookTimeSpec)
	}
	for name, want := range recordedBookings {
		if want.class != transport.Object {
			continue
		}
		if missing, err := BookTimeSpec.Missing(recordedREST(t, bookGroup, name)); err != nil || len(missing) > 0 {
			t.Errorf("%s lacks %v (%v)", name, missing, err)
		}
	}
}

// The recorded repeat under a key is the same booking: the first answer
// again, nothing booked twice.
func TestTheRecordedRepeatIsTheSameBooking(t *testing.T) {
	first, ok := recordedBooking(t, "idempotency_first")
	again, same := recordedBooking(t, "idempotency_replay")
	if !ok || !same || first != again {
		t.Fatalf("first %+v, repeat %+v", first, again)
	}
}

// bookingServer answers every booking with a 201 of the recorded shape and
// keeps what it was sent.
func bookingServer(t *testing.T) (*API, *[]recordedWrite) {
	t.Helper()
	return restWriteCase(t, bookGroup, "ok_minutes_text")
}

// The duration goes as the person typed it: the client reads nothing into
// it, neither blanks nor a comma. An empty note is not sent at all, a note
// is sent as it is, and the request carries nothing else.
func TestABookingSendsWhatWasTyped(t *testing.T) {
	for _, tc := range []struct {
		name, duration, note string
		wire                 string
	}{
		{"minutes", "30", "", `{"time_session":{"duration_string":"30"}}`},
		{"hours and minutes", "1:30", "", `{"time_session":{"duration_string":"1:30"}}`},
		{"a decimal comma", "1,5", "", `{"time_session":{"duration_string":"1,5"}}`},
		{"what the server refuses", "1,5x", "", `{"time_session":{"duration_string":"1,5x"}}`},
		{"blanks stay", "  30 ", "", `{"time_session":{"duration_string":"  30 "}}`},
		{"a note", "30", "Vertrag geprüft", `{"time_session":{"duration_string":"30","description":"Vertrag geprüft"}}`},
		{"a note of blanks is the server's", "30", "  ", `{"time_session":{"duration_string":"30","description":"  "}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, sent := bookingServer(t)
			if _, ans := api.BookTime(context.Background(), login, tc.duration, tc.note, "key-0002"); !ans.OK() {
				t.Fatalf("%v %s", ans.Class, ans.Message)
			}
			want := recordedWrite{method: http.MethodPost, path: bookPath, key: "key-0002", body: tc.wire}
			if len(*sent) != 1 || (*sent)[0] != want {
				t.Fatalf("sent %+v, want %+v", *sent, want)
			}
		})
	}
}

// Text the encoder escapes still arrives byte for byte: what the server
// decodes is what was typed.
func TestTheDurationArrivesByteForByte(t *testing.T) {
	for _, typed := range []string{"<1&2>", "1\u00a030", "\u2028", "\\\"30", "30\U0001F600"} {
		api, sent := bookingServer(t)
		if _, ans := api.BookTime(context.Background(), login, typed, "<note>", "key-0003"); !ans.OK() {
			t.Fatalf("%q: %v", typed, ans.Class)
		}
		var body struct {
			Session map[string]string `json:"time_session"`
		}
		if err := json.Unmarshal([]byte((*sent)[0].body), &body); err != nil {
			t.Fatal(err)
		}
		if body.Session["duration_string"] != typed || body.Session["description"] != "<note>" || len(body.Session) != 2 {
			t.Fatalf("%q arrived as %q", typed, body.Session)
		}
	}
}

// Two sends of one action are the same bytes under the same key: the
// server compares the request.
func TestARepeatedBookingIsTheSameRequest(t *testing.T) {
	api, sent := bookingServer(t)
	for range 2 {
		if _, ans := api.BookTime(context.Background(), login, "0:15", "einmal", "key-0004"); !ans.OK() {
			t.Fatalf("%v", ans.Class)
		}
	}
	if len(*sent) != 2 || (*sent)[0] != (*sent)[1] {
		t.Fatalf("sent %+v", *sent)
	}
}

// A booking without a key of the right form, or on an id that is no path
// segment, is refused before anything is sent: there is no booking
// without its key, and no other route by a crafted id.
func TestABookingIsRefusedBeforeSending(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	a := New(c)
	for _, key := range []string{"", "short", "key with spaces", strings.Repeat("k", 129)} {
		if _, ans := a.BookTime(context.Background(), login, "30", "", key); ans.Class != transport.InvalidInput || ans.Sent {
			t.Errorf("key %q: %v sent %v", key, ans.Class, ans.Sent)
		}
	}
	for _, id := range []string{"", ".", "..", "a/b", `a\b`} {
		if _, ans := a.BookTime(context.Background(), id, "30", "", "key-0005"); ans.Class != transport.InvalidInput || ans.Sent {
			t.Errorf("id %q: %v sent %v", id, ans.Class, ans.Sent)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests sent", hits)
	}
}

// An answer that lacks what confirms the booking, or holds it in the wrong
// type, is a protocol error kept sent: the server may have booked, so it
// is reloaded, never sent again. What cannot be held by the contract, so
// the only hand-written answers here.
func TestABookingAnswerWithoutItsFieldsIsAProtocolErrorKeptSent(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"data":null}`,
		`{"data":{"id":"x","ticket_id":"t"}}`,
		`{"data":{"id":"x","duration_minutes":30}}`,
		`{"data":{"ticket_id":"t","duration_minutes":30}}`,
		`{"data":{"id":"x","ticket_id":"t","duration_minutes":"30"}}`,
		`{"data":{"id":"x","ticket_id":"t","duration_minutes":30.5}}`,
		`{"data":{"id":7,"ticket_id":"t","duration_minutes":30}}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(body))
		}))
		c, _ := transport.New(srv.URL, "lalab_pat_test")
		_, ans := New(c).BookTime(context.Background(), login, "30", "", "key-0006")
		srv.Close()
		if ans.Class != transport.Protocol || !ans.Sent {
			t.Errorf("%s: %v sent %v", body, ans.Class, ans.Sent)
		}
	}
}
