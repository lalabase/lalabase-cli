package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// C9b: the Agents area under the organisations: glyph, number and title
// per agent, the organisation under it when it is not the chosen one. A
// title the session does not hold is read once, in the background.

func (h *harness) areaRow(n int) present.Row {
	h.t.Helper()
	for _, r := range h.m.agentsColumn().Rows {
		if p := r.Primary.String(); p == "#"+itoa(n) || strings.HasPrefix(p, "#"+itoa(n)+" ") {
			return r
		}
	}
	h.t.Fatalf("no row of #%d in %+v", n, h.m.agentsColumn().Rows)
	return present.Row{}
}

// titleReads are the reads of a ticket of the website project by number.
func (h *harness) titleReads(n int) int {
	reads := 0
	for _, r := range h.f.detailReads() {
		if r.id == ticketID(website, n) {
			reads++
		}
	}
	return reads
}

func TestTheAreaShowsOnlyWithAnAgent(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher()
	h.start()
	if rows := h.m.screen().Agents.Rows; rows != nil {
		t.Fatalf("an area without agents: %+v", rows)
	}
	f.set(agentIn(1, orgA, 6, agents.Working, h.now))
	h.m.app.agents.until = h.now
	h.due()
	c := h.m.screen().Agents
	if len(c.Rows) != 1 || c.Title.String() != "Agents" {
		t.Fatalf("%+v", c)
	}
}

// Glyph, number and title; the title read once in the background, #n
// alone until it comes; the organisation under it only when it is not the
// chosen one.
func TestAnAgentsRowAndItsTitle(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.WaitingForYou, h.now), agentIn(2, orgB, 7, agents.Lost, h.now))
	h.drive(h.m.Init())
	r := h.areaRow(6)
	if r.Primary.String() != "#6 Ticket 6" || !r.Marked || r.Mark.String() != "⣿" || r.MarkStyle != present.AgentWaiting {
		t.Fatalf("%+v", r)
	}
	if h.titleReads(6) != 1 || h.titleReads(7) != 1 {
		t.Fatalf("reads %d and %d", h.titleReads(6), h.titleReads(7))
	}
	// Nothing chosen: every agent names its organisation.
	if got := h.areaRow(6).Secondary.String(); got != "Nordlicht GmbH" {
		t.Fatalf("%q", got)
	}
	h.drive(h.chooseTickets(orgA))
	if got := h.areaRow(6).Secondary.String(); got != "" {
		t.Fatalf("the chosen organisation is named: %q", got)
	}
	if got := h.areaRow(7).Secondary.String(); got != "Südwind AG" {
		t.Fatalf("%q", got)
	}
	// More reads of the agents read no title again.
	for range 5 {
		h.due()
	}
	if h.titleReads(6) != 1 || h.titleReads(7) != 1 {
		t.Fatalf("reads %d and %d after more reads of the agents", h.titleReads(6), h.titleReads(7))
	}
}

// The read of a title is idle work, below what the person looks at and
// below the previews (queue.Idle).
func TestATitleIsReadInTheBackground(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	titles := 0
	cmds := h.feed(messages(t, h.m.Init())...)
	for len(cmds) > 0 {
		var next []tea.Cmd
		for _, w := range h.m.app.waiters {
			if w.purpose == forTitle && w.ticket.Area == gateway.Titles {
				if w.ticket.Priority != queue.Idle {
					t.Fatalf("a title read of priority %d", w.ticket.Priority)
				}
				titles++
			}
		}
		for _, cmd := range cmds {
			next = append(next, h.feed(messages(t, cmd)...)...)
		}
		cmds = next
	}
	if titles == 0 {
		t.Fatal("no title read")
	}
}

