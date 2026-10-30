//go:build linux || darwin

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// A comment (K3a) through a real terminal, against the loopback fixture: the
// key, the box, a draft left with esc and brought back, typed and pasted text,
// ctrl+s, the comment in the pane. Ctrl-S reaches the program only with IXON
// off in raw mode: the CI's Linux job runs this, the check C2 left open.

const offerComment = "k add comment"

func TestPTYComment(t *testing.T) {
	t.Parallel()

	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			f, url := fixtureServer(t, inboxfixture.Normal)
			home := t.TempDir()
			c := startTerminalWith(t, "comment", width, terminalOptions{url: url, marker: tokenWrite, limit: markLimit, home: home})
			if err := c.ready(); err != nil {
				t.Fatalf("%v\n%s", err, c.bytes())
			}
			openSix(t, c, width)
			c.act(t, "k", width, "Comment", "#6 Ticket 6", "ctrl+s", "send", "esc", "cancel")
			c.act(t, "Zeile eins", width, "Zeile eins"+boxCaret)
			c.act(t, keyEsc, width, offerComment)
			c.act(t, "k", width, "Zeile eins"+boxCaret)
			c.act(t, keyEnter+pasteStart+"Zeile zwei"+pasteEnd, width, "Zeile zwei"+boxCaret)
			if err := c.send("\x13"); err != nil {
				t.Fatal(err)
			}
			c.boxCloses(t, width, "Comment posted on #6")
			c.expect(t, "the comment in the pane", width, "Zeile eins", "Zeile zwei", offerComment)
			c.act(t, "k", width, "Comment", boxCaret)
			c.act(t, "UNSENT", width, "UNSENT"+boxCaret)
			c.act(t, keyEsc, width, offerComment)
			c.quit(t)
			at := slices.IndexFunc(f.Requests(), func(r inboxfixture.Request) bool { return r.Route == "posts.create" })
			if at < 0 || f.Requests()[at].Args["body"] != "Zeile eins\nZeile zwei" || f.Requests()[at].Args["format"] != "plain" || len(f.Mutations()) != 1 {
				t.Fatalf("comment at %d of %+v", at, f.Requests())
			}
			if keys := f.Keys(); len(keys) != 1 || !strings.HasPrefix(keys[0], "lalabase-comment-") {
				t.Fatalf("keys %v", keys)
			}
			// A draft is memory alone: nothing the client left holds it.
			_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, _ error) error {
				if text, err := os.ReadFile(path); err == nil && strings.Contains(string(text), "UNSENT") {
					t.Errorf("the draft is on the disk: %s", path)
				}
				return nil
			})
		})
	}
}
