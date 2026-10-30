package inboxfixture

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// A booking of time (K6) against the fixture, through the real adapter and
// the real transport.

// sum is the time the fixture says is booked on a ticket, through the read
// the client uses.
func sum(t *testing.T, a *api.API, project, id string) int {
	t.Helper()
	booked, ans := a.BookedTime(context.Background(), project, id)
	if !ans.OK() {
		t.Fatalf("the sum: %v %s", ans.Class, ans.Message)
	}
	return booked.Minutes
}

// It books with state: the answer has the minutes the server read, the sum
// reads the booking afterwards, for that ticket alone; the change and its
// key are logged, and the ticket was touched as R6 says.
func TestABookingIsKeptAndTheSumReadsIt(t *testing.T) {
	f, a, _ := serve(t, Normal)
	ctx := context.Background()
	six, seven := TicketID(ProjectWebsite, 6), TicketID(ProjectWebsite, 7)
	before := sum(t, a, ProjectWebsite, six)
	booking, ans := a.BookTime(ctx, six, "1:30", "Vertrag", "key-00000001")
	if !ans.OK() || ans.Status != http.StatusCreated || booking.TicketID != six || booking.ID == "" || booking.Minutes != 90 {
		t.Fatalf("%v %d %+v", ans.Class, ans.Status, booking)
	}
	if got := sum(t, a, ProjectWebsite, six); got != before+90 {
		t.Fatalf("the sum: %d, was %d", got, before)
	}
	if got := sum(t, a, ProjectWebsite, seven); got != 7*15 {
		t.Fatalf("#7's sum: %d", got)
	}
	if got := f.Mutations(); len(got) != 1 || got[0] != (Mutation{Kind: MutationBook, Ticket: six, Key: "key-00000001"}) {
		t.Fatalf("mutations %+v", got)
	}
	if got := f.Keys(); len(got) != 1 || got[0] != "key-00000001" {
		t.Fatalf("keys %v", got)
	}
	log := f.Requests()
	at := slices.IndexFunc(log, func(r Request) bool { return r.Route == "time_sessions.book" })
	if at < 0 {
		t.Fatalf("not logged: %+v", log)
	}
	if got := log[at]; got.Method != http.MethodPost || got.Status != http.StatusCreated ||
		got.Args["ticket_id"] != six || got.Args["duration_string"] != "1:30" || got.Args["description"] != "Vertrag" {
		t.Fatalf("logged %+v", got)
	}
	second, ans := a.BookTime(ctx, six, "15", "", "key-00000002")
	if !ans.OK() || second.ID == booking.ID || second.Minutes != 15 {
		t.Fatalf("the second: %v %+v", ans.Class, second)
	}
	if got := sum(t, a, ProjectWebsite, six); got != before+105 {
		t.Fatalf("the sum after two: %d", got)
	}
}

