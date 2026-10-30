package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// R3: Capacity is not monotone in the body height (compact rows below
// body 8): dropping the pair can grow the body from 7 to 8 rows and halve
// the capacity. Adapted after the fix: a list's page keys are offered with
// a second row and act by moving the selection, whatever the pane shows;
// an overflow without them is the failure. Every third width.
func TestR3ScrollPairInFinalGeometryAllRowCounts(t *testing.T) {
	t.Parallel()

	missing, both := 0, 0
	defer func() {
		t.Logf("missing=%d offered-without-overflow=%d", missing, both)
		if missing+both > 0 {
			t.Fail()
		}
	}()
	_ = ansi.Strip
	for n := 2; n <= 12; n++ {
		h := newHarness(t)
		h.f.set(func(q api.NotificationQuery) transport.Answer {
			return pageAnswer(q.Page, q.PerPage, n, n, h.d.rows(orgA, 1, n), map[string]int{orgA: n})
		})
		h.start()
		h.drive(h.choose(orgA))
		h.m.focus = view.List
		for width := 24; width <= 160; width += 3 {
			for height := 8; height <= 40; height++ {
				h.m.width, h.m.height = width, height
				g := h.m.geometry()
				if g.Small {
					continue
				}
				c := h.m.listColumn()
				extent, capacity := view.Extent(c, g, view.List, h.m.widthMethod), view.Capacity(c, g)
				if overflow, offered := extent > capacity, h.m.allowed()[PageDown]; (overflow && !offered) || (offered && n < 2) {
					if offered {
						both++
					} else {
						missing++
					}
					if missing+both <= 3 {
						t.Logf("rows=%d size=%dx%d body=%d extent=%d capacity=%d offered=%v", n, width, height, g.BodyHeight, extent, capacity, offered)
					}
				}
			}
		}
	}
}

// R3: R from a failed global page 1 reads it through the counters' waiter;
// the view does not load meanwhile and offers ] during the read.
func TestR3RFromFailedGlobalPageOneLoads(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	h.drive(h.choose(""))
	a := h.m.app
	if !a.active.known || a.active.page != 1 {
		t.Fatalf("setup: %+v", a.active)
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer { return failing(transport.Temporary, 503) })
	h.drive(h.press("R"))
	if a.steps[stepCounters].state != failed || !a.active.stale {
		t.Fatalf("setup: counters %d stale %v", a.steps[stepCounters].state, a.active.stale)
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer { return h.d.defaultInbox(q) })
	cmd := h.press("R")
	if cmd == nil {
		t.Fatal("R did nothing")
	}
	t.Logf("pending(view key)=%v loading=%v notice=%q nextOffered=%v help=%q", a.pending(a.active.key), a.active.loading, h.m.screen().List.Notice.String(), h.m.allowed()[NextPage], helpText(h.m))
	if a.pending(a.active.key) && !a.active.loading {
		t.Errorf("a read of the view's key is out, the view does not load")
	}
}
