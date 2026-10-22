package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// wheel turns the mouse wheel once over the cell x, y.
func (h *harness) wheel(button tea.MouseButton, x, y int) {
	h.t.Helper()
	h.feed(tea.MouseWheelMsg{X: x, Y: y, Button: button})
}

// objectCell is a cell inside the object pane's body: its first column,
// the body's first line.
func (h *harness) objectCell() (x, y int) {
	g := h.m.geometry()
	if g.Columns == 1 {
		return 0, 2 + g.NoticeHeight
	}
	return g.Width - g.ObjectWidth, 2 + g.NoticeHeight
}

// The interface asks for the mouse's buttons and wheel, never its motion.
func TestTheViewReportsTheWheel(t *testing.T) {
	h := newHarness(t)
	if mode := h.m.View().MouseMode; mode != tea.MouseModeCellMotion {
		t.Fatalf("mouse mode %v", mode)
	}
}

// The wheel over the ticket scrolls it by wheelLines, lets the anchor go,
// stops at both ends where pgup and pgdown stop, and leaves the selected
// comment as it is.
func TestTheWheelScrollsTheTicket(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab") // comments: the anchor follows the section
	post := h.m.app.detail.post
	anchored := h.m.objectColumn().Offset
	if anchored < wheelLines || h.m.app.detail.follow == anchorNone {
		t.Fatalf("the comments' heading is not followed below the top: offset %d", anchored)
	}
	x, y := h.objectCell()
	// From where the pane is, the anchor's place.
	h.wheel(tea.MouseWheelUp, x, y)
	if d := h.m.app.detail; d.offset != anchored-wheelLines || d.follow != anchorNone {
		t.Fatalf("up from the anchor at %d: offset %d, follow %v", anchored, d.offset, d.follow)
	}
	for range anchored {
		h.wheel(tea.MouseWheelUp, x, y)
	}
	if got := h.m.app.detail.offset; got != 0 {
		t.Fatalf("past the top: offset %d", got)
	}
	h.wheel(tea.MouseWheelDown, x, y)
	if got := h.m.app.detail.offset; got != wheelLines {
		t.Fatalf("one turn down: offset %d", got)
	}
	h.wheel(tea.MouseWheelDown, x, y)
	if got := h.m.app.detail.offset; got != 2*wheelLines {
		t.Fatalf("two turns down: offset %d", got)
	}
	h.wheel(tea.MouseWheelUp, x, y)
	if got := h.m.app.detail.offset; got != wheelLines {
		t.Fatalf("back up: offset %d", got)
	}
	for range 500 {
		h.wheel(tea.MouseWheelDown, x, y)
	}
	end := h.m.app.detail.offset
	for range 50 {
		h.press("pgdown")
	}
	if end == 0 || h.m.app.detail.offset != end {
		t.Fatalf("the wheel ends at %d, pgdown at %d", end, h.m.app.detail.offset)
	}
	if h.m.app.detail.post != post {
		t.Fatal("the wheel moved the selected comment")
	}
	for range 500 {
		h.wheel(tea.MouseWheelUp, x, y)
	}
	if got := h.m.app.detail.offset; got != 0 {
		t.Fatalf("at the top: offset %d", got)
	}
}

// Only the object pane's body scrolls: the wheel over the list, the
// header, the help, and the sideways wheel leave the ticket where it is.
func TestTheWheelScrollsOnlyOverTheObjectPane(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab") // an anchor the wheel would let go
	follow, offset := h.m.app.detail.follow, h.m.objectColumn().Offset
	g := h.m.geometry()
	x, y := h.objectCell()
	for _, c := range []struct {
		name   string
		button tea.MouseButton
		x, y   int
	}{
		{"over the list", tea.MouseWheelDown, x - view.SeparatorWidth - 1, y},
		{"over the separator", tea.MouseWheelDown, x - 1, y},
		{"over the header", tea.MouseWheelDown, x, 0},
		{"over the context line", tea.MouseWheelDown, x, 1},
		{"over the help", tea.MouseWheelDown, x, g.Height - 1},
		{"below the screen", tea.MouseWheelDown, x, g.Height},
		{"right of the screen", tea.MouseWheelDown, g.Width, y},
		{"sideways", tea.MouseWheelRight, x, y},
	} {
		h.wheel(c.button, c.x, c.y)
		if d := h.m.app.detail; d.follow != follow || h.m.objectColumn().Offset != offset {
			t.Fatalf("%s: offset %d, follow %v", c.name, h.m.objectColumn().Offset, d.follow)
		}
	}
	// The pane's last line and last column still count.
	h.wheel(tea.MouseWheelDown, g.Width-1, 2+g.NoticeHeight+g.BodyHeight-1)
	if got := h.m.app.detail.offset; got != offset+wheelLines {
		t.Fatalf("over the last cell: offset %d", got)
	}
}

