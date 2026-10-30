//go:build linux || darwin

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// Booking time (K6) through a real terminal, against the loopback fixture:
// the whole booking at 80 and 120 columns, then what the server refuses, the
// lost answer, a 429, a read-only token, a server before the capability, two
// presses of enter, a booking left with esc, and a pending booking at the
// end of the program.

const (
	offerBook  = "b book time"
	bookTitle  = "Book time"
	bookHint   = "30, 1:30 or 1.5"
	tokenWrite = "Token can write: yes"
)

func startBook(t *testing.T, scenario inboxfixture.Scenario, width uint16, opts ...inboxfixture.Option) (*terminalProcess, *inboxfixture.Fixture) {
	t.Helper()
	c, f := startMark(t, scenario, width, tokenWrite, opts...)
	openSix(t, c, width)
	return c, f
}

// bookings are the booking attempts the fixture saw, one line each: the
// ticket, the duration as it arrived, the note, whether a key came along,
// and the status.
func bookings(f *inboxfixture.Fixture) []string {
	var out []string
	for _, r := range f.Requests() {
		if r.Route != "time_sessions.book" {
			continue
		}
		note, hasNote := r.Args["description"]
		line := fmt.Sprintf("%v %q %d", r.Args["ticket_id"], r.Args["duration_string"], r.Status)
		if hasNote {
			line += fmt.Sprintf(" note=%q", note)
		}
		out = append(out, line)
	}
	return out
}

// quit ends the program and proves the terminal came back.
func (c *terminalProcess) quit(t *testing.T) {
	t.Helper()
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	if err := terminalOutputError(c.bytes()); err != nil {
		t.Fatalf("%v\n%q", err, c.bytes())
	}
}

// boxCloses waits for the box to leave and the ticket's keys to be back.
func (c *terminalProcess) boxCloses(t *testing.T, width uint16, markers ...string) {
	t.Helper()
	if err := c.until("the box closing", readyLimit, func() bool {
		return !c.screenShows(width, bookTitle) && c.screenShows(width, markers...)
	}); err != nil {
		t.Fatalf("%v, waiting for %q:\n%s", err, markers, strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
	}
}

// The whole booking: the key in the help, the box, what is typed, the send;
// the box goes, the notice has the minutes of the answer, the sum moves. One
// write, as typed, with its key; of the reads only the time.
func TestPTYBookTime(t *testing.T) {
	t.Parallel()

	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			c, f := startBook(t, inboxfixture.Normal, width)
			c.expect(t, "the key", width, offerBook)
			before := len(f.Requests())
			c.act(t, "b", width, bookTitle, "#6 Ticket 6", "Duration", bookHint, "Note", "enter", "book", "esc", "cancel")
			c.act(t, "30", width, "30"+boxCaret)
			c.act(t, keyTab, width, "Note", boxCaret)
			c.act(t, "b", width, "b"+boxCaret) // text here, not the key
			c.act(t, "\x7f"+"Vertrag", width, "Vertrag"+boxCaret)
			if err := c.send(keyEnter); err != nil {
				t.Fatal(err)
			}
			c.boxCloses(t, width, "0:30 booked on #6")
			if width == 120 {
				c.expect(t, "the sum", width, "Booked by you", "2:00")
			}
			c.expect(t, "the key again", width, offerBook)
			c.quit(t)
			if got := bookings(f); len(got) != 1 || got[0] != fmt.Sprintf(`%s "30" 201 note="Vertrag"`, ticket6) {
				t.Fatalf("bookings %q", got)
			}
			if keys := f.Keys(); len(keys) != 1 || !strings.HasPrefix(keys[0], "lalabase-book-") {
				t.Fatalf("keys %v", keys)
			}
			if got := f.Mutations(); len(got) != 1 || got[0].Kind != inboxfixture.MutationBook || got[0].Ticket != ticket6 {
				t.Fatalf("mutations %+v", got)
			}
			// After the write the time and nothing else was read.
			for _, r := range f.Requests()[before:] {
				if r.Route == "time_sessions.book" {
					continue
				}
				if r.Tool != "summarize_time_sessions" {
					t.Fatalf("read besides the time: %s %s %s", r.Method, r.Path, r.Tool)
				}
			}
		})
	}
}

