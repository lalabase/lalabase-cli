//go:build linux || darwin

package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// sgrParams are the SGR parameters a stream sets, each once.
func sgrParams(stream string) map[int]bool {
	seen := map[int]bool{}
	for _, m := range regexp.MustCompile(`\x1b\[([0-9;]*)m`).FindAllStringSubmatch(stream, -1) {
		for _, p := range strings.Split(m[1], ";") {
			if n, err := strconv.Atoi(p); err == nil {
				seen[n] = true
			}
		}
	}
	return seen
}

// The tones of a status through a real terminal: the tickets list draws
// #1 (in progress) yellow and #2 (new) cyan, each while the cursor bar is
// on the other. With NO_COLOR the same list
// comes without a colour, its bold and faint kept. The cursor bar
// (reversed) is no colour and shows in both.
func TestPTYStatusTones(t *testing.T) {
	t.Parallel()

	for _, noColor := range []bool{false, true} {
		t.Run(fmt.Sprintf("NO_COLOR=%v", noColor), func(t *testing.T) {
			_, url := fixtureServer(t, inboxfixture.Normal)
			opts := terminalOptions{url: url, marker: "Token can write: yes", limit: inboxLimit}
			if noColor {
				opts.env = []string{"NO_COLOR=1"}
			}
			c := startTerminalWith(t, "inbox-"+string(inboxfixture.Normal), 120, opts)
			if err := c.ready(); err != nil {
				t.Fatalf("%v\n%s", err, c.bytes())
			}
			openWebsite(t, c, 120)
			// The cursor bar draws #1 without its tone; one row down, #1
			// is an ordinary row again.
			c.act(t, keyDown, 120, ">    #2 Export als CSV")
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			out := c.bytes()
			if err := terminalOutputError(out); err != nil {
				t.Fatalf("%v\n%q", err, out)
			}
			seen := sgrParams(out)
			if !seen[1] || !seen[2] {
				t.Fatalf("bold %v, faint %v: the weights are gone", seen[1], seen[2])
			}
			if !seen[7] {
				t.Fatal("no reversed cells: the cursor bar is gone")
			}
			if noColor {
				if seen[38] || seen[48] {
					t.Fatalf("a colour despite NO_COLOR: %v", seen)
				}
				if strings.Contains(out, "\x1b]1") {
					t.Fatalf("the window was painted despite NO_COLOR: %q", out)
				}
				return
			}
			if !seen[180] || !seen[116] {
				t.Fatalf("yellow %v, cyan %v: a tone is missing", seen[180], seen[116])
			}
			// The window takes the palette, and gets the terminal's back.
			for _, osc := range []string{"\x1b]10;#c4c7cd", "\x1b]11;#0e1014", "\x1b]110", "\x1b]111"} {
				if !strings.Contains(out, osc) {
					t.Fatalf("no %q in %q", osc, out)
				}
			}
		})
	}
}
