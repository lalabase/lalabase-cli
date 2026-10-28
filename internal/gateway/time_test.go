package gateway

import (
	"context"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// The ids of the recorded calls of summarize_time_sessions.
const (
	timeProject = "00000000-0000-4000-8000-000000000031"
	timeTicket  = "00000000-0000-4000-8000-000000000041"
)

func timeKey(ns cache.Namespace, project, ticket string) cache.Key {
	k := ns.Key(api.BookedTimeSpec.Tool)
	k.Project, k.Ticket = project, ticket
	return k
}

// S6: the booked time of one ticket is read in the Time area alone, with
// no option beside its project and ticket; anything else is refused before
// a request is made.
func TestTheChecksOfTheBookedTimeRead(t *testing.T) {
	ns := confirmed(t)
	s := NewScope(ns)
	if err := CheckReadTicket(s.Begin(Time, timeKey(ns, timeProject, timeTicket), queue.Background)); err != nil {
		t.Fatal(err)
	}
	bad := map[string]Ticket{
		"another area":     s.Begin(Detail, timeKey(ns, timeProject, timeTicket), queue.Background),
		"no ticket":        s.Begin(Time, timeKey(ns, timeProject, ""), queue.Background),
		"no project":       s.Begin(Time, timeKey(ns, "", timeTicket), queue.Background),
		"a page":           s.Begin(Time, func() cache.Key { k := timeKey(ns, timeProject, timeTicket); k.Page = 1; return k }(), queue.Background),
		"full text":        s.Begin(Time, func() cache.Key { k := timeKey(ns, timeProject, timeTicket); k.FullText = true; return k }(), queue.Background),
		"an organisation":  s.Begin(Time, func() cache.Key { k := timeKey(ns, timeProject, timeTicket); k.Organisation = orgA; return k }(), queue.Background),
		"a ticket's pages": s.Begin(Time, detailKey(ns, timeProject, timeTicket, true), queue.Background),
	}
	for name, b := range bad {
		if err := CheckReadTicket(b); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// The read takes the recorded answer; one that names another ticket than
// the key asked of is a contradiction, never a time.
func TestTheBookedTimeIsOfTheTicketAsked(t *testing.T) {
	f := setupReads(t, map[string]string{"summarize_time_sessions": "ok_own"})
	close(f.srv.release)
	r, ok := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Time, timeKey(f.ns, timeProject, timeTicket), queue.Background)))).(TimeResult)
	if !ok || !r.Answer.OK() || r.Time.Minutes != 30 || r.Time.Ticket != timeTicket || r.Flight == 0 {
		t.Fatalf("own time: %+v", r)
	}
	other := "00000000-0000-4000-8000-000000000042"
	r, ok = result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Time, timeKey(f.ns, timeProject, other), queue.Background)))).(TimeResult)
	if !ok || r.Answer.Class != transport.Protocol || r.Time != (api.BookedTime{}) {
		t.Fatalf("the time of another ticket: %+v", r)
	}
	for _, b := range []api.BookedTime{{Ticket: timeTicket, Scope: "everyone"}, {Ticket: timeTicket, Scope: "own", Minutes: -1}} {
		if CheckBookedTime(timeKey(f.ns, timeProject, timeTicket), b) == nil {
			t.Errorf("%+v accepted", b)
		}
	}
}
