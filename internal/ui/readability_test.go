package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// The ticket in the object pane is drawn in its styles: the title bold, its
// facts on one faint line, every section's heading bold with a rule, a
// comment's author bold and its time faint, the texts indented, an
// attachment's metadata faint.
func TestTheTicketIsDrawnInItsStyles(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	d := h.m.app.detail
	tl := ticketText(d, ticketContext{paged: true, when: h.m.app.when})
	if len(tl.styles) != len(tl.lines) {
		t.Fatalf("%d styles for %d lines", len(tl.styles), len(tl.lines))
	}
	c := h.m.objectColumn()
	if got := strings.Split(c.Text.String(), "\n"); len(got) != len(c.Styles) {
		t.Fatalf("the column has %d styles for %d lines", len(c.Styles), len(got))
	}
	if st := tl.styles[0]; st.Lead != len(tl.lines[0]) || st.LeadStyle != present.Strong || !strings.HasPrefix(tl.lines[0], "#6 ") {
		t.Fatalf("the title %q: %+v", tl.lines[0], st)
	}
	if st := tl.styles[1]; st.RestStyle != present.Faint || st.LeadStyle != statusStyle(d.record.StatusFunctions, present.Faint) || !strings.Contains(tl.lines[1], " · created by ") || !strings.Contains(tl.lines[1], " · updated ") {
		t.Fatalf("the facts %q: %+v", tl.lines[1], st)
	}
	for area := range ticketAreas {
		i := tl.areas[area]
		if st := tl.styles[i]; !st.Rule || st.LeadStyle != present.Strong || st.Lead != len(tl.lines[i]) {
			t.Fatalf("the heading %q: %+v", tl.lines[i], st)
		}
	}
	if st := tl.styles[tl.areas[areaDescription]+1]; st.Indent != 2 || st.LeadStyle != present.Plain || st.RestStyle != present.Plain {
		t.Fatalf("the description: %+v", st)
	}
	for _, p := range d.record.Posts {
		i := tl.posts[p.ID]
		author := personOf(p.Author)
		if st := tl.styles[i]; st.Lead != len(author) || st.LeadStyle != present.Author || st.RestStyle != present.Faint || !strings.HasPrefix(tl.lines[i], author+"  ") {
			t.Fatalf("the head %q: %+v", tl.lines[i], st)
		}
		if st := tl.styles[i+1]; st.Indent != 2 {
			t.Fatalf("the comment's text is not indented: %+v", st)
		}
	}
	last := len(tl.lines) - 1
	if st := tl.styles[last]; st.Indent != 2 || st.LeadStyle != present.Plain || st.RestStyle != present.Faint || tl.lines[last][:st.Lead] != "six-10.txt" {
		t.Fatalf("an attachment %q: %+v", tl.lines[last], st)
	}
	// The selected comment's marker is part of the bold lead.
	h.press("tab")
	tl = ticketText(h.m.app.detail, ticketContext{paged: true, when: h.m.app.when})
	i := tl.posts[h.m.app.detail.post]
	if st := tl.styles[i]; !strings.HasPrefix(tl.lines[i], "> ") || tl.lines[i][:st.Lead] != "> "+personOf(h.m.app.detail.selectedPost().Author) {
		t.Fatalf("the selected head %q: %+v", tl.lines[i], st)
	}
}

// A lead that cleaning would change is not drawn by a stale length: the
// line falls back to one style.
func TestALeadTheCleaningChangesFallsBackToOneStyle(t *testing.T) {
	var tl ticketLines
	tl.addLead("a\tb", present.Strong, " rest", present.Faint)
	if tl.lines[0] != "a b rest" || tl.styles[0].Lead != 0 {
		t.Fatalf("%q %+v", tl.lines[0], tl.styles[0])
	}
}

// With three columns an open ticket takes the navigation's room; back in
// the list the navigation returns.
func TestTheNavigationGivesWayToAnOpenTicket(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 120, 30
	h.openSix()
	screen := ansi.Strip(h.m.View().Content)
	if strings.Contains(screen, "Organisations") || !strings.Contains(screen, "Tickets") || !strings.Contains(screen, "#6 ") {
		t.Fatalf("with the ticket in focus:\n%s", screen)
	}
	if g := h.m.geometry(); g.Columns != 2 || g.ObjectWidth <= layoutWith(120, 30, 1, view.List).ObjectWidth {
		t.Fatalf("the geometry: %+v", g)
	}
	h.press("esc")
	if h.m.focus == view.Object {
		t.Fatal("esc did not leave the ticket")
	}
	if screen = ansi.Strip(h.m.View().Content); !strings.Contains(screen, "Organisations") {
		t.Fatalf("back in the list:\n%s", screen)
	}
}

// The inbox's count is faint at zero and bold above it.
func TestTheInboxCountIsQuietAtZeroAndHighlightedAbove(t *testing.T) {
	h := newHarness(t)
	h.start()
	// Südwind's notifications were all read meanwhile.
	h.m.app.counters[orgB] = count{known: true, value: 0, seq: h.m.app.counters[orgB].seq}
	var zero, unread bool
	for _, r := range h.m.chooserColumn().Rows {
		if r.Trailing.String() == "0" {
			zero = true
			if !r.Quiet || r.TrailingStyle != present.Plain {
				t.Fatalf("a zero count: %+v", r)
			}
		}
		if n := r.Trailing.String(); n != "" && n != "0" && n != "?" {
			unread = true
			if r.Quiet || r.TrailingStyle != present.Strong {
				t.Fatalf("an unread count: %+v", r)
			}
		}
	}
	if !unread || !zero {
		t.Fatalf("unread %v, zero %v: a count to look at is missing", unread, zero)
	}
}
