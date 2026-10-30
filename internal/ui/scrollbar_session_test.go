package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// screenBar reads the scrollbar off a screen: the last cell of the text
// inside the frame of every line that ends in the thumb (#) or the track (.). The
// object pane is the rightmost, so its frame's side is the screen's last
// column.
func screenBar(content string) string {
	var b strings.Builder
	for _, line := range strings.Split(ansi.Strip(content), "\n") {
		switch {
		case strings.HasSuffix(line, "┃ │"):
			b.WriteByte('#')
		case strings.HasSuffix(line, "╎ │"):
			b.WriteByte('.')
		}
	}
	return b.String()
}

// Long details carry the bar: the thumb at the top before scrolling, at the
// bottom after scrolling to the end, where the text's last words show.
func TestLongDetailsShowWhereTheyAreOnTheBar(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	const end = "ENDOFTHEMESSAGE"
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := h.d.rows(orgA, 1, 2)
		rows[0].Message = strings.Repeat("A long message that wraps onto many lines of the details pane. ", 40) + end
		return pageAnswer(q.Page, q.PerPage, 2, 2, rows, map[string]int{orgA: 2})
	})
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	bar := screenBar(h.m.View().Content)
	if len(bar) < 3 || !strings.HasPrefix(bar, "#") || strings.HasSuffix(bar, "#") {
		t.Fatalf("at the top, bar %q", bar)
	}
	for range 50 {
		h.press("pgdown")
	}
	content := h.m.View().Content
	bar = screenBar(content)
	if !strings.HasSuffix(bar, "#") || strings.HasPrefix(bar, "#") {
		t.Fatalf("at the end, bar %q", bar)
	}
	if !strings.Contains(ansi.Strip(content), end) {
		t.Fatalf("at the end, the last words are not in sight:\n%s", ansi.Strip(content))
	}
	// Short details: no bar.
	h.press("esc")
	h.press("down")
	h.drive(h.press("enter"))
	if bar := screenBar(h.m.View().Content); bar != "" {
		t.Fatalf("a bar on details that fit: %q", bar)
	}
}
