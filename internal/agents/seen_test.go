//go:build unix

package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// C9b: an agent the person opened once is seen; the client alone records
// it, in a file of its own beside the agent's.

func readOne(t *testing.T, dir string) Agent {
	t.Helper()
	got, err := Read(dir, nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	return got[0]
}

func TestSeenIsWrittenOnceAndPrivately(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	if readOne(t, dir).Seen {
		t.Fatal("an agent nobody opened is seen")
	}
	if err := MarkSeen(dir, agentID, t0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, agentID+seenSuffix)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", info, err)
	}
	if !readOne(t, dir).Seen {
		t.Fatal("a seen agent is not seen")
	}
	before, err := os.ReadFile(path) //nolint:gosec // the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	// A second time is no error, and the file stays as it is: it is never
	// replaced.
	if err := MarkSeen(dir, agentID, t0.Add(time.Hour)); err != nil {
		t.Fatalf("a second mark: %v", err)
	}
	after, err := os.ReadFile(path) //nolint:gosec // the test's own directory
	if err != nil || string(after) != string(before) {
		t.Fatalf("the file changed: %q to %q (%v)", before, after, err)
	}
	if got := names(t, dir); len(got) != 3 {
		t.Fatalf("files %v", got)
	}
}

func TestSeenRefusesWhatIsNotItsOwn(t *testing.T) {
	dir := agentsDir(t)
	for _, id := range []string{"", "x", "../" + agentID[3:], "0F8FAD5B-D9CB-469F-A165-70867728950E"} {
		if err := MarkSeen(dir, id, t0); err == nil {
			t.Errorf("%q passed", id)
		}
	}
	// The directory is never made here.
	missing := filepath.Join(realTemp(t), Dir)
	if err := MarkSeen(missing, agentID, t0); err == nil {
		t.Error("a missing directory passed")
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the mark made the directory: %v", err)
	}
	// A directory others may change.
	open := filepath.Join(realTemp(t), Dir)
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil { //nolint:gosec // the test opens it on purpose
		t.Fatal(err)
	}
	if err := MarkSeen(open, agentID, t0); err == nil {
		t.Error("a directory others may change passed")
	}
	// A link in the file's place is not followed, and not replaced.
	target := filepath.Join(realTemp(t), "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, agentID+seenSuffix)); err != nil {
		t.Fatal(err)
	}
	if err := MarkSeen(dir, agentID, t0); err == nil {
		t.Error("a link in the file's place passed")
	}
	if data, err := os.ReadFile(target); err != nil || len(data) != 0 { //nolint:gosec // the test's own file
		t.Errorf("the link's target was written: %q %v", data, err)
	}
}

// A seen file that is broken, open to others, a link or a directory says
// nothing: the agent is not seen, and it stays in the list.
func TestABrokenSeenIsNotSeen(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T, path string){
		"empty":     func(t *testing.T, path string) { write(t, path, "", 0o600) },
		"not json":  func(t *testing.T, path string) { write(t, path, "seen", 0o600) },
		"format":    func(t *testing.T, path string) { write(t, path, `{"format":2,"at":"2026-10-26T09:00:00Z"}`, 0o600) },
		"no time":   func(t *testing.T, path string) { write(t, path, `{"format":1}`, 0o600) },
		"too large": func(t *testing.T, path string) { write(t, path, string(make([]byte, maxFile+1)), 0o600) },
		"open": func(t *testing.T, path string) {
			write(t, path, `{"format":1,"at":"2026-10-26T09:00:00Z"}`, 0o600)
			if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // the test opens it on purpose
				t.Fatal(err)
			}
		},
		"link": func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(path), "elsewhere")
			write(t, target, `{"format":1,"at":"2026-10-26T09:00:00Z"}`, 0o600)
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
		"directory": func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := agentsDir(t)
			if err := WriteLaunch(dir, launchOf(t0)); err != nil {
				t.Fatal(err)
			}
			prepare(t, filepath.Join(dir, agentID+seenSuffix))
			if a := readOne(t, dir); a.Seen || a.ID != agentID {
				t.Fatalf("%+v", a)
			}
		})
	}
}

// What MarkSeen writes is what Read takes as seen: no hand-written form
// stands in for it.
func TestTheSeenFormReadsBack(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	if err := MarkSeen(dir, agentID, time.Time{}); err == nil {
		t.Fatal("a mark without a time passed")
	}
	if got := names(t, dir); len(got) != 1 {
		t.Fatalf("a refused mark left %v", got)
	}
	if err := MarkSeen(dir, agentID, t0); err != nil {
		t.Fatal(err)
	}
	if !readOne(t, dir).Seen {
		t.Fatal("not seen")
	}
}

func write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

// The watcher's command records the agent under the supervisor, and the
// next read has it seen.
func TestTheWatcherMarksSeen(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	sup := worker.New(context.Background())
	w := NewWatcher(sup, dir)
	if msg := w.See(agentID, t0)(); msg != (Marked{ID: agentID}) {
		t.Fatalf("%#v", msg)
	}
	snap, ok := w.Read(1)().(Snapshot)
	if !ok || snap.Err != nil || len(snap.Agents) != 1 || !snap.Agents[0].Seen {
		t.Fatalf("%+v", snap)
	}
	// A hook after it leaves the mark alone.
	hook(t, dir, Stop, `{"session_id":"`+sessionID+`"}`, nil)
	if !readOne(t, dir).Seen {
		t.Fatal("a hook took the mark")
	}
	if msg, ok := w.See("x", t0)().(Marked); !ok || msg.Err == nil {
		t.Fatalf("%#v", msg)
	}
}

// A seen mark changes the retention age. It must take the same lock as
// cleanup, so it cannot appear between the age check and the deletion.
func TestSeenSharesTheRemovalLock(t *testing.T) {
	dir := agentsDir(t)
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	launchAgent(t, dir, agentID, old, SessionEnd, live(4242, 7))
	age(t, dir, agentID, old)
	unlock, err := privatefs.Lock(filepath.Join(dir, agentID+lockSuffix))
	if err != nil {
		t.Fatal(err)
	}
	// MarkSeen's lock wait is bounded; no scheduling or sleep is needed
	// to prove that it cannot write while cleanup holds the lock.
	err = MarkSeen(dir, agentID, now)
	unlock()
	if !errors.Is(err, privatefs.ErrLocked) {
		t.Fatalf("seen mark bypassed the removal lock: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, agentID+seenSuffix)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused mark left a file: %v", err)
	}
	if err := MarkSeen(dir, agentID, now); err != nil {
		t.Fatal(err)
	}
	if err := Prune(dir, now, live(4242, 7)); err != nil {
		t.Fatal(err)
	}
	if ag := readOne(t, dir); !ag.Seen || ag.State != Ended {
		t.Fatalf("the fresh mark did not retain the ended agent: %+v", ag)
	}
}
