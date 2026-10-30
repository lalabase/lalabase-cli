// Command dialog-fixture shows the dialog of the interface (M1) on a person's
// terminal, without a server: no key of the client opens one yet.
//
//	go run ./tools/dialog-fixture           # the small box
//	go run ./tools/dialog-fixture -large    # the large one
//
// The box stands over panes of hostile text, on a ticket with a hostile
// title. Every send takes a second; the first is refused with what it would
// have sent, the next goes through and the box closes. q then quits.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// hostile is what a member of an organisation could write into a title or a
// comment: a clear screen, a colour, a link, the clipboard, a bell, bidi
// overrides, and broad characters for the box's edges.
const hostile = "\x1b[2J\x1b[95mCSI\x1b[0m\x1b]8;;https://evil.invalid\x07LINK\x1b]8;;\x1b\\\x1b]52;c;YQ==\x1b\\\x07\u202e\u2066\u2069 \u4e16\u754c \U0001F468\u200d\U0001F469\u200d\U0001F467"

// sendTime is how long a send takes here: long enough to try a second Enter.
const sendTime = time.Second

// spec is the dialog of the fixture.
func spec(large bool) ui.DialogSpec {
	refused := false
	s := ui.DialogSpec{
		Action:   present.NewInline("Book time"),
		SendHelp: present.NewInline("book"),
		Target:   ui.DialogTarget{Project: "project", Ticket: "ticket", Number: 42, Title: "Login bricht ab " + hostile},
		Fields: []ui.DialogField{
			{Label: present.NewInline("Duration"), Hint: present.NewInline("30, 1:30 or 1.5"), Limit: 32},
			{Label: present.NewInline("Note"), Limit: 200},
		},
	}
	if large {
		s.Action, s.SendHelp = present.NewInline("Comment"), present.Inline{}
		s.Large = true
		s.Fields = []ui.DialogField{{Label: present.NewInline("Comment"), Hint: present.NewInline("Plain text"), Limit: 1 << 16}}
	}
	s.Send = func(sub ui.Submission) tea.Cmd {
		outcome := ui.DialogOutcome{Dialog: sub.Dialog, Done: refused}
		if !refused {
			outcome.Refusal = present.NewInline(fmt.Sprintf("Refused, as a server would; it would have sent %q", sub.Values))
		}
		refused = true
		return tea.Tick(sendTime, func(time.Time) tea.Msg { return outcome })
	}
	return s
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
}

// run shows the dialog until the person quits.
func run(args []string, in io.Reader, out, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("dialog-fixture", flag.ContinueOnError)
	fs.SetOutput(stderr)
	large := fs.Bool("large", false, "the large box, one field of several lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var environment []string
	for _, entry := range environ {
		for _, key := range []string{"TERM=", "TERM_PROGRAM=", "COLORTERM=", "NO_COLOR="} {
			if strings.HasPrefix(entry, key) {
				environment = append(environment, entry)
			}
		}
	}
	ctx := context.Background()
	sup := worker.New(ctx)
	defer sup.Stop(time.Second)
	text := "Foreign text behind the box " + hostile + "\n"
	err := ui.Run(ctx, in, out, environment, ui.NewWithContent("Nordlicht GmbH "+hostile, strings.Repeat(text, 40), strings.Repeat(text, 40), ui.WithSupervisor(sup), ui.WithDialog(spec(*large))), sup)
	if err != nil && !errors.Is(err, tea.ErrInterrupted) {
		fmt.Fprintln(stderr, "dialog-fixture:", err)
		return 1
	}
	return 0
}
