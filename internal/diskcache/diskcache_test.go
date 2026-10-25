package diskcache

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/privatefs"
)

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

const within = 2 * time.Second

func ns(t *testing.T, profile, server, token string) cache.Namespace {
	t.Helper()
	n, ok := cache.Confirmed(profile, server, token)
	if !ok {
		t.Fatalf("not confirmed: %s %s %s", profile, server, token)
	}
	return n
}

func nsA(t *testing.T) cache.Namespace { return ns(t, "default", "https://a.example", "tok-1") }

func inboxKey(n cache.Namespace, org string, page int) cache.Key {
	k := n.Key("list_notifications")
	k.Organisation, k.Status, k.Page, k.PerPage = org, "unread", page, 25
	return k
}

func ticketKey(n cache.Namespace, project, id string) cache.Key {
	k := n.Key("get_ticket")
	k.Project, k.Ticket, k.PostsPage, k.PostsPerPage, k.Newest, k.AttachmentsPage = project, id, 1, 25, true, 1
	return k
}

func entry(payload string, at time.Time) cache.Entry {
	return cache.Entry{Payload: []byte(payload), LoadedAt: at}
}

func put(k cache.Key, e cache.Entry) cache.Change {
	return cache.Change{Kind: cache.ChangePut, Key: k, Entry: e}
}

