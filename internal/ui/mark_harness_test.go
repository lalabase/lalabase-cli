package ui

import (
	"context"
	"slices"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// The harness of the write (C1f): the fakes answer mark_notification_read,
// count every attempt with its idempotency key, carry out the change on
// their own rows when they answer a success, and can hold the answer on a
// gate. The default inbox hides the rows marked so far, as the server's
// unread pages do.

// markCall is one attempt the fakes saw: the target and the key sent.
type markCall struct {
	id, key string
}

// markFakes is the write side of the fakes, kept apart so the C1b to C1d
// harness stays what it was.
type markFakes struct {
	mu sync.Mutex
	// mark answers an attempt; executed says whether the fakes carried
	// the change out before answering (the default on a success).
	mark  func(id, key string) (api.NotificationState, transport.Answer)
	calls []markCall
	// read are the notifications marked so far: the default inbox hides
	// them, the counters count without them.
	read map[string]bool
	// gate, when set, holds every attempt until it is closed.
	gate chan struct{}
	// executed records every change the fakes carried out, in order.
	executed []string
}

func (f *fakes) MarkNotificationRead(ctx context.Context, id, key string) (api.NotificationState, transport.Answer) {
	w := &f.writes
	w.mu.Lock()
	w.calls = append(w.calls, markCall{id: id, key: key})
	answer, gate := w.mark, w.gate
	w.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return api.NotificationState{}, transport.Answer{Class: transport.Canceled, Message: "canceled", Sent: true}
		}
	}
	return answer(id, key)
}

// markCalls are the attempts so far.
func (f *fakes) markCalls() []markCall {
	f.writes.mu.Lock()
	defer f.writes.mu.Unlock()
	return slices.Clone(f.writes.calls)
}

// executed are the changes carried out so far.
func (f *fakes) executed() []string {
	f.writes.mu.Lock()
	defer f.writes.mu.Unlock()
	return slices.Clone(f.writes.executed)
}

// setMark replaces the answer of the write.
func (f *fakes) setMark(fn func(id, key string) (api.NotificationState, transport.Answer)) {
	f.writes.mu.Lock()
	defer f.writes.mu.Unlock()
	f.writes.mark = fn
}

// setMarkGate holds every attempt on gate from now on (nil frees them).
func (f *fakes) setMarkGate(gate chan struct{}) {
	f.writes.mu.Lock()
	defer f.writes.mu.Unlock()
	f.writes.gate = gate
}

// execute carries the change out on the fakes' rows: the row is read from
// now on, hidden from the unread pages and not counted.
func (f *fakes) execute(id string) {
	f.writes.mu.Lock()
	defer f.writes.mu.Unlock()
	f.writes.read[id] = true
	f.writes.executed = append(f.writes.executed, id)
}

// readIDs are the notifications marked so far.
func (f *fakes) readIDs() map[string]bool {
	f.writes.mu.Lock()
	defer f.writes.mu.Unlock()
	out := make(map[string]bool, len(f.writes.read))
	for id := range f.writes.read {
		out[id] = true
	}
	return out
}

// markedAnswer is the server's success: the state of the notification,
// read, with the time of the fakes.
func markedAnswer(id string) (api.NotificationState, transport.Answer) {
	at := "2026-10-09T12:00:00Z"
	return api.NotificationState{ID: id, State: "read", ReadAt: &at}, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
}

// defaultMark carries the change out and answers the success.
func (f *fakes) defaultMark(id, _ string) (api.NotificationState, transport.Answer) {
	f.execute(id)
	return markedAnswer(id)
}

// inboxWithout is the default inbox without the rows marked so far: A has
// 30 unread rows (two pages) and B 3 before any is marked.
func (d recordedData) inboxWithout(q api.NotificationQuery, read map[string]bool) transport.Answer {
	per := q.PerPage
	if per == 0 {
		per = 25
	}
	unread := func(rows []api.Notification) []api.Notification {
		return slices.DeleteFunc(slices.Clone(rows), func(n api.Notification) bool { return read[n.ID] })
	}
	a, b := unread(d.rows(orgA, 1, 30)), unread(d.rows(orgB, 101, 3))
	switch q.OrganisationID {
	case "":
		all := append(slices.Clone(a), b...)
		return pageAnswer(q.Page, per, len(all), len(all), slice(all, q.Page, per), map[string]int{orgA: len(a), orgB: len(b)})
	case orgA:
		return pageAnswer(q.Page, per, len(a), len(a), slice(a, q.Page, per), map[string]int{orgA: len(a)})
	case orgB:
		return pageAnswer(q.Page, per, len(b), len(b), slice(b, q.Page, per), map[string]int{orgB: len(b)})
	}
	return transport.Answer{Class: transport.NotFound, Message: "Organisation not found or not accessible.", Sent: true, Status: 200}
}

// setReadOnly makes the token of the recorded /me read-only (true) or of
// unknown permission (nil).
func (h *harness) setReadOnly(value *bool) {
	me := h.d.me
	me.Token.ReadOnly = value
	h.f.me = func() (api.Me, transport.Answer) {
		return me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
}

// openDetails opens the inbox of A and the details of the row with the
// given index.
func (h *harness) openDetails(org string, index int) {
	h.t.Helper()
	h.drive(h.choose(org))
	v := h.m.app.active
	if index >= len(v.rows) {
		h.t.Fatalf("row %d of %d", index, len(v.rows))
	}
	v.selected = v.rows[index].ID
	h.drive(h.press("enter"))
	if h.m.focus != view.Object || v.opened != v.rows[index].ID {
		h.t.Fatalf("details not open: focus %d, opened %q", h.m.focus, v.opened)
	}
}

// openLinked opens the details of a row and the ticket it names.
func (h *harness) openLinked(org string, index int) {
	h.t.Helper()
	h.openDetails(org, index)
	h.drive(h.press("o"))
	if a := h.m.app; a.origin == nil || a.detail == nil || !a.detail.known {
		h.t.Fatalf("the ticket did not open from the notification: origin %+v, detail %+v", a.origin, a.detail)
	}
}

// detailsText is the text of the details pane.
func (h *harness) detailsText() string { return h.m.objectColumn().Text.String() }

// rowTrailing is the word at the end of the row with the given id.
func (h *harness) rowTrailing(id string) string {
	v := h.m.app.active
	c := h.m.listColumn()
	if i := v.index(id); i >= 0 && i < len(c.Rows) {
		return c.Rows[i].Trailing.String()
	}
	return ""
}

// waitForWriteOnWire waits until the fakes saw n attempts.
func (h *harness) waitForWriteOnWire(n int) {
	h.t.Helper()
	h.waitFor("the write on the wire", func() bool { return len(h.f.markCalls()) >= n })
}

// keyFormOK is the form the server keeps unchanged.
func keyFormOK(key string) bool {
	if len(key) < 8 || len(key) > 128 {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == ':', r == '-':
		default:
			return false
		}
	}
	return true
}

// resultOf runs a mark command on the test's goroutine and returns its
// result.
func resultOf(h *harness, cmd tea.Cmd) gateway.MarkReadResult {
	h.t.Helper()
	msgs := messages(h.t, cmd)
	if len(msgs) != 1 {
		h.t.Fatalf("%d messages from the write", len(msgs))
	}
	r, ok := msgs[0].(gateway.MarkReadResult)
	if !ok {
		h.t.Fatalf("%T from the write", msgs[0])
	}
	return r
}
