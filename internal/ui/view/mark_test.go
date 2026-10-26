package view

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// C9a: a ticket list reserves two cells between the marker and the text
// for an agent's glyph, in every row, with an agent or without.

func marked(primary, glyph string, style present.Style, quiet bool) present.Row {
	return present.Row{
		Primary: present.NewInline(primary), Trailing: present.NewInline("open"), Secondary: present.NewInline("   Anna · 09:30"),
		Marked: true, Mark: present.NewInline(glyph), MarkStyle: style, Quiet: quiet,
	}
}

func TestTheAgentsColumnIsAlwaysReserved(t *testing.T) {
	c := present.Column{Rows: []present.Row{
		marked("#6 Login", "⣿", present.AgentWaiting, false),
		marked("#7 Logout", "", present.Plain, false),
	}, Cursor: -1}
	got := content(c, 30, 12, false, ansi.WcWidth)
	pad := func(s string) string { return s + strings.Repeat(" ", 30-ansi.WcWidth.StringWidth(s)) }
	want := []string{
		"  \u28ff " + pad("#6 Login")[:21] + " open",
		pad("       Anna · 09:30"),
		"    " + pad("#7 Logout")[:21] + " open",
		pad("       Anna · 09:30"),
	}
	for i := range want {
		if ansi.Strip(got[i]) != want[i] {
			t.Errorf("line %d: %q, want %q", i, ansi.Strip(got[i]), want[i])
		}
		if w := ansi.WcWidth.StringWidth(got[i]); w != 30 {
			t.Errorf("line %d is %d cells wide", i, w)
		}
	}
	// Unmarked rows keep their bytes: no column.
	plain := content(present.Column{Rows: []present.Row{{Primary: present.NewInline("#7 Logout")}}, Cursor: -1}, 20, 1, false, ansi.WcWidth)
	if ansi.Strip(plain[0]) != "  #7 Logout         " {
		t.Errorf("an unmarked row: %q", plain[0])
	}
}

// Each state's glyph in its tone, byte for byte; the two waits alone are
// magenta.
func TestEveryStatesGlyphInItsTone(t *testing.T) {
	for _, c := range []struct {
		glyph string
		style present.Style
		want  string
	}{
		{"!", present.AgentApproval, "\x1b[1;35m!\x1b[m"},
		{"⣿", present.AgentWaiting, "\x1b[35m⣿\x1b[m"},
		{"✕", present.AgentLost, "\x1b[31m✕\x1b[m"},
		{"⠹", present.AgentWorking, "\x1b[36m⠹\x1b[m"},
		{"⠂", present.AgentStarting, "\x1b[2;36m⠂\x1b[m"},
		{"✓", present.AgentEnded, "\x1b[2;32m✓\x1b[m"},
	} {
		got := row(marked("#6 Login", c.glyph, c.style, false), 30, 4, false, noBar, false, ansi.WcWidth)[0]
		if want := "  " + c.want + " #6 Login"; !strings.HasPrefix(got, want) {
			t.Errorf("%q: %q, want it to start %q", c.glyph, got, want)
		}
	}
}

// In a quiet row (a done ticket) the text is faint, the glyph is not.
func TestTheGlyphIsNotFaintInAQuietRow(t *testing.T) {
	got := row(marked("#6 Login", "⣿", present.AgentWaiting, true), 30, 4, false, noBar, false, ansi.WcWidth)[0]
	if !strings.HasPrefix(got, "  \x1b[35m⣿\x1b[m \x1b[2m#6 Login") {
		t.Fatalf("%q", got)
	}
}

// In the cursor bar the glyph takes the bar's colour: the whole line is
// reversed (or on the grey ground), and no tone of its own is set.
func TestTheGlyphTakesTheBarsColour(t *testing.T) {
	for _, bar := range []barKind{focusBar, restBar} {
		for _, style := range []present.Style{present.AgentApproval, present.AgentWaiting, present.AgentLost, present.AgentWorking} {
			lines := row(marked("#6 Login", "!", style, true), 30, 4, true, bar, true, ansi.WcWidth)
			for i, line := range lines {
				if strings.Contains(line, "35") || strings.Contains(line, "36") || strings.Contains(line, "31") {
					t.Errorf("bar %d, style %d, line %d: a tone in the bar: %q", bar, style, i, line)
				}
				if w := ansi.WcWidth.StringWidth(line); w != 30 {
					t.Errorf("bar %d: line %d is %d cells wide", bar, i, w)
				}
			}
			if !strings.Contains(ansi.Strip(lines[0]), "> ! #6 Login") || !strings.HasPrefix(ansi.Strip(lines[1]), "       Anna") {
				t.Errorf("bar %d: %q", bar, lines)
			}
			switch bar {
			case focusBar:
				if reversedCells(lines[0]) != 30 {
					t.Errorf("focused bar: %d cells reversed", reversedCells(lines[0]))
				}
			case restBar:
				if grounded(lines[0]) != 30 {
					t.Errorf("rest bar: %d cells grounded", grounded(lines[0]))
				}
			}
		}
	}
}

// Without colour the glyph stays: it is a character, not a tone.
func TestTheGlyphWithoutColour(t *testing.T) {
	got := ansi.Strip(row(marked("#6 Login", "✕", present.AgentLost, false), 30, 4, false, noBar, false, ansi.WcWidth)[0])
	if !strings.HasPrefix(got, "  ✕ #6 Login") {
		t.Fatalf("%q", got)
	}
}

// A glyph wider than one cell is not drawn: the column stays two cells.
func TestAWideGlyphIsDropped(t *testing.T) {
	got := row(marked("#6 Login", "界", present.AgentLost, false), 30, 4, false, noBar, false, ansi.WcWidth)[0]
	if ansi.Strip(got)[:6] != "    #6" || ansi.WcWidth.StringWidth(got) != 30 {
		t.Fatalf("%q", got)
	}
}
