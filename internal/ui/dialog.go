package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/textentry"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// The dialog (M1) is the box a person writes in: the time booked on a ticket
// (K6) is its first user, the comment and the title its next. It is one
// component in two sizes and knows nothing of what it is for: its user names
// the fields, the target and what a send does (DialogSpec). It sends nothing
// by itself; b opens one (book.go), and a test or a fixture may (WithDialog).
//
// While it is open a key does one thing, inside the box (routeDialog): the
// screen behind it goes on showing what arrives, and nothing there moves by a
// key. Its target is what the person pressed the key on, taken when it opens;
// a reload that moves the selection behind the box does not move it. While a
// send is out the input stands still and sending is locked. A refusal stays
// in the error line, with the input, until the next send. A send nobody
// knows the end of holds the dialog: the input stays and the lock with it,
// since a second press could write twice. Esc closes it whatever is going
// on, and a late outcome finds no dialog.

// DialogTarget is what a dialog acts on, taken when it opens and never read
// again from the selection. Title is the ticket's, foreign text, for the
// box's title alone.
type DialogTarget struct {
	Project, Ticket string
	Number          int
	Title           string
}

// DialogField is one field of a dialog: its label and its hint, its limit in
// runes, and what it starts with. Value is foreign text as often as not (a
// ticket's title to change): it passes the same filter as a key. A required
// field left empty stops the send before it begins, by what it holds: what
// its text means is the server's to say.
type DialogField struct {
	Label, Hint present.Inline
	Limit       int
	Value       string
	Required    bool
}

// DialogSpec is a dialog as its user describes it. Action opens the box's
// title ("Book time"), and SendHelp names the key that sends, "send" when
// empty. Large is the size of one field of several lines; a small dialog
// has one or a few fields of one line. Send is called once per send, with
// what the fields hold; what it returns runs as any command, and how it ended
// must reach the dialog as a DialogOutcome with the submission's number: as
// the command's message, or through the model that received its result.
type DialogSpec struct {
	Action, SendHelp present.Inline
	Target           DialogTarget
	Large            bool
	Fields           []DialogField
	Send             func(Submission) tea.Cmd
}

// Submission is one send: the dialog's number, its target as it was taken,
// and each field's text as it is shown.
type Submission struct {
	Dialog uint64
	Target DialogTarget
	Values []string
}

// DialogOutcome is how a send ended. Done closes the dialog; else it stays,
// with the input and with Refusal in its error line, ready for the next send.
// Held says nobody knows how it ended: the dialog stays with its input, sends
// nothing again and takes no text, and its error line is what Held says at
// each frame, as its user learns more. An outcome of a dialog that is gone
// changes nothing: what became of the write is the sender's.
type DialogOutcome struct {
	Dialog  uint64
	Done    bool
	Refusal present.Inline
	Held    func() (present.Inline, present.Style)
}

// dialog is the open dialog. It stays behind a pointer: the model is
// comparable.
type dialog struct {
	id      uint64
	spec    DialogSpec
	title   present.Inline
	entries []*textentry.Entry
	active  int
	sending bool
	// refusal stands in the error line until the next send; cut says that
	// the last thing put into a field did not fit, until the next key.
	refusal, cut present.Inline
	// held names the error line of a send nobody knows the end of, nil
	// for any other.
	held func() (present.Inline, present.Style)
}

// WithDialog starts the interface with a dialog open: how a test or a
// fixture reaches one without a session.
func WithDialog(spec DialogSpec) Option {
	return func(m *Model) { *m = m.openDialog(spec) }
}

// openDialog opens a dialog over the screen. A large one has one field, and
// one without a field nothing to show.
func (m Model) openDialog(spec DialogSpec) Model {
	if len(spec.Fields) == 0 {
		return m
	}
	m.mode = Dialog
	if spec.Large {
		m.mode = DialogLines
		spec.Fields = spec.Fields[:1]
	}
	m.dialogs++
	d := &dialog{id: m.dialogs, spec: spec, title: spec.title()}
	for i, f := range spec.Fields {
		d.entries = append(d.entries, textentry.New(f.Limit, spec.Large))
		d.insert(i, f.Value)
	}
	m.dialog = d
	return m
}

