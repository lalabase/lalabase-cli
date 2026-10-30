//go:build linux || darwin

package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"syscall"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/creack/pty"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// The dialog (M1) through a real terminal, on its own: the child starts
// with it open, over panes of hostile text, on a hostile ticket
// title; the first send is refused with a hostile text that names what the
// send carried, the next goes through.

const (
	dialogSmall = "dialog-small"
	dialogLarge = "dialog-large"
	pasteStart  = "\x1b[200~"
	pasteEnd    = "\x1b[201~"
	boxCaret    = "\u258f"
)

func dialogSpec(large bool) ui.DialogSpec {
	refused := false
	spec := ui.DialogSpec{
		Action:   present.NewInline("Book time"),
		SendHelp: present.NewInline("book"),
		Target:   ui.DialogTarget{Project: "project", Ticket: "ticket", Number: 42, Title: "SAFE_TITLE" + terminalHostile},
		Fields: []ui.DialogField{
			{Label: present.NewInline("Duration"), Hint: present.NewInline("30, 1:30 or 1.5"), Limit: 32},
			{Label: present.NewInline("Note"), Limit: 200},
		},
		Send: func(s ui.Submission) tea.Cmd {
			first := !refused
			refused = true
			return func() tea.Msg {
				if first {
					return ui.DialogOutcome{Dialog: s.Dialog, Refusal: present.NewInline(fmt.Sprintf("REFUSED %q %s", s.Values, terminalHostile))}
				}
				return ui.DialogOutcome{Dialog: s.Dialog, Done: true}
			}
		},
	}
	if large {
		spec.Action, spec.SendHelp = present.NewInline("Comment"), present.Inline{}
		spec.Large = true
		spec.Fields = []ui.DialogField{{Label: present.NewInline("Text"), Hint: present.NewInline("Plain text"), Limit: 2000}}
	}
	return spec
}

func dialogRunner(scenario string) func(context.Context, io.Reader, io.Writer, []string, *worker.Supervisor) error {
	return func(ctx context.Context, in io.Reader, out io.Writer, environment []string, sup *worker.Supervisor) error {
		return ui.Run(ctx, in, out, environment, ui.NewWithContent("SAFE_INLINE"+terminalHostile, "No data loaded\nSAFE_LIST"+terminalHostile, "SAFE_OBJECT"+terminalHostile, ui.WithDialog(dialogSpec(scenario == dialogLarge))), sup)
	}
}

func startDialog(t *testing.T, scenario string, width uint16) *terminalProcess {
	t.Helper()
	marker := "Book time"
	if scenario == dialogLarge {
		marker = "Comment"
	}
	c := startTerminalWith(t, scenario, width, terminalOptions{url: "http://127.0.0.1:1", marker: marker, limit: caseLimit})
	if err := c.ready(); err != nil {
		t.Fatalf("%v\n%q", err, c.bytes())
	}
	return c
}

// closes sends input and waits for the box's title to leave the screen; the
// panes behind it take its place.
func (c *terminalProcess) closes(t *testing.T, input string, width uint16, title string) {
	t.Helper()
	if err := c.send(input); err != nil {
		t.Fatal(err)
	}
	if err := c.until("the box closing", readyLimit, func() bool {
		return !c.screenShows(width, title) && c.screenShows(width, "SAFE_LIST")
	}); err != nil {
		t.Fatalf("%v:\n%s", err, strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
	}
}

// A person's run: type, change the field, paste what must not arrive, send,
// be refused, send again; the box goes, and the keys are the interface's
// again.
func TestPTYTheSmallDialog(t *testing.T) {
	for _, width := range []uint16{80, 120, 50} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			c := startDialog(t, dialogSmall, width)
			c.expect(t, "the box", width, "Book time", "#42", "Duration", boxCaret, "30, 1:30 or 1.5", "Note", "esc", "cancel")
			c.act(t, "3", width, "3"+boxCaret)
			c.act(t, "0", width, "30"+boxCaret)
			c.act(t, "\t", width, "Note")
			c.act(t, pasteStart+"ok\x1b[31mred\x07\u202eend\r\nnext"+pasteEnd, width, "okred end next"+boxCaret)
			if c.screenShows(width, "[31m") {
				t.Fatalf("the sequence reached the screen:\n%s", strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
			}
			// q is text here, not the way out.
			c.act(t, "q", width, "next"+"q"+boxCaret)
			c.act(t, "\x7f", width, "next"+boxCaret)
			c.act(t, "\r", width, "REFUSED", `"30"`, `"okred end next"`)
			c.expect(t, "the input stays", width, "30", "okred end next"+boxCaret, "Book time")
			c.closes(t, "\r", width, "Book time")
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
		})
	}
}

