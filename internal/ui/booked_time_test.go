package ui

// S6: the time the person booked on the ticket in the pane, in its meta
// bar, read when the ticket opens and with R.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
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
		h.f.mu.Lock()
		h.f.booked = func(string, string) transport.Answer { return answer }
		h.f.mu.Unlock()
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
	h.f.mu.Lock()
	h.f.booked = func(_, id string) transport.Answer {
		if id == ticketID(website, 6) {
			return bookedAnswer(id, 60)
		}
		return bookedAnswer(id, 7)
	}
	h.f.mu.Unlock()
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

// setBooked answers the time reads from now on with answer.
func (h *harness) setBooked(answer func(project, id string) transport.Answer) {
	h.f.mu.Lock()
	h.f.booked = answer
	h.f.mu.Unlock()
}

// reopen goes from the open ticket to the list, opens the ticket of row
// number n there and returns.
func (h *harness) reopen(n int) {
	h.t.Helper()
	h.m.focus = view.List
	h.m.app.tickets.selected = ticketID(website, n)
	h.drive(h.press("enter"))
}

// The time read is the person's, as the open: no budget to wait for.
func TestTheTimeIsReadWithThePersonsPriority(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	h.m.app.tickets.selected = ticketID(website, 6)
	h.press("enter")
	reads := 0
	for _, w := range h.m.app.waiters {
		if w.ticket.Area == gateway.Time {
			reads++
			if w.ticket.Priority != queue.User {
				t.Fatalf("the time read's priority %v", w.ticket.Priority)
			}
		}
	}
	if reads != 1 {
		t.Fatalf("%d time reads", reads)
	}
}

// Review of #42: a time no longer readable goes from the line and from
// the cache, so it does not come back at the next open.
func TestARefusalTakesTheTimeFromTheCacheToo(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.setBooked(func(string, string) transport.Answer {
		return transport.Answer{Class: transport.Refused, Message: "no access to the time sessions of this project", Status: 200, Sent: true}
	})
	h.m.focus = view.Object
	h.drive(h.press("R"))
	if strings.Contains(h.metaText(), "Booked") {
		t.Fatalf("the refused time stayed:\n%s", h.metaText())
	}
	if _, ok := h.m.app.store.Get(h.m.app.timeKey(website, ticketID(website, 6))); ok {
		t.Fatal("the refused time stayed in the cache")
	}
}

// A time the cache holds shows at once at the next open, also when its
// reload fails for a moment; one the cache holds of another ticket never.
func TestTheCacheShowsTheTimeAtOnce(t *testing.T) {
	h := newHarness(t)
	h.openSix() // 2:30, cached
	h.reopen(7)
	h.setBooked(func(string, string) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h.reopen(6)
	if got := h.metaText(); !strings.Contains(got, "Booked by you\n2:30") {
		t.Fatalf("the cached time did not show:\n%s", got)
	}
	// An entry under #6's key that names another ticket shows nothing.
	a := h.m.app
	key := a.timeKey(website, ticketID(website, 6))
	other := bookedAnswer(ticketID(website, 7), 45)
	a.store.Apply(key, cache.Replace, other.Payload, h.now)
	h.reopen(7)
	h.reopen(6)
	if got := h.metaText(); strings.Contains(got, "0:45") {
		t.Fatalf("another ticket's time from the cache:\n%s", got)
	}
}

// Leaving a ticket cancels its time read; and an answer of another
// ticket's time, read meanwhile, does not show in this ticket's bar.
func TestEachGuardOfTheTimesTicket(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	a.tickets.selected = ticketID(website, 6)
	h.press("enter") // #6's time read out, not run
	six := a.timeKey(website, ticketID(website, 6))
	if !a.pending(six) {
		t.Fatal("no time read of #6 out")
	}
	h.reopen(7)
	if a.pending(six) {
		t.Fatal("leaving #6 did not cancel its time read")
	}
	h.setBooked(func(_, id string) transport.Answer { return bookedAnswer(id, 99) })
	h.drive(a.issue(gateway.Time, six, forTime, queue.User)) // #6's time, #7 in the pane
	if got := h.metaText(); strings.Contains(got, "1:39") {
		t.Fatalf("#6's time in #7's bar:\n%s", got)
	}
}

// Review round 2 of #42: a time a run read shows in the next run from
// the disk, also when its reload fails.
func TestTheTimeComesBackFromTheDisk(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.openSix() // 2:30, on the disk once the journal is written
	h2 := h.restart(root)
	h2.setBooked(func(string, string) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h2.openSix()
	if got := h2.metaText(); !strings.Contains(got, "Booked by you\n2:30") {
		t.Fatalf("the time did not come from the disk:\n%s", got)
	}
}