// insert puts text into a field, on the field's terms; what does not fit is
// said in the error line, by the field's name: it may not be the active one.
func (d *dialog) insert(field int, text string) {
	if d.entries[field].Insert(text) {
		f := d.spec.Fields[field]
		d.cut = uitext.Fill(uitext.DialogCut, f.Label.String(), f.Limit)
	}
}

// title is the box's title: the action and the ticket, if it has one.
func (s DialogSpec) title() present.Inline {
	if s.Target.Ticket == "" {
		return s.Action
	}
	return uitext.Fill(uitext.DialogTitle, s.Action.String(), s.Target.Number, textOr(s.Target.Title, uitext.UntitledTicket))
}

// ticketTarget is the ticket a key would act on: the known ticket in the
// focused object pane, as it is now. An open search line has the keys.
func (m Model) ticketTarget() (DialogTarget, bool) {
	a := m.app
	if a == nil || a.expired || m.mode != Navigate || m.focus != view.Object || a.detail == nil || !a.detail.known {
		return DialogTarget{}, false
	}
	d := a.detail
	return DialogTarget{Project: d.project, Ticket: d.id, Number: d.record.Number, Title: d.record.Title}, true
}

// openTicketDialog opens a dialog on the ticket in the focused object pane,
// which it holds from here on; without such a ticket it opens nothing.
func (m Model) openTicketDialog(spec DialogSpec) Model {
	target, ok := m.ticketTarget()
	if !ok {
		return m
	}
	spec.Target = target
	return m.openDialog(spec)
}

// closeDialog closes the dialog. A preview's timer that fired while it was
// open is set again, as after the search line.
func (m Model) closeDialog() (Model, tea.Cmd) {
	m.mode, m.dialog = Navigate, nil
	if m.app == nil {
		return m, nil
	}
	return m, m.selectionChanged()
}

// dialogInterrupt acts in a dialog as everywhere; the line below the box
// names it.
var dialogInterrupt = binding{Interrupt, uitext.InterruptKey, uitext.InterruptHelp}

// dialogBindings are the actions of the dialog. While it is locked, or where
// no box shows, only the way out acts; esc leaves a send under way, so it
// does not say it cancels one. The caret's keys act unnamed: nobody needs
// them named, and the box has one line for its help.
func (m Model) dialogBindings(small bool) []binding {
	d := m.dialog
	cancel := binding{Cancel, uitext.EscapeKey, uitext.CancelHelp}
	if d.locked() {
		cancel.help = uitext.CloseHelp
	}
	if d.locked() || small {
		return []binding{cancel, dialogInterrupt}
	}
	var out []binding
	if d.spec.Large {
		out = append(out, binding{Submit, uitext.SendKey, uitext.SendHelp}, binding{InsertText, uitext.EnterKey, uitext.NewLineHelp})
	} else {
		out = append(out, binding{Submit, uitext.EnterKey, uitext.SendHelp})
	}
	if len(d.entries) > 1 {
		out = append(out, binding{NextField, uitext.TabKey, uitext.NextFieldHelp})
	}
	out = append(out, cancel, dialogInterrupt)
	kinds := []Action{Left, Right, LineStart, LineEnd, DeleteBack, DeleteForward}
	if d.spec.Large {
		kinds = append(kinds, Up, Down, PageUp, PageDown)
	}
	for _, kind := range kinds {
		out = append(out, binding{kind, uitext.Unlisted, uitext.Unlisted})
	}
	return out
}

// dialogKey is a key the bindings let through.
func (m Model) dialogKey(action keyAction) (Model, tea.Cmd) {
	d := m.dialog
	e := d.entries[d.active]
	d.cut = present.Inline{}
	switch action.Kind {
	case Cancel:
		return m.closeDialog()
	case Submit:
		return m, d.send()
	case NextField:
		d.active = (d.active + 1) % len(d.entries)
	case InsertText:
		d.insert(d.active, action.Text)
	case DeleteBack:
		e.Backspace()
	case DeleteForward:
		e.Delete()
	case Left:
		e.Left()
	case Right:
		e.Right()
	case LineStart:
		e.Home()
	case LineEnd:
		e.End()
	case Up:
		e.Move(-1)
	case Down:
		e.Move(1)
	case PageUp:
		e.Page(-1)
	case PageDown:
		e.Page(1)
	}
	return m, nil
}

