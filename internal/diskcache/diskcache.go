// Package diskcache keeps the memory cache's entries on disk between runs
// (C1e). It takes no decision of its own about data: it replays the
// journal of the memory store (internal/cache, Journal), change by change
// and in order, and answers a read with an entry only after every check
// below passed.
//
// Where: <root>/<profile>/, root being $XDG_CACHE_HOME/lalabase/cli when that
// is an absolute path, else ~/.cache/lalabase/cli (on macOS too). The
// directory is the user's alone (0700, privatefs.CheckDirs on the whole
// way), the files are 0600. It is opened only after a successful /me: the
// identity a file is filed under (profile, normalised server, the token id
// the server confirmed) is in the file and in its name, never a token or a
// fingerprint of one.
//
// One lalabase at a time: Open takes an exclusive lock on <dir>/lock and
// never waits for it. A second lalabase on the same profile gets ErrBusy
// and runs on memory alone. With one writer the disk is a replay of the
// memory's decisions; atomic renames alone would not stop a second client
// from writing back what the first one removed.
//
// A file is one entry: a JSON record with the format version, the key with
// its identity field by field, the time of the last successful network
// read, the stale mark and the answer's payload. It is written to a unique
// temporary file in the same directory, synced, closed, stamped with its
// load time and renamed over the old one: a reader sees the old entry or the
// new one, never half of either. A temporary file left behind by a crash
// never matches an entry's name and is removed on the next open.
//
// Bounds: MaxBytes per profile directory, MaxAge since the last network
// read (a hit, a promotion into memory or a failed refresh extends nothing),
// MaxEntryBytes per payload (the memory's own limit), MaxFileBytes per file
// with its metadata, MaxFiles per directory. Pruning runs on open and when a
// write crosses a bound, never on a timer, in this order: temporary files
// and names of ours that are no regular file; entries older than MaxAge or
// stamped in the future; then, while the directory is over MaxBytes or
// MaxFiles, the oldest entry first. The directory walk on open is bounded
// (maxWalk); a directory beyond it is refused as unusable.
//
// A read checks the file on the opened descriptor (safefile: a regular
// file, opened without blocking, no link in its place, at most
// MaxFileBytes), its owner and its mode, the format version, that the key
// in the record is exactly the key asked for (a name alone proves nothing),
// the age, and that the payload is JSON of at most MaxEntryBytes. Anything
// else is a miss and the file goes. Whether the payload answers the
// question stays the adapter's check (internal/api, the same Spec as for a
// network answer), run by the model after the entry was read; a payload
// that fails it is forgotten through the journal like any other.
//
// A failing file system (a write, a removal, a read that is no plain miss)
// is reported to the caller, who stops using the disk for the session and
// says so once; a single bad file is not.
package diskcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/privatefs"
	"github.com/gboyke/lalabase-cli/internal/safefile"
)

// The bounds.
const (
	FormatVersion = 1
	MaxBytes      = 64 << 20
	MaxAge        = 24 * time.Hour
	MaxEntryBytes = cache.MaxEntryBytes
	// MaxFileBytes is a file with its metadata: the record around the
	// payload (the key, with a search of the person's typing in it).
	MaxFileBytes = MaxEntryBytes + 64<<10
	MaxFiles     = 1024
	// maxWalk bounds the directory entries visited on open.
	maxWalk = 8192
)

// The pieces of a name: an entry is e-<identity>-<resource>-<key>.json, each
// part a hash of the record, so a removal of a resource or of an identity is
// a prefix, not a read of every file.
const (
	entryPrefix = "e-"
	entrySuffix = ".json"
	tempPrefix  = ".tmp-"
	lockName    = "lock"
	hashLen     = 32
)

var (
	// ErrBusy: another lalabase holds the profile's disk cache.
	ErrBusy = errors.New("the disk cache of this profile is in use by another lalabase")
	// ErrClosed: the cache was closed.
	ErrClosed = errors.New("the disk cache is closed")
	// errWalk: more entries in the directory than the open reads.
	errWalk = errors.New("the cache directory holds too many files")
)