// Enter breaks the line, Ctrl-S sends: the terminal's flow control must not
// take it (raw mode clears IXON, seen here on Linux and macOS alike).
func TestPTYTheLargeDialog(t *testing.T) {
	for _, width := range []uint16{80, 120, 50} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			c := startDialog(t, dialogLarge, width)
			c.expect(t, "the box", width, "Comment", "#42", "Text", boxCaret, "Plain text", "ctrl+s", "send", "enter", "new line", "esc")
			c.act(t, "one", width, "one"+boxCaret)
			// The caret alone at the start of a line of its own.
			c.act(t, "\r", width, "\u2502  "+boxCaret)
			c.act(t, "two", width, "two"+boxCaret)
			if !c.screenShows(width, "one") {
				t.Fatal("a line break took the first line")
			}
			c.act(t, pasteStart+"three\rfour\x1b]0;x\x07"+pasteEnd, width, "three", "four"+boxCaret)
			c.act(t, "\x13", width, "REFUSED", `"one\ntwothree\nfour"`)
			c.closes(t, "\x13", width, "Comment")
			if err := c.send("\x03"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 130)
		})
	}
}

// Esc goes back to the keys of the screen, whatever was typed.
func TestPTYEscClosesTheDialog(t *testing.T) {
	c := startDialog(t, dialogSmall, 80)
	c.act(t, "q30", 80, "q30"+boxCaret)
	// A lone ESC waits for the decoder's timeout: the box closes without
	// another key.
	c.closes(t, "\x1b", 80, "Book time")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
}

// Ctrl-C ends the program from the box like from anywhere, and the terminal
// is what it was: termios, the screen, the modes (finish).
func TestPTYCtrlCLeavesTheDialogRestored(t *testing.T) {
	for _, scenario := range []string{dialogSmall, dialogLarge} {
		t.Run(scenario, func(t *testing.T) {
			c := startDialog(t, scenario, 80)
			c.act(t, "text", 80, "text"+boxCaret)
			if err := c.send("\x03"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 130)
		})
	}
}

// A resize while the box is open: it moves to the new middle with its input,
// a terminal too small for anything keeps it for later, and the terminal is
// restored at the end.
func TestPTYAResizeKeepsTheDialog(t *testing.T) {
	c := startDialog(t, dialogSmall, 120)
	c.act(t, "30", 120, "30"+boxCaret)
	resize := func(cols, rows uint16) {
		t.Helper()
		if err := pty.Setsize(c.master, &pty.Winsize{Rows: rows, Cols: cols}); err != nil {
			t.Fatal(err)
		}
		if err := c.cmd.Process.Signal(syscall.SIGWINCH); err != nil {
			t.Fatal(err)
		}
	}
	shows := func(cols, rows uint16, markers ...string) {
		t.Helper()
		if err := c.until(fmt.Sprintf("%dx%d", cols, rows), readyLimit, func() bool {
			return screenHas(renderScreen(c.bytes(), int(rows), int(cols)), markers...)
		}); err != nil {
			t.Fatalf("%v:\n%s", err, strings.Join(renderScreen(c.bytes(), int(rows), int(cols)), "\n"))
		}
	}
	for _, size := range [][2]uint16{{80, 24}, {59, 24}, {50, 12}, {30, 9}, {24, 8}} {
		resize(size[0], size[1])
		shows(size[0], size[1], "30"+boxCaret, "Book time")
	}
	resize(20, 6)
	shows(20, 6, "Terminal too small", "esc", "ctrl+c")
	resize(120, 24)
	shows(120, 24, "30"+boxCaret, "Book time", "Note")
	if err := c.send("\x03"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 130)
}