// A duration the server refuses: its text in the box, the input kept; the
// next try goes on enter alone, with a new key, and an empty note is not sent.
func TestPTYBookTimeRefused(t *testing.T) {
	t.Parallel()

	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			c, f := startBook(t, inboxfixture.Normal, width)
			c.act(t, "b", width, bookTitle)
			c.act(t, "1,5x", width, "1,5x"+boxCaret)
			c.act(t, keyEnter, width, "muss ein", "1,5x"+boxCaret)
			c.expect(t, "the keys of the box", width, "enter", "book")
			time.Sleep(200 * time.Millisecond)
			if got := bookings(f); len(got) != 1 {
				t.Fatalf("bookings %q", got)
			}
			c.act(t, "\x7f", width, "1,5"+boxCaret)
			if err := c.send(keyEnter); err != nil {
				t.Fatal(err)
			}
			c.boxCloses(t, width, "1:30 booked on #6")
			c.quit(t)
			if got := bookings(f); len(got) != 2 || got[0] != fmt.Sprintf(`%s "1,5x" 422`, ticket6) || got[1] != fmt.Sprintf(`%s "1,5" 201`, ticket6) {
				t.Fatalf("bookings %q", got)
			}
			if keys := f.Keys(); len(keys) != 2 || keys[0] == keys[1] {
				t.Fatalf("keys %v", keys)
			}
			if got := f.Mutations(); len(got) != 1 {
				t.Fatalf("mutations %+v", got)
			}
		})
	}
}

// Without the right the key is offered all the same, and the box says no;
// the input stays, and esc leaves.
func TestPTYBookTimeForbidden(t *testing.T) {
	t.Parallel()

	c, f := startMark(t, inboxfixture.Refusals, 120, tokenWrite)
	openWebsite(t, c, 120)
	openTicketBelow(t, c, 120, 1, 4, "esc back", offerBook)
	c.act(t, "b", 120, bookTitle, "#4 Ticket 4")
	c.act(t, "30"+keyEnter, 120, "You may not book time on this ticket", "30"+boxCaret)
	c.act(t, keyEsc, 120, offerBook)
	c.quit(t)
	if got := f.Mutations(); len(got) != 0 {
		t.Fatalf("mutations %+v", got)
	}
}

// A lost answer: the server booked and nobody knows. Nothing goes out again,
// the sum is read back into the box, enter does nothing there; esc and b are
// the way on, with a new key.
func TestPTYBookTimeLost(t *testing.T) {
	t.Parallel()

	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			c, f := startBook(t, inboxfixture.Lost, width)
			c.act(t, "b", width, bookTitle)
			c.act(t, "30"+keyEnter, width, "Not clear whether", "2:00", "30"+boxCaret)
			c.expect(t, "the way out", width, "esc", "close")
			if c.screenShows(width, "booked on") {
				t.Fatalf("a booking claimed:\n%s", strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
			}
			if err := c.send(keyEnter); err != nil {
				t.Fatal(err)
			}
			time.Sleep(300 * time.Millisecond)
			if got := bookings(f); len(got) != 1 || got[0] != fmt.Sprintf(`%s "30" 0`, ticket6) {
				t.Fatalf("bookings %q", got)
			}
			c.act(t, keyEsc, width, offerBook)
			c.act(t, "b", width, bookTitle)
			c.expect(t, "a box of its own", width, "enter", "book", boxCaret)
			if err := c.send("\x03"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 130)
			if got := f.Mutations(); len(got) != 1 {
				t.Fatalf("mutations %+v", got)
			}
		})
	}
}

// A 429: the queue pauses and shows it, then sends the one request again,
// under its key. One booking.
func TestPTYBookTimeAfterARateLimit(t *testing.T) {
	t.Parallel()

	c, f := startBook(t, inboxfixture.RateLimited, 80)
	c.act(t, "b", 80, bookTitle)
	c.act(t, "30"+keyEnter, 80, "Rate limited until")
	c.boxCloses(t, 80, "0:30 booked on #6")
	c.quit(t)
	if got := bookings(f); len(got) != 2 || got[0] != fmt.Sprintf(`%s "30" 429`, ticket6) || got[1] != fmt.Sprintf(`%s "30" 201`, ticket6) {
		t.Fatalf("bookings %q", got)
	}
	if keys := f.Keys(); len(keys) != 2 || keys[0] != keys[1] {
		t.Fatalf("keys %v", keys)
	}
	if got := f.Mutations(); len(got) != 1 {
		t.Fatalf("mutations %+v", got)
	}
}

