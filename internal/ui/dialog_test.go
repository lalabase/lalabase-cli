package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

const dialogHostile = "\x1b[2J\x1b[95mCSI\x1b[0m\x1b]8;;https://evil.invalid\x07LINK\x1b]8;;\x1b\\\x1b]52;c;YQ==\x1b\\\x07\u202e\u2066\u2069\u009b"

// sendLog is what a dialog's user sees of a send.
type sendLog struct{ got []Submission }

// send takes a submission and holds its outcome back: the test answers.
func (l *sendLog) send(s Submission) tea.Cmd {
	l.got = append(l.got, s)
	return nil
}

func oneLineSpec(l *sendLog) DialogSpec {
	return DialogSpec{
		Action:   present.NewInline("Book time"),
		SendHelp: present.NewInline("book"),
		Target:   DialogTarget{Project: website, Ticket: login, Number: 42, Title: "Login bricht ab"},
		Fields: []DialogField{
			{Label: present.NewInline("Duration"), Hint: present.NewInline("30, 1:30 or 1.5"), Limit: 32},
			{Label: present.NewInline("Note"), Limit: 200},
		},
		Send: l.send,
	}
}

func largeSpec(l *sendLog) DialogSpec {
	return DialogSpec{
		Action: present.NewInline("Comment"),
		Target: DialogTarget{Project: website, Ticket: login, Number: 42, Title: "Login bricht ab"},
		Large:  true,
		Fields: []DialogField{{Label: present.NewInline("Comment"), Limit: 2000}},
		Send:   l.send,
	}
}

