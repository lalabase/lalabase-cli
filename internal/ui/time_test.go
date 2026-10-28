package ui

// S6: the time the person booked on the ticket in the pane, in its meta
// bar, read when the ticket opens and with R.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

func (f *fakes) BookedTimeAnswer(_ context.Context, project, id string) transport.Answer {
	f.mu.Lock()
	f.timeReads = append(f.timeReads, id)
	answer := f.booked
	f.mu.Unlock()
	if answer != nil {
		return answer(project, id)
	}
	return bookedAnswer(id, 150)
}

func (f *fakes) bookedReads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.timeReads)
}

// bookedAnswer is the server's answer for the own time on ticket id.
func bookedAnswer(id string, minutes int) transport.Answer {
	body := map[string]any{
		"scope":   "own",
		"filters": map[string]any{"ticket": map[string]any{"id": id}},
		"total":   map[string]any{"minutes": minutes, "sessions": 1, "hours": float64(minutes) / 60},
	}
	payload, _ := json.Marshal(body)
	return transport.Answer{Class: transport.Object, Payload: payload, Status: 200, Sent: true}
}

// metaText is the meta bar of the pane, its lines joined.
func (h *harness) metaText() string {
	var lines []string
	for _, l := range h.m.objectColumn().Meta {
		lines = append(lines, l.Text.String())
	}
	return strings.Join(lines, "\n")
}

// Opening a ticket reads the person's own time on it, once, and the meta
// bar shows it in hours and minutes under its label.
func TestAnOpenTicketShowsTheTimeBookedByYou(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	if got := h.f.bookedReads(); len(got) != 1 || got[0] != ticketID(website, 6) {
		t.Fatalf("time reads %v", got)
	}
	if got := h.metaText(); !strings.Contains(got, "Booked by you\n2:30") {
		t.Fatalf("meta bar:\n%s", got)
	}
}

// A preview reads no time: only an opened ticket does, so moving through
// the list costs no request more.
func TestAPreviewReadsNoTime(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	h.drive(h.press("down"))
	h.drive(h.press("down"))
	if got := h.f.bookedReads(); len(got) != 0 {
		t.Fatalf("time reads on previews: %v", got)
	}
}

// R in the ticket reads the time again.
func TestRReadsTheTimeAgain(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.f.mu.Lock()
	h.f.booked = func(_, id string) transport.Answer { return bookedAnswer(id, 5) }
	h.f.mu.Unlock()
	h.m.focus = view.Object
	h.drive(h.press("R"))
	if got := h.f.bookedReads(); len(got) != 2 {
		t.Fatalf("time reads %v", got)
	}
	if got := h.metaText(); !strings.Contains(got, "Booked by you\n0:05") {
		t.Fatalf("meta bar:\n%s", got)
	}
}

// No access to the project's time, no such ticket, or a server without
// the tool: no line, and nothing else changes.
func TestNoTimeLineWithoutTheTime(t *testing.T) {
	for name, answer := range map[string]transport.Answer{
		"no access": {Class: transport.Refused, Message: "no access to the time sessions of this project", Status: 200, Sent: true},
		"not found": {Class: transport.NotFound, Message: "No ticket found.", Status: 200, Sent: true},
	} {
		h := newHarness(t)
		h.f.booked = func(string, string) transport.Answer { return answer }
		h.openSix()
		if got := h.metaText(); strings.Contains(got, "Booked") {
			t.Fatalf("%s: meta bar:\n%s", name, got)
		}
	}
	h := newHarness(t)
	h.openTickets(website)
	h.m.app.report.Enabled["booked_time"] = false // a server before S6
	h.m.app.tickets.selected = ticketID(website, 6)
	h.drive(h.press("enter"))
	if got := h.f.bookedReads(); len(got) != 0 || strings.Contains(h.metaText(), "Booked") {
		t.Fatalf("an older server: reads %v, meta\n%s", got, h.metaText())
	}
}

// The time of a ticket left meanwhile never reaches the pane: leaving the
// ticket moves the Time area.
func TestTheTimeOfATicketLeftDoesNotShow(t *testing.T) {
	h := newHarness(t)
	h.f.booked = func(_, id string) transport.Answer {
		if id == ticketID(website, 6) {
			return bookedAnswer(id, 60)
		}
		return bookedAnswer(id, 7)
	}
	h.openTickets(website)
	h.m.app.tickets.selected = ticketID(website, 6)
	held := messages(t, h.press("enter")) // the reads of #6, its time among them
	h.m.focus = view.List
	h.m.app.tickets.selected = ticketID(website, 7)
	h.drive(h.press("enter"))
	h.drive(h.feed(held...)...)
	if d := h.m.app.detail; d == nil || d.id != ticketID(website, 7) {
		t.Fatalf("the pane: %+v", d)
	}
	if got := h.metaText(); !strings.Contains(got, "Booked by you\n0:07") {
		t.Fatalf("the late time of #6 reached #7's meta bar:\n%s", got)
	}
}

// withoutBookedTime switches S6 off, as a server before it: for the tests
// of the ticket's own read that run the open's command and want the read
// alone, not batched with the time beside it (that is tested here).
func (h *harness) withoutBookedTime() {
	h.t.Helper()
	h.m.app.report.Enabled["booked_time"] = false
}
