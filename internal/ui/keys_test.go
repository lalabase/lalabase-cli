package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

func press(name string) tea.KeyPressMsg {
	special := map[string]rune{"left": tea.KeyLeft, "right": tea.KeyRight, "up": tea.KeyUp, "down": tea.KeyDown, "enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown}
	if code, ok := special[name]; ok {
		return tea.KeyPressMsg{Code: code}
	}
	if name == "ctrl+c" {
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	if name == "ctrl+s" {
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(name)[0], Text: name}
}

// helpText is the help as shown for the model, one plain string.
func helpText(m Model) string {
	return ansi.Strip(strings.Join(view.HelpLines(m.help(), max(m.width, 1), m.widthMethod), "\n"))
}

func TestShellKeysAndHelpAgree(t *testing.T) {
	m := New()
	m.width, m.height = 120, 24
	if help := helpText(m); !strings.Contains(help, "ctrl+c") || !strings.Contains(help, "left/right") || !strings.Contains(help, "q") {
		t.Fatalf("missing shell help: %q", help)
	}
	for _, name := range []string{"up", "down", "tab", "pgup", "pgdown", "[", "]", "/", "r", "R", "ctrl+s", "?", "j", "k", "v"} {
		next, cmd := step(t, m, press(name))
		if next != m || cmd != nil {
			t.Fatalf("inactive key %s acted", name)
		}
	}
	if next, cmd := step(t, m, tea.PasteMsg{Content: "q\n\x03"}); next != m || cmd != nil {
		t.Fatal("paste acted as navigation")
	}
	for _, tc := range []struct {
		from view.Pane
		key  string
		to   view.Pane
	}{
		{view.List, "left", view.Navigation}, {view.Navigation, "left", view.Navigation}, {view.List, "right", view.Object}, {view.Object, "right", view.Object},
		{view.Object, "esc", view.List}, {view.List, "esc", view.Navigation}, {view.Navigation, "esc", view.List}, {view.Navigation, "enter", view.List}, {view.Object, "enter", view.Object},
	} {
		m.focus = tc.from
		next, cmd := step(t, m, press(tc.key))
		if next.focus != tc.to || cmd != nil {
			t.Fatalf("%d %s: %d", tc.from, tc.key, next.focus)
		}
	}
	for _, small := range []bool{false, true} {
		if small {
			m.width = 23
		}
		for _, name := range []string{"q", "ctrl+c"} {
			_, cmd := step(t, m, press(name))
			if cmd == nil {
				t.Fatalf("%s has no command", name)
			}
			msg := cmd()
			if name == "q" {
				if _, ok := msg.(tea.QuitMsg); !ok {
					t.Fatalf("q: %T", msg)
				}
			} else if _, ok := msg.(tea.InterruptMsg); !ok {
				t.Fatalf("ctrl+c: %T", msg)
			}
		}
	}
	m.width = 23
	before := m
	m, cmd := step(t, m, press("left"))
	if m != before || cmd != nil {
		t.Fatal("small terminal changed navigation")
	}
	if help := helpText(m); strings.Contains(help, "focus") || strings.Contains(help, "enter") || strings.Contains(help, "esc") {
		t.Fatalf("small help advertises frozen actions: %q", help)
	}
	m.width, m.focus = 120, view.List
	if help := helpText(m); strings.Contains(help, "enter") {
		t.Fatalf("empty list advertises enter: %q", help)
	}
	m.focus = view.Navigation
	if help := helpText(m); !strings.Contains(help, "enter") {
		t.Fatalf("chooser has no enter help: %q", help)
	}
}

func TestKeyRoutingHasOneMode(t *testing.T) {
	for _, text := range []string{"q", "r", "R", "/", "[", "]", "?", " ", "é", "界"} {
		if got := routeKey(TextEntry, press(text)); got.Kind != InsertText || got.Text != text {
			t.Fatalf("text %q: %+v", text, got)
		}
	}
	for _, mode := range []Mode{Navigate, TextEntry} {
		if got := routeKey(mode, press("ctrl+c")); got.Kind != Interrupt {
			t.Fatalf("mode %d: ctrl+c: %+v", mode, got)
		}
	}
	if routeKey(TextEntry, press("enter")).Kind != Submit || routeKey(TextEntry, press("esc")).Kind != Cancel {
		t.Fatal("text submit/cancel routing")
	}
	for _, key := range []string{"left", "right", "tab", "ctrl+s"} {
		if got := routeKey(TextEntry, press(key)); got.Kind != Ignore {
			t.Fatalf("text mode routed %s: %+v", key, got)
		}
	}
}

func TestRootRoutesTextWithoutNavigation(t *testing.T) {
	for _, width := range []int{23, 79, 80, 120} {
		for _, focus := range []view.Pane{view.Navigation, view.List, view.Object} {
			m := NewWithContent("organisation", "list", "object")
			m.width, m.height, m.focus, m.mode = width, 24, focus, TextEntry
			for _, name := range []string{"q", "r", "R", "/", "[", "]", "left", "right", "enter", "esc"} {
				next, cmd := step(t, m, press(name))
				if next != m || cmd != nil {
					t.Fatalf("text mode at width %d focus %d: %s navigated or launched work", width, focus, name)
				}
			}
			next, cmd := step(t, m, press("ctrl+c"))
			if next != m || cmd == nil {
				t.Fatal("text mode did not preserve state and emit an interrupt")
			}
			if msg := cmd(); msg != tea.Interrupt() {
				t.Fatalf("text mode Ctrl-C emitted %T", msg)
			}
		}
	}
}

func TestTextEntryKeyPayload(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'q'}, {Code: '界'}, {Code: 'Q', Mod: tea.ModShift},
		{Code: 'é', Text: "é"}, {Code: 'Q', Text: "Q", Mod: tea.ModShift},
		{Text: "two words"}, {Code: 'e', Text: "e\u0301"},
	} {
		want := key.Text
		if want == "" {
			want = string(key.Code)
		}
		if got := routeKey(TextEntry, key); got != (keyAction{Kind: InsertText, Text: want}) {
			t.Errorf("printable key %+v: got %+v, want text %q", key, got, want)
		}
	}
	for _, text := range []string{"x\ny", "x\ry", "x\ty", "x\x00y", "x\x1by", "x\u009by", "x\u202ey", "x\u2066y"} {
		if got := routeKey(TextEntry, tea.KeyPressMsg{Code: 'x', Text: text}); got != (keyAction{}) {
			t.Errorf("non-printable text %q accepted: %+v", text, got)
		}
	}
	for _, mod := range []tea.KeyMod{tea.ModAlt, tea.ModCtrl, tea.ModShift | tea.ModAlt, tea.ModShift | tea.ModCtrl} {
		for _, text := range []string{"", "q"} {
			key := tea.KeyPressMsg{Code: 'q', Text: text, Mod: mod}
			if got := routeKey(TextEntry, key); got != (keyAction{}) {
				t.Errorf("modified key %+v inserted text: %+v", key, got)
			}
		}
	}
}

// A Mac keyboard has no PgUp or PgDn: space and u turn the page in
// navigation, where b books time; in the search line they are text.
func TestSpaceAndUTurnThePage(t *testing.T) {
	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	if got := routeKey(Navigate, space); got.Kind != PageDown {
		t.Fatalf("space: %v", got)
	}
	if got := routeKey(Navigate, press("u")); got.Kind != PageUp {
		t.Fatalf("u: %v", got)
	}
	if got := routeKey(Navigate, press("b")); got.Kind != BookTime {
		t.Fatalf("b: %v", got)
	}
	if got := routeKey(TextEntry, space); got.Kind != InsertText || got.Text != " " {
		t.Fatalf("space in the search line: %v", got)
	}
	if got := routeKey(TextEntry, press("u")); got.Kind != InsertText || got.Text != "u" {
		t.Fatalf("u in the search line: %v", got)
	}
}