func dialogModel(t *testing.T, spec DialogSpec, width, height int) Model {
	t.Helper()
	m, _ := step(t, New(WithDialog(spec)), tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

func typeInto(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m, _ = step(t, m, press(string(r)))
	}
	return m
}

func dialogValues(m Model) []string {
	var out []string
	for _, e := range m.dialog.entries {
		out = append(out, e.Value())
	}
	return out
}

func boxed(m Model) string { return ansi.Strip(m.View().Content) }

func key(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func TestRouteKeyInADialog(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
		mode Mode
		want keyAction
	}{
		{"enter sends one line", press("enter"), Dialog, keyAction{Kind: Submit}},
		{"enter is a line break in several", press("enter"), DialogLines, keyAction{Kind: InsertText, Text: "\n"}},
		{"ctrl+s sends several lines", press("ctrl+s"), DialogLines, keyAction{Kind: Submit}},
		{"ctrl+s does nothing in one line", press("ctrl+s"), Dialog, keyAction{}},
		{"esc", press("esc"), Dialog, keyAction{Kind: Cancel}},
		{"tab", press("tab"), DialogLines, keyAction{Kind: NextField}},
		{"q is text", press("q"), Dialog, keyAction{Kind: InsertText, Text: "q"}},
		{"R is text", press("R"), DialogLines, keyAction{Kind: InsertText, Text: "R"}},
		{"a space is text", tea.KeyPressMsg{Code: ' ', Text: " "}, Dialog, keyAction{Kind: InsertText, Text: " "}},
		{"a joined emoji is text", tea.KeyPressMsg{Code: 0x1F468, Text: "\U0001F468\u200d\U0001F469"}, Dialog, keyAction{Kind: InsertText, Text: "\U0001F468\u200d\U0001F469"}},
		{"ctrl+c", press("ctrl+c"), Dialog, keyAction{Kind: Interrupt}},
		{"backspace", key(tea.KeyBackspace), Dialog, keyAction{Kind: DeleteBack}},
		{"delete", key(tea.KeyDelete), Dialog, keyAction{Kind: DeleteForward}},
		{"home", key(tea.KeyHome), Dialog, keyAction{Kind: LineStart}},
		{"end", key(tea.KeyEnd), Dialog, keyAction{Kind: LineEnd}},
		{"left", key(tea.KeyLeft), Dialog, keyAction{Kind: Left}},
		{"right", key(tea.KeyRight), Dialog, keyAction{Kind: Right}},
		{"up", key(tea.KeyUp), DialogLines, keyAction{Kind: Up}},
		{"down", key(tea.KeyDown), DialogLines, keyAction{Kind: Down}},
		{"pgup", key(tea.KeyPgUp), DialogLines, keyAction{Kind: PageUp}},
		{"pgdown", key(tea.KeyPgDown), DialogLines, keyAction{Kind: PageDown}},
		{"ctrl+a is nothing", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, Dialog, keyAction{}},
		{"alt+x is nothing", tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt}, Dialog, keyAction{}},
		{"f1 is nothing", key(tea.KeyF1), Dialog, keyAction{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := routeKey(tc.mode, tc.key); got != tc.want {
				t.Fatalf("%+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTheBoxStandsOverTheScreen(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	text := boxed(m)
	for _, want := range []string{"Book time · #42 Login bricht ab", "Duration", "\u258f", "30, 1:30 or 1.5", "Note", "enter", "book", "tab", "next field", "esc", "cancel"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q missing in:\n%s", want, text)
		}
	}
	// Only the active field has a caret.
	if got := strings.Count(text, "\u258f"); got != 1 {
		t.Fatalf("%d carets in:\n%s", got, text)
	}
	if m.mode != Dialog {
		t.Fatalf("mode %d", m.mode)
	}
}

func TestTypingAndTheFields(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	m = typeInto(t, m, "30")
	m, _ = step(t, m, press("tab"))
	m = typeInto(t, m, "qR[")
	if got := dialogValues(m); got[0] != "30" || got[1] != "qR[" {
		t.Fatalf("values %q", got)
	}
	if text := boxed(m); !strings.Contains(text, "30 ") || !strings.Contains(text, "qR[\u258f") {
		t.Fatalf("the caret did not follow the field:\n%s", text)
	}
	// Tab comes round.
	m, _ = step(t, m, press("tab"))
	m = typeInto(t, m, "x")
	if got := dialogValues(m); got[0] != "30x" {
		t.Fatalf("values %q", got)
	}
	for _, k := range []tea.KeyPressMsg{key(tea.KeyLeft), key(tea.KeyLeft), key(tea.KeyDelete), key(tea.KeyEnd), key(tea.KeyBackspace), key(tea.KeyHome)} {
		m, _ = step(t, m, k)
	}
	m = typeInto(t, m, "1")
	m, _ = step(t, m, key(tea.KeyRight))
	if got := dialogValues(m); got[0] != "13" {
		t.Fatalf("values %q", got)
	}
}

func TestOneFieldHasNoNextField(t *testing.T) {
	var l sendLog
	spec := oneLineSpec(&l)
	spec.Fields = spec.Fields[:1]
	m := dialogModel(t, spec, 80, 24)
	if strings.Contains(boxed(m), "next field") || m.allowed()[NextField] {
		t.Fatal("tab is offered with one field")
	}
	if strings.Contains(boxed(dialogModel(t, largeSpec(&l), 80, 24)), "next field") {
		t.Fatal("tab is offered with one field")
	}
}

func TestEnterSendsOnceAndTheSecondDoesNothing(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	m = typeInto(t, m, "30")
	m, _ = step(t, m, press("tab"))
	m = typeInto(t, m, "note")
	m, _ = step(t, m, press("enter"))
	if len(l.got) != 1 {
		t.Fatalf("%d sends", len(l.got))
	}
	s := l.got[0]
	if s.Dialog != m.dialog.id || s.Target.Ticket != login || s.Target.Project != website || len(s.Values) != 2 || s.Values[0] != "30" || s.Values[1] != "note" {
		t.Fatalf("submission %+v", s)
	}
	if !m.dialog.sending || !strings.Contains(boxed(m), "Sending…") {
		t.Fatalf("not sending:\n%s", boxed(m))
	}
	m, _ = step(t, m, press("enter"))
	if len(l.got) != 1 {
		t.Fatalf("the second enter sent again: %d", len(l.got))
	}
}

// While a send is out the input stands still: what is shown is what went.
// The keys that act are named: no enter, and esc closes, it cancels nothing.
func TestAnInputStandsStillWhileItIsSent(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	m = typeInto(t, m, "30")
	m, _ = step(t, m, press("enter"))
	for _, k := range []tea.KeyPressMsg{press("x"), key(tea.KeyBackspace), key(tea.KeyDelete), key(tea.KeyLeft), press("tab")} {
		m, _ = step(t, m, k)
	}
	m, _ = step(t, m, tea.PasteMsg{Content: "pasted"})
	if got := dialogValues(m); got[0] != "30" || got[1] != "" {
		t.Fatalf("values %q", got)
	}
	if m.dialog.active != 0 {
		t.Fatal("the field moved")
	}
	text := boxed(m)
	if strings.Contains(text, "book") || strings.Contains(text, "next field") || !strings.Contains(text, "close") || strings.Contains(text, "cancel") {
		t.Fatalf("the keys of a send:\n%s", text)
	}
}

func TestARefusalStaysAndKeepsTheInput(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	m = typeInto(t, m, "1,5")
	m, _ = step(t, m, press("enter"))
	id := m.dialog.id
	m, _ = step(t, m, DialogOutcome{Dialog: id, Refusal: present.NewInline("Duration not understood")})
	if m.dialog == nil || m.dialog.sending {
		t.Fatal("the dialog did not stay")
	}
	if got := dialogValues(m); got[0] != "1,5" {
		t.Fatalf("values %q", got)
	}
	if text := boxed(m); !strings.Contains(text, "Duration not understood") {
		t.Fatalf("no refusal:\n%s", text)
	}
	// It stays while the person changes the input, and a send of its own
	// takes it away.
	m, _ = step(t, m, key(tea.KeyBackspace))
	m, _ = step(t, m, key(tea.KeyBackspace))
	m = typeInto(t, m, ":30")
	if text := boxed(m); !strings.Contains(text, "Duration not understood") {
		t.Fatalf("the refusal went with a key:\n%s", text)
	}
	m, _ = step(t, m, press("enter"))
	if text := boxed(m); strings.Contains(text, "Duration not understood") {
		t.Fatalf("the refusal stayed through a new send:\n%s", text)
	}
	if len(l.got) != 2 || l.got[1].Values[0] != "1:30" {
		t.Fatalf("sends %+v", l.got)
	}
}

// A refusal without a word of its own still says something, and never
// passes for a success, which would close the box with the input.
func TestARefusalWithoutAWordStaysARefusal(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	m = typeInto(t, m, "1")
	m, _ = step(t, m, press("enter"))
	m, _ = step(t, m, DialogOutcome{Dialog: m.dialog.id, Refusal: present.NewInline("\u202e")})
	if m.dialog == nil {
		t.Fatal("the dialog closed")
	}
	if text := boxed(m); !strings.Contains(text, "Not sent") {
		t.Fatalf("nothing said:\n%s", text)
	}
}

func TestDoneClosesTheDialog(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	m, _ = step(t, m, press("enter"))
	m, _ = step(t, m, DialogOutcome{Dialog: m.dialog.id, Done: true})
	if m.dialog != nil || m.mode != Navigate {
		t.Fatalf("still open, mode %d", m.mode)
	}
	if strings.Contains(boxed(m), "Book time") {
		t.Fatal("the box stayed on the screen")
	}
}

// Esc leaves a send under way: its outcome finds no dialog, and it does not
// close the next one.
func TestALateOutcomeTouchesNoOtherDialog(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	m, _ = step(t, m, press("enter"))
	first := m.dialog.id
	m, _ = step(t, m, press("esc"))
	if m.dialog != nil {
		t.Fatal("esc did not close it")
	}
	if next, cmd := step(t, m, DialogOutcome{Dialog: first, Refusal: present.NewInline("late")}); next != m || cmd != nil {
		t.Fatal("a late outcome acted")
	}
	m = m.openDialog(oneLineSpec(&l))
	if m.dialog.id == first {
		t.Fatal("two dialogs share an id")
	}
	before := m
	for _, done := range []bool{false, true} {
		next, _ := step(t, m, DialogOutcome{Dialog: first, Done: done, Refusal: present.NewInline("late")})
		if next != before || next.dialog.refusal.String() != "" {
			t.Fatalf("a late outcome reached the new dialog: %+v", next.dialog)
		}
	}
	// One that is not sending has no outcome to wait for.
	if next, _ := step(t, m, DialogOutcome{Dialog: m.dialog.id, Done: true}); next.dialog == nil {
		t.Fatal("an outcome closed a dialog that sent nothing")
	}
}

func TestSeveralLinesEnterBreaksAndCtrlSSends(t *testing.T) {
	var l sendLog
	m := dialogModel(t, largeSpec(&l), 80, 24)
	if m.mode != DialogLines {
		t.Fatalf("mode %d", m.mode)
	}
	m = typeInto(t, m, "one")
	m, _ = step(t, m, press("enter"))
	m = typeInto(t, m, "two")
	if len(l.got) != 0 {
		t.Fatal("enter sent")
	}
	if got := dialogValues(m); got[0] != "one\ntwo" {
		t.Fatalf("value %q", got)
	}
	text := boxed(m)
	for _, want := range []string{"Comment · #42 Login bricht ab", "one", "two\u258f", "ctrl+s", "send", "enter", "new line", "esc", "cancel"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q missing:\n%s", want, text)
		}
	}
	m, _ = step(t, m, press("ctrl+s"))
	if len(l.got) != 1 || l.got[0].Values[0] != "one\ntwo" {
		t.Fatalf("sends %+v", l.got)
	}
	m, _ = step(t, m, press("ctrl+s"))
	if len(l.got) != 1 {
		t.Fatal("the second ctrl+s sent again")
	}
	if !strings.Contains(boxed(m), "close") {
		t.Fatal("esc still says cancel")
	}
}

func TestCtrlSDoesNothingInOneLine(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	if next, cmd := step(t, m, press("ctrl+s")); next != m || cmd != nil || len(l.got) != 0 {
		t.Fatal("ctrl+s acted")
	}
}

func TestUpDownAndThePagesWalkTheLargeField(t *testing.T) {
	var l sendLog
	m := dialogModel(t, largeSpec(&l), 80, 24)
	m, _ = step(t, m, tea.PasteMsg{Content: strings.Repeat("line\n", 60) + "last"})
	if text := boxed(m); !strings.Contains(text, "last\u258f") || strings.Count(text, "line") > 20 {
		t.Fatalf("the end of a long text:\n%s", text)
	}
	m, _ = step(t, m, key(tea.KeyPgUp))
	m, _ = step(t, m, key(tea.KeyPgUp))
	m, _ = step(t, m, key(tea.KeyUp))
	if text := boxed(m); strings.Contains(text, "last") || !strings.Contains(text, "\u258f") {
		t.Fatalf("after moving up:\n%s", text)
	}
	for range 3 {
		m, _ = step(t, m, key(tea.KeyPgDown))
	}
	m, _ = step(t, m, key(tea.KeyDown))
	if text := boxed(m); !strings.Contains(text, "last") {
		t.Fatalf("back at the end:\n%s", text)
	}
}

func TestAPasteComesInAsOne(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	m, _ = step(t, m, tea.PasteMsg{Content: "a\nb\r\nc"})
	if got := dialogValues(m); got[0] != "a b c" {
		t.Fatalf("value %q", got)
	}
	if len(l.got) != 0 {
		t.Fatal("a line break in a paste sent")
	}
	m = dialogModel(t, largeSpec(&l), 80, 24)
	m, _ = step(t, m, tea.PasteMsg{Content: "a\nb\r\nc\rd"})
	if got := dialogValues(m); got[0] != "a\nb\nc\nd" {
		t.Fatalf("value %q", got)
	}
}

// What a paste brings passes the one filter: neither the buffer nor the
// screen holds a byte of it.
func TestAHostilePasteLandsNowhere(t *testing.T) {
	var l sendLog
	for _, spec := range []DialogSpec{oneLineSpec(&l), largeSpec(&l)} {
		m := dialogModel(t, spec, 80, 24)
		m, _ = step(t, m, tea.PasteMsg{Content: "ok\x1b[31mred\x07\u202eend"})
		if got := dialogValues(m); got[0] != "okred end" {
			t.Fatalf("buffer %q", got[0])
		}
		m, _ = step(t, m, tea.PasteMsg{Content: dialogHostile})
		content := m.View().Content
		for _, bad := range []string{"\x1b[31m", "\x1b[95m", "\x1b]", "\x1b[2J", "\x07", "\u202e", "\u2066", "\u009b"} {
			if strings.Contains(content, bad) {
				t.Fatalf("%q on the screen: %q", bad, content)
			}
		}
		if strings.Contains(dialogValues(m)[0], "\x1b") {
			t.Fatalf("buffer %q", dialogValues(m)[0])
		}
	}
}

func TestAPasteOverTheLimitSaysSo(t *testing.T) {
	var l sendLog
	spec := oneLineSpec(&l)
	spec.Fields[0].Limit = 5
	m := dialogModel(t, spec, 80, 24)
	m, _ = step(t, m, tea.PasteMsg{Content: strings.Repeat("x", 1<<20)})
	if got := dialogValues(m); got[0] != "xxxxx" {
		t.Fatalf("value %q", got)
	}
	if text := boxed(m); !strings.Contains(text, "Cut at 5 characters") {
		t.Fatalf("no notice:\n%s", text)
	}
	m, _ = step(t, m, key(tea.KeyBackspace))
	if text := boxed(m); strings.Contains(text, "Cut at") {
		t.Fatalf("the notice stayed:\n%s", text)
	}
}

// A prefilled value is one more thing the buffer takes: the filter holds.
func TestAPrefilledFieldPassesTheFilter(t *testing.T) {
	var l sendLog
	spec := oneLineSpec(&l)
	spec.Fields[0].Value = "Login\x1b[31m\u202e bricht"
	m := dialogModel(t, spec, 80, 24)
	if got := dialogValues(m); got[0] != "Login bricht" {
		t.Fatalf("value %q", got)
	}
}

func TestAHostileTitleAndRefusalReachTheScreenClean(t *testing.T) {
	var l sendLog
	spec := oneLineSpec(&l)
	spec.Target.Title = "SAFE" + dialogHostile
	m := dialogModel(t, spec, 80, 24)
	m, _ = step(t, m, press("enter"))
	m, _ = step(t, m, DialogOutcome{Dialog: m.dialog.id, Refusal: present.NewInline("REFUSED" + dialogHostile)})
	content := m.View().Content
	if !strings.Contains(content, "SAFE") || !strings.Contains(content, "REFUSED") {
		t.Fatalf("no text: %q", content)
	}
	for _, bad := range []string{"\x1b[95m", "\x1b]", "\x1b[2J", "\x07", "\u202e", "\u2066", "\u009b"} {
		if strings.Contains(content, bad) {
			t.Fatalf("%q on the screen: %q", bad, content)
		}
	}
}

func TestAnUntitledTicketIsNamed(t *testing.T) {
	var l sendLog
	spec := oneLineSpec(&l)
	spec.Target.Title = "\u202e  "
	if text := boxed(dialogModel(t, spec, 80, 24)); !strings.Contains(text, "#42 Untitled ticket") {
		t.Fatalf("title:\n%s", text)
	}
}

func TestAResizeKeepsTheDialogAndItsInput(t *testing.T) {
	var l sendLog
	m := dialogModel(t, largeSpec(&l), 120, 40)
	m = typeInto(t, m, strings.Repeat("word ", 60))
	for _, size := range [][2]int{{80, 24}, {59, 20}, {40, 12}, {24, 8}, {120, 40}} {
		next, _ := step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != size[1] {
			t.Fatalf("%v: %d lines", size, len(lines))
		}
		for i, line := range lines {
			if w := ansi.WcWidth.StringWidth(line); w > size[0] {
				t.Fatalf("%v: line %d is %d wide: %q", size, i, w, ansi.Strip(line))
			}
		}
		if !strings.Contains(boxed(m), "\u258f") {
			t.Fatalf("%v: no caret:\n%s", size, boxed(m))
		}
	}
	if got := dialogValues(m); got[0] != strings.Repeat("word ", 60) {
		t.Fatalf("value %q", got)
	}
}

// A terminal too small for anything shows its notice and the two keys that
// still act; nothing is typed blind, and the box comes back whole.
func TestATooSmallTerminalKeepsTheDialogForLater(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	m = typeInto(t, m, "30")
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 20, Height: 6})
	text := boxed(m)
	if !strings.Contains(text, "Terminal too small") || !strings.Contains(text, "esc") || !strings.Contains(text, "ctrl+c") || strings.Contains(text, "Book time") {
		t.Fatalf("small:\n%s", text)
	}
	m = typeInto(t, m, "x")
	m, _ = step(t, m, tea.PasteMsg{Content: "pasted"})
	m, _ = step(t, m, press("enter"))
	if got := dialogValues(m); got[0] != "30" || len(l.got) != 0 {
		t.Fatalf("value %q, %d sends", got, len(l.got))
	}
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if text := boxed(m); !strings.Contains(text, "30\u258f") {
		t.Fatalf("back:\n%s", text)
	}
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 20, Height: 6})
	m, _ = step(t, m, press("esc"))
	if m.dialog != nil {
		t.Fatal("esc does not close it on a small terminal")
	}
}