// The ticket in the pane beside the list scrolls under the wheel while the
// focus stays on the list, in three columns and in two.
func TestTheWheelScrollsThePreviewBesideTheList(t *testing.T) {
	for _, width := range []int{120, 80} {
		h := newHarness(t)
		h.feed(tea.WindowSizeMsg{Width: width, Height: 24})
		h.openSix()
		h.press("esc")
		if h.m.focus != view.List || h.m.app.detail == nil {
			t.Fatalf("width %d: focus %v, detail %v", width, h.m.focus, h.m.app.detail)
		}
		x, y := h.objectCell()
		h.wheel(tea.MouseWheelDown, x, y)
		if got := h.m.app.detail.offset; got != wheelLines || h.m.focus != view.List {
			t.Fatalf("width %d: offset %d, focus %v", width, got, h.m.focus)
		}
	}
}

// In one column the pane fills the screen when it has the focus; with the
// focus on the list it is not in sight and the wheel does nothing to it.
func TestTheWheelInOneColumn(t *testing.T) {
	h := newHarness(t)
	h.feed(tea.WindowSizeMsg{Width: 60, Height: 24})
	h.openSix()
	h.wheel(tea.MouseWheelDown, 0, 2)
	if got := h.m.app.detail.offset; got != wheelLines {
		t.Fatalf("pane in focus: offset %d", got)
	}
	h.press("esc")
	before := h.m.app.detail.offset
	h.wheel(tea.MouseWheelDown, 0, 2)
	if got := h.m.app.detail.offset; got != before {
		t.Fatalf("pane out of sight: offset %d, was %d", got, before)
	}
}

// The details of a notification scroll under the wheel as well.
func TestTheWheelScrollsTheDetails(t *testing.T) {
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := h.d.rows(orgA, 1, 2)
		rows[0].Message = strings.Repeat("A long message that wraps onto many lines of the details pane. ", 40)
		return pageAnswer(q.Page, q.PerPage, 2, 2, rows, map[string]int{orgA: 2})
	})
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	x, y := h.objectCell()
	h.wheel(tea.MouseWheelDown, x, y)
	if got := h.m.app.active.detailOffset; got != wheelLines {
		t.Fatalf("one turn down: offset %d", got)
	}
	h.wheel(tea.MouseWheelUp, x, y)
	h.wheel(tea.MouseWheelUp, x, y)
	if got := h.m.app.active.detailOffset; got != 0 {
		t.Fatalf("past the top: offset %d", got)
	}
}

// Without a session (the shell) and on a small terminal the wheel does
// nothing and does not panic.
func TestTheWheelWithoutAPane(t *testing.T) {
	m := New()
	m.width, m.height = 120, 24
	next, _ := m.Update(tea.MouseWheelMsg{X: 100, Y: 3, Button: tea.MouseWheelDown})
	if next.(Model).focus != m.focus {
		t.Fatal("the wheel changed the shell")
	}
	h := newHarness(t)
	h.openSix()
	h.feed(tea.WindowSizeMsg{Width: 20, Height: 6})
	h.wheel(tea.MouseWheelDown, 10, 2)
	if got := h.m.app.detail.offset; got != 0 {
		t.Fatalf("small terminal: offset %d", got)
	}
}
