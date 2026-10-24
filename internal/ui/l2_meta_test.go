package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// factLine is the head's line of facts of ticket #1 as before L2.
const factLine = "in_progress · Website-Relaunch · assigned Anna Albers · created by Anna Albers · updated 5 Jan 09:26"

// showLogin opens ticket #1 and goes back to the list, the ticket still
// shown beside it.
func (h *harness) showLogin() {
	h.t.Helper()
	h.openLogin()
	h.press("esc")
	if h.m.focus != view.List || h.m.app.detail == nil || !h.m.app.detail.known {
		h.t.Fatalf("ticket #1 is not shown beside the list: focus %d", h.m.focus)
	}
}

// metaText is the bar's lines as text, one per line.
func metaText(c present.Column) []string {
	var out []string
	for _, l := range c.Meta {
		out = append(out, l.Text.String())
	}
	return out
}

// L2: from an inner width of 76 of the ticket pane the facts stand in a
// bar on its right: status (in its tone), project, people (the assignees,
// the creator), the time of the last change; never a creation time. The
// head's line of facts is dropped then. 158 columns with the list in
// focus are the first with room (inner 76), 157 the last without.
func TestTheMetaBarFollowsTheTicketPanesWidth(t *testing.T) {
	h := newHarness(t)
	h.showLogin()
	want := []string{"Status", "in_progress", "", "Project", "Website-Relaunch", "", "People", "Anna Albers", "created by Anna Albers", "", "Updated", "5 Jan 09:26"}
	for _, tc := range []struct {
		width int
		focus view.Pane
		inner int
	}{
		{157, view.List, 75},
		{158, view.List, 76},
		{160, view.List, 77},
		{120, view.List, 53},
		{120, view.Object, 79},
		{119, view.Object, 68},
	} {
		h.m.width, h.m.focus = tc.width, tc.focus
		if got := view.InnerWidth(h.m.geometry(), view.Object); got != tc.inner {
			t.Fatalf("width %d focus %d: inner %d, want %d", tc.width, tc.focus, got, tc.inner)
		}
		c := h.m.objectColumn()
		text := c.Text.String()
		if tc.inner < 76 {
			if c.Meta != nil || !strings.Contains(text, "\n"+factLine+"\n") {
				t.Fatalf("width %d focus %d: meta %q, head:\n%s", tc.width, tc.focus, metaText(c), text)
			}
			continue
		}
		if got := metaText(c); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("width %d focus %d: meta %q, want %q", tc.width, tc.focus, got, want)
		}
		if strings.Contains(text, "created by") || strings.Contains(text, "in_progress") {
			t.Fatalf("width %d focus %d: the facts stand in the head too:\n%s", tc.width, tc.focus, text)
		}
		if !strings.HasPrefix(text, "#1 Login bricht ab\n\n") {
			t.Fatalf("width %d focus %d: head:\n%s", tc.width, tc.focus, text)
		}
		styles := map[string]present.Style{}
		for _, l := range c.Meta {
			styles[l.Text.String()] = l.Style
		}
		for _, label := range []string{"Status", "Project", "People", "Updated"} {
			if styles[label] != present.Faint {
				t.Fatalf("label %q drawn %v, want faint", label, styles[label])
			}
		}
		if styles["in_progress"] != present.StatusActive || styles["Website-Relaunch"] != present.Plain || styles["Anna Albers"] != present.Plain {
			t.Fatalf("value styles %v", styles)
		}
		for _, line := range strings.Split(h.m.View().Content, "\n") {
			if w := h.m.widthMethod.StringWidth(line); w != tc.width && strings.Contains(line, "│") {
				t.Fatalf("width %d focus %d: a line of %d cells: %q", tc.width, tc.focus, w, ansi.Strip(line))
			}
		}
		if !strings.Contains(ansi.Strip(h.m.View().Content), "│ Status") {
			t.Fatalf("width %d focus %d: the bar is not drawn", tc.width, tc.focus)
		}
	}
}

// A ticket without assignees and without a creator still shows its people
// as the head did; one with several assignees names each on a line.
func TestTheMetaBarNamesEveryPerson(t *testing.T) {
	h := newHarness(t)
	h.showLogin()
	h.m.width = 160
	d := h.m.app.detail
	d.record.Assignees, d.record.Creator = nil, nil
	got := strings.Join(metaText(h.m.objectColumn()), "|")
	if !strings.Contains(got, "People|Unassigned|created by Unknown user|") {
		t.Fatalf("no people: %q", got)
	}
	d.record.Assignees = append(d.record.Assignees, d.record.Project, d.record.Project)
	d.record.Assignees[0].Name, d.record.Assignees[1].Name = "Anna Albers", "Ben Berger"
	got = strings.Join(metaText(h.m.objectColumn()), "|")
	if !strings.Contains(got, "People|Anna Albers|Ben Berger|created by") {
		t.Fatalf("two people: %q", got)
	}
}

// PgDn reaches the end of the ticket beside the bar: the pane measures the
// text at the width it draws it.
func TestPageDownReachesTheEndBesideTheMetaBar(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.m.width, h.m.height = 120, 24
	g := h.m.geometry()
	c := h.m.objectColumn()
	if c.Meta == nil {
		t.Fatal("no bar at 120 columns with the ticket in focus")
	}
	for i := 0; i < 200; i++ {
		h.press("pgdown")
	}
	c = h.m.objectColumn()
	if top := view.Extent(c, g, view.Object, h.m.widthMethod) - view.Capacity(c, g); h.m.app.detail.offset != top {
		t.Fatalf("PgDn ends at offset %d, the last line is in sight from %d", h.m.app.detail.offset, top)
	}
}

// The pane keeps a section's heading in sight beside the bar: its place is
// measured at the text's width there, so a long description that wraps
// into more lines beside the bar does not push the heading out.
func TestTabKeepsTheHeadingInSightBesideTheMetaBar(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.m.width, h.m.height = 120, 24
	h.m.app.detail.record.DescriptionText = strings.Repeat("A long description that wraps differently beside the bar. ", 30)
	if h.m.objectColumn().Meta == nil {
		t.Fatal("no bar")
	}
	h.press("tab")
	lines := strings.Split(ansi.Strip(h.m.View().Content), "\n")
	first := 3
	if h.m.objectColumn().Notice.String() != "" {
		first = 4
	}
	if !strings.Contains(lines[first], "» Comments") {
		t.Fatalf("the heading is not the pane's first line:\n%s", strings.Join(lines[:8], "\n"))
	}
}
