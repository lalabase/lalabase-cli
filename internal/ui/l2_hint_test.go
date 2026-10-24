package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

const pathOfSix = "Nordlicht GmbH › Tickets › Website-Relaunch · page 1 of 2"

// L2: a short hint (a plain notice, what x did) takes the path's place in
// the second line for 5 s by the model's clock, then the path is back;
// the expiry of its deadline drops it, an expiry of an older one does not.
func TestAHintTakesThePathsPlaceForFiveSeconds(t *testing.T) {
	for _, width := range []int{80, 120, 160} {
		h := newHarness(t)
		h.withLauncher()
		h.openSix()
		h.m.width = width
		if got := line2(h); got != pathOfSix {
			t.Fatalf("width %d, before x: %q", width, got)
		}
		h.drive(h.press("x"))
		const hint = "Claude Code opened beside #6"
		if got := line2(h); got != hint {
			t.Fatalf("width %d, the hint: %q, want %q", width, got, hint)
		}
		start := h.now
		h.now = start.Add(5*time.Second - time.Nanosecond)
		if got := line2(h); got != hint {
			t.Fatalf("width %d, just before 5 s: %q", width, got)
		}
		h.now = start.Add(5 * time.Second)
		if got := line2(h); got != pathOfSix {
			t.Fatalf("width %d, after 5 s: %q", width, got)
		}
		if s := h.m.screen(); s.Status.String() != "" {
			t.Fatalf("width %d, the hint is still the status: %q", width, s.Status.String())
		}
	}
}

// The deadline's expiry, by the Events' way, drops the hint the deadline
// is for; an expiry of another session or of an older hint leaves the
// newer one.
func TestAHintsExpiryDropsOnlyItsOwnHint(t *testing.T) {
	h := newHarness(t)
	h.withLauncher()
	h.openSix()
	h.drive(h.press("x"))
	first := h.m.app.launch.until
	if !first.Equal(h.now.Add(5 * time.Second)) {
		t.Fatalf("the hint ends at %v, want 5 s after %v", first, h.now)
	}
	h.now = h.now.Add(2 * time.Second)
	h.drive(h.press("x"))
	second := h.m.app.launch.until
	if !second.Equal(h.now.Add(5 * time.Second)) {
		t.Fatalf("the second hint ends at %v", second)
	}
	h.feed(gateway.Expired{Session: h.m.app.session, Until: first})
	if got := h.status(); got != "Claude Code opened beside #6" {
		t.Fatalf("an older hint's expiry dropped the newer one: %q", got)
	}
	h.feed(gateway.Expired{Session: h.m.app.session + 1, Until: second})
	if got := h.status(); got == "" {
		t.Fatal("another session's expiry dropped the hint")
	}
	h.feed(gateway.Expired{Session: h.m.app.session, Until: second})
	if got := h.status(); got != "" {
		t.Fatalf("the hint's own expiry left it: %q", got)
	}
	if got := line2(h); got != pathOfSix {
		t.Fatalf("after the expiry: %q", got)
	}
}

// A problem keeps today's notice: beside the path, in its tone, as long as
// the state holds, whatever the clock says; so does a launch that runs.
func TestAProblemStaysBesideThePath(t *testing.T) {
	h := newHarness(t)
	f := h.withLauncher()
	f.kind = launch.NoClaude
	h.openSix()
	cmd := h.press("x")
	if got, want := line2(h), pathOfSix+" | "; !strings.HasPrefix(ansi.Strip(got), want) || h.m.screen().StatusStyle != present.NoticeWaiting {
		t.Fatalf("while launching: %q", got)
	}
	h.drive(cmd)
	want := pathOfSix + " | claude was not found on the PATH"
	if got := ansi.Strip(line2(h)); got != want {
		t.Fatalf("the problem: %q, want %q", got, want)
	}
	if !h.m.app.launch.until.IsZero() {
		t.Fatalf("a problem has a deadline: %v", h.m.app.launch.until)
	}
	h.now = h.now.Add(time.Hour)
	if got := ansi.Strip(line2(h)); got != want {
		t.Fatalf("an hour later: %q", got)
	}
	// A rate-limit pause waits beside the path too.
	h.m.app.launch.notice = present.Inline{}
	h.m.app.pause = h.now.Add(time.Minute)
	if got := ansi.Strip(line2(h)); !strings.HasPrefix(got, pathOfSix+" | Rate limited until ") {
		t.Fatalf("the pause: %q", got)
	}
}