// A read-only token and a server without the capability have no b: not in
// the help, and the key does nothing.
func TestPTYNoBWithoutTheRightOrTheCapability(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		scenario inboxfixture.Scenario
		marker   string
		opts     []inboxfixture.Option
	}{
		{"read-only", inboxfixture.ReadOnly, "Token can write: no", nil},
		{"before the capability", inboxfixture.Normal, tokenWrite, []inboxfixture.Option{inboxfixture.BeforeR1()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := startMark(t, tc.scenario, 120, tc.marker, tc.opts...)
			openSix(t, c, 120)
			if c.screenShows(120, offerBook) {
				t.Fatalf("b offered:\n%s", strings.Join(renderScreen(c.bytes(), 24, 120), "\n"))
			}
			if err := c.send("b"); err != nil {
				t.Fatal(err)
			}
			time.Sleep(300 * time.Millisecond)
			if c.screenShows(120, bookTitle) {
				t.Fatalf("a box:\n%s", strings.Join(renderScreen(c.bytes(), 24, 120), "\n"))
			}
			c.quit(t)
			if got := bookings(f); len(got) != 0 {
				t.Fatalf("bookings %q", got)
			}
		})
	}
}

// Two presses of enter at once, on a server that takes its time: one
// booking.
func TestPTYTwoEntersBookOnce(t *testing.T) {
	t.Parallel()

	c, f := startBook(t, inboxfixture.Slow, 120)
	c.act(t, "b", 120, bookTitle)
	c.act(t, "30", 120, "30"+boxCaret)
	c.act(t, keyEnter+keyEnter, 120, "Sending")
	c.boxCloses(t, 120, "0:30 booked on #6")
	c.quit(t)
	if got := bookings(f); len(got) != 1 {
		t.Fatalf("bookings %q", got)
	}
	if keys := f.Keys(); len(keys) != 1 {
		t.Fatalf("keys %v", keys)
	}
}

// Esc leaves a send under way: b waits for it on this ticket, the person
// goes on to another, and the end of the first is told there. The booking
// is on the ticket b was pressed on.
func TestPTYABookingLeftBehindIsToldElsewhere(t *testing.T) {
	t.Parallel()

	c, f := startBook(t, inboxfixture.Slow, 120)
	c.act(t, "b", 120, bookTitle)
	c.act(t, "30"+keyEnter, 120, "Sending", "esc", "close")
	c.act(t, keyEsc, 120, "esc back")
	if c.screenShows(120, offerBook) {
		t.Fatalf("b offered while a booking runs:\n%s", strings.Join(renderScreen(c.bytes(), 24, 120), "\n"))
	}
	c.act(t, keyEsc, 120, "esc projects")
	c.act(t, keyDown, 120, ">    #7 Ticket 7")
	c.act(t, keyEnter, 120, "esc back", "#7 Ticket 7", offerBook)
	c.expect(t, "the end of the booking", 120, "0:30 booked on #6")
	c.quit(t)
	if got := f.Mutations(); len(got) != 1 || got[0].Ticket != ticket6 {
		t.Fatalf("mutations %+v", got)
	}
}

// A 401 on a booking ends the session; the box goes with it.
func TestPTYBookTimeExpiresOn401(t *testing.T) {
	t.Parallel()

	c, f := startMark(t, inboxfixture.Unauthorized, 120, tokenWrite)
	openWebsite(t, c, 120)
	openTicketBelow(t, c, 120, 1, 3, "esc back", offerBook)
	c.act(t, "b", 120, bookTitle)
	if err := c.send("30" + keyEnter); err != nil {
		t.Fatal(err)
	}
	c.boxCloses(t, 120, "Session expired")
	c.quit(t)
	if got := f.Mutations(); len(got) != 0 {
		t.Fatalf("mutations %+v", got)
	}
}

// Ctrl-C while a booking is out restores the terminal.
func TestPTYBookTimeInterruptWithAPendingWrite(t *testing.T) {
	t.Parallel()

	c, _ := startBook(t, inboxfixture.Slow, 80)
	c.act(t, "b", 80, bookTitle)
	c.act(t, "30"+keyEnter, 80, "Sending")
	if err := c.send("\x03"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 130)
	if err := terminalOutputError(c.bytes()); err != nil {
		t.Fatal(err)
	}
}
