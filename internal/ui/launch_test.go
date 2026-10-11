package ui

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// C7: x in the ticket view starts Claude Code beside the ticket shown,
// through the launcher the command gives the session; these tests give a
// fake one.

type launchCall struct {
	ref    uint64
	ticket launch.Ticket
}

// fakeLauncher records every launch; its command answers with kind, or
// holds the answer back while hold is set (the launch is still running).
type fakeLauncher struct {
	mu    sync.Mutex
	calls []launchCall
	kind  launch.Kind
}

func (f *fakeLauncher) Launch(ref uint64, t launch.Ticket) tea.Cmd {
	f.mu.Lock()
	f.calls = append(f.calls, launchCall{ref, t})
	kind := f.kind
	f.mu.Unlock()
	return func() tea.Msg { return launch.Done{Ref: ref, Ticket: t, Kind: kind} }
}

func (f *fakeLauncher) launched() []launchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]launchCall(nil), f.calls...)
}

func (h *harness) withLauncher() *fakeLauncher {
	f := &fakeLauncher{kind: launch.Opened}
	h.m.app.deps.Launcher = f
	return f
}

func (h *harness) offers(kind Action) bool { return h.m.allowed()[kind] }

func (h *harness) status() string { return h.m.screen().Status.String() }

// the ticket the pane shows, as the launch must hand it over.
func (h *harness) shownTicket() launch.Ticket {
	d := h.m.app.detail
	return launch.Ticket{Project: d.project, ID: d.id, Number: d.record.Number}
}

func TestXOpensClaudeCodeBesideTheTicketShown(t *testing.T) {
	h := newHarness(t)
	f := h.withLauncher()
	h.openSix()
	if !h.offers(Launch) {
		t.Fatal("x is not offered in the ticket view")
	}
	if !strings.Contains(helpText(h.m), "Claude Code") {
		t.Fatalf("the help does not name x: %q", helpText(h.m))
	}
	cmd := h.press("x")
	want := launch.Ticket{Project: website, ID: ticketID(website, 6), Number: 6}
	if calls := f.launched(); len(calls) != 1 || calls[0].ticket != want || calls[0].ticket != h.shownTicket() {
		t.Fatalf("launched %+v, want %+v", calls, want)
	}
	// While it runs: a notice, and a second x does nothing.
	if h.offers(Launch) {
		t.Fatal("x is offered while a launch runs")
	}
	if got := h.status(); got != "Opening Claude Code beside #6\u2026" {
		t.Fatalf("status while launching %q", got)
	}
	h.press("x")
	if n := len(f.launched()); n != 1 {
		t.Fatalf("a second x launched again: %d launches", n)
	}
	h.drive(cmd)
	if got := h.status(); got != "Claude Code opened beside #6" {
		t.Fatalf("status after the launch %q", got)
	}
	// After it, a new press opens a new split.
	if !h.offers(Launch) {
		t.Fatal("x is not offered after the launch")
	}
	h.drive(h.press("x"))
	if calls := f.launched(); len(calls) != 2 || calls[1].ticket != want || calls[1].ref == calls[0].ref {
		t.Fatalf("second launch %+v", calls)
	}
}

// x is the ticket's key: not in the list, the chooser, a notification's
// details, a ticket still loading, or without a launcher.
func TestXIsOnlyInTheTicketView(t *testing.T) {
	h := newHarness(t)
	h.withLauncher()
	h.start()
	if h.offers(Launch) {
		t.Fatal("x offered in the chooser")
	}
	h.openTickets(website)
	if h.offers(Launch) {
		t.Fatal("x offered in the ticket list")
	}
	h.m.app.tickets.selected = ticketID(website, 6)
	h.drive(h.press("enter"))
	h.m.focus = view.List
	if h.offers(Launch) {
		t.Fatal("x offered with the focus on the list")
	}
	h.m.focus = view.Object
	if !h.offers(Launch) {
		t.Fatal("x not offered in the ticket view")
	}
	h.m.app.detail.known = false
	if h.offers(Launch) {
		t.Fatal("x offered for a ticket not shown yet")
	}
	h.m.app.detail.known = true
	h.m.app.deps.Launcher = nil
	if h.offers(Launch) {
		t.Fatal("x offered without a launcher")
	}

	// A notification's details are no ticket; the ticket opened from it is.
	h = newHarness(t)
	f := h.withLauncher()
	h.start()
	h.openDetails(orgA, 0)
	if h.offers(Launch) {
		t.Fatal("x offered in a notification's details")
	}
	h.drive(h.press("o"))
	if h.m.app.origin == nil || !h.offers(Launch) {
		t.Fatal("x not offered in the ticket opened from a notification")
	}
	h.drive(h.press("x"))
	if calls := f.launched(); len(calls) != 1 || calls[0].ticket != h.shownTicket() {
		t.Fatalf("launched %+v, want %+v", calls, h.shownTicket())
	}
}

