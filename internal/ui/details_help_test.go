package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestR1HelpHasNoScrollForFullyVisibleDetails(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.press("enter")
	before := ansi.Strip(h.m.View().Content)
	up := h.press("pgup")
	afterUp := ansi.Strip(h.m.View().Content)
	down := h.press("pgdown")
	afterDown := ansi.Strip(h.m.View().Content)
	if strings.Contains(before, "u/space page") && up == nil && down == nil && before == afterUp && before == afterDown {
		t.Fatalf("help advertises scroll but both keys leave a fully visible details pane unchanged (hidden offset=%d):\n%s", h.m.app.active.detailOffset, before)
	}
}