func TestCtrlCEndsTheProgramFromADialog(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	_, cmd := step(t, m, press("ctrl+c"))
	if cmd == nil {
		t.Fatal("no command")
	}
	if _, ok := cmd().(tea.InterruptMsg); !ok {
		t.Fatal("ctrl+c does not interrupt")
	}
}

func (h *harness) dialogKeepsEverything(k tea.KeyPressMsg) {
	h.t.Helper()
	a := h.m.app
	focus, selected, opened, area := h.m.focus, a.tickets.selected, a.tickets.opened, a.detail.area
	reads, lists, offset := len(h.f.detailReads()), len(h.f.ticketListReads()), a.detail.offset
	key := a.detail.key
	h.drive(h.feed(k)...)
	if h.m.focus != focus || a.tickets.selected != selected || a.tickets.opened != opened || a.detail.area != area || a.detail.offset != offset || a.detail.key != key {
		h.t.Fatalf("%s moved the screen behind the box", k.String())
	}
	if len(h.f.detailReads()) != reads || len(h.f.ticketListReads()) != lists {
		h.t.Fatalf("%s read something", k.String())
	}
}

// openDialogOnTicket opens the ticket #n of Website-Relaunch and a dialog on
// it.
func (h *harness) openDialogOnTicket(n int, spec DialogSpec) {
	h.t.Helper()
	h.openTickets(website)
	h.openTicket(n)
	h.m = h.m.openTicketDialog(spec)
	if h.m.dialog == nil {
		h.t.Fatal("no dialog on the ticket")
	}
}

