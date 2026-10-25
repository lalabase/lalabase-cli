package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestR1HighlightDoesNotRelabelActiveInbox(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.press("esc")
	h.press("down") // A's tickets entry (C1c)
	h.press("down") // B's inbox entry
	text := ansi.Strip(h.m.View().Content)
	if !strings.Contains(text, "Nordlicht GmbH › Inbox") || h.m.app.active.org != orgA || h.m.app.highlight != orgB {
		t.Fatalf("A rows must keep A heading when only B is highlighted; active=%q highlight=%q\n%s", h.m.app.active.org, h.m.app.highlight, text)
	}
}