// profileForm is a profile name that may be a directory's name: no path
// separator, no leading dot, no "..".
var profileForm = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Root is the cache root of this user: $XDG_CACHE_HOME/lalabase/cli when
// XDG_CACHE_HOME names an absolute path (a relative one is ignored, as the
// specification says), else ~/.cache/lalabase/cli. Empty without a home.
func Root(getenv func(string) string, home string) string {
	if dir := getenv("XDG_CACHE_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "lalabase", "cli")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "lalabase", "cli")
}

// Opener opens the disk cache of a profile under Root.
type Opener struct {
	Root string
	// Faults, when set, is asked before every step on the file system
	// (tests): the error it returns is the step's.
	Faults func(step, path string) error
}

// Cache is the open disk cache of one profile. Apply and Read serialise
// on mu; Close never waits for them: it marks the cache closed and lets
// the lock go itself when nothing is running, else the running batch lets
// it go on its way out (the model closes from its update step, which must
// not wait for a slow write).
type Cache struct {
	dir    string
	faults func(step, path string) error

	// state guards closed; unlock is released once, by Close or by the
	// batch that was running when Close came.
	state   sync.Mutex
	closed  bool
	release sync.Once
	unlock  func()

	mu sync.Mutex
	// index is what the directory holds of ours: name, size, load time.
	index map[string]fileMeta
	bytes int64
}

type fileMeta struct {
	size  int64
	mtime time.Time
}

// record is the file's content.
type record struct {
	Format   int             `json:"format"`
	Key      cache.KeyRecord `json:"key"`
	LoadedAt time.Time       `json:"loaded_at"`
	Stale    bool            `json:"stale"`
	Payload  json.RawMessage `json:"payload"`
}

// Open opens the cache of a profile: the directory made and checked, the
// lock taken without waiting, the directory pruned. now is the clock of
// the pruning. Nothing is read or written before this, so a session that
// never confirmed its identity touches no disk.
func (o Opener) Open(profile string, now time.Time) (*Cache, error) {
	if !filepath.IsAbs(o.Root) {
		return nil, errors.New("no cache directory: the root is not an absolute path")
	}
	if !profileForm.MatchString(profile) {
		return nil, fmt.Errorf("profile %q cannot name a cache directory", profile)
	}
	c := &Cache{dir: filepath.Join(o.Root, profile), faults: o.Faults, index: map[string]fileMeta{}}
	if err := c.step("mkdir", c.dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return nil, err
	}
	if err := c.step("checkdirs", c.dir); err != nil {
		return nil, err
	}
	if err := privatefs.CheckDirs(c.dir); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(c.dir, lockName)
	if err := c.step("lock", lockPath); err != nil {
		return nil, err
	}
	unlock, err := privatefs.TryLock(lockPath)
	if errors.Is(err, privatefs.ErrLocked) {
		return nil, ErrBusy
	}
	if err != nil {
		return nil, err
	}
	c.unlock = unlock
	if err := c.prune(now); err != nil {
		unlock()
		return nil, err
	}
	return c, nil
}

// Dir is the profile's cache directory.
func (c *Cache) Dir() string { return c.dir }

// Files and Bytes report what the index holds (tests, limits).
func (c *Cache) Files() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.index)
}

func (c *Cache) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// Close marks the cache closed and lets the lock go: at once when no
// batch runs, else as soon as the running batch returns (it stops before
// its next change). It never waits. Every call after it fails with
// ErrClosed.
func (c *Cache) Close() error {
	c.state.Lock()
	c.closed = true
	c.state.Unlock()
	if c.mu.TryLock() {
		c.letGo()
		c.mu.Unlock()
	}
	return nil
}

// isClosed says whether Close was called.
func (c *Cache) isClosed() bool {
	c.state.Lock()
	defer c.state.Unlock()
	return c.closed
}

// letGo releases the lock once.
func (c *Cache) letGo() { c.release.Do(c.unlock) }

// enter takes mu for an Apply or a Read, unless the cache is closed; leave
// gives it back and, when Close came meanwhile, lets the lock go.
func (c *Cache) enter() bool {
	c.mu.Lock()
	if c.isClosed() {
		c.letGo()
		c.mu.Unlock()
		return false
	}
	return true
}

func (c *Cache) leave() {
	if c.isClosed() {
		c.letGo()
	}
	c.mu.Unlock()
}

// step asks the injected faults, if any.
func (c *Cache) step(name, path string) error {
	if c.faults == nil {
		return nil
	}
	return c.faults(name, path)
}

// --- names -------------------------------------------------------------------

func hashOf(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:hashLen]
}

type identity struct {
	Profile, Server, TokenID string
}

func namespaceHash(ns cache.Namespace) string {
	return hashOf(identity{Profile: ns.Profile(), Server: ns.Server(), TokenID: ns.TokenID()})
}

// namespacePrefix is the start of every name of an identity.
func namespacePrefix(ns cache.Namespace) string { return entryPrefix + namespaceHash(ns) + "-" }