// A hint wider than the terminal is not cut: it wraps into lines under the
// second one, taken from the body, and the screen keeps its height.
func TestAWideHintWrapsInsteadOfBeingCut(t *testing.T) {
	h := newHarness(t)
	h.withLauncher()
	h.openSix()
	h.drive(h.press("x"))
	h.m.width, h.m.height = 24, 24
	lines := strings.Split(ansi.Strip(h.m.View().Content), "\n")
	if len(lines) != 24 {
		t.Fatalf("%d lines", len(lines))
	}
	got := strings.TrimRight(lines[1], " ") + " " + strings.TrimRight(lines[2], " ")
	if got != "Claude Code opened beside #6" {
		t.Fatalf("lines 2 and 3: %q", got)
	}
	if g := h.m.geometry(); g.NoticeHeight != 1 {
		t.Fatalf("the hint takes %d lines of the body, want 1", g.NoticeHeight)
	}
}

// A hint under a sweep of focuses, widths and heights: in the path's line
// and the lines it wraps into under it, whole; cut, and marked, only where
// one more line would leave the focused pane no row.
func TestAHintIsWholeInEveryFocusWidthAndHeight(t *testing.T) {
	const hint = "Claude Code ended; back at #6, and a longer hint that wraps on a narrow terminal"
	h := newHarness(t)
	h.withLauncher()
	h.openSix()
	a := h.m.app
	a.launch.ticket, a.launch.notice, a.launch.tone, a.launch.until = a.detail.id, present.NewInline(hint), present.Plain, h.now.Add(time.Second)
	widths := []int{}
	for w := 24; w <= 40; w++ {
		widths = append(widths, w)
	}
	widths = append(widths, 79, 80, 119, 120, 170)
	wrapped, cut := 0, 0
	for _, focus := range []view.Pane{view.Navigation, view.List, view.Object} {
		h.m.focus = focus
		for _, width := range widths {
			for _, height := range []int{8, 9, 10, 11, 12, 14, 24} {
				h.m.width, h.m.height = width, height
				label := fmt.Sprintf("focus=%d size=%dx%d", focus, width, height)
				g := h.m.geometry()
				lines := strings.Split(ansi.Strip(h.m.View().Content), "\n")
				if len(lines) != height {
					t.Fatalf("%s: %d lines", label, len(lines))
				}
				for i, line := range lines {
					if w := h.m.widthMethod.StringWidth(line); w > width {
						t.Fatalf("%s: line %d is %d cells", label, i, w)
					}
				}
				if g.Small {
					continue
				}
				focused := h.m.focusedColumn()
				if view.Capacity(focused, g) < 1 {
					t.Fatalf("%s: the hint left the focused pane no row", label)
				}
				shown := strings.Join(strings.Fields(strings.Join(lines[1:2+g.NoticeHeight], " ")), " ")
				if shown == hint {
					wrapped += boolInt(g.NoticeHeight > 0)
					continue
				}
				cut++
				if !strings.HasSuffix(shown, "…") {
					t.Fatalf("%s: a cut hint does not say so: %q", label, shown)
				}
				more := g
				more.NoticeHeight, more.BodyHeight = g.NoticeHeight+1, g.BodyHeight-1
				if view.Capacity(focused, more) >= 1 {
					t.Fatalf("%s: the hint is cut with room for another line: %q", label, shown)
				}
			}
		}
	}
	if wrapped == 0 || cut == 0 {
		t.Fatalf("the sweep saw %d wrapped and %d cut hints, want both", wrapped, cut)
	}
}
