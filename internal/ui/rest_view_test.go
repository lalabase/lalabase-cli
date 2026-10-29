package ui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// K1: the ticket view over REST, as the interface shows it. The way comes
// from the start: /me lists the capabilities, the feature report takes
// them, the scope routes the reads by it.

// The start wires /me's capabilities to the reads: the recorded /me (R1)
// sends the ticket view over REST, a /me before R1 or without
// capabilities over the tools, read for read.
func TestTheStartRoutesTheTicketViewByWhatMeLists(t *testing.T) {
	r1 := []string{"tickets.paging", "tickets.by_number", "posts.show"}
	cases := []struct {
		name string
		caps func(d recordedData) []string
		way  features.Way
	}{
		{"the recorded /me", func(d recordedData) []string { return d.capabilities }, features.REST},
		{"a /me before R1", func(d recordedData) []string {
			return slices.DeleteFunc(slices.Clone(d.capabilities), func(c string) bool { return slices.Contains(r1, c) })
		}, features.MCP},
		{"a /me without capabilities", func(recordedData) []string { return nil }, features.MCP},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := recorded(t)
			f := defaultFakes(d)
			f.me = func() (api.Me, transport.Answer) {
				me := d.me
				me.Capabilities = c.caps(d)
				return me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
			}
			h := buildHarness(t, f, nil)
			h.openSix()
			h.drive(h.press("tab"))
			h.drive(h.press("]"))
			for _, name := range []string{"tickets", "tickets_search", "ticket", "ticket_posts_paged", "ticket_full_text"} {
				if got := h.m.app.report.Via[name]; got != c.way {
					t.Errorf("%s via %v", name, got)
				}
			}
			rest, lists, tickets := h.f.restCalls(), h.f.ticketListReads(), h.f.detailReads()
			if c.way == features.REST && (len(rest) < 3 || len(lists)+len(tickets) != 0) {
				t.Fatalf("over REST: %d REST reads, %d list and %d ticket reads of the tools", len(rest), len(lists), len(tickets))
			}
			if c.way == features.MCP && (len(rest) != 0 || len(lists) == 0 || len(tickets) < 2) {
				t.Fatalf("over the tools: %d REST reads, %d list and %d ticket reads of the tools", len(rest), len(lists), len(tickets))
			}
			// The rest of the session keeps the tools: the inbox and the
			// booked time are read as before.
			if len(h.f.inboxCalls()) == 0 || len(h.f.timeReads) == 0 {
				t.Fatalf("inbox %d, time %d", len(h.f.inboxCalls()), len(h.f.timeReads))
			}
		})
	}
}

// screenAfter runs a step and returns the rendered screen.
func screenAfter(h *harness, keys ...string) string {
	h.t.Helper()
	for _, k := range keys {
		h.drive(h.press(k))
	}
	return h.m.View().Content
}

// openTicket selects ticket n of Website in the list shown and opens it.
func (h *harness) openTicket(n int) {
	h.t.Helper()
	h.m.focus = view.List
	h.m.app.tickets.selected = ticketID(website, n)
	h.drive(h.press("enter"))
}

// The view is the same on both ways, screen for screen and read for read,
// for tickets whose texts the tools do not cut: the list, a ticket with
// two pages of comments, another order, the attachments' second page.
func TestTheTicketViewShowsTheSameOnBothWays(t *testing.T) {
	run := func(h *harness) (screens, reads []string) {
		h.openTickets(website)
		screens = append(screens, h.m.View().Content)
		h.openTicket(3)
		screens = append(screens, h.m.View().Content, screenAfter(h, "tab", "]"), screenAfter(h, "o"))
		h.drive(h.press("esc"))
		h.openTicket(4)
		screens = append(screens, h.m.View().Content, screenAfter(h, "tab", "tab", "]"))
		h.openTicket(2)
		screens = append(screens, h.m.View().Content)
		return screens, h.f.viewReads()
	}
	mcpScreens, mcpReads := run(newHarness(t))
	restScreens, restReads := run(newRESTHarness(t))
	if len(mcpScreens) != len(restScreens) {
		t.Fatalf("%d and %d screens", len(mcpScreens), len(restScreens))
	}
	for i := range mcpScreens {
		if mcpScreens[i] != restScreens[i] {
			t.Errorf("screen %d differs:\n--- tools\n%s\n--- REST\n%s", i, ansi.Strip(mcpScreens[i]), ansi.Strip(restScreens[i]))
		}
	}
	if !slices.Equal(mcpReads, restReads) {
		t.Fatalf("reads differ:\ntools %q\nREST  %q", mcpReads, restReads)
	}
}

