package ui

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

func TestReview2RemovalClearsAnotherPageOfActiveResource(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	h.drive(h.choose(""))
	h.drive(h.press("]"))
	h.drive(h.press("enter"))
	a := h.m.app
	if a.active.page != 2 || len(a.active.rows) == 0 || a.active.opened == "" {
		t.Fatal("need page 2 with opened details")
	}
	oldID := a.active.opened
	h.press("left")
	h.press("left")
	h.f.set(func(api.NotificationQuery) transport.Answer { return failing(transport.NoAccess, 200) })
	h.drive(h.press("R"))
	_, cached := a.store.Get(a.active.key)
	t.Logf("after newer resource removal: page=%d rows=%d opened=%q old=%q cached=%v globalKnown=%v notice=%q", a.active.page, len(a.active.rows), a.active.opened, oldID, cached, a.global.known, h.m.screen().List.Notice.String())
	if len(a.active.rows) != 0 || a.active.opened != "" {
		t.Fatal("resource removal left stale page 2 rows and details on screen")
	}
}

func TestReview2OtherResourceNewerPageDoesNotRefuseRemoval(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	a := h.m.app
	h.f.set(func(api.NotificationQuery) transport.Answer { return failing(transport.NoAccess, 200) })
	old := messages(t, h.press("R"))
	h.f.set(h.d.defaultInbox)
	h.drive(h.choose(orgB))
	h.feed(old...)
	_, cachedGlobal := a.store.Get(a.inboxKey("", 1))
	_, cachedB := a.store.Get(a.inboxKey(orgB, 1))
	t.Logf("globalKnown=%v cachedGlobal=%v cachedB=%v BRows=%d BCount=%+v", a.global.known, cachedGlobal, cachedB, len(a.active.rows), a.countOf(orgB))
	if a.global.known || cachedGlobal || !cachedB || len(a.active.rows) != 3 || a.countOf(orgB).value != 3 {
		t.Fatal("removal of global resource was blocked by another resource or damaged B")
	}
}
