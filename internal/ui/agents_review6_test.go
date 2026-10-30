package ui

import (
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// Review of #36 on 4c001a7: four findings, each with its failing case.

func notFound(project, id string, o api.TicketOptions) transport.Answer {
	return transport.Answer{Class: transport.NotFound, Message: "No ticket found.", Status: 200, Sent: true}
}

// Every accepted removal of a ticket takes its agent's title, also one
// that came with the expansion of its description.
func TestARemovalThroughTheExpansionForgetsTheTitle(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("%q", got)
	}
	h.press("right") // into the ticket beside the agents' list
	h.f.setTicket(notFound)
	if !h.offers(Expand) {
		t.Fatal("e is not offered on #6")
	}
	h.drive(h.press("e"))
	if got := h.areaRow(6).Primary.String(); got != "#6" || !h.m.app.titlesFailed() {
		t.Fatalf("after the removal: %q, failed %v", got, h.m.app.titlesFailed())
	}
}

// A removal outweighs the older rows of the list shown: the row of the
// removed ticket does not bring its title back, and R is offered.
func TestARemovalOutweighsTheListShown(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.openSix() // from the list, which holds #6
	h.f.setTicket(notFound)
	h.drive(h.press("R"))
	if h.m.app.tickets == nil || h.m.app.tickets.index(ticketID(website, 6)) < 0 {
		t.Fatal("the list no longer holds #6: not the case")
	}
	if got := h.areaRow(6).Primary.String(); got != "#6" {
		t.Fatalf("the list brought the removed title back: %q", got)
	}
	if !h.m.app.titlesFailed() {
		t.Fatal("no R for the removed title")
	}
	// A newer successful read brings it back.
	h.f.setTicket(h.d.defaultTicket)
	h.drive(h.press("R"))
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("after a successful read: %q", got)
	}
}

// More agents than the disk takes reads at once: their titles are looked
// up on the disk in batches, none dropped.
func TestTitlesAreReadFromTheDiskInBatches(t *testing.T) {
	h := newDiskHarness(t, t.TempDir(), nil)
	h.withWatcher()
	h.start()
	a := h.m.app
	if !a.disk.usable() {
		t.Fatal("no disk")
	}
	var list []agents.Agent
	for n := 6; n <= 15; n++ {
		list = append(list, agentIn(n, orgA, n, agents.Working, h.now))
	}
	a.agents.list = list
	looked := map[string]bool{}
	for round := 0; round < 4; round++ {
		a.requestTitles()
		for _, r := range a.disk.reads {
			if r.purpose != forTitle {
				continue
			}
			if r.canceled {
				t.Fatalf("round %d: a title's disk read was dropped", round)
			}
			looked[r.ticket.Key.Ticket] = true
		}
		h.drive(a.flushDisk())
	}
	for _, ag := range list {
		if !looked[ag.Ticket] {
			t.Errorf("#%d was never looked up on the disk", ag.Number)
		}
	}
}

// A title read that succeeds gives its title even when its answer is too
// large for the cache to keep.
func TestATitleTooLargeForTheCacheStillShows(t *testing.T) {
	h := newHarness(t)
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 6, o)
		r.DescriptionText = strings.Repeat("x", cache.MaxEntryBytes+1)
		return ticketAnswer(r)
	})
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	if _, ok := h.m.app.store.Get(h.m.app.detailKey(website, ticketID(website, 6))); ok {
		t.Fatal("the cache kept the answer: not the case")
	}
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("%q", got)
	}
}
