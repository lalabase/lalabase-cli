//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// The disk cache (C1e) through a real terminal, against the loopback
// fixture: one run fills it, a second run on the same home shows its pages
// and its ticket as "Cached" while the reload is slow, then fresh; with
// failing reloads "Cached, possibly stale" and R; a 401 clears the
// directory and the next run refills nothing from it; an unusable
// directory and a lock held by another lalabase fall back to memory with
// one notice. At 80 and 120 columns, with resize, q and Ctrl-C and the
// terminal restored.

// cacheDir is where the child keeps the cache of its default profile:
// under XDG_CACHE_HOME when the run sets it, else under its home.
func cacheDir(home, cacheHome string) string {
	if cacheHome != "" {
		return filepath.Join(cacheHome, "lalabase", "cli", "default")
	}
	return filepath.Join(home, ".cache", "lalabase", "cli", "default")
}

// cacheEntries are the entry files in the child's cache directory.
func cacheEntries(t *testing.T, home string) []string {
	t.Helper()
	return cacheEntriesIn(t, cacheDir(home, ""))
}

func cacheEntriesIn(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range list {
		if strings.HasPrefix(e.Name(), "e-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// startDisk starts a run on home against url; the "inbox-" prefix gives
// the child the longer limit.
func startDisk(t *testing.T, scenario inboxfixture.Scenario, width uint16, url, home string) *terminalProcess {
	t.Helper()
	return startDiskIn(t, scenario, width, url, home, "")
}

// startDiskIn is startDisk with the child's XDG_CACHE_HOME.
func startDiskIn(t *testing.T, scenario inboxfixture.Scenario, width uint16, url, home, cacheHome string) *terminalProcess {
	t.Helper()
	c := startTerminalWith(t, "inbox-disk-"+string(scenario), width, terminalOptions{url: url, marker: "Token can write: yes", limit: inboxLimit, home: home, cacheHome: cacheHome})
	if err := c.ready(); err != nil {
		t.Fatalf("%v\n%s", err, c.bytes())
	}
	return c
}

// countTool counts the requests of one tool, or of a REST path when tool
// is empty.
func countTool(f *inboxfixture.Fixture, tool, path string) int {
	n := 0
	for _, r := range f.Requests() {
		if (tool != "" && r.Tool == tool) || (tool == "" && r.Path == path) {
			n++
		}
	}
	return n
}

// noticeGone waits until the list or pane shows the rows without the
// cache notice: the reload landed.
func (c *terminalProcess) noticeGone(t *testing.T, width uint16, notice string, markers ...string) {
	t.Helper()
	if err := c.until("the reload after "+notice, readyLimit, func() bool {
		return c.screenShows(width, markers...) && !c.screenShows(width, notice)
	}); err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
	}
}

// Two runs on one home: the second shows the inbox and the ticket from
// disk at once while the slow reloads are out, then fresh; the start of
// the second run asks /me again and makes the same five requests.
func TestPTYDiskCacheRestartFlow(t *testing.T) {
	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprintf("%d", width), func(t *testing.T) {
			f, url := fixtureServer(t, inboxfixture.SlowReads)
			home := t.TempDir()
			// At 80 columns the run sets XDG_CACHE_HOME: the cache lies there
			// and nothing under the home (review round 2).
			cacheHome := ""
			if width == 80 {
				cacheHome = filepath.Join(home, "xdg-cache")
			}
			dir := cacheDir(home, cacheHome)
			entries := func() []string { return cacheEntriesIn(t, dir) }
			c := startDiskIn(t, inboxfixture.SlowReads, width, url, home, cacheHome)
			// The header shows before the counters' page landed and was
			// written: wait for the one entry of the start.
			if err := c.until("the counters' page on disk", readyLimit, func() bool { return len(entries()) == 1 }); err != nil {
				t.Fatalf("%v: %d entries after the start", err, len(entries()))
			}
			if cacheHome != "" && len(cacheEntries(t, home)) != 0 {
				t.Fatal("entries under the home although XDG_CACHE_HOME is set")
			}
			openInboxA(t, c, width)
			openRow6Details(t, c, width)
			c.act(t, "o", width, "#6 Ticket 6", "Notification: unread", "Description of six", "esc notification")
			c.act(t, keyEsc, width, "esc close")
			c.act(t, keyEsc, width, "esc organisations")
			c.act(t, keyEsc, width, "up/down highlight")
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			first := len(f.Requests())
			if first != 7 {
				t.Fatalf("%d requests in the first run", first)
			}
			if n := len(entries()); n != 3 {
				t.Fatalf("%d entries after the first run", n)
			}
			// The second run: /me again, the counters fresh, the inbox from
			// disk while its slow reload is out.
			c = startDiskIn(t, inboxfixture.SlowReads, width, url, home, cacheHome)
			if n := countTool(f, "", "/api/v1/me"); n != 2 {
				t.Fatalf("/me was asked %d times", n)
			}
			c.expect(t, "the directory and the counters", width, "All organisations", "Nordlicht GmbH", "Südwind AG", "33", "30")
			c.moveTo(t, keyDown, width, "Nordlicht GmbH", "Inbox", 1)
			c.act(t, keyEnter, width, "Cached", "#1 Benachrichtigung", "Nordlicht GmbH | Inbox | page 1 of 2")
			c.noticeGone(t, width, "Cached", "#1 Benachrichtigung", "page 1 of 2")
			openRow6Details(t, c, width)
			c.act(t, "o", width, "Cached", "#6 Ticket 6", "Description of six")
			other := uint16(120)
			if width == 120 {
				other = 80
			}
			c.resize(t, other)
			c.expect(t, "the ticket after the resize", other, "#6 Ticket 6", "Description of six")
			c.resize(t, width)
			c.noticeGone(t, width, "Cached", "#6 Ticket 6", "Description of six")
			c.act(t, keyEsc, width, "esc close")
			c.act(t, keyEsc, width, "esc organisations")
			c.act(t, keyEsc, width, "up/down highlight")
			// q at one width, Ctrl-C at the other: the terminal is restored
			// either way, with the disk let go.
			code, input := 0, "q"
			if width == 120 {
				code, input = 130, "\x03"
			}
			if err := c.send(input); err != nil {
				t.Fatal(err)
			}
			c.finish(t, code)
			if err := terminalOutputError(c.bytes()); err != nil {
				t.Fatalf("%v\n%q", err, c.bytes())
			}
			// The second run made the same requests as the first: the disk
			// added none and saved none (every cached page is reloaded
			// once).
			if n := len(f.Requests()) - first; n != 7 {
				var lines []string
				for _, r := range f.Requests()[first:] {
					lines = append(lines, fmt.Sprintf("%s %s %s %v", r.Method, r.Path, r.Tool, r.Args))
				}
				t.Fatalf("%d requests in the second run:\n%s", n, strings.Join(lines, "\n"))
			}
			if n := len(entries()); n != 3 {
				t.Fatalf("%d entries after the second run", n)
			}
			// The directory and its files are the user's alone.
			info, err := os.Stat(dir)
			if err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("the cache directory: %v %v", info, err)
			}
			for _, name := range entries() {
				info, err := os.Stat(filepath.Join(dir, name))
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("%s: %v %v", name, info, err)
				}
			}
		})
	}
}