// Ticket #6 is where the ways differ: the tools cut its description and
// its two newest comments, REST sends them whole, and the pane shows
// them whole, with nothing to expand.
func TestOverRESTTheTextsTheToolsCutAreWhole(t *testing.T) {
	h := newRESTHarness(t)
	h.openSix()
	text := h.paneText()
	mustContain(t, text, sixDescriptionWhole, "Six comment 30 begins and ends here.", "Six comment 29 begins and ends here.")
	mustNotContain(t, text, "Text shortened")
	if h.offers(Expand) {
		t.Fatal("e is offered on the description")
	}
	h.drive(h.press("tab"))
	if h.offers(Expand) {
		t.Fatal("e is offered on a whole comment")
	}
	// The same ticket over the tools: cut, with e.
	m := newHarness(t)
	m.openSix()
	mustContain(t, m.paneText(), "Text shortened by server")
	if !m.offers(Expand) {
		t.Fatal("the tools' cut description offers no e")
	}
}

// longText is n characters of words, with hostile text in its first part.
func longText(n int) string {
	text := "START" + hostileText + " " + strings.Repeat("Lorem ipsum dolor sit amet, consectetur adipiscing elit. ", n/57+1)
	return text[:n]
}

// REST sends every text whole, whatever its length. The pane builds its
// text on every frame, so it shows what the tools showed: up to the
// tools' cut on the page, then e reads the text whole, shown up to the
// tools' bound of a whole text. The screen's bytes stay bounded and
// cleaned, a megabyte of a comment or not.
func TestALongWholeTextIsShownBoundedAndCleaned(t *testing.T) {
	h := newRESTHarness(t)
	description, comment := longText(1_000_000), longText(300_000)
	h.f.setRESTTicket(func(id string, o api.TicketOptions) transport.Answer {
		ans := h.d.defaultRESTTicket(id, o)
		var body map[string]any
		_ = json.Unmarshal(ans.Payload, &body)
		data := body["data"].(map[string]any)
		data["description_text"] = description
		for _, p := range data["posts"].([]any) {
			p.(map[string]any)["body_text"] = comment
		}
		for _, a := range data["attachments"].([]any) {
			a.(map[string]any)["text"] = description
		}
		ans.Payload, _ = json.Marshal(body)
		return ans
	})
	h.f.setRESTPost(func(id, post string) transport.Answer {
		ans := h.d.defaultRESTPost(id, post)
		var body map[string]any
		_ = json.Unmarshal(ans.Payload, &body)
		body["data"].(map[string]any)["body_text"] = comment
		ans.Payload, _ = json.Marshal(body)
		return ans
	})
	h.openTickets(website)
	h.openTicket(1)
	bounded := func(what string, wholeChars int) {
		t.Helper()
		pane := h.paneText()
		// Two comments and the description, each cut: their characters
		// (at most four bytes each) and the lines around them.
		if limit := 4*(wholeChars+2*maxPageText) + 4096; len(pane) > limit {
			t.Fatalf("%s: the pane holds %d bytes, over %d", what, len(pane), limit)
		}
		for _, width := range []int{80, 120} {
			h.m.width, h.m.height = width, 30
			screen := h.m.View().Content
			assertViewSafe(t, screen)
			if len(screen) > 64*width*30 {
				t.Fatalf("%s at %d columns: %d bytes on the screen", what, width, len(screen))
			}
		}
		h.m.width, h.m.height = 120, 24
	}
	bounded("the page", maxPageText)
	mustContain(t, ansi.Strip(h.paneText()), "START", "Text shortened for this view")
	// Cleaned: no control sequence of the text is left, its visible
	// characters are.
	mustNotContain(t, h.paneText(), "\x1b", "\x07")
	if !h.offers(Expand) {
		t.Fatal("e is not offered on a description the view cut")
	}
	// e reads the description whole: the page again over REST (no
	// full_text), shown up to the whole text's bound.
	before := len(h.f.restCalls())
	h.drive(h.press("e"))
	reads := h.f.restCalls()[before:]
	if len(reads) != 1 || reads[0].route != "ticket" || !reads[0].o.FullText {
		t.Fatalf("reads %+v", reads)
	}
	// The expansion holds the text whole, past the bound the pane shows.
	if e := h.m.app.detail.expansion; e == nil || !e.known || utf8.RuneCountInString(e.text) < 5*maxWholeText {
		t.Fatal("the description was not read whole")
	}
	bounded("the description whole", maxWholeText)
	mustContain(t, h.paneText(), "Description · whole text", "Text shortened for this view")
	// e on a comment the view cut reads it on its own route.
	h.drive(h.press("esc"))
	h.drive(h.press("tab"))
	if !h.offers(Expand) {
		t.Fatal("e is not offered on a comment the view cut")
	}
	before = len(h.f.restCalls())
	h.drive(h.press("e"))
	reads = h.f.restCalls()[before:]
	if len(reads) != 1 || reads[0].route != "post" || reads[0].post != h.m.app.detail.post {
		t.Fatalf("reads %+v", reads)
	}
	bounded("a comment whole", maxWholeText)
}

