//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// C10 with the real launcher: on Linux the opener comes from the PATH, so
// a stub stands in for it and records what it is handed (macOS has a fixed
// path, whose run would start a browser).
func TestPTYOpenFileHandsTheAddressToTheOpener(t *testing.T) {
	_, url := fixtureServer(t, inboxfixture.Normal)
	bin := t.TempDir()
	probe := filepath.Join(bin, "args")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$#\" \"$@\" > \"" + probe + "\"\n"
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte(stub), 0o700); err != nil { //nolint:gosec // a test program
		t.Fatal(err)
	}
	c := startTerminalWith(t, "inbox-normal", 120, terminalOptions{
		url: url, marker: "Token can write: yes", limit: inboxLimit,
		env: []string{"PATH=" + bin + ":/usr/bin:/bin"},
	})
	if err := c.ready(); err != nil {
		t.Fatalf("%v\n%s", err, c.bytes())
	}
	openWebsite(t, c, 120)
	c.act(t, keyEnter, 120, "esc back")
	c.act(t, keyTab, 120, "Comments")
	c.act(t, keyTab, 120, "> design.pdf")
	c.act(t, keyEnter, 120, "Opened design.pdf in the browser")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	got := strings.Fields(read(t, probe))
	want := url + "/org/" + inboxfixture.OrgA + "/files/ticket_document/00000000-0000-4000-8000-000000800001"
	if len(got) != 2 || got[0] != "1" || got[1] != want {
		t.Fatalf("args %q, want one argument %q", got, want)
	}
}