func TestNoKeyActsBehindTheDialog(t *testing.T) {
	var l sendLog
	for _, spec := range []DialogSpec{oneLineSpec(&l), largeSpec(&l)} {
		h := newHarness(t)
		h.openDialogOnTicket(1, spec)
		for _, name := range []string{"up", "down", "left", "right", "pgup", "pgdown", "[", "]", "o", "e", "x", "r", "R", "A", "d", "/", "q", "tab", "enter"} {
			h.dialogKeepsEverything(press(name))
			if h.m.dialog == nil {
				t.Fatalf("%s closed the dialog", name)
			}
			h.m.dialog.sending = false
		}
	}
}

func TestTheWheelActsNotBehindTheDialog(t *testing.T) {
	var l sendLog
	h := newHarness(t)
	h.openDialogOnTicket(6, oneLineSpec(&l))
	x, y := h.objectCell()
	before := h.m.app.detail.offset
	h.wheel(tea.MouseWheelDown, x, y)
	if h.m.app.detail.offset != before {
		t.Fatal("the wheel scrolled behind the box")
	}
}

// The ticket is taken when the dialog opens: a reload that moves the row, or
// drops it, changes what the person is looking at behind the box and nothing
// of where the send goes.
func TestTheTargetStaysWhenTheSelectionBehindItMoves(t *testing.T) {
	var l sendLog
	h := newHarness(t)
	h.openTickets(website)
	h.m.focus = view.List
	second := h.m.app.tickets.rows[1].ID
	h.m.app.tickets.selected = second
	h.drive(h.press("enter"))
	if h.m.focus != view.Object || h.m.app.detail == nil || h.m.app.detail.id != second {
		t.Fatalf("no ticket opened: focus %d", h.m.focus)
	}
	number, title := h.m.app.detail.record.Number, h.m.app.detail.record.Title
	// A reload of the list is on its way when the dialog opens, and brings
	// a list without the row; the selection behind the box moves.
	h.f.setTickets(func(q api.TicketQuery) transport.Answer {
		return ticketPageAnswer(1, 25, 1, h.d.ticketRows(website, 1))
	})
	reload := h.m.app.loadTickets()
	h.m = h.m.openTicketDialog(oneLineSpec(&l))
	target := h.m.dialog.spec.Target
	if target.Ticket != second || target.Project != website || target.Number != number || target.Title != title {
		t.Fatalf("target %+v", target)
	}
	h.drive(reload)
	if v := h.m.app.tickets; v.selected == second || h.m.dialog == nil {
		t.Fatalf("the reload did not move the selection: %q", v.selected)
	}
	h.typeText("30")
	h.press("enter")
	if len(l.got) != 1 {
		t.Fatalf("%d sends", len(l.got))
	}
	if got := l.got[0].Target; got != target {
		t.Fatalf("the target moved: %+v, want %+v", got, target)
	}
	if !strings.Contains(h.m.View().Content, "#"+itoa(number)) {
		t.Fatalf("the title moved: %q", ansi.Strip(h.m.View().Content))
	}
}

