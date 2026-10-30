package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// K6's additions to the dialog: a field that must not be empty, and a send
// whose end nobody knows.

// A required field left empty sends nothing and takes the caret; the send
// has not begun, so nothing is locked, and the next key is the person's.
// Blanks are not empty: what they mean is the server's to say. Would miss:
// the check on the active field alone.
func TestARequiredFieldThatIsEmptySendsNothing(t *testing.T) {
	t.Parallel()

	var l sendLog
	spec := oneLineSpec(&l)
	spec.Fields[0].Required = true
	m := dialogModel(t, spec, 80, 24)
	m, _ = step(t, m, press("tab")) // to the note, which may stay empty
	m = typeInto(t, m, "note")
	m, cmd := step(t, m, press("enter"))
	if len(l.got) != 0 || cmd != nil {
		t.Fatalf("%d sends", len(l.got))
	}
	if m.dialog.sending || m.dialog.active != 0 {
		t.Fatalf("sending %v, active %d", m.dialog.sending, m.dialog.active)
	}
	if text := boxed(m); !strings.Contains(text, "Duration is required") {
		t.Fatalf("no word of it:\n%s", text)
	}
	m = typeInto(t, m, " ")
	m, _ = step(t, m, press("enter"))
	if len(l.got) != 1 || l.got[0].Values[0] != " " {
		t.Fatalf("sends %+v", l.got)
	}
	if text := boxed(m); strings.Contains(text, "is required") {
		t.Fatalf("the word stayed through a send:\n%s", text)
	}
}

// heldLine is a line a held dialog reads anew at every frame.
func heldLine(text *string) func() (present.Inline, present.Style) {
	return func() (present.Inline, present.Style) { return present.NewInline(*text), present.NoticeProblem }
}

// A held outcome: the send left and nobody knows what became of it. The
// box stays with the input, says what is known as it learns more, and sends
// nothing again: a second press of enter could book twice. Only the way out
// acts. Would miss: a hold that frees enter, or takes text.
func TestAHeldDialogSendsNothingAgain(t *testing.T) {
	t.Parallel()

	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	m = typeInto(t, m, "30")
	m, _ = step(t, m, press("enter"))
	line := "not clear yet"
	m, _ = step(t, m, DialogOutcome{Dialog: m.dialog.id, Held: heldLine(&line)})
	if m.dialog == nil || m.dialog.sending {
		t.Fatalf("no held dialog: %+v", m.dialog)
	}
	if text := boxed(m); !strings.Contains(text, "not clear yet") || !strings.Contains(text, "close") || strings.Contains(text, "book ") {
		t.Fatalf("the box:\n%s", text)
	}
	line = "booked by you now 1:00"
	if text := boxed(m); !strings.Contains(text, "booked by you now 1:00") {
		t.Fatalf("the line did not follow:\n%s", text)
	}
	for _, name := range []string{"enter", "tab", "x"} {
		m, _ = step(t, m, press(name))
	}
	m, _ = step(t, m, tea.PasteMsg{Content: "more"})
	if len(l.got) != 1 || dialogValues(m)[0] != "30" || m.dialog.active != 0 {
		t.Fatalf("sends %+v, values %q, active %d", l.got, dialogValues(m), m.dialog.active)
	}
	m, _ = step(t, m, press("esc"))
	if m.dialog != nil {
		t.Fatal("esc did not close it")
	}
}

// An outcome of a dialog that is gone holds nothing.
func TestAHeldOutcomeOfAnotherDialogChangesNothing(t *testing.T) {
	t.Parallel()

	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	m, _ = step(t, m, press("enter"))
	line := "held"
	m, _ = step(t, m, DialogOutcome{Dialog: m.dialog.id + 1, Held: heldLine(&line)})
	if !m.dialog.sending || m.dialog.held != nil {
		t.Fatalf("sending %v, held %v", m.dialog.sending, m.dialog.held != nil)
	}
}
