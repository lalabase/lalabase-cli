package api

import (
	"context"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// The ids of the recorded calls of summarize_time_sessions.
const (
	timeProject = "00000000-0000-4000-8000-000000000031"
	timeTicket  = "00000000-0000-4000-8000-000000000041"
)

// S6: the time the token's own person booked on one ticket. The adapter
// asks with creator "me" and the ticket's id, and takes the total and the
// ticket the answer names.
func TestBookedTimeIsTheOwnTimeOfOneTicket(t *testing.T) {
	for _, c := range []struct {
		name, scope string
		minutes     int
	}{{"ok_own", "all", 30}, {"ok_own_tier", "own", 30}} {
		api, sent := replayCase(t, "summarize_time_sessions", c.name)
		got, ans := api.BookedTime(context.Background(), timeProject, timeTicket)
		if !ans.OK() || got.Minutes != c.minutes || got.Scope != c.scope || got.Ticket != timeTicket {
			t.Fatalf("%s: %+v %v", c.name, got, ans)
		}
		args := *sent
		if args["creator"] != "me" || args["ticket"] != timeTicket || args["project_id"] != timeProject || len(args) != 3 {
			t.Fatalf("%s: sent %v", c.name, args)
		}
	}
}

// No ticket, or one the token cannot read, is not found; no access to the
// project's time is a refusal, a foreign project not found.
func TestBookedTimeAnswersByClass(t *testing.T) {
	for name, want := range map[string]transport.Class{"empty": transport.NotFound, "refused": transport.Refused, "not_found": transport.NotFound} {
		api, _ := replayCase(t, "summarize_time_sessions", name)
		got, ans := api.BookedTime(context.Background(), timeProject, timeTicket)
		if ans.Class != want || got != (BookedTime{}) {
			t.Fatalf("%s: %v %+v", name, ans.Class, got)
		}
	}
}