// A page whose reload fails is shown from disk as "Cached, possibly stale";
// R reads it.
func TestPTYDiskCacheStaleWhenTheReloadFails(t *testing.T) {
	width := uint16(80)
	f, url := fixtureServer(t, inboxfixture.Temporary)
	home := t.TempDir()
	c := startDisk(t, inboxfixture.Temporary, width, url, home)
	openInboxA(t, c, width)
	// The notice is cut to the list's width at 80 columns.
	c.act(t, "]", width, "Could not reach the server")
	c.act(t, "R", width, "#26 Benachrichtigung", "page 2 of 2")
	c.act(t, keyEsc, width, "up/down highlight")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	f.ResetAttempts()
	c = startDisk(t, inboxfixture.Temporary, width, url, home)
	openInboxA(t, c, width)
	c.act(t, "]", width, "#26 Benachrichtigung", "page 2 of 2")
	c.expect(t, "the failed reload", width, "Cached, possibly stale", "#26 Benachrichtigung")
	c.act(t, "R", width, "#26 Benachrichtigung", "page 2 of 2")
	c.noticeGone(t, width, "Cached", "#26 Benachrichtigung", "page 2 of 2")
	c.act(t, keyEsc, width, "up/down highlight")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	if err := terminalOutputError(c.bytes()); err != nil {
		t.Fatalf("%v\n%q", err, c.bytes())
	}
}

