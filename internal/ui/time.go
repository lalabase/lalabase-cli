package ui

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// The time the person booked on the ticket in the pane (S6): in its meta
// bar, under "Booked by you", in hours and minutes. It is read when the
// ticket opens (never for a preview, so moving through a list costs no
// request more) and again with R, beside the ticket's own read and with
// the person's priority. The cache shows it at once when the ticket
// opened before: from memory, else from the disk (C1e), as every read.
// The time is extra: no access to the project's time, no such
// ticket or a server without the tool shows no line, and nothing else of
// the ticket changes.

// bookedTime is what the pane knows of the person's time on its ticket.
type bookedTime struct {
	known   bool
	minutes int
}

// timeKey is the key of the person's own time on a ticket.
func (a *app) timeKey(project, id string) cache.Key {
	k := a.ns.Key(api.BookedTimeSpec.Tool)
	k.Project, k.Ticket = project, id
	return k
}

// readTime asks the time on the ticket in the pane, unless a read of it is
// out; a time the memory cache holds shows at once, and on an open the
// disk is asked beside (not on R: the network read is out by then).
func (a *app) readTime(d *detailView, open bool) tea.Cmd {
	if d == nil || !a.on("booked_time") {
		return nil
	}
	key := a.timeKey(d.project, d.id)
	if !d.booked.known && !a.timeFromCache(d, key) && open {
		a.diskRead(gateway.Time, key, forTime)
	}
	if a.pending(key) {
		return nil
	}
	// The person's priority, as the ticket's own read: one request per
	// open or R the person made, never a stream of them, and no budget
	// to wait for (the agents' titles, many and unasked, are idle work).
	return a.issue(gateway.Time, key, forTime, queue.User)
}

// timeFromCache shows the time the memory entry of key holds, if it still
// reads and is of the ticket, and says whether it did.
func (a *app) timeFromCache(d *detailView, key cache.Key) bool {
	e, ok := a.store.Get(key)
	if !ok {
		return false
	}
	booked, ans := api.DecodeBookedTime(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
	if !ans.OK() || gateway.CheckBookedTime(key, booked) != nil {
		a.forget(key)
		return false
	}
	d.booked = bookedTime{known: true, minutes: booked.Minutes}
	return true
}

// receiveTime takes a time read's answer, filed through the scope like
// any read: an accepted one of the ticket in the pane shows; no access or
// no such ticket takes the line away; a failure keeps what was shown.
func (m Model) receiveTime(r gateway.TimeResult) (Model, tea.Cmd) {
	a := m.app
	d, ok := m.receive(r)
	if !ok || a == nil {
		return m, nil
	}
	v := a.detail
	if v == nil || v.id != r.Key.Ticket || v.project != r.Key.Project {
		return m, nil
	}
	switch {
	case d == cache.Replace:
		v.booked = bookedTime{known: true, minutes: r.Time.Minutes}
	case r.Answer.Class == transport.Refused || r.Answer.Class == transport.NotFound || r.Answer.Class == transport.NoAccess:
		v.booked = bookedTime{}
		// A refusal keeps the cache's entry (it keeps a page with its
		// message); a time the person may no longer see must not come
		// back from it at the next open.
		a.forget(r.Key)
	}
	return m, nil
}

// timeMeta are the meta bar's lines of the time on the ticket: after an
// empty line its label, then the time; none while it is not known.
func timeMeta(d *detailView) []present.MetaLine {
	if !d.booked.known {
		return nil
	}
	return []present.MetaLine{
		{},
		{Text: uitext.Inline(uitext.BarBooked), Style: present.Faint},
		{Text: present.NewInline(hoursMinutes(d.booked.minutes))},
	}
}

// hoursMinutes writes minutes as hours and minutes: 150 is 2:30, 5 is 0:05.
func hoursMinutes(minutes int) string {
	m := strconv.Itoa(minutes % 60)
	if len(m) < 2 {
		m = "0" + m
	}
	return strconv.Itoa(minutes/60) + ":" + m
}
