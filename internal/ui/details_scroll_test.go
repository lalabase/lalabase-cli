package ui

import (
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// Review round 3, a decision pinned: the details' scroll pair is measured
// in the geometry of the help without it. Where the text fits the pane the
// help without the pair leaves, the person gets that pane and no scroll
// line; measured with the pair in, the same text would be shown in a
// smaller pane with a scroll line (consistent too, but a line of help
// spent on scrolling that is not needed). Every width from 24 to 60, the
// heights around the wrap of the help.
func TestTheDetailsScrollIsNotOfferedWhereTheTextFitsWithoutIt(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := h.d.rows(orgA, 1, 2)
		rows[0].Message = strings.Repeat("A message of some length. ", 6)
		return pageAnswer(q.Page, q.PerPage, 2, 2, rows, map[string]int{orgA: 2})
	})
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	h.m.focus = view.Object
	fitsWithout := 0
	for width := 24; width <= 60; width++ {
		for height := 8; height <= 40; height++ {
			h.m.width, h.m.height = width, height
			if h.m.small() {
				continue
			}
			// The geometry of the help without the pair: the bindings of
			// the state, the scroll pair taken out.
			var without []binding
			for _, b := range h.m.bindings(false) {
				if b.kind != PageUp {
					without = append(without, b)
				}
			}
			g := layoutWith(width, height, len(view.HelpLines(toHelp(without), width, h.m.widthMethod)), h.m.focus)
			c := h.m.objectColumn()
			if view.Extent(c, g, view.Object, h.m.widthMethod) > view.Capacity(c, g) {
				continue
			}
			fitsWithout++
			if h.m.allowed()[PageDown] {
				t.Fatalf("%dx%d: the text fits the pane without the scroll line, and the line is offered: help %q", width, height, helpText(h.m))
			}
		}
	}
	if fitsWithout == 0 {
		t.Fatal("no size where the text fits without the pair: the sweep proves nothing")
	}
}
