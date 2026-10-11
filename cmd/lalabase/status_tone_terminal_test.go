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
// #1 (in progress) yellow and #2 (new) cyan. With NO_COLOR the same list
// comes without a colour, its bold and faint kept.
func TestPTYStatusTones(t *testing.T) {
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
			if noColor {
				if seen[32] || seen[33] || seen[36] {
					t.Fatalf("a colour despite NO_COLOR: %v", seen)
				}
				return
			}
			if !seen[33] || !seen[36] {
				t.Fatalf("yellow %v, cyan %v: a tone is missing", seen[33], seen[36])
			}
		})
	}
}