// The tools' and REST's comment of the same id file the same entry: the
// comment alone, read back as the expansion shows it.
func TestOneCommentOverRESTIsReadOnItsRouteAndFiledAlone(t *testing.T) {
	h := newRESTHarness(t)
	long := "Six comment 30 " + strings.Repeat("goes on and on ", 1000) + "and ends here."
	h.f.setRESTPost(func(id, post string) transport.Answer {
		ans := h.d.defaultRESTPost(id, post)
		if post == sixPostID(30) {
			var body map[string]any
			_ = json.Unmarshal(ans.Payload, &body)
			body["data"].(map[string]any)["body_text"] = long
			ans.Payload, _ = json.Marshal(body)
		}
		return ans
	})
	h.f.setRESTTicket(func(id string, o api.TicketOptions) transport.Answer {
		ans := h.d.defaultRESTTicket(id, o)
		var body map[string]any
		_ = json.Unmarshal(ans.Payload, &body)
		for _, p := range body["data"].(map[string]any)["posts"].([]any) {
			if p := p.(map[string]any); p["id"] == sixPostID(30) {
				p["body_text"] = long
			}
		}
		ans.Payload, _ = json.Marshal(body)
		return ans
	})
	h.openSix()
	h.drive(h.press("tab"))
	if h.m.app.detail.post != sixPostID(30) || !h.offers(Expand) {
		t.Fatalf("selected %q, e offered %v", h.m.app.detail.post, h.offers(Expand))
	}
	h.drive(h.press("e"))
	mustContain(t, h.paneText(), "and ends here.")
	key := h.m.app.detail.expansion.key
	e, ok := h.m.app.store.Get(key)
	if !ok {
		t.Fatal("the comment was not filed")
	}
	post, ans := api.DecodePost(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
	if !ans.OK() || post.ID != sixPostID(30) || post.BodyText != long {
		t.Fatalf("filed %v %q", ans.Class, ans.Message)
	}
	// A missing comment is the missing-comment rule over REST too: the
	// text goes, the ticket is read once more and stays.
	h.drive(h.press("esc"))
	h.f.setRESTPost(func(string, string) transport.Answer { return restNotFound() })
	h.m.app.store.Forget(key)
	before := len(h.f.restCalls())
	h.drive(h.press("e"))
	mustContain(t, h.paneText(), "Comment unavailable")
	reads := h.f.restCalls()[before:]
	if len(reads) != 2 || reads[0].route != "post" || reads[1].route != "ticket" || h.m.app.detail == nil || !h.m.app.detail.known {
		t.Fatalf("reads %+v", reads)
	}
}

// A server that moved to REST between two runs: what the tools filed in
// the first run (the list, the pages, the description whole) shows at
// once in the second and REST's reloads replace it; a comment the first
// run kept as a ticket (before K1) is dropped, never shown, and read again
// on its route.
func TestEntriesTheToolsFiledReadAfterTheSwitchToREST(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.openSix()
	h.drive(h.press("tab"))
	h.drive(h.press("]"))
	h.drive(h.press("tab"))
	h.drive(h.press("tab"))
	h.drive(h.press("e"))
	if d := h.m.app.detail; d == nil || d.expansion == nil || !d.expansion.known {
		t.Fatal("the first run did not expand")
	}
	// One comment as the tools' answer was filed before K1: the ticket
	// with the comment.
	post := h.m.app.detail.key
	post.PostsPage, post.PostsPerPage, post.Newest, post.AttachmentsPage, post.Post = 0, 0, false, 0, sixPostID(30)
	h.m.app.store.Apply(post, cache.Replace, h.d.onePost(website, 6, sixPostID(30)).Payload, h.now)
	if len(h.f.restCalls()) != 0 {
		t.Fatal("the first run read over REST")
	}
	h.press("q")
	d2 := recorded(t)
	h2 := newDiskHarness(t, root, restFakes(d2))
	h2.now = h.now
	h2.start()
	a := h2.m.app
	if a.report.Via["ticket"] != features.REST {
		t.Fatal("the second run is not on REST")
	}
	gate := make(chan struct{})
	h2.f.setGate(gate, ticketID(website, 6), false)
	h2.f.gateTickets, h2.f.gateProjects = true, true
	held := func(cmd tea.Cmd) []<-chan tea.Msg {
		t.Helper()
		var network []<-chan tea.Msg
		for _, ch := range h2.spawn(cmd) {
			select {
			case msg := <-ch:
				h2.drive(h2.feed(msg)...)
			case <-time.After(300 * time.Millisecond):
				network = append(network, ch)
			}
		}
		return network
	}
	var pending []<-chan tea.Msg
	pending = append(pending, held(tea.Batch(h2.feed(func() tea.Msg {
		h2.m.focus = view.Navigation
		a.highlight, a.highlightSection = orgA, sectionTickets
		return press("enter")
	}())...))...)
	a.projects.selected = website
	h2.m.focus = view.List
	pending = append(pending, held(tea.Batch(h2.feed(press("enter"))...))...)
	if v := a.tickets; v == nil || !v.cached || len(v.rows) != 25 {
		t.Fatalf("the tools' list after the switch: %+v", v)
	}
	a.tickets.selected = ticketID(website, 6)
	pending = append(pending, held(tea.Batch(h2.feed(press("enter"))...))...)
	d := a.detail
	if d == nil || !d.cached || d.record.ID != ticketID(website, 6) || d.err.String() != "" {
		t.Fatalf("the tools' ticket after the switch: %+v", d)
	}
	mustContain(t, h2.paneText(), "Text shortened by server")
	// The comment kept as a ticket is no comment: e drops it and reads the
	// comment on its route, held like the rest.
	h2.drive(h2.feed(press("tab"))...)
	if d.post != sixPostID(30) {
		t.Fatalf("selected %q", d.post)
	}
	pending = append(pending, held(tea.Batch(h2.feed(press("e"))...))...)
	if e := d.expansion; e == nil || e.known || e.text != "" {
		t.Fatalf("the ticket kept for a comment was shown: %+v", e)
	}
	close(gate)
	for _, ch := range pending {
		h2.drive(h2.feed(h2.receive(ch))...)
	}
	if d.cached || a.tickets.cached || d.err.String() != "" {
		t.Fatalf("after REST's reloads: ticket %+v, list %+v", d.status, a.tickets.status)
	}
	mustContain(t, h2.paneText(), "Six comment 30 begins and ends here.")
	mustNotContain(t, h2.paneText(), "Text shortened by server")
	var routes []string
	for _, r := range h2.f.restCalls() {
		routes = append(routes, r.route)
	}
	if !slices.Contains(routes, "post") || len(h2.f.detailReads())+len(h2.f.ticketListReads()) != 0 {
		t.Fatalf("REST reads %v, tools' reads %d", routes, len(h2.f.detailReads())+len(h2.f.ticketListReads()))
	}
}

// The request budget of the ticket view (K1), counted per action on
// either way: open a project's list, open a ticket, turn its comments a
// page on and back. Each costs one read on either way; over REST the
// list's and the ticket's leave the tools (and their budget per token),
// the booked time (S6) stays with them. The start and the projects are
// what they were.
func TestTheRequestBudgetOfTheTicketView(t *testing.T) {
	type count struct{ tools, rest, projects int }
	run := func(h *harness) []count {
		at := func() count {
			return count{
				tools:    len(h.f.ticketListReads()) + len(h.f.detailReads()) + len(h.f.bookedReads()) + len(h.f.inboxCalls()),
				rest:     len(h.f.restCalls()),
				projects: len(h.f.projectReads()),
			}
		}
		var steps []count
		h.start()
		steps = append(steps, at())
		h.drive(h.chooseTickets(orgA))
		steps = append(steps, at())
		h.drive(h.openProject(website))
		steps = append(steps, at())
		h.openTicket(3)
		steps = append(steps, at())
		h.drive(h.press("tab"))
		h.drive(h.press("]"))
		steps = append(steps, at())
		h.drive(h.press("["))
		steps = append(steps, at())
		// Per step, what it cost.
		var costs []count
		for i := range steps {
			c := steps[i]
			if i > 0 {
				c = count{c.tools - steps[i-1].tools, c.rest - steps[i-1].rest, c.projects - steps[i-1].projects}
			}
			costs = append(costs, c)
		}
		return costs
	}
	steps := []string{"the start (the counters)", "the projects", "the list", "the ticket", "a page on", "back"}
	tools := []count{{1, 0, 0}, {0, 0, 1}, {1, 0, 0}, {2, 0, 0}, {1, 0, 0}, {1, 0, 0}}
	rest := []count{{1, 0, 0}, {0, 0, 1}, {0, 1, 0}, {1, 1, 0}, {0, 1, 0}, {0, 1, 0}}
	for name, c := range map[string]struct {
		h    *harness
		want []count
	}{"tools": {newHarness(t), tools}, "rest": {newRESTHarness(t), rest}} {
		got := run(c.h)
		for i := range steps {
			if got[i] != c.want[i] {
				t.Errorf("%s, %s: %+v, want %+v", name, steps[i], got[i], c.want[i])
			}
		}
	}
}

// One comment read whole is a read of the comment alone, on either way:
// the title of the ticket's agent stays what it was (REST's answer names
// no ticket, and the tools' is cut down to its comment).
func TestOneCommentReadWholeLeavesTheTitleAsItWas(t *testing.T) {
	for name, h := range map[string]*harness{"tools": newHarness(t), "rest": newRESTHarness(t)} {
		t.Run(name, func(t *testing.T) {
			long := "Six comment 30 " + strings.Repeat("goes on ", 1200) + "and ends here."
			h.f.setRESTTicket(func(id string, o api.TicketOptions) transport.Answer {
				ans := h.d.defaultRESTTicket(id, o)
				var body map[string]any
				_ = json.Unmarshal(ans.Payload, &body)
				for _, p := range body["data"].(map[string]any)["posts"].([]any) {
					if p := p.(map[string]any); p["id"] == sixPostID(30) {
						p["body_text"] = long
					}
				}
				ans.Payload, _ = json.Marshal(body)
				return ans
			})
			h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
			h.openSix()
			if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
				t.Fatalf("before: %q", got)
			}
			h.drive(h.press("tab"))
			if !h.offers(Expand) {
				t.Fatal("e is not offered on the newest comment")
			}
			h.drive(h.press("e"))
			if e := h.m.app.detail.expansion; e == nil || !e.known || e.post != sixPostID(30) {
				t.Fatalf("expansion %+v", e)
			}
			if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
				t.Fatalf("after one comment: %q", got)
			}
		})
	}
}
