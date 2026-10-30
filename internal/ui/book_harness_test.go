package ui

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// The harness of a booking (K6): the fakes take the write behind the real
// gateway and queue, keep what every attempt carried, and by default book
// 30 minutes. A session of newRESTHarness offers b: its /me lists the
// recorded capabilities and says the token may write.

// bookCall is one attempt the fakes saw.
type bookCall struct {
	ticket, duration, note, key string
}

type bookFakes struct {
	mu     sync.Mutex
	calls  []bookCall
	answer func(c bookCall) (api.TimeBooking, transport.Answer)
}

func (f *fakes) BookTime(_ context.Context, ticket, duration, note, key string) (api.TimeBooking, transport.Answer) {
	w := &f.bookings
	c := bookCall{ticket: ticket, duration: duration, note: note, key: key}
	w.mu.Lock()
	w.calls = append(w.calls, c)
	answer := w.answer
	w.mu.Unlock()
	if answer != nil {
		return answer(c)
	}
	return bookedOf(ticket, 30)
}

// bookedOf is the server's success: a booking of the minutes on the ticket.
func bookedOf(ticket string, minutes int) (api.TimeBooking, transport.Answer) {
	return api.TimeBooking{ID: idOf(800000), TicketID: ticket, Minutes: minutes}, transport.Answer{Class: transport.Object, Status: 201, Sent: true}
}

func (f *fakes) bookCalls() []bookCall {
	f.bookings.mu.Lock()
	defer f.bookings.mu.Unlock()
	return slices.Clone(f.bookings.calls)
}

// setBook answers every attempt from now on with answer.
func (f *fakes) setBook(answer func(c bookCall) (api.TimeBooking, transport.Answer)) {
	f.bookings.mu.Lock()
	defer f.bookings.mu.Unlock()
	f.bookings.answer = answer
}

// bookFails answers every attempt from now on with ans.
func (f *fakes) bookFails(ans transport.Answer) {
	f.setBook(func(bookCall) (api.TimeBooking, transport.Answer) { return api.TimeBooking{}, ans })
}

// recordedRefusal is a recorded refusal of a booking as an answer: its body
// in Message, as the transport hands it on.
func recordedRefusal(t *testing.T, name string, class transport.Class) transport.Answer {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	ex, ok := file.Contract.REST["POST /api/v1/tickets/:ticket_id/time_sessions"][name]
	if !ok {
		t.Fatalf("the contract has no booking case %s", name)
	}
	return transport.Answer{Class: class, Message: string(ex.Response.Body), Status: ex.Response.Status, Sent: true}
}

// bookingHarness is a session with ticket #6 of Website open in the pane.
func bookingHarness(t *testing.T) *harness {
	t.Helper()
	h := newRESTHarness(t)
	h.openSix()
	return h
}

// setToken is /me with the recorded capabilities but the ones left out, and
// the token's read_only as given.
func (h *harness) setToken(readOnly *bool, without ...string) {
	me := h.d.me
	me.Token.ReadOnly = readOnly
	me.Capabilities = slices.DeleteFunc(slices.Clone(h.d.capabilities), func(c string) bool { return slices.Contains(without, c) })
	h.f.me = func() (api.Me, transport.Answer) {
		return me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
}

// fillBooking opens the dialog and types what a booking says.
func (h *harness) fillBooking(duration, note string) {
	h.t.Helper()
	h.press("b")
	if h.m.dialog == nil {
		h.t.Fatalf("b opened no dialog: %s", h.helpText())
	}
	h.typeText(duration)
	h.press("tab")
	h.typeText(note)
}

// sendBooking opens the dialog, types and presses enter; what the send
// does waits in the command returned.
func (h *harness) sendBooking(duration, note string) func() {
	h.t.Helper()
	h.fillBooking(duration, note)
	cmd := h.press("enter")
	if cmd == nil {
		h.t.Fatal("enter sent nothing")
	}
	return func() { h.t.Helper(); h.drive(cmd) }
}

// dialogLine is the box's error line.
func (h *harness) dialogLine() string { return h.m.dialogScreen().Error.String() }