// The ticket handed over is the one the pane shows: the list's cursor may
// have moved to another row meanwhile.
func TestXHandsOverTheTicketShownNotTheRowUnderTheCursor(t *testing.T) {
	h := newHarness(t)
	f := h.withLauncher()
	h.openSix()
	h.press("left")
	h.press("down")
	if h.m.app.tickets.selected == ticketID(website, 6) {
		t.Fatal("the cursor did not move")
	}
	h.press("right")
	if h.m.focus != view.Object || h.m.app.detail == nil || !h.offers(Launch) {
		t.Fatalf("not back in the ticket view: focus %v", h.m.focus)
	}
	shownNow := h.shownTicket()
	if !strings.Contains(h.paneText(), "#"+strconv.Itoa(shownNow.Number)) {
		t.Fatalf("the pane does not show #%d", shownNow.Number)
	}
	h.press("x")
	if calls := f.launched(); len(calls) != 1 || calls[0].ticket != shownNow {
		t.Fatalf("launched %+v, want the ticket shown %+v", calls, shownNow)
	}
}

// A launch's answer unlocks only its own launch: one from before does not
// free the lock of the one now running.
func TestAnOldLaunchsAnswerDoesNotUnlock(t *testing.T) {
	h := newHarness(t)
	h.withLauncher()
	h.openSix()
	h.press("x")
	old := launch.Done{Ref: h.m.app.launch.ref - 1, Ticket: h.shownTicket(), Kind: launch.Opened}
	h.feed(old)
	if h.offers(Launch) || !strings.HasPrefix(h.status(), "Opening") {
		t.Fatalf("an old answer unlocked: status %q", h.status())
	}
}

// Every outcome says what came of it, or why nothing opened.
func TestEveryOutcomeHasItsStatusText(t *testing.T) {
	for _, c := range []struct {
		kind   launch.Kind
		detail string
		want   string
	}{
		{launch.Opened, "", "Claude Code opened beside #6"},
		{launch.Ended, "", "Claude Code ended; back at #6"},
		{launch.EndedWithError, "exit status 2", "Claude Code ended with an error: exit status 2"},
		{launch.NotHandedOver, "", "This ticket's address cannot be handed over; nothing opened"},
		{launch.NoFolder, "/home/anna/dev/gone", "Folder not found: /home/anna/dev/gone"},
		{launch.NotADirectory, "/home/anna/notes.txt", "Not a folder: /home/anna/notes.txt"},
		{launch.BadFolder, "/tmp/a\nb", "This folder cannot be typed into a shell: /tmp/a b"},
		{launch.ConfigUnreadable, "/home/anna/.claude.json: unexpected end", "Claude Code's configuration could not be read: /home/anna/.claude.json: unexpected end"},
		{launch.NoServer, "https://lalabase.com; Claude Code has: https://staging.example", "No MCP server of Claude Code's for https://lalabase.com; Claude Code has: https://staging.example"},
		{launch.Conflict, "lalabase; https://evil.example", "An MCP server name of Claude Code's points at two servers: lalabase; https://evil.example"},
		{launch.BadName, "lala base", "This MCP server name cannot go into the prompt, rename it in Claude Code: lala base"},
		{launch.NoClaude, "", "claude was not found on the PATH"},
		{launch.NoPermission, "", "macOS does not let this terminal control iTerm: allow it in System Settings > Privacy & Security > Automation"},
		{launch.SessionGone, "", "iTerm did not find this session; nothing opened"},
		{launch.NoAnswer, "", "iTerm did not answer within 5 s; if macOS asks for permission, allow it and press x again"},
		{launch.ITermFailed, "", "iTerm could not open a pane"},
		{launch.EndedWithError, "", "Claude Code ended with an error: "},
		{launch.Canceled, "", ""},
	} {
		h := newHarness(t)
		f := h.withLauncher()
		f.kind = c.kind
		h.openSix()
		cmd := h.press("x")
		msg := cmd().(launch.Done)
		msg.Detail = c.detail
		h.feed(msg)
		if got := h.status(); got != c.want {
			t.Errorf("kind %v: status %q, want %q", c.kind, got, c.want)
		}
		if !h.offers(Launch) {
			t.Errorf("kind %v: x is not offered again", c.kind)
		}
	}
}

// Foreign text in a detail (a name from a project's .mcp.json) is cleaned
// before it reaches the status line.
func TestAForeignDetailIsCleaned(t *testing.T) {
	h := newHarness(t)
	h.withLauncher()
	h.openSix()
	h.press("x")
	h.feed(launch.Done{Ref: h.m.app.launch.ref, Ticket: h.shownTicket(), Kind: launch.Conflict, Detail: "lala\x1b]52;c;cHduZWQ=\x07base\u202e; https://evil.example"})
	if got := h.status(); strings.ContainsAny(got, "\x1b\x07\u202e") {
		t.Fatalf("status %q", got)
	}
}

// The notice belongs to its ticket: another ticket in the pane does not
// carry it.
func TestTheNoticeStaysWithItsTicket(t *testing.T) {
	h := newHarness(t)
	h.withLauncher()
	h.openSix()
	h.drive(h.press("x"))
	if h.status() == "" {
		t.Fatal("no notice")
	}
	h.press("esc")
	h.m.app.tickets.selected = ticketID(website, 7)
	h.drive(h.press("enter"))
	if h.m.app.detail == nil || h.m.app.detail.id != ticketID(website, 7) {
		t.Fatal("ticket #7 is not open")
	}
	if got := h.status(); got != "" {
		t.Fatalf("ticket #7 shows %q", got)
	}
}