// Without a title the row says #n alone; a read that failed is not
// repeated by the reading of the agents, R reads it again.
func TestAFailedTitleStaysUntilR(t *testing.T) {
	h := newHarness(t)
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h.withWatcher(agentIn(1, orgA, 6, agents.Ended, h.now))
	h.start()
	if got := h.areaRow(6).Primary.String(); got != "#6" {
		t.Fatalf("%q", got)
	}
	reads := h.titleReads(6)
	if reads == 0 {
		t.Fatal("no read")
	}
	for range 4 {
		h.due()
	}
	if h.titleReads(6) != reads {
		t.Fatalf("%d reads after a failure, %d before", h.titleReads(6), reads)
	}
	h.f.setTicket(h.d.defaultTicket)
	h.drive(h.press("R"))
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" || h.titleReads(6) <= reads {
		t.Fatalf("after R: %q, %d reads", got, h.titleReads(6))
	}
}

// A title is foreign text: cleaned as in the ticket list.
func TestAHostileTitleIsCleaned(t *testing.T) {
	h := newHarness(t)
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 6, o)
		r.Title = "Evil\x1b]0;owned\x07\x1b[31m red\u202e"
		return ticketAnswer(r)
	})
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	got := h.areaRow(6).Primary.String()
	if strings.ContainsAny(got, "\x1b\x07\u202e") || !strings.HasPrefix(got, "#6 Evil") {
		t.Fatalf("%q", got)
	}
}

// A title the session holds is not read: the ticket in the pane, a row of
// the list shown.
func TestATitleTheSessionHoldsIsNotRead(t *testing.T) {
	h := newHarness(t)
	h.withWatcher()
	h.openTickets(website)
	before := h.titleReads(7)
	h.m.app.agents.list = []agents.Agent{agentIn(1, orgA, 7, agents.Working, h.now)}
	h.drive(h.m.app.requestTitles())
	if h.titleReads(7) != before {
		t.Fatalf("a title of the list was read: %d", h.titleReads(7)-before)
	}
	if got := h.areaRow(7).Primary.String(); got != "#7 Ticket 7" {
		t.Fatalf("%q", got)
	}
}

// A seen agent that ended is faint; any other seen one is not.
func TestASeenEndedAgentIsFaint(t *testing.T) {
	h := newHarness(t)
	ended, working := agentIn(1, orgA, 6, agents.Ended, h.now), agentIn(2, orgA, 7, agents.Working, h.now)
	ended.Seen, working.Seen = true, true
	unseen := agentIn(3, orgA, 8, agents.Ended, h.now)
	h.withWatcher(ended, working, unseen)
	h.start()
	if r := h.areaRow(6); !r.Quiet || r.MarkStyle != present.AgentEnded {
		t.Errorf("seen and ended: %+v", r)
	}
	if r := h.areaRow(7); r.Quiet {
		t.Errorf("seen and working: %+v", r)
	}
	if r := h.areaRow(8); r.Quiet {
		t.Errorf("ended, not seen: %+v", r)
	}
}

// Review round 1 of #36, F1: a title the disk cache holds cancels its read
// from the server, which waits as idle work: after a restart the titles
// cost no request.
func TestATitleFromTheDiskCancelsItsRead(t *testing.T) {
	h := newHarness(t)
	h.withWatcher()
	h.start()
	a := h.m.app
	ag := agentIn(1, orgA, 6, agents.Working, h.now)
	a.agents.list = []agents.Agent{ag}
	cmd := a.requestTitles()
	if cmd == nil {
		t.Fatal("no read")
	}
	var w *waiter
	for _, x := range a.waiters {
		if x.purpose == forTitle {
			w = x
		}
	}
	if w == nil {
		t.Fatal("no waiter of the title")
	}
	key := a.detailKey(website, ag.Ticket)
	a.store.Apply(key, cache.Replace, ticketAnswer(h.d.ticketRecord(website, 6, gateway.DetailOptions(key))).Payload, h.now)
	if !a.showFromDisk(forTitle, key) {
		t.Fatal("the disk's entry was not taken")
	}
	if !w.canceled {
		t.Fatal("the server's read of the title goes on")
	}
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("%q", got)
	}
	// Its canceled answer fails nothing.
	h.drive(cmd)
	if tt := a.titles[ag.Ticket]; !tt.known || tt.failed {
		t.Fatalf("%+v", tt)
	}
}