func TestADialogOnATicketNeedsTheTicketInFocus(t *testing.T) {
	var l sendLog
	h := newHarness(t)
	h.openTickets(website)
	h.m.focus = view.List
	if next := h.m.openTicketDialog(oneLineSpec(&l)); next.dialog != nil {
		t.Fatal("a dialog without a ticket in the pane")
	}
	h.openTicket(1)
	if _, ok := h.m.ticketTarget(); !ok {
		t.Fatal("the ticket in focus is no target")
	}
	h.m.app.detail.known = false
	if next := h.m.openTicketDialog(oneLineSpec(&l)); next.dialog != nil {
		t.Fatal("a dialog on a ticket that is not known yet")
	}
}

// A preview's timer that fires while the box is open reads nothing, and the
// close sets it again, as the search line's does.
func TestThePreviewWaitsForTheDialog(t *testing.T) {
	var l sendLog
	h := newHarness(t)
	h.openTickets(website)
	h.m.focus = view.List
	h.m = h.m.openDialog(oneLineSpec(&l))
	reads := len(h.f.detailReads())
	token := h.m.app.previewToken
	h.drive(h.feed(gateway.Due{Session: h.m.app.session, Token: token})...)
	if len(h.f.detailReads()) != reads {
		t.Fatal("a preview was read behind the box")
	}
	h.press("esc")
	if h.m.app.previewToken == token {
		t.Fatal("closing the box set no new timer")
	}
}

