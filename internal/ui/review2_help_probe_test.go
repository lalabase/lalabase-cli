package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

func TestR2ScrollBindingsUseFinalGeometry(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	for _, focus := range []view.Pane{view.Object, view.List} {
		h.m.focus = focus
		for width := 24; width <= 160; width += 3 { // every third width: under the race detector a full sweep takes minutes
			for height := 8; height <= 50; height++ {
				h.m.width, h.m.height = width, height
				g := h.m.geometry()
				if g.Small {
					continue
				}
				c := h.m.objectColumn()
				if focus == view.List {
					c = h.m.listColumn()
				}
				extent, capacity := view.Extent(c, g, focus, h.m.widthMethod), view.Capacity(c, g)
				if overflow, offered := extent > capacity, h.m.allowed()[PageDown]; overflow != offered {
					t.Fatalf("focus=%d size=%dx%d final extent=%d capacity=%d help=%q offered=%v\n%s", focus, width, height, extent, capacity, helpText(h.m), offered, ansi.Strip(h.m.View().Content))
				}
			}
		}
	}
}

func TestR2DetailsMessageIsCleanedAsBodyOnce(t *testing.T) {
	h := newHarness(t)
	raw := "First line\nSecond line\tindented"
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := h.d.rows(orgA, 1, 1)
		rows[0].Message = raw
		return pageAnswer(q.Page, q.PerPage, 1, 1, rows, map[string]int{orgA: 1})
	})
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	want := present.NewBody(raw).String() + "\n\nActor:"
	got := h.m.screen().Object.Text.String()
	if !strings.HasPrefix(got, want) {
		t.Fatalf("body cleaned once should start %q, got %q", want, got)
	}
}

func TestR2AdvertisedMovementActuallyMoves(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	for width := 24; width <= 160; width += 3 { // every third width, see above
		for height := 8; height <= 40; height++ {
			h.m.width, h.m.height = width, height
			if h.m.small() || !h.m.allowed()[Down] {
				continue
			}
			v := h.m.app.active
			v.selected = v.rows[0].ID
			before := ansi.Strip(h.m.View().Content)
			down := h.press("down")
			afterDown := ansi.Strip(h.m.View().Content)
			up := h.press("up")
			afterUp := ansi.Strip(h.m.View().Content)
			if down == nil && up == nil && before == afterDown && before == afterUp {
				t.Fatalf("%dx%d: offered up/down, neither changes visible content; body=%d help=%q\n%s", width, height, h.m.geometry().BodyHeight, helpText(h.m), before)
			}
		}
	}
}

func TestR2FrameSweep(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	for _, focus := range []view.Pane{view.Navigation, view.List, view.Object} {
		h.m.focus = focus
		for width := 1; width <= 160; width += 3 { // every third width, see above
			for height := 1; height <= 40; height++ {
				h.m.width, h.m.height = width, height
				lines := strings.Split(h.m.View().Content, "\n")
				if len(lines) != height {
					t.Fatalf("%dx%d: got %d lines", width, height, len(lines))
				}
				for _, line := range lines {
					if cells := h.m.widthMethod.StringWidth(line); cells > width {
						t.Fatalf("%dx%d: got %d cells", width, height, cells)
					}
				}
			}
		}
	}
}