// paste takes a paste as one piece into the active field, on the terms of a
// key: not while a send is out, and not where no box shows.
func (m Model) paste(msg tea.PasteMsg) Model {
	if d := m.dialog; d != nil && m.allowed()[InsertText] {
		d.cut = present.Inline{}
		d.insert(d.active, msg.Content)
	}
	return m
}

// locked: the input stands still and nothing sends, while a send is out or
// held.
func (d *dialog) locked() bool { return d.sending || d.held != nil }

// send starts the send: the lock first, since the bindings read it. A
// required field left empty keeps it from beginning, and gets the caret.
func (d *dialog) send() tea.Cmd {
	values := make([]string, len(d.entries))
	for i, e := range d.entries {
		values[i] = e.Value()
		if d.spec.Fields[i].Required && values[i] == "" {
			d.active, d.refusal = i, uitext.Fill(uitext.DialogRequired, d.spec.Fields[i].Label.String())
			return nil
		}
	}
	d.sending, d.refusal = true, present.Inline{}
	return d.spec.Send(Submission{Dialog: d.id, Target: d.spec.Target, Values: values})
}

// receiveOutcome ends a send of the open dialog.
func (m Model) receiveOutcome(o DialogOutcome) (Model, tea.Cmd) {
	d := m.dialog
	if d == nil || d.id != o.Dialog || !d.sending {
		return m, nil
	}
	if o.Done {
		return m.closeDialog()
	}
	d.sending = false
	if o.Held != nil {
		d.held = o.Held
		return m, nil
	}
	d.refusal = o.Refusal
	if d.refusal.String() == "" {
		d.refusal = uitext.Inline(uitext.DialogRefused)
	}
	return m, nil
}

// errorLine is what stands under the fields, with its tone.
func (d *dialog) errorLine() (present.Inline, present.Style) {
	switch {
	case d.held != nil:
		return d.held()
	case d.sending:
		return uitext.Inline(uitext.DialogSending), present.NoticeWaiting
	case d.cut.String() != "":
		return d.cut, present.NoticeProblem
	}
	return d.refusal, present.NoticeProblem
}

// dialogScreen is the dialog's display values, as the entries have them.
func (m Model) dialogScreen() present.Dialog {
	d := m.dialog
	out := present.Dialog{Title: d.title, Large: d.spec.Large}
	out.Error, out.ErrorStyle = d.errorLine()
	more := uitext.Inline(uitext.Ellipsis).String()
	for i, e := range d.entries {
		// The text of a field out of focus stays where it was: a blank in
		// the caret's cell.
		caret := " "
		if i == d.active {
			caret = uitext.Inline(uitext.DialogCaret).String()
		}
		f := present.DialogField{Label: d.spec.Fields[i].Label, Hint: d.spec.Fields[i].Hint, Active: i == d.active}
		for _, line := range e.Lines(caret, more) {
			f.Entry = append(f.Entry, present.NewInline(line))
		}
		out.Fields = append(out.Fields, f)
	}
	for _, b := range m.dialogBindings(false) {
		if b.kind == Interrupt || b.help == uitext.Unlisted {
			continue
		}
		help := uitext.Inline(b.help)
		if b.kind == Submit && d.spec.SendHelp.String() != "" {
			help = d.spec.SendHelp
		}
		out.Help = append(out.Help, present.KeyHelp{Key: uitext.Inline(b.key), Help: help})
	}
	return out
}

// fitDialog tells the entries the room the box has for them, after whatever
// the step changed: the terminal, the error line, the text. A small terminal
// shows no box, and the entries stay as they are until it grows again.
func (m Model) fitDialog() {
	if m.dialog == nil {
		return
	}
	g := m.geometry()
	if g.Small {
		return
	}
	width, height := view.DialogEntry(m.dialogScreen(), g, m.widthMethod)
	for _, e := range m.dialog.entries {
		e.Size(width, height, m.widthMethod)
	}
}
