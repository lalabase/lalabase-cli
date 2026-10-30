//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// openerStub stands in for xdg-open in a directory of its own, which it
// names beside the file that records what the stub is handed.
func openerStub(t *testing.T) (bin, probe string) {
	t.Helper()
	bin = t.TempDir()
	probe = filepath.Join(bin, "args")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$#\" \"$@\" > \"" + probe + "\"\n"
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte(stub), 0o700); err != nil { //nolint:gosec // a test program
		t.Fatal(err)
	}
	return bin, probe
}

// C10 with the real launcher: on Linux the opener comes from the PATH, so
// a stub stands in for it and records what it is handed (macOS has a fixed
// path, whose run would start a browser).
func TestPTYOpenFileHandsTheAddressToTheOpener(t *testing.T) {
	_, url := fixtureServer(t, inboxfixture.Normal)
	bin, probe := openerStub(t)
	// The profile's text with an empty fragment, which the transport takes:
	// the address starts with the checked base, not with this text.
	c := startTerminalWith(t, "inbox-normal", 120, terminalOptions{
		url: url + "#", marker: "Token can write: yes", limit: inboxLimit,
		env: []string{"PATH=" + bin + ":/usr/bin:/bin"},
	})
	if err := c.ready(); err != nil {
		t.Fatalf("%v\n%s", err, c.bytes())
	}
	openWebsite(t, c, 120)
	c.act(t, keyEnter, 120, "esc back")
	c.act(t, keyTab, 120, "Comments")
	c.act(t, keyTab, 120, "> design.pdf")
	c.act(t, keyEnter, 120, "Sent design.pdf to the browser")
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

// C12: a comment's file reaches the opener at its own address.
func TestPTYOpenACommentsFileHandsTheAddressToTheOpener(t *testing.T) {
	t.Parallel()

	_, url := fixtureServer(t, inboxfixture.Normal)
	bin, probe := openerStub(t)
	c := startTerminalWith(t, "inbox-normal", 120, terminalOptions{
		url: url, marker: "Token can write: yes", limit: inboxLimit,
		env: []string{"PATH=" + bin + ":/usr/bin:/bin"},
	})
	if err := c.ready(); err != nil {
		t.Fatalf("%v\n%s", err, c.bytes())
	}
	openWebsite(t, c, 120)
	c.act(t, keyEnter, 120, "esc back")
	c.act(t, keyTab, 120, "> Anna Albers")
	c.act(t, keyDown, 120, "> Ben Berger")
	c.act(t, keyDown, 120, "> test_file.txt")
	c.act(t, keyEnter, 120, "Sent test_file.txt to the browser")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	got := strings.Fields(read(t, probe))
	want := url + "/org/" + inboxfixture.OrgA + "/files/post_document/00000000-0000-4000-8000-000000000053"
	if len(got) != 2 || got[0] != "1" || got[1] != want {
		t.Fatalf("args %q, want one argument %q", got, want)
	}
}