// A 401 ends the session and clears the profile's directory; the next run
// starts with /me and finds nothing to show from disk.
func TestPTYDiskCache401ClearsTheDirectory(t *testing.T) {
	width := uint16(120)
	f, url := fixtureServer(t, inboxfixture.Unauthorized)
	home := t.TempDir()
	c := startDisk(t, inboxfixture.Unauthorized, width, url, home)
	openInboxA(t, c, width)
	if n := len(cacheEntries(t, home)); n < 2 {
		t.Fatalf("%d entries before the 401", n)
	}
	c.act(t, "]", width, "Session expired. Restart lalabase.")
	if c.screenShows(width, "could not be cleared") {
		t.Fatal("the removal was reported as failed")
	}
	if err := c.until("the directory cleared", readyLimit, func() bool { return len(cacheEntries(t, home)) == 0 }); err != nil {
		t.Fatalf("%v: %v", err, cacheEntries(t, home))
	}
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	before := len(f.Requests())
	c = startDisk(t, inboxfixture.Unauthorized, width, url, home)
	c.expect(t, "the directory and the counters", width, "All organisations", "Nordlicht GmbH", "33", "30")
	c.moveTo(t, keyDown, width, "Nordlicht GmbH", "Inbox", 1)
	if err := c.send(keyEnter); err != nil {
		t.Fatal(err)
	}
	// Nothing comes from disk: the page shows only when the server
	// answered, and every frame before shows no cached rows.
	if err := c.until("the page from the server", readyLimit, func() bool {
		if c.screenShows(width, "Cached") {
			t.Fatalf("a page came from disk after the 401:\n%s", strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
		}
		return c.screenShows(width, "#1 Benachrichtigung", "page 1 of 2")
	}); err != nil {
		t.Fatal(err)
	}
	if n := countTool(f, "list_notifications", "") - countOf(f.Requests()[:before], "list_notifications"); n != 2 {
		t.Fatalf("%d inbox reads in the second run", n)
	}
	c.act(t, keyEsc, width, "up/down highlight")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
}

func countOf(reqs []inboxfixture.Request, tool string) int {
	n := 0
	for _, r := range reqs {
		if r.Tool == tool {
			n++
		}
	}
	return n
}

// A directory that cannot be used, and one another lalabase holds: memory
// alone, with one notice each, and the inbox loads as before.
func TestPTYDiskCacheFallsBackToMemory(t *testing.T) {
	width := uint16(120)
	t.Run("unusable", func(t *testing.T) {
		_, url := fixtureServer(t, inboxfixture.Normal)
		home := t.TempDir()
		// A file where the directory should be: nothing can be made there.
		if err := os.MkdirAll(filepath.Dir(cacheDir(home, "")), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cacheDir(home, ""), []byte("in the way"), 0o600); err != nil {
			t.Fatal(err)
		}
		c := startDisk(t, inboxfixture.Normal, width, url, home)
		c.expect(t, "the notice", width, "Disk cache unavailable; using memory only")
		openInboxA(t, c, width)
		if !c.screenShows(width, "Disk cache unavailable; using memory only") {
			t.Fatal("the notice went")
		}
		c.act(t, keyEsc, width, "up/down highlight")
		if err := c.send("q"); err != nil {
			t.Fatal(err)
		}
		c.finish(t, 0)
	})
	t.Run("busy", func(t *testing.T) {
		_, url := fixtureServer(t, inboxfixture.Normal)
		home := t.TempDir()
		first := startDisk(t, inboxfixture.Normal, width, url, home)
		first.expect(t, "the first run", width, "All organisations", "33")
		if first.screenShows(width, "Disk cache") {
			t.Fatal("the first run shows a disk notice")
		}
		second := startDisk(t, inboxfixture.Normal, width, url, home)
		second.expect(t, "the notice", width, "Disk cache in use by another lalabase; using memory only")
		openInboxA(t, second, width)
		second.act(t, keyEsc, width, "up/down highlight")
		if err := second.send("q"); err != nil {
			t.Fatal(err)
		}
		second.finish(t, 0)
		// The first lets go on quit; a third run gets the disk.
		if err := first.send("q"); err != nil {
			t.Fatal(err)
		}
		first.finish(t, 0)
		third := startDisk(t, inboxfixture.Normal, width, url, home)
		third.expect(t, "the third run", width, "All organisations", "33")
		if err := third.until("no disk notice", time.Second, func() bool { return !third.screenShows(width, "Disk cache") }); err != nil {
			t.Fatalf("%v\n%s", err, strings.Join(renderScreen(third.bytes(), 24, int(width)), "\n"))
		}
		if err := third.send("q"); err != nil {
			t.Fatal(err)
		}
		third.finish(t, 0)
	})
}
