package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// The status notice under a sweep of focuses, widths and heights: beside
// the context line where it fits, else on the lines under it, wrapped,
// whole; cut only where the focused pane would keep no row otherwise.
// Review round 1 of #24: a notice longer than the width was cut on its
// own line, and seven wrong decisions passed every test.

// noticeShown is the notice as the screen shows it on its own lines, the
// lines under the context, without spaces: a long word is broken without
// one, and a cut notice ends in an ellipsis, so the text without spaces
// is the notice's exactly when it is whole.
func noticeShown(lines []string, n int) string {
	return strings.Join(strings.Fields(strings.Join(lines[2:2+n], "")), "")
}

// checkNotice checks one frame with the notice: its height, every line
// within the width, the notice whole beside the context or under it, and
// where it is not whole, that a line more would leave the focused pane no
// row.
func checkNotice(t *testing.T, h *harness, notice, label string) {
	t.Helper()
	m := h.m
	g := m.geometry()
	plain := ansi.Strip(m.View().Content)
	lines := strings.Split(plain, "\n")
	if len(lines) != m.height {
		t.Fatalf("%s: %d lines for a height of %d", label, len(lines), m.height)
	}
	for i, line := range lines {
		if w := m.widthMethod.StringWidth(line); w > m.width {
			t.Fatalf("%s: line %d is %d cells wide:\n%s", label, i, w, plain)
		}
	}
	if g.Small {
		return
	}
	focused := m.focusedColumn()
	if view.Capacity(focused, g) < 1 {
		t.Fatalf("%s: the notice left the focused pane no row (notice lines %d)\n%s", label, g.NoticeHeight, plain)
	}
	context := strings.TrimRight(lines[1], " ")
	switch {
	case g.NoticeHeight == 0 && strings.HasSuffix(context, " | "+notice):
		return
	case g.NoticeHeight > 0 && noticeShown(lines, g.NoticeHeight) == strings.Join(strings.Fields(notice), ""):
		if m.widthMethod.StringWidth(context+" | "+notice) <= m.width {
			t.Fatalf("%s: the notice took %d lines beside room for it: %q", label, g.NoticeHeight, context)
		}
		return
	}
	if last := strings.TrimRight(lines[1+g.NoticeHeight], " "); !strings.HasSuffix(last, "\u2026") {
		t.Fatalf("%s: a cut notice does not say so: %q", label, last)
	}
	more := g
	more.NoticeHeight, more.BodyHeight = g.NoticeHeight+1, g.BodyHeight-1
	if view.Capacity(focused, more) >= 1 {
		t.Fatalf("%s: the notice is cut with room for another line (notice lines %d)\n%s", label, g.NoticeHeight, strings.Join(lines[:min(len(lines), 2+g.NoticeHeight+1)], "\n"))
	}
}

// showNotice shows text as the launch notice of the ticket open in h.
func showNotice(h *harness, text string) {
	a := h.m.app
	a.launch.ticket, a.launch.notice = a.detail.id, present.NewInline(text)
}

func TestTheNoticeIsWholeInEveryFocusWidthAndHeight(t *testing.T) {
	notices := []struct {
		label  string
		text   string
		method ansi.Method
	}{
		{"short", "Rate limited until 15:04:05", ansi.GraphemeWidth},
		{"medium", "No MCP server of Claude Code's for https://lalabase.com; Claude Code has", ansi.GraphemeWidth},
		{"long", "macOS does not let this terminal control iTerm: allow it in System Settings > Privacy & Security > Automation", ansi.GraphemeWidth},
		// A joined emoji takes two cells as one grapheme and six in the
		// terminal's wcwidth mode: the decision measures as the drawing.
		{"wcwidth", "Folder not found: /home/anna/\U0001F468\u200d\U0001F469\u200d\U0001F467/dev/gone/and/a/longer/path", ansi.WcWidth},
	}
	// Every width to 40, where the help wraps and the fallback acts, then
	// every third (a sweep of every width took 23 s under the race
	// detector), and the column changes.
	widths := []int{}
	for w := 24; w <= 40; w++ {
		widths = append(widths, w)
	}
	for w := 43; w <= 130; w += 3 {
		widths = append(widths, w)
	}
	widths = append(widths, 79, 80, 119, 120, 150, 170)
	for _, n := range notices {
		h := newHarness(t)
		h.start()
		h.openLinked(orgA, 0)
		h.m.widthMethod = n.method
		showNotice(h, n.text)
		for _, focus := range []view.Pane{view.Navigation, view.List, view.Object} {
			h.m.focus = focus
			for _, width := range widths {
				heights := []int{9, 24}
				if width <= 40 {
					heights = []int{8, 9, 10, 11, 12, 13, 14}
				}
				for _, height := range heights {
					h.m.width, h.m.height = width, height
					checkNotice(t, h, n.text, n.label+" focus="+[]string{"navigation", "list", "object"}[focus]+" size="+itoa(width)+"x"+itoa(height))
				}
			}
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// The pane that keeps a row is the focused one: in the ticket list with
// the search line open (a notice of the list's own) on a low terminal.
func TestTheNoticeLeavesTheFocusedListARowWithTheSearchLineOpen(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	notice := "macOS does not let this terminal control iTerm: allow it in System Settings > Privacy & Security > Automation"
	showNotice(h, notice)
	h.press("esc")
	h.press("/")
	h.typeText("lo")
	if h.m.focus != view.List || h.m.mode != TextEntry {
		t.Fatalf("focus %d mode %d", h.m.focus, h.m.mode)
	}
	for width := 24; width <= 40; width++ {
		for height := 8; height <= 14; height++ {
			h.m.width, h.m.height = width, height
			checkNotice(t, h, notice, "size="+itoa(width)+"x"+itoa(height))
		}
	}
}

// PgDn reaches the end of the ticket with the notice on its lines: the
// pages are counted in the body that is left.
func TestPageDownReachesTheEndBesideANoticeOnItsOwnLines(t *testing.T) {
	for _, notice := range []string{
		"No MCP server of Claude Code's for https://lalabase.com",
		"macOS does not let this terminal control iTerm: allow it in System Settings > Privacy & Security > Automation",
	} {
		h := newHarness(t)
		h.openSix()
		showNotice(h, notice)
		h.m.width, h.m.height = 80, 24
		g := h.m.geometry()
		if g.NoticeHeight == 0 {
			t.Fatalf("%q: no line of its own at 80 columns", notice)
		}
		for i := 0; i < 100; i++ {
			h.press("pgdown")
		}
		c := h.m.objectColumn()
		if top := view.Extent(c, g, view.Object, h.m.widthMethod) - view.Capacity(c, g); h.m.app.detail.offset != top {
			t.Fatalf("%q: PgDn ends at offset %d, the last line is in sight from %d", notice, h.m.app.detail.offset, top)
		}
	}
}
