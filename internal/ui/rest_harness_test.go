package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// The REST way of the ticket view in the harness (K1): the same tickets
// as the tools give, in REST's form, every text whole (REST cuts none).
// A session takes it when its /me lists the capabilities of the core's R1
// (newRESTHarness); the default harness lists none and takes the tools.

// restRead is one REST read of the ticket view the fakes saw: the list
// with its query, a ticket with its options, or one comment.
type restRead struct {
	route string // "list", "ticket" or "post"
	query api.TicketQuery
	id    string
	o     api.TicketOptions
	post  string
}

func (f *fakes) TicketsRESTAnswer(ctx context.Context, q api.TicketQuery) transport.Answer {
	f.mu.Lock()
	f.restReads = append(f.restReads, restRead{route: "list", query: q})
	answer, held := f.restTickets, f.gateTickets
	f.mu.Unlock()
	if !f.hold(ctx, held) {
		return transport.Answer{Class: transport.Canceled, Message: "canceled"}
	}
	return answer(q)
}

func (f *fakes) TicketRESTAnswer(ctx context.Context, id string, o api.TicketOptions) transport.Answer {
	f.mu.Lock()
	f.restReads = append(f.restReads, restRead{route: "ticket", id: id, o: o})
	answer, held := f.restTicket, f.gateTicket != "" && f.gateTicket == id
	f.mu.Unlock()
	if !f.hold(ctx, held) {
		return transport.Answer{Class: transport.Canceled, Message: "canceled"}
	}
	return answer(id, o)
}

func (f *fakes) PostRESTAnswer(ctx context.Context, id, post string) transport.Answer {
	f.mu.Lock()
	f.restReads = append(f.restReads, restRead{route: "post", id: id, post: post})
	answer, held := f.restPost, f.gateTicket != "" && f.gateTicket == id
	f.mu.Unlock()
	if !f.hold(ctx, held) {
		return transport.Answer{Class: transport.Canceled, Message: "canceled"}
	}
	return answer(id, post)
}

func (f *fakes) restCalls() []restRead {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.restReads)
}

func (f *fakes) setRESTTicket(fn func(id string, o api.TicketOptions) transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restTicket = fn
}

func (f *fakes) setRESTPost(fn func(id, post string) transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restPost = fn
}

// newRESTHarness is a session whose server lists the capabilities of R1:
// the ticket view takes REST.
func newRESTHarness(t *testing.T) *harness {
	t.Helper()
	return buildHarness(t, restFakes(recorded(t)), nil)
}

// restFakes are the default fakes with a /me that lists the recorded
// capabilities.
func restFakes(d recordedData) *fakes {
	f := defaultFakes(d)
	f.me = func() (api.Me, transport.Answer) {
		me := d.me
		me.Capabilities = d.capabilities
		return me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	return f
}

// restNotFound is REST's answer for a missing or hidden record.
func restNotFound() transport.Answer {
	return transport.Answer{Class: transport.NotFound, Code: "not_found", Message: `{"error":"not_found","code":"not_found"}`, Status: 404, Sent: true}
}

// ticketByID finds a ticket by its id: its project and number.
func ticketByID(id string) (project string, n int, ok bool) {
	for _, p := range []string{website, intern, handbuch, archiv} {
		for i := 1; i <= projectRows(p); i++ {
			if ticketID(p, i) == id {
				return p, i, true
			}
		}
	}
	return "", 0, false
}

// wholeTicket is ticket n as REST sends it: ticketRecord with every text
// whole.
func (d recordedData) wholeTicket(project string, n int, o api.TicketOptions) api.Ticket {
	o.FullText = true
	t := d.ticketRecord(project, n, o)
	if n == 6 {
		whole := map[string]string{}
		for _, p := range d.sixPosts(true) {
			whole[p.ID] = p.BodyText
		}
		for i := range t.Posts {
			t.Posts[i].BodyText = whole[t.Posts[i].ID]
		}
	}
	return t
}

// restData wraps a record as REST answers it.
func restData(record any, withPermissions bool) transport.Answer {
	payload, _ := json.Marshal(record)
	body := map[string]any{"data": json.RawMessage(payload)}
	if withPermissions {
		body["permissions"] = map[string]any{"tickets": map[string]any{"read": true, "write": true}}
	}
	return objectAnswer(body)
}

// defaultRESTTicket is GET /api/v1/tickets/:id as the server answers the
// adapter, which always asks for a page.
func (d recordedData) defaultRESTTicket(id string, o api.TicketOptions) transport.Answer {
	project, n, ok := ticketByID(id)
	if !ok {
		return restNotFound()
	}
	o.PostsPage = max(1, o.PostsPage)
	var record map[string]any
	_ = json.Unmarshal(ticketAnswer(d.wholeTicket(project, n, o)).Payload, &record)
	return restData(record, true)
}

// defaultRESTPost is GET /api/v1/tickets/:ticket_id/posts/:id: the comment
// whole, or not found.
func (d recordedData) defaultRESTPost(id, post string) transport.Answer {
	project, n, ok := ticketByID(id)
	if !ok {
		return restNotFound()
	}
	posts := d.ticketRecord(project, n, api.TicketOptions{}).Posts
	if n == 6 {
		posts = d.sixPosts(true)
	}
	for _, p := range posts {
		if p.ID == post {
			return restData(p, false)
		}
	}
	return restNotFound()
}

// viewReads are the reads of the ticket view on either way, written the
// same: a test can compare the reads of the two ways.
func (f *fakes) viewReads() []string {
	var out []string
	for _, q := range f.ticketListReads() {
		out = append(out, fmt.Sprintf("list %s %q %d", q.ProjectID, q.Q, q.Page))
	}
	for _, r := range f.detailReads() {
		out = append(out, fmt.Sprintf("ticket %s %+v", r.id, r.o))
	}
	for _, r := range f.restCalls() {
		switch r.route {
		case "list":
			out = append(out, fmt.Sprintf("list %s %q %d", r.query.ProjectID, r.query.Q, r.query.Page))
		case "ticket":
			out = append(out, fmt.Sprintf("ticket %s %+v", r.id, r.o))
		case "post":
			out = append(out, fmt.Sprintf("ticket %s %+v", r.id, api.TicketOptions{PostID: r.post}))
		}
	}
	return out
}