// A 401 ends the session: nothing of it stays on the screen, the ticket's
// title in the box least of all.
func TestAnExpiredSessionClosesTheDialog(t *testing.T) {
	var l sendLog
	h := newHarness(t)
	h.openDialogOnTicket(1, oneLineSpec(&l))
	h.typeText("30")
	h.m.expire()
	if h.m.dialog != nil || h.m.mode != Navigate {
		t.Fatalf("mode %d", h.m.mode)
	}
	if strings.Contains(ansi.Strip(h.m.View().Content), "Book time") {
		t.Fatal("the box stayed")
	}
}

// The keys of the box stand in the box, and the line below names what acts
// besides them; the panes keep the help's height they would have had, or
// one line less, never a key that does not act.
func TestTheHelpBelowTheBoxNamesOnlyWhatActs(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 120, 24)
	help := helpText(m)
	if !strings.Contains(help, "ctrl+c") || strings.Contains(help, "quit") || strings.Contains(help, "focus") {
		t.Fatalf("help %q", help)
	}
}

// Every binding of a dialog comes from the same list, the dispatch and the
// help alike.
func TestTheBoxNamesWhatActs(t *testing.T) {
	var l sendLog
	m := dialogModel(t, oneLineSpec(&l), 80, 24)
	set := m.allowed()
	for _, kind := range []Action{Submit, NextField, Cancel, Interrupt, InsertText, DeleteBack, DeleteForward, Left, Right, LineStart, LineEnd} {
		if !set[kind] {
			t.Errorf("action %d does not act", kind)
		}
	}
	for _, kind := range []Action{Quit, OpenList, Back, Refresh, Search, NextArea, Order, Expand, MarkRead, Launch, OpenAgents, RemoveAgent, PrevPage, NextPage, Up, Down, PageUp, PageDown} {
		if set[kind] {
			t.Errorf("action %d acts in a dialog of one line", kind)
		}
	}
}