// open opens the cache of "default" under a root of the test's own.
func open(t *testing.T, root string, now time.Time) *Cache {
	t.Helper()
	c, err := (Opener{Root: root}).Open("default", now)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func apply(t *testing.T, c *Cache, now time.Time, changes ...cache.Change) {
	t.Helper()
	if err := c.Apply(changes, now); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

// read is a hit with the payload, or a miss (""), never a failure.
func read(t *testing.T, c *Cache, k cache.Key, now time.Time) (string, bool) {
	t.Helper()
	e, ok, err := c.Read(k, now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(e.Payload), ok
}

func fileOf(c *Cache, k cache.Key) string { return filepath.Join(c.Dir(), nameOf(k)) }

// names lists the directory, for assertions on what is there.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestRootFollowsAnAbsoluteXDGCacheHomeOrTheHome(t *testing.T) {
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	if got := Root(env(map[string]string{"XDG_CACHE_HOME": "/tmp/x"}), "/home/u"); got != "/tmp/x/lalabase/cli" {
		t.Fatalf("absolute: %s", got)
	}
	// A relative XDG_CACHE_HOME is invalid by the specification: ignored.
	if got := Root(env(map[string]string{"XDG_CACHE_HOME": "cache"}), "/home/u"); got != "/home/u/.cache/lalabase/cli" {
		t.Fatalf("relative: %s", got)
	}
	if got := Root(env(nil), "/Users/u"); got != "/Users/u/.cache/lalabase/cli" {
		t.Fatalf("home: %s", got)
	}
	if got := Root(env(nil), ""); got != "" {
		t.Fatalf("no home: %q", got)
	}
}

// The profile's directory is the disk cache's and, below it, the launch
// files' (C7b): one rule for both.
func TestProfileDir(t *testing.T) {
	if got, err := ProfileDir("/home/u/.cache/lalabase/cli", "default"); err != nil || got != "/home/u/.cache/lalabase/cli/default" {
		t.Fatalf("%q, %v", got, err)
	}
	for _, c := range [][2]string{{"relative", "default"}, {"", "default"}, {"/c", ".."}, {"/c", "a/b"}, {"/c", ".hidden"}, {"/c", ""}} {
		if got, err := ProfileDir(c[0], c[1]); err == nil {
			t.Errorf("%q: %q passed", c, got)
		}
	}
}

// Nothing foreign becomes a path: a profile that could leave the root or
// hide, a root that is no absolute path.
func TestOpenRefusesAProfileOrRootThatCannotNameADirectory(t *testing.T) {
	root := t.TempDir()
	for _, profile := range []string{"", ".", "..", "../x", "a/b", ".hidden", "with space", strings.Repeat("a", 65), "nul\x00"} {
		if _, err := (Opener{Root: root}).Open(profile, t0); err == nil {
			t.Errorf("profile %q opened", profile)
		}
	}
	if _, err := (Opener{Root: "relative/root"}).Open("default", t0); err == nil {
		t.Error("a relative root opened")
	}
	if _, err := (Opener{}).Open("default", t0); err == nil {
		t.Error("no root opened")
	}
	if got := names(t, root); len(got) != 0 {
		t.Fatalf("a refused open made %v", got)
	}
	c := open(t, root, t0)
	if c.Dir() != filepath.Join(root, "default") {
		t.Fatalf("dir %s", c.Dir())
	}
	info, err := os.Stat(c.Dir())
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v %v", info.Mode(), err)
	}
}

// Plan test 4: an entry written by one run is read by the next as it was:
// the load time of the network read, not of the restart, and the stale
// mark.
func TestEntriesSurviveARestartWithTheirLoadTime(t *testing.T) {
	root := t.TempDir()
	k := inboxKey(nsA(t), "org-1", 1)
	c := open(t, root, t0)
	apply(t, c, t0, put(k, entry(`{"data":[1]}`, t0)))
	info, err := os.Stat(fileOf(c, k))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the entry's file: %v %v", info, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Read(k, t0); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
	later := t0.Add(time.Hour)
	again := open(t, root, later)
	e, ok, err := again.Read(k, later)
	if err != nil || !ok || string(e.Payload) != `{"data":[1]}` || !e.LoadedAt.Equal(t0) || e.Stale {
		t.Fatalf("after a restart: %+v %v %v", e, ok, err)
	}
	// A stale mark survives too.
	stale := entry(`{"data":[2]}`, t0)
	stale.Stale = true
	apply(t, again, later, put(k, stale))
	_ = again.Close()
	third := open(t, root, later)
	if e, ok, _ := third.Read(k, later); !ok || !e.Stale || string(e.Payload) != `{"data":[2]}` {
		t.Fatalf("the stale mark: %+v %v", e, ok)
	}
}

// Plan tests 2 and 3: the same ids under another profile, server or token
// are another entry; so is every option of the query.
func TestNamespaceAndEveryOptionIsolate(t *testing.T) {
	c := open(t, t.TempDir(), t0)
	base := ticketKey(nsA(t), "p-1", "t-1")
	base.Query, base.Organisation = "", ""
	apply(t, c, t0, put(base, entry(`"base"`, t0)))
	if got, ok := read(t, c, base, t0); !ok || got != `"base"` {
		t.Fatalf("the base entry: %q %v", got, ok)
	}
	variants := map[string]cache.Key{}
	for name, other := range map[string]cache.Namespace{
		"profile": ns(t, "work", "https://a.example", "tok-1"),
		"server":  ns(t, "default", "https://b.example", "tok-1"),
		"token":   ns(t, "default", "https://a.example", "tok-2"),
	} {
		k := other.Key(base.Operation)
		k.Project, k.Ticket, k.PostsPage, k.PostsPerPage, k.Newest, k.AttachmentsPage = base.Project, base.Ticket, base.PostsPage, base.PostsPerPage, base.Newest, base.AttachmentsPage
		variants[name] = k
	}
	set := func(name string, f func(*cache.Key)) { k := base; f(&k); variants[name] = k }
	set("operation", func(k *cache.Key) { k.Operation = "list_tickets"; k.Ticket = "" })
	set("project", func(k *cache.Key) { k.Project = "p-2" })
	set("ticket", func(k *cache.Key) { k.Ticket = "t-2" })
	set("posts page", func(k *cache.Key) { k.PostsPage = 2 })
	set("posts per page", func(k *cache.Key) { k.PostsPerPage = 10 })
	set("order", func(k *cache.Key) { k.Newest = false })
	set("attachments page", func(k *cache.Key) { k.AttachmentsPage = 2 })
	set("full text", func(k *cache.Key) { k.FullText = true })
	set("one comment", func(k *cache.Key) {
		k.PostsPage, k.PostsPerPage, k.Newest, k.AttachmentsPage, k.Post = 0, 0, false, 0, "c-1"
	})
	inbox := inboxKey(nsA(t), "org-1", 1)
	apply(t, c, t0, put(inbox, entry(`"inbox"`, t0)))
	set("organisation", func(k *cache.Key) { *k = inbox; k.Organisation = "org-2" })
	set("global", func(k *cache.Key) { *k = inbox; k.Organisation, k.Global = "", true })
	set("status", func(k *cache.Key) { *k = inbox; k.Status = "all" })
	set("page", func(k *cache.Key) { *k = inbox; k.Page = 2 })
	set("per page", func(k *cache.Key) { *k = inbox; k.PerPage = 50 })
	list := nsA(t).Key("list_tickets")
	list.Project, list.Page, list.PerPage = "p-1", 1, 25
	apply(t, c, t0, put(list, entry(`"list"`, t0)))
	set("query", func(k *cache.Key) { *k = list; k.Query = "login" })
	if len(variants) != 18 {
		t.Fatalf("%d variants", len(variants))
	}
	for name, k := range variants {
		if got, ok := read(t, c, k, t0); ok {
			t.Errorf("%s: sees another entry %q", name, got)
		}
	}
	// And every variant has a file of its own once written.
	for name, k := range variants {
		apply(t, c, t0, put(k, entry(`"`+name+`"`, t0)))
	}
	if c.Files() != len(variants)+3 {
		t.Fatalf("%d files for %d entries", c.Files(), len(variants)+3)
	}
	for name, k := range variants {
		if got, ok := read(t, c, k, t0); !ok || got != `"`+name+`"` {
			t.Errorf("%s: %q %v", name, got, ok)
		}
	}
	if got, ok := read(t, c, base, t0); !ok || got != `"base"` {
		t.Fatalf("the base entry after the variants: %q %v", got, ok)
	}
}

// Plan test 5, the age: below, at and above MaxAge since the network read;
// a hit, a restart and a failed refresh extend nothing; a time in the
// future is no time.
func TestTheAgeBoundCountsFromTheNetworkReadAlone(t *testing.T) {
	root := t.TempDir()
	k := inboxKey(nsA(t), "org-1", 1)
	c := open(t, root, t0)
	apply(t, c, t0, put(k, entry(`1`, t0)))
	for _, tc := range []struct {
		name string
		at   time.Duration
		hit  bool
	}{{"below", MaxAge - time.Second, true}, {"at", MaxAge, true}, {"above", MaxAge + time.Second, false}} {
		// A fresh file each time: the miss above removes it.
		apply(t, c, t0, put(k, entry(`1`, t0)))
		if _, ok := read(t, c, k, t0.Add(tc.at)); ok != tc.hit {
			t.Errorf("%s: hit %v", tc.name, ok)
		}
	}
	if _, err := os.Stat(fileOf(c, k)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the expired entry stayed")
	}
	// Hits in between extend nothing: read at 23 h, miss at 25 h.
	apply(t, c, t0, put(k, entry(`1`, t0)))
	if _, ok := read(t, c, k, t0.Add(23*time.Hour)); !ok {
		t.Fatal("no hit at 23 h")
	}
	if _, ok := read(t, c, k, t0.Add(25*time.Hour)); ok {
		t.Fatal("a hit extended the lifetime")
	}
	// A restart extends nothing: the file's own stamp is the network read.
	apply(t, c, t0, put(k, entry(`1`, t0)))
	_ = c.Close()
	late := open(t, root, t0.Add(25*time.Hour))
	if _, ok := read(t, late, k, t0.Add(25*time.Hour)); ok {
		t.Fatal("a restart extended the lifetime")
	}
	if late.Files() != 0 {
		t.Fatal("the open did not prune the expired entry")
	}
	// A failed refresh writes the stale mark with the old load time.
	apply(t, late, t0.Add(25*time.Hour), put(k, entry(`1`, t0.Add(2*time.Hour))))
	stale := entry(`1`, t0.Add(2*time.Hour))
	stale.Stale = true
	apply(t, late, t0.Add(25*time.Hour), put(k, stale))
	if e, ok := read(t, late, k, t0.Add(26*time.Hour)); !ok || e != `1` {
		t.Fatal("the stale entry at 24 h")
	}
	if _, ok := read(t, late, k, t0.Add(26*time.Hour+time.Second)); ok {
		t.Fatal("the stale mark extended the lifetime")
	}
	// A load time in the future keeps nothing fresh: never written, and
	// never read when it is on disk.
	apply(t, late, t0, put(k, entry(`1`, t0.Add(time.Hour))))
	if late.Files() != 0 {
		t.Fatal("an entry from the future was written")
	}
	writeRecord(t, late, k, record{Format: FormatVersion, Key: k.Record(), LoadedAt: t0.Add(time.Hour), Payload: []byte(`1`)})
	if _, ok := read(t, late, k, t0); ok {
		t.Fatal("an entry from the future was read")
	}
}

// writeRecord puts a record on disk by hand, under the key's name, and
// files it in the index (as the open would).
func writeRecord(t *testing.T, c *Cache, k cache.Key, r record) {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	writeRaw(t, c, k, data)
}

func writeRaw(t *testing.T, c *Cache, k cache.Key, data []byte) {
	t.Helper()
	path := fileOf(c, k)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.index[nameOf(k)] = fileMeta{size: int64(len(data)), mtime: t0}
	c.mu.Unlock()
}

// Plan test 5, the sizes: a payload of MaxEntryBytes is kept, one byte more
// is not, and then the older entry under the key is gone too; the directory
// holds at most MaxBytes and MaxFiles, the oldest entry going first.
func TestTheSizeBoundsHoldAndTheOldestGoesFirst(t *testing.T) {
	root := t.TempDir()
	c := open(t, root, t0)
	k := inboxKey(nsA(t), "org-1", 1)
	exact := []byte(`"` + strings.Repeat("a", MaxEntryBytes-2) + `"`)
	apply(t, c, t0, put(k, cache.Entry{Payload: exact, LoadedAt: t0}))
	if got, ok := read(t, c, k, t0); !ok || len(got) != MaxEntryBytes {
		t.Fatalf("a payload of MaxEntryBytes: %d %v", len(got), ok)
	}
	// One byte more, with a load time the age bound accepts (review round
	// 1: the earlier case was refused for its time, not its size).
	over := []byte(`"` + strings.Repeat("a", MaxEntryBytes-1) + `"`)
	apply(t, c, t0, put(k, cache.Entry{Payload: over, LoadedAt: t0}))
	// Never written in the first place: the read's own size check must
	// not be what keeps it out.
	if c.Files() != 0 || len(names(t, c.Dir())) != 1 {
		t.Fatalf("an oversized payload was written: %v", names(t, c.Dir()))
	}
	if _, ok := read(t, c, k, t0); ok {
		t.Fatal("the older entry came back")
	}
	// A record whose metadata takes it over MaxFileBytes is not kept either.
	long := k
	long.Query = strings.Repeat("q", 70<<10)
	apply(t, c, t0, put(long, cache.Entry{Payload: exact, LoadedAt: t0}))
	if c.Files() != 0 {
		t.Fatal("a record over MaxFileBytes was kept")
	}
	// MaxBytes: 2 MiB payloads until the directory is over 64 MiB; the
	// oldest leave first, by their load time, not by the order of writing.
	n := MaxBytes/MaxEntryBytes + 2
	for i := range n {
		at := t0.Add(time.Duration(n-i) * time.Minute) // the first written is the newest
		apply(t, c, t0.Add(time.Duration(n)*time.Minute), put(inboxKey(nsA(t), "org-1", i+1), cache.Entry{Payload: exact, LoadedAt: at}))
	}
	if c.Bytes() > MaxBytes || c.Files() >= n {
		t.Fatalf("%d bytes in %d files", c.Bytes(), c.Files())
	}
	now := t0.Add(time.Duration(n) * time.Minute)
	if _, ok := read(t, c, inboxKey(nsA(t), "org-1", 1), now); !ok {
		t.Fatal("the newest entry went")
	}
	if _, ok := read(t, c, inboxKey(nsA(t), "org-1", n), now); ok {
		t.Fatal("the oldest entry stayed")
	}
	// The sum is the sum of the files: what the directory holds.
	var onDisk int64
	for _, name := range names(t, c.Dir()) {
		if isEntryName(name) {
			info, err := os.Stat(filepath.Join(c.Dir(), name))
			if err != nil {
				t.Fatal(err)
			}
			onDisk += info.Size()
		}
	}
	if onDisk != c.Bytes() {
		t.Fatalf("index %d bytes, disk %d", c.Bytes(), onDisk)
	}
	_ = c.Close()
	// MaxFiles, with small entries, across a restart's prune too.
	root = t.TempDir()
	c = open(t, root, t0)
	var changes []cache.Change
	for i := range MaxFiles + 3 {
		changes = append(changes, put(inboxKey(nsA(t), "org-2", i+1), entry(`1`, t0.Add(time.Duration(i)*time.Second))))
	}
	apply(t, c, t0.Add(time.Hour), changes...)
	if c.Files() != MaxFiles {
		t.Fatalf("%d files", c.Files())
	}
	for _, page := range []int{1, 2, 3} {
		if _, ok := read(t, c, inboxKey(nsA(t), "org-2", page), t0.Add(time.Hour)); ok {
			t.Fatalf("page %d, one of the oldest, stayed", page)
		}
	}
	// A file added behind the index's back is counted by the next open.
	for i := range 3 {
		writeRaw(t, c, inboxKey(nsA(t), "org-3", i+1), []byte(`{}`))
	}
	_ = c.Close()
	c = open(t, root, t0.Add(time.Hour))
	if c.Files() != MaxFiles {
		t.Fatalf("after a restart: %d files", c.Files())
	}
}

// Plan test 6: a file is an entry only when everything in it checks out;
// a name alone, or JSON that decodes, is not enough. Each bad file is a
// miss and goes.
func TestABadFileIsAMissAndGoes(t *testing.T) {
	c := open(t, t.TempDir(), t0)
	n := nsA(t)
	k := ticketKey(n, "p-1", "t-1")
	good := record{Format: FormatVersion, Key: k.Record(), LoadedAt: t0, Payload: []byte(`{"id":"t-1"}`)}
	cases := map[string]func() []byte{
		"broken JSON":     func() []byte { return []byte(`{"format":1,`) },
		"unknown version": func() []byte { r := good; r.Format = 2; return marshal(t, r) },
		"version zero":    func() []byte { r := good; r.Format = 0; return marshal(t, r) },
		"no key":          func() []byte { return []byte(`{"format":1,"loaded_at":"2026-10-10T12:00:00Z","payload":{}}`) },
		"no load time":    func() []byte { return []byte(`{"format":1,"key":` + string(marshal(t, k.Record())) + `,"payload":{}}`) },
		"no payload": func() []byte {
			return []byte(`{"format":1,"key":` + string(marshal(t, k.Record())) + `,"loaded_at":"2026-10-10T12:00:00Z"}`)
		},
		"payload no JSON": func() []byte {
			return []byte(`{"format":1,"key":` + string(marshal(t, k.Record())) + `,"loaded_at":"2026-10-10T12:00:00Z","payload":{`)
		},
		"another ticket":  func() []byte { r := good; r.Key.Ticket = "t-2"; return marshal(t, r) },
		"another project": func() []byte { r := good; r.Key.Project = "p-2"; return marshal(t, r) },
		"another page":    func() []byte { r := good; r.Key.PostsPage = 2; return marshal(t, r) },
		"one comment":     func() []byte { r := good; r.Key.Post = "c-1"; return marshal(t, r) },
		"another token":   func() []byte { r := good; r.Key.TokenID = "tok-2"; return marshal(t, r) },
		"another profile": func() []byte { r := good; r.Key.Profile = "work"; return marshal(t, r) },
		"another server":  func() []byte { r := good; r.Key.Server = "https://b.example"; return marshal(t, r) },
		"no identity":     func() []byte { r := good; r.Key.TokenID = ""; return marshal(t, r) },
		"oversized payload": func() []byte {
			return []byte(`{"format":1,"key":` + string(marshal(t, k.Record())) + `,"loaded_at":"2026-10-10T12:00:00Z","payload":"` + strings.Repeat("a", MaxEntryBytes-1) + `"}`)
		},
		"oversized file": func() []byte {
			return append([]byte(`{"format":1,"pad":"`), append(bytes.Repeat([]byte("a"), MaxFileBytes), []byte(`"}`)...)...)
		},
	}
	for name, broken := range cases {
		writeRaw(t, c, k, broken())
		if got, ok := read(t, c, k, t0); ok {
			t.Errorf("%s: read %q", name, got)
		}
		if _, err := os.Stat(fileOf(c, k)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: the file stayed", name)
		}
		if c.Files() != 0 {
			t.Errorf("%s: still in the index", name)
		}
	}
	// The good record reads.
	writeRaw(t, c, k, marshal(t, good))
	if got, ok := read(t, c, k, t0); !ok || got != `{"id":"t-1"}` {
		t.Fatalf("the good record: %q %v", got, ok)
	}
	// A file of another key under this key's name: the name proves nothing.
	other := ticketKey(n, "p-1", "t-2")
	apply(t, c, t0, put(other, entry(`{"id":"t-2"}`, t0)))
	if err := os.Rename(fileOf(c, other), fileOf(c, k)); err != nil {
		t.Fatal(err)
	}
	if got, ok := read(t, c, k, t0); ok {
		t.Fatalf("a renamed file was read: %q", got)
	}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Plan test 7: a link, a FIFO, a file of another user or one open to others
// in an entry's place is no entry; none of them blocks.
func TestASpecialOrForeignFileInAnEntrysPlaceIsRefused(t *testing.T) {
	c := open(t, t.TempDir(), t0)
	k := inboxKey(nsA(t), "org-1", 1)
	good := marshal(t, record{Format: FormatVersion, Key: k.Record(), LoadedAt: t0, Payload: []byte(`1`)})
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(elsewhere, good, 0o600); err != nil {
		t.Fatal(err)
	}
	type setup func(path string)
	for name, prepare := range map[string]setup{
		"a link to a good record": func(path string) {
			if err := os.Symlink(elsewhere, path); err != nil {
				t.Fatal(err)
			}
		},
		"a FIFO": func(path string) {
			if err := mkfifo(path); err != nil {
				t.Skip("no FIFO here:", err)
			}
		},
		"a directory": func(path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"open to the group": func(path string) {
			if err := os.WriteFile(path, good, 0o640); err != nil { //nolint:gosec // the test opens it on purpose
				t.Fatal(err)
			}
		},
		"open to others": func(path string) {
			if err := os.WriteFile(path, good, 0o604); err != nil { //nolint:gosec // the test opens it on purpose
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := fileOf(c, k)
			_ = os.RemoveAll(path)
			prepare(path)
			c.mu.Lock()
			c.index[nameOf(k)] = fileMeta{mtime: t0}
			c.mu.Unlock()
			done := make(chan bool, 1)
			go func() { _, ok := read(t, c, k, t0); done <- ok }()
			select {
			case ok := <-done:
				if ok {
					t.Fatal("read as an entry")
				}
			case <-time.After(within):
				t.Fatal("the read blocked")
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("still there: %v", err)
			}
			if _, err := os.Stat(elsewhere); err != nil {
				t.Fatal("the link's target was removed")
			}
		})
	}
	t.Run("of another user", func(t *testing.T) {
		writeRaw(t, c, k, good)
		me := privatefs.CurrentUID
		privatefs.CurrentUID = func() int { return me() + 1 }
		t.Cleanup(func() { privatefs.CurrentUID = me })
		if _, ok := read(t, c, k, t0); ok {
			t.Fatal("a file of another user was read")
		}
	})
	// The same shapes are swept by the next open, never followed.
	root := t.TempDir()
	first := open(t, root, t0)
	path := fileOf(first, k)
	_ = first.Close()
	if err := os.Symlink(elsewhere, path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), ".tmp-left"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := open(t, root, t0)
	got := names(t, second.Dir())
	if len(got) != 2 || got[0] != "lock" || got[1] != "notes.txt" {
		t.Fatalf("after the open: %v", got)
	}
	if _, err := os.Stat(elsewhere); err != nil {
		t.Fatal("the link's target was removed")
	}
	if second.Files() != 0 {
		t.Fatal("a foreign file was counted")
	}
}

// Plan test 7: the way to the directory is checked like the config's.
func TestAnUnsafeDirectoryChainIsRefused(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(filepath.Join(root, "default"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, filepath.Join(root, "default")} {
		if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // the test opens it on purpose
			t.Fatal(err)
		}
		_, err := (Opener{Root: root}).Open("default", t0)
		if err == nil || !strings.Contains(err.Error(), "chmod go-w") {
			t.Fatalf("%s open to others: %v", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The rights are never changed by the client, neither to refuse nor to
	// accept: a directory others may list but not change passes the one
	// model (the files in it are 0600, their names say nothing) and stays as
	// it is.
	if err := os.Chmod(filepath.Join(root, "default"), 0o755); err != nil {
		t.Fatal(err)
	}
	listed, err := (Opener{Root: root}).Open("default", t0)
	if err != nil {
		t.Fatalf("a directory others may list: %v", err)
	}
	_ = listed.Close()
	info, err := os.Stat(filepath.Join(root, "default"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("the rights were changed: %v", info.Mode())
	}
	if err := os.Chmod(filepath.Join(root, "default"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A directory of another user, however its bits read.
	me := privatefs.CurrentUID
	privatefs.CurrentUID = func() int { return me() + 1 }
	_, err = (Opener{Root: root}).Open("default", t0)
	privatefs.CurrentUID = me
	if err == nil || !strings.Contains(err.Error(), "another user") {
		t.Fatalf("a directory of another user: %v", err)
	}
	// Once the way is right it opens, and the lock file is private.
	c := open(t, root, t0)
	info, err = os.Stat(filepath.Join(c.Dir(), "lock"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the lock file: %v %v", info, err)
	}
}

// Plan test 8: a failure while writing or replacing leaves the old entry
// whole and no temporary file; the failure is reported.
func TestAFailedWriteLeavesTheOldEntryWhole(t *testing.T) {
	k := inboxKey(nsA(t), "org-1", 1)
	for _, step := range []string{"create", "write", "sync", "close", "chtimes", "rename"} {
		t.Run(step, func(t *testing.T) {
			fail := ""
			o := Opener{Root: t.TempDir(), Faults: func(s, _ string) error {
				if s == fail {
					return errors.New("injected " + s)
				}
				return nil
			}}
			c, err := o.Open("default", t0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			apply(t, c, t0, put(k, entry(`"old"`, t0)))
			fail = step
			err = c.Apply([]cache.Change{put(k, entry(`"new"`, t0.Add(time.Minute)))}, t0.Add(time.Minute))
			if err == nil || !strings.Contains(err.Error(), "injected "+step) {
				t.Fatalf("got %v", err)
			}
			fail = ""
			if got, ok := read(t, c, k, t0.Add(time.Minute)); !ok || got != `"old"` {
				t.Fatalf("the old entry: %q %v", got, ok)
			}
			if got := names(t, c.Dir()); len(got) != 2 {
				t.Fatalf("a temporary file was left: %v", got)
			}
			if c.Files() != 1 {
				t.Fatalf("index %d", c.Files())
			}
		})
	}
	// The second change of a batch fails: the first stays applied, the
	// rest is not, and the error says so.
	calls := 0
	o := Opener{Root: t.TempDir(), Faults: func(s, _ string) error {
		if s == "rename" {
			if calls++; calls == 2 {
				return errors.New("injected rename")
			}
		}
		return nil
	}}
	c, err := o.Open("default", t0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	k2, k3 := inboxKey(nsA(t), "org-1", 2), inboxKey(nsA(t), "org-1", 3)
	err = c.Apply([]cache.Change{put(k, entry(`1`, t0)), put(k2, entry(`2`, t0)), put(k3, entry(`3`, t0))}, t0)
	if err == nil {
		t.Fatal("no error")
	}
	if _, ok := read(t, c, k, t0); !ok {
		t.Fatal("the first change was lost")
	}
	if _, ok := read(t, c, k3, t0); ok {
		t.Fatal("a change after the failure was applied")
	}
}

// A temporary file a crash left behind is no entry: never read, removed by
// the next open. The entry's own name is the only one read.
func TestALeftoverTemporaryFileIsNeverAnEntry(t *testing.T) {
	root := t.TempDir()
	c := open(t, root, t0)
	k := inboxKey(nsA(t), "org-1", 1)
	good := marshal(t, record{Format: FormatVersion, Key: k.Record(), LoadedAt: t0, Payload: []byte(`1`)})
	if err := os.WriteFile(filepath.Join(c.Dir(), ".tmp-123456"), good, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := read(t, c, k, t0); ok {
		t.Fatal("a temporary file was read")
	}
	_ = c.Close()
	again := open(t, root, t0)
	if got := names(t, again.Dir()); len(got) != 1 || got[0] != "lock" {
		t.Fatalf("after the open: %v", got)
	}
}

// The journal's changes reach the disk as the memory applied them: a
// forget takes the key, a removal every variant of the resource and
// nothing else, the namespace's removal everything of the identity and
// nothing of another.
func TestChangesReplayByKeyResourceAndNamespace(t *testing.T) {
	c := open(t, t.TempDir(), t0)
	a, b := nsA(t), ns(t, "default", "https://a.example", "tok-2")
	t1, t1p2, t1c := ticketKey(a, "p-1", "t-1"), ticketKey(a, "p-1", "t-1"), ticketKey(a, "p-1", "t-1")
	t1p2.PostsPage = 2
	t1c.PostsPage, t1c.PostsPerPage, t1c.Newest, t1c.AttachmentsPage, t1c.Post = 0, 0, false, 0, "c-1"
	t2 := ticketKey(a, "p-1", "t-2")
	list1, list2 := a.Key("list_tickets"), a.Key("list_tickets")
	list1.Project, list1.Page, list1.PerPage = "p-1", 1, 25
	list2.Project, list2.Page, list2.PerPage, list2.Query = "p-1", 2, 25, "x"
	other := ticketKey(b, "p-1", "t-1")
	all := []cache.Key{t1, t1p2, t1c, t2, list1, list2, other}
	var puts []cache.Change
	for i, k := range all {
		puts = append(puts, put(k, entry(fmt.Sprintf(`%d`, i), t0)))
	}
	apply(t, c, t0, puts...)
	if c.Files() != len(all) {
		t.Fatalf("%d files", c.Files())
	}
	present := func(want map[cache.Key]bool) {
		t.Helper()
		for _, k := range all {
			if _, ok := read(t, c, k, t0); ok != want[k] {
				t.Errorf("%+v: present %v", k.Record(), ok)
			}
		}
	}
	apply(t, c, t0, cache.Change{Kind: cache.ChangeForget, Key: t1p2})
	present(map[cache.Key]bool{t1: true, t1c: true, t2: true, list1: true, list2: true, other: true})
	apply(t, c, t0, cache.Change{Kind: cache.ChangeRemoveResource, Key: t1c.Resource()})
	present(map[cache.Key]bool{t2: true, list1: true, list2: true, other: true})
	apply(t, c, t0, cache.Change{Kind: cache.ChangeRemoveResource, Key: list2.Resource()})
	present(map[cache.Key]bool{t2: true, other: true})
	apply(t, c, t0, cache.Change{Kind: cache.ChangeRemoveNamespace, Key: a.Key("")})
	present(map[cache.Key]bool{other: true})
	if c.Files() != 1 {
		t.Fatalf("%d files after the namespace's removal", c.Files())
	}
}

// A failing file system is reported, a bad file is not: the caller tells
// the two apart by the error alone.
func TestAFailingFileSystemIsAnErrorABadFileIsAMiss(t *testing.T) {
	fail := ""
	o := Opener{Root: t.TempDir(), Faults: func(s, _ string) error {
		if s == fail {
			return errors.New("injected " + s)
		}
		return nil
	}}
	c, err := o.Open("default", t0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	k := inboxKey(nsA(t), "org-1", 1)
	apply(t, c, t0, put(k, entry(`1`, t0)))
	fail = "open"
	if _, _, err := c.Read(k, t0); err == nil || !strings.Contains(err.Error(), "injected open") {
		t.Fatalf("a failing read: %v", err)
	}
	fail = "remove"
	if err := c.Apply([]cache.Change{{Kind: cache.ChangeForget, Key: k}}, t0); err == nil {
		t.Fatal("a failing removal was no error")
	}
	writeRaw(t, c, k, []byte(`{"format":1,`))
	if _, _, err := c.Read(k, t0); err == nil {
		t.Fatal("a bad file that cannot be removed was no error")
	}
	fail = ""
	if _, ok, err := c.Read(k, t0); ok || err != nil {
		t.Fatalf("a bad file: %v %v", ok, err)
	}
	// A directory that cannot be read is no cache, and a failed open holds
	// no lock: the next open gets it.
	_ = c.Close()
	fail = "readdir"
	if _, err := o.Open("default", t0); err == nil || !strings.Contains(err.Error(), "injected readdir") {
		t.Fatalf("open with a failing directory: %v", err)
	}
	fail = ""
	again, err := o.Open("default", t0)
	if err != nil {
		t.Fatalf("after the failed open: %v", err)
	}
	_ = again.Close()
}

// A second cache on the same profile in the same process is refused like a
// second process (flock conflicts between descriptors): the first stays
// usable, the second says why.
func TestASecondOpenOfTheSameProfileIsBusy(t *testing.T) {
	root := t.TempDir()
	first := open(t, root, t0)
	done := make(chan error, 1)
	go func() {
		_, err := (Opener{Root: root}).Open("default", t0)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(within):
		t.Fatal("the second open waited for the lock")
	}
	// Another profile is another directory and another lock.
	other, err := (Opener{Root: root}).Open("work", t0)
	if err != nil {
		t.Fatalf("another profile: %v", err)
	}
	_ = other.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := (Opener{Root: root}).Open("default", t0)
	if err != nil {
		t.Fatalf("after the close: %v", err)
	}
	_ = again.Close()
}

// A file stamped in the future is expired on open too (review round 1,
// probe M6): it is removed, and it is not the "newest" that size pruning
// keeps.
func TestAFutureStampIsExpiredOnOpen(t *testing.T) {
	root := t.TempDir()
	c := open(t, root, t0)
	k, k2 := inboxKey(nsA(t), "org-1", 1), inboxKey(nsA(t), "org-1", 2)
	apply(t, c, t0, put(k, entry(`1`, t0)), put(k2, entry(`2`, t0)))
	_ = c.Close()
	future := t0.Add(time.Hour)
	if err := os.Chtimes(fileOf(c, k), future, future); err != nil {
		t.Fatal(err)
	}
	again := open(t, root, t0)
	if again.Files() != 1 {
		t.Fatalf("%d files after the open", again.Files())
	}
	if _, ok := read(t, again, k, t0); ok {
		t.Fatal("the file from the future stayed")
	}
	if _, ok := read(t, again, k2, t0); !ok {
		t.Fatal("the good file went")
	}
}

// Close never waits for a batch (review round 1, finding 2): the model
// closes from its update step, and a slow write must not hold the person's
// q. The running batch stops before its next change and lets the lock go
// on its way out; the next open gets it.
func TestCloseNeverWaitsForABatch(t *testing.T) {
	root := t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	o := Opener{Root: root, Faults: func(step, _ string) error {
		if step == "sync" {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		}
		return nil
	}}
	c, err := o.Open("default", t0)
	if err != nil {
		t.Fatal(err)
	}
	k1, k2, k3 := inboxKey(nsA(t), "org-1", 1), inboxKey(nsA(t), "org-1", 2), inboxKey(nsA(t), "org-1", 3)
	applied := make(chan error, 1)
	go func() {
		applied <- c.Apply([]cache.Change{put(k1, entry(`1`, t0)), put(k2, entry(`2`, t0)), put(k3, entry(`3`, t0))}, t0)
	}()
	select {
	case <-entered:
	case <-time.After(within):
		t.Fatal("the batch did not start")
	}
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close waited for the batch")
	}
	// The lock is still the batch's until it returns.
	if _, err := (Opener{Root: root}).Open("default", t0); !errors.Is(err, ErrBusy) {
		t.Fatalf("open while the batch holds the lock: %v", err)
	}
	close(release)
	select {
	case err := <-applied:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("the batch ended with %v, want ErrClosed", err)
		}
	case <-time.After(within):
		t.Fatal("the batch did not return")
	}
	// The first change landed, the rest did not; the lock is free.
	again, err := (Opener{Root: root}).Open("default", t0)
	if err != nil {
		t.Fatalf("open after the batch: %v", err)
	}
	defer func() { _ = again.Close() }()
	if _, ok := read(t, again, k1, t0); !ok {
		t.Fatal("the first change was lost")
	}
	if _, ok := read(t, again, k2, t0); ok {
		t.Fatal("a change after the close was applied")
	}
	if got := names(t, again.Dir()); len(got) != 2 {
		t.Fatalf("a temporary file was left: %v", got)
	}
}