// The answer has the recorded shape: the fields of the recorded success,
// no other, and the spec's.
func TestABookingAnswersInTheRecordedShape(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(file.Contract.REST["POST /api/v1/tickets/:ticket_id/time_sessions"]["ok_minutes_text"].Response.Body, &recorded); err != nil {
		t.Fatal(err)
	}
	_, _, c := serve(t, Normal)
	ans := c.Write(context.Background(), http.MethodPost, "/api/v1/tickets/"+TicketID(ProjectWebsite, 6)+"/time_sessions", json.RawMessage(`{"time_session":{"duration_string":"30"}}`), "key-00000003")
	if missing, err := api.BookTimeSpec.Missing(ans.Payload); !ans.OK() || err != nil || len(missing) > 0 {
		t.Fatalf("%v %v %v", ans.Class, missing, err)
	}
	var got struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(ans.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != len(recorded.Data) {
		t.Fatalf("%v, recorded %v", got.Data, recorded.Data)
	}
	for k := range recorded.Data {
		if _, ok := got.Data[k]; !ok {
			t.Errorf("no %s", k)
		}
	}
	if got.Data["description"] != nil {
		t.Errorf("a description out of nothing: %v", got.Data["description"])
	}
}

// The duration by the server's rule: minutes, hours and minutes, decimal
// hours with a point or a comma, blanks around; refused with its texts,
// each from the core's locale.
func TestTheFixtureReadsADurationAsTheServerDoes(t *testing.T) {
	for typed, minutes := range map[string]int{"30": 30, "90": 90, "1:30": 90, "1:5": 65, "1.5": 90, "1,5": 90, "  30 ": 30, "0:15": 15, "24:00": 1440} {
		_, a, _ := serve(t, Normal)
		booking, ans := a.BookTime(context.Background(), TicketID(ProjectWebsite, 6), typed, "", "key-00000004")
		if !ans.OK() || booking.Minutes != minutes {
			t.Errorf("%q: %v %d, want %d", typed, ans.Class, booking.Minutes, minutes)
		}
	}
	for typed, want := range map[string]string{
		"1,5x":  textDurationFormat,
		"abc":   textDurationZero,
		"0":     textDurationZero,
		"0:00":  textDurationZero,
		"24:01": textDurationOverDay,
		"1441":  textDurationOverDay,
		"":      textDurationMissing,
		"   ":   textDurationMissing,
	} {
		f, a, _ := serve(t, Normal)
		booking, ans := a.BookTime(context.Background(), TicketID(ProjectWebsite, 6), typed, "", "key-00000005")
		if ans.Class != transport.InvalidInput || ans.Status != http.StatusUnprocessableEntity || ans.Code != "invalid_input" || ans.RefusalText() != want || booking != (api.TimeBooking{}) {
			t.Errorf("%q: %v %d %q %q", typed, ans.Class, ans.Status, ans.Code, ans.RefusalText())
		}
		if len(f.Mutations()) != 0 {
			t.Errorf("%q was booked", typed)
		}
	}
}

// The order of the core: a missing ticket first, then what the input lacks,
// then the right, then the model. So without the right an empty duration
// is refused as such (422, not 403), and a duration the model refuses meets
// the right first.
func TestABookingIsRefusedInTheServersOrder(t *testing.T) {
	_, a, _ := serve(t, Refusals)
	ctx := context.Background()
	forbidden := TicketID(ProjectWebsite, refusedWrite)
	vanished := TicketID(ProjectWebsite, refusedVanished)
	for _, tc := range []struct {
		name, ticket, duration string
		class                  transport.Class
	}{
		{"no ticket, no duration", "00000000-0000-4000-8000-000000000999", "", transport.NotFound},
		{"vanished", vanished, "30", transport.NotFound},
		{"vanished, no duration", vanished, "", transport.NotFound},
		{"no right, no duration", forbidden, "", transport.InvalidInput},
		{"no right, a bad duration", forbidden, "1,5x", transport.NoAccess},
		{"no right", forbidden, "30", transport.NoAccess},
	} {
		if _, ans := a.BookTime(ctx, tc.ticket, tc.duration, "", "key-00000006"); ans.Class != tc.class {
			t.Errorf("%s: %v %s", tc.name, ans.Class, ans.Message)
		}
	}
	// Every other ticket books.
	if _, ans := a.BookTime(ctx, TicketID(ProjectWebsite, 6), "30", "", "key-00000007"); !ans.OK() {
		t.Fatalf("%v", ans.Class)
	}
}

// A key seen before answers the first answer, without a second booking; with
// other arguments it is refused, and neither books.
func TestARepeatedKeyBooksOnce(t *testing.T) {
	f, a, _ := serve(t, Normal)
	ctx := context.Background()
	six := TicketID(ProjectWebsite, 6)
	first, ans := a.BookTime(ctx, six, "30", "x", "key-00000008")
	if !ans.OK() {
		t.Fatalf("%v", ans.Class)
	}
	again, ans := a.BookTime(ctx, six, "30", "x", "key-00000008")
	if !ans.OK() || again != first {
		t.Fatalf("%v %+v, first %+v", ans.Class, again, first)
	}
	_, ans = a.BookTime(ctx, six, "45", "x", "key-00000008")
	if ans.Class != transport.InvalidInput || ans.RefusalText() != textKeyMismatch {
		t.Fatalf("%v %q", ans.Class, ans.RefusalText())
	}
	if got := f.Mutations(); len(got) != 1 {
		t.Fatalf("mutations %+v", got)
	}
	if got := sum(t, a, ProjectWebsite, six); got != 6*15+30 {
		t.Fatalf("the sum %d", got)
	}
}

// The scenarios: a lost answer has booked; a 429 has not, and the retry
// with the same key books once; a read-only token and a revoked one book
// nothing; a slow answer has booked already.
func TestTheScenariosOfABooking(t *testing.T) {
	ctx := context.Background()
	six := TicketID(ProjectWebsite, 6)

	f, a, _ := serve(t, Lost)
	if _, ans := a.BookTime(ctx, six, "30", "", "key-00000009"); ans.Class != transport.Temporary || !ans.Sent {
		t.Fatalf("lost: %v sent %v", ans.Class, ans.Sent)
	}
	if got := f.Mutations(); len(got) != 1 || sum(t, a, ProjectWebsite, six) != 6*15+30 {
		t.Fatalf("lost: mutations %+v", got)
	}

	f, a, _ = serve(t, RateLimited)
	if _, ans := a.BookTime(ctx, six, "30", "", "key-00000010"); ans.Status != http.StatusTooManyRequests || len(f.Mutations()) != 0 {
		t.Fatalf("429: %v %d, %+v", ans.Class, ans.Status, f.Mutations())
	}
	if _, ans := a.BookTime(ctx, six, "30", "", "key-00000010"); !ans.OK() || len(f.Mutations()) != 1 {
		t.Fatalf("after the 429: %v, %+v", ans.Class, f.Mutations())
	}
	if got := f.Keys(); !slices.Equal(got, []string{"key-00000010", "key-00000010"}) {
		t.Fatalf("429: keys %v", got)
	}

	f, a, _ = serve(t, ReadOnly)
	if _, ans := a.BookTime(ctx, six, "30", "", "key-00000011"); ans.Class != transport.ReadOnly || ans.Status != http.StatusForbidden || ans.Code != "read_only" || len(f.Mutations()) != 0 {
		t.Fatalf("read-only: %v %d %q", ans.Class, ans.Status, ans.Code)
	}

	f, a, _ = serve(t, Unauthorized)
	if _, ans := a.BookTime(ctx, six, "30", "", "key-00000012"); ans.Class != transport.Unauthenticated || len(f.Mutations()) != 0 {
		t.Fatalf("401: %v", ans.Class)
	}

	f, a, _ = serve(t, Slow)
	if _, ans := a.BookTime(ctx, six, "30", "", "key-00000013"); !ans.OK() || len(f.Mutations()) != 1 {
		t.Fatalf("slow: %v", ans.Class)
	}
}

// A server before R6 has no such route: nothing there books.
func TestAServerBeforeR6HasNoBooking(t *testing.T) {
	f, err := New(Normal, contract.Raw(), BeforeR1())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := clientOf(t, f)
	if _, ans := a.BookTime(context.Background(), TicketID(ProjectWebsite, 6), "30", "", "key-00000014"); ans.Class != transport.NotFound || len(f.Mutations()) != 0 {
		t.Fatalf("%v, %+v", ans.Class, f.Mutations())
	}
}

// What the fixture cannot read is its own 400 and books nothing.
func TestABookingThatDoesNotParseBooksNothing(t *testing.T) {
	f, _, c := serve(t, Normal)
	path := "/api/v1/tickets/" + TicketID(ProjectWebsite, 6) + "/time_sessions"
	for _, body := range []string{`{}`, `{"time_session":"30"}`, `{"time_session":{"duration_string":30}}`} {
		ans := c.Write(context.Background(), http.MethodPost, path, json.RawMessage(body), "key-00000015")
		if ans.OK() {
			t.Errorf("%s booked", body)
		}
	}
	if len(f.Mutations()) != 0 {
		t.Fatalf("%+v", f.Mutations())
	}
}

// The text of the refusals is the core's, word for word as recorded.
func TestTheRefusalsAreTheRecordedOnes(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	group := file.Contract.REST["POST /api/v1/tickets/:ticket_id/time_sessions"]
	for name, want := range map[string]string{"invalid_input_no_duration": textDurationMissing, "invalid_input_over_a_day": textDurationOverDay} {
		if !strings.Contains(string(group[name].Response.Body), want) {
			t.Errorf("%s: %s, want %q", name, group[name].Response.Body, want)
		}
	}
}
