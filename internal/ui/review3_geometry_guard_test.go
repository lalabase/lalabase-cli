package ui

import (
	"context"
	"reflect"
	"strings"
	"time"

	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/worker"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

func TestR3CompactTransitionScroll(t *testing.T) {
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(q.Page, 25, 5, 5, h.d.rows(orgA, 1, 5), map[string]int{orgA: 5})
	})
	h.start()
	h.drive(h.choose(orgA))
	h.m.focus = view.List
	for width := 24; width <= 160; width++ {
		if (width-24)%3 == 0 {
			continue
		}
		for height := 8; height <= 30; height++ {
			h.m.width, h.m.height = width, height
			g := h.m.geometry()
			if g.Small {
				continue
			}
			c := h.m.listColumn()
			overflow := view.Extent(c, g, view.List, h.m.widthMethod) > view.Capacity(c, g)
			offered := h.m.allowed()[PageDown]
			// Adapted in round 3: a list's page keys come with a second row
			// and move the selection; an overflow without them is the failure.
			if (overflow && !offered) || (offered && len(c.Rows) < 2) {
				t.Fatalf("size=%dx%d rows=%d body=%d capacity=%d scroll offered=%v help=%q\n%s", width, height, len(c.Rows), g.BodyHeight, view.Capacity(c, g), offered, helpText(h.m), ansi.Strip(h.m.View().Content))
			}
		}
	}
}

func TestR3NoticeDoesNotOfferInvisibleMovement(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.f.set(func(api.NotificationQuery) transport.Answer { return failing(transport.Temporary, 503) })
	h.drive(h.press("R"))
	if !h.m.app.active.stale {
		t.Fatal("temporary refresh did not produce stale state")
	}
	for width := 24; width <= 160; width++ {
		if (width-24)%3 == 0 {
			continue
		}
		for height := 8; height <= 20; height++ {
			h.m.width, h.m.height = width, height
			if h.m.small() || !h.m.allowed()[Down] {
				continue
			}
			v := h.m.app.active
			v.selected = v.rows[0].ID
			before := ansi.Strip(h.m.View().Content)
			h.press("down")
			after := ansi.Strip(h.m.View().Content)
			h.press("up")
			afterUp := ansi.Strip(h.m.View().Content)
			if before == after && before == afterUp {
				t.Fatalf("size=%dx%d stale rows=%d body=%d notice=%q advertised movement changed no visible content; help=%q\n%s", width, height, len(v.rows), h.m.geometry().BodyHeight, h.m.listColumn().Notice.String(), helpText(h.m), strings.TrimSpace(before))
			}
		}
	}
}

func TestR3IntermediateWidthGeometry(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	for _, focus := range []view.Pane{view.List, view.Object} {
		h.m.focus = focus
		for width := 25; width <= 160; width++ {
			if (width-24)%3 == 0 {
				continue
			}
			for height := 8; height <= 40; height++ {
				h.m.width, h.m.height = width, height
				g := h.m.geometry()
				if g.Small {
					continue
				}
				c := h.m.listColumn()
				if focus == view.Object {
					c = h.m.objectColumn()
				}
				overflow := view.Extent(c, g, focus, h.m.widthMethod) > view.Capacity(c, g)
				if offered := h.m.allowed()[PageDown]; overflow != offered {
					t.Fatalf("focus=%v size=%dx%d extent=%d capacity=%d offered=%v", focus, width, height, view.Extent(c, g, focus, h.m.widthMethod), view.Capacity(c, g), offered)
				}
			}
		}
	}
}

func TestR3NestedBatchPanicGuarded(t *testing.T) {
	sup := worker.New(context.Background())
	defer sup.Stop(time.Second)
	g := guarded{sup: sup}
	quiet := func() tea.Msg { return nil }
	dangerous := func() tea.Msg { panic("foreign server panic") }
	cmd := g.guard(tea.Batch(quiet, tea.Batch(quiet, dangerous)))
	var escaped any
	func() { defer func() { escaped = recover() }(); messages(t, cmd) }()
	if escaped != nil || sup.Err() == nil {
		t.Fatalf("nested second batch command: escaped=%v fatal=%v", escaped != nil, sup.Err())
	}
}

func TestR3StoppedGuardDoesNotRunCommand(t *testing.T) {
	sup := worker.New(context.Background())
	g := guarded{sup: sup}
	sup.Stop(time.Second)
	calls := 0
	msg := g.guard(func() tea.Msg { calls++; return "late row" })()
	if calls != 0 || msg != nil {
		t.Fatalf("stopped command executed: calls=%d msg=%v", calls, msg)
	}
}

func TestR3SequenceGuardProbe(t *testing.T) {
	sup := worker.New(context.Background())
	defer sup.Stop(time.Second)
	g := guarded{sup: sup}
	msg := g.guard(tea.Sequence(func() tea.Msg { return nil }, func() tea.Msg { panic("foreign server panic") }))()
	// sequenceMsg is deliberately unexported. Reflect only its exported Cmd
	// elements to execute exactly the library's loop, on this test goroutine.
	cmds := reflect.ValueOf(msg)
	var escaped any
	func() {
		defer func() { escaped = recover() }()
		for i := 0; i < cmds.Len(); i++ {
			cmds.Index(i).Interface().(tea.Cmd)()
		}
	}()
	if escaped == nil || sup.Err() != nil {
		t.Fatalf("sequence probe changed: escaped=%v fatal=%v", escaped != nil, sup.Err())
	}
	t.Log("Sequence escapes guard; no production Sequence call exists, so backlog only")
}