// resourcePrefix is the start of every name of a resource.
func resourcePrefix(res cache.Key) string {
	return namespacePrefix(res.Namespace()) + hashOf(res.Resource().Record()) + "-"
}

// nameOf is the file of one key.
func nameOf(k cache.Key) string { return resourcePrefix(k) + hashOf(k.Record()) + entrySuffix }

// isEntryName says whether a name has the form of ours.
func isEntryName(name string) bool {
	if !strings.HasPrefix(name, entryPrefix) || !strings.HasSuffix(name, entrySuffix) {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, entryPrefix), entrySuffix), "-")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if len(p) != hashLen {
			return false
		}
		for _, r := range p {
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				return false
			}
		}
	}
	return true
}

// --- pruning -----------------------------------------------------------------

// prune builds the index from the directory and removes what the bounds
// say: temporary files and names of ours that are no regular file, then
// expired entries, then the oldest while over MaxBytes or MaxFiles.
func (c *Cache) prune(now time.Time) error {
	if err := c.step("readdir", c.dir); err != nil {
		return err
	}
	d, err := os.Open(c.dir) //nolint:gosec // the cache directory, checked by privatefs
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	visited := 0
	for {
		entries, err := d.ReadDir(256)
		for _, e := range entries {
			if visited++; visited > maxWalk {
				return errWalk
			}
			if err := c.index1(e); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	for name, m := range c.index {
		if expired(m.mtime, now) {
			if err := c.removeEntry(name); err != nil {
				return err
			}
		}
	}
	return c.enforce()
}

// index1 files one directory entry, or removes it.
func (c *Cache) index1(e fs.DirEntry) error {
	name := e.Name()
	path := filepath.Join(c.dir, name)
	switch {
	case strings.HasPrefix(name, tempPrefix):
		return c.remove(path)
	case !isEntryName(name):
		return nil // not ours: left alone, not counted
	}
	if err := c.step("lstat", path); err != nil {
		return err
	}
	info, err := e.Info()
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		// A name of ours that is no file of ours: removed by its name,
		// never followed.
		return c.remove(path)
	}
	c.index[name] = fileMeta{size: info.Size(), mtime: info.ModTime()}
	c.bytes += info.Size()
	return nil
}

// expired: older than MaxAge, or stamped in the future (an implausible
// time keeps nothing fresh).
func expired(loadedAt, now time.Time) bool {
	age := now.Sub(loadedAt)
	return age < 0 || age > MaxAge
}

// enforce removes the oldest entries while the directory is over a bound.
func (c *Cache) enforce() error {
	for len(c.index) > MaxFiles || c.bytes > MaxBytes {
		oldest, found := "", false
		var at time.Time
		for name, m := range c.index {
			if !found || m.mtime.Before(at) || (m.mtime.Equal(at) && name < oldest) {
				oldest, at, found = name, m.mtime, true
			}
		}
		if !found {
			return nil
		}
		if err := c.removeEntry(oldest); err != nil {
			return err
		}
	}
	return nil
}

// removeEntry removes a file of the index, by name.
func (c *Cache) removeEntry(name string) error {
	if err := c.remove(filepath.Join(c.dir, name)); err != nil {
		return err
	}
	if m, ok := c.index[name]; ok {
		c.bytes -= m.size
		delete(c.index, name)
	}
	return nil
}

// remove removes a path; a path already gone is no error.
func (c *Cache) remove(path string) error {
	if err := c.step("remove", path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// --- the journal's replay ----------------------------------------------------

// Apply carries out changes in order. The first failure of the file system
// stops the batch and is returned: the changes after it were not applied.
// An entry too large for the bounds is not written, and what the key held
// before goes, so nothing older comes back under it.
func (c *Cache) Apply(changes []cache.Change, now time.Time) error {
	if !c.enter() {
		return ErrClosed
	}
	defer c.leave()
	for _, ch := range changes {
		if c.isClosed() {
			// Close came while the batch ran: nothing more is written, the
			// lock goes with leave.
			return ErrClosed
		}
		var err error
		switch ch.Kind {
		case cache.ChangePut:
			err = c.put(ch.Key, ch.Entry, now)
		case cache.ChangeForget:
			err = c.removeEntry(nameOf(ch.Key))
		case cache.ChangeRemoveResource:
			err = c.removePrefix(resourcePrefix(ch.Key))
		case cache.ChangeRemoveNamespace:
			err = c.removePrefix(namespacePrefix(ch.Key.Namespace()))
		default:
			err = fmt.Errorf("disk cache: unknown change %d", ch.Kind)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache) removePrefix(prefix string) error {
	var names []string
	for name := range c.index {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		if err := c.removeEntry(name); err != nil {
			return err
		}
	}
	return nil
}

// put writes one entry: to a unique temporary file in the directory,
// synced, closed, stamped with its load time, renamed over the old one.
func (c *Cache) put(k cache.Key, e cache.Entry, now time.Time) error {
	name := nameOf(k)
	if e.LoadedAt.IsZero() || expired(e.LoadedAt, now) || len(e.Payload) > MaxEntryBytes || !json.Valid(e.Payload) {
		// Not kept; what the key held before is outdated by it.
		return c.removeEntry(name)
	}
	data, err := json.Marshal(record{Format: FormatVersion, Key: k.Record(), LoadedAt: e.LoadedAt.UTC(), Stale: e.Stale, Payload: e.Payload})
	if err != nil || len(data) > MaxFileBytes {
		return c.removeEntry(name)
	}
	if err := c.step("create", c.dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, tempPrefix+"*") // O_EXCL, mode 0600
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()
	if err := c.write(tmp, tmpName, data); err != nil {
		return err
	}
	if err := c.step("chtimes", tmpName); err != nil {
		return err
	}
	if err := os.Chtimes(tmpName, e.LoadedAt, e.LoadedAt); err != nil {
		return err
	}
	path := filepath.Join(c.dir, name)
	if err := c.step("rename", path); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	keep = true
	if m, ok := c.index[name]; ok {
		c.bytes -= m.size
	}
	c.index[name] = fileMeta{size: int64(len(data)), mtime: e.LoadedAt}
	c.bytes += int64(len(data))
	return c.enforce()
}

// write fills the temporary file, syncs and closes it; every step checked.
func (c *Cache) write(tmp *os.File, name string, data []byte) error {
	if err := c.step("write", name); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := c.step("sync", name); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := c.step("close", name); err != nil {
		_ = tmp.Close()
		return err
	}
	return tmp.Close()
}

// --- reading -----------------------------------------------------------------

// Read answers the entry of k when the directory holds one that passes
// every check; ok is false for a miss. A file that fails a check is a miss
// and is removed. err reports a failing file system, never a bad file.
func (c *Cache) Read(k cache.Key, now time.Time) (e cache.Entry, ok bool, err error) {
	if !c.enter() {
		return cache.Entry{}, false, ErrClosed
	}
	defer c.leave()
	name := nameOf(k)
	if _, known := c.index[name]; !known {
		return cache.Entry{}, false, nil
	}
	path := filepath.Join(c.dir, name)
	if err := c.step("open", path); err != nil {
		return cache.Entry{}, false, err
	}
	data, info, err := safefile.ReadRegularNoLink(path, MaxFileBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.bytes -= c.index[name].size
		delete(c.index, name)
		return cache.Entry{}, false, nil
	case errors.Is(err, safefile.ErrLink), errors.Is(err, safefile.ErrNotRegular), errors.Is(err, safefile.ErrTooLarge):
		return c.drop(name)
	case err != nil:
		return cache.Entry{}, false, err
	}
	e, valid := c.decode(k, data, info, now)
	if !valid {
		return c.drop(name)
	}
	return e, true, nil
}

// drop removes a file that failed a check: a miss, and the file is gone.
func (c *Cache) drop(name string) (cache.Entry, bool, error) {
	if err := c.removeEntry(name); err != nil {
		return cache.Entry{}, false, err
	}
	return cache.Entry{}, false, nil
}

// decode checks the opened file (the user's alone: owner, mode, access
// list, as the config's lock file) and its record against the key asked
// for.
func (c *Cache) decode(k cache.Key, data []byte, info fs.FileInfo, now time.Time) (cache.Entry, bool) {
	if privatefs.OwnFile(filepath.Join(c.dir, nameOf(k)), info) != nil {
		return cache.Entry{}, false
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil || r.Format != FormatVersion {
		return cache.Entry{}, false
	}
	filed, ok := r.Key.Key()
	if !ok || filed != k {
		return cache.Entry{}, false
	}
	if r.LoadedAt.IsZero() || expired(r.LoadedAt, now) {
		return cache.Entry{}, false
	}
	if len(r.Payload) == 0 || len(r.Payload) > MaxEntryBytes || !json.Valid(r.Payload) {
		return cache.Entry{}, false
	}
	return cache.Entry{Payload: []byte(r.Payload), LoadedAt: r.LoadedAt, Stale: r.Stale}, true
}
