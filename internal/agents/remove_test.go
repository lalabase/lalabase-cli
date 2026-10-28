package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
)

// agentNumbered is the id of a test's agent n.
func agentNumbered(n int) string { return fmt.Sprintf("0f8fad5b-d9cb-469f-a165-%012d", n) }

// launchAgent records agent id, launched at at, and its state after event
// at the same time; pid 4242 lives in p unless p says otherwise.
func launchAgent(t *testing.T, dir, id string, at time.Time, event string, p Procs) {
	t.Helper()
	l := launchOf(at)
	l.Agent = id
	if err := WriteLaunch(dir, l); err != nil {
		t.Fatal(err)
	}
	if event == "" {
		return
	}
	if err := Hook(HookCall{Dir: dir, Event: event, Agent: id, PID: "4242", Now: at, Procs: p}); err != nil {
		t.Fatal(err)
	}
}

// age sets the modification time of every file of dir whose name starts
// with prefix.
func age(t *testing.T, dir, prefix string, at time.Time) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			if err := os.Chtimes(filepath.Join(dir, e.Name()), at, at); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func filesOf(t *testing.T, dir, id string) []string {
	t.Helper()
	var out []string
	for _, name := range names(t, dir) {
		if strings.HasPrefix(name, id) {
			out = append(out, name)
		}
	}
	return out
}

// Remove takes every file of the agent, its launch record last; a file
// already gone is no error, and nothing of another agent or of another
// name is touched.
func TestRemoveTakesEveryFileOfTheAgent(t *testing.T) {
	dir := agentsDir(t)
	one, two := agentNumbered(1), agentNumbered(2)
	launchAgent(t, dir, one, t0, SessionEnd, live(4242, 7))
	launchAgent(t, dir, two, t0, SessionEnd, live(4242, 7))
	if err := MarkSeen(dir, one, t0); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if len(filesOf(t, dir, one)) != 4 {
		t.Fatalf("set-up: %v", names(t, dir))
	}
	if err := Remove(dir, one, t0, live(4242, 7)); err != nil {
		t.Fatal(err)
	}
	if got := filesOf(t, dir, one); len(got) != 0 {
		t.Fatalf("left: %v", got)
	}
	if len(filesOf(t, dir, two)) != 3 || !slices.Contains(names(t, dir), "notes.txt") {
		t.Fatalf("took more: %v", names(t, dir))
	}
	if err := Remove(dir, one, t0, live(4242, 7)); err != nil {
		t.Fatalf("again: %v", err)
	}
	if got := filesOf(t, dir, one); len(got) != 0 {
		t.Fatalf("a removal of a removed agent left %v", got)
	}
	if err := Remove(dir, "../"+one, t0, live(4242, 7)); err == nil {
		t.Fatal("an id of another form was taken")
	}
}

// Prune at the start of a run: an agent that ended, is lost or never got
// past starting goes 30 days after the last change of its files (its last
// hook, its end, its open; owner, 2026-10-28), seen or not; an agent that
// works or waits stays, whatever its age.
func TestPruneTakesEndedLostAndStuckAgentsAfterThirtyDays(t *testing.T) {
	dir := agentsDir(t)
	now := t0.Add(31 * 24 * time.Hour)
	young := now.Add(-29 * 24 * time.Hour)
	cases := []struct {
		event string
		at    time.Time
		stays bool
	}{
		{SessionEnd, t0, false},       // ended, old
		{UserPromptSubmit, t0, false}, // lost (below), old
		{SessionEnd, young, true},     // ended, young
		{UserPromptSubmit, t0, true},  // working, old
		{Stop, t0, true},              // waiting, old
		{"", t0, false},               // starting, old: never got past it
		{"", young, true},             // starting, young
	}
	for i, c := range cases {
		launchAgent(t, dir, agentNumbered(i), c.at, c.event, live(4242, 7))
	}
	// Agent 1 ran as pid 5151, which is gone by the time of the prune.
	if err := Hook(HookCall{Dir: dir, Event: UserPromptSubmit, Agent: agentNumbered(1), PID: "5151", Now: t0, Procs: live(5151, 9)}); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		age(t, dir, agentNumbered(i), c.at)
	}
	if err := Prune(dir, now, live(4242, 7)); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		if got := len(filesOf(t, dir, agentNumbered(i))) > 0; got != c.stays {
			t.Errorf("agent %d (%q): stays %v, want %v", i, c.event, got, c.stays)
		}
	}
}

// The age counts from the last change of any of the agent's files: an
// ended agent opened a week ago stays, though its end is older.
func TestPruneCountsFromTheLastChange(t *testing.T) {
	dir := agentsDir(t)
	now := t0.Add(40 * 24 * time.Hour)
	launchAgent(t, dir, agentID, t0, SessionEnd, live(4242, 7))
	age(t, dir, agentID, t0)
	if err := MarkSeen(dir, agentID, now.Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	age(t, dir, agentID+seenSuffix, now.Add(-7*24*time.Hour))
	if err := Prune(dir, now, live(4242, 7)); err != nil {
		t.Fatal(err)
	}
	if len(filesOf(t, dir, agentID)) == 0 {
		t.Fatal("an agent opened a week ago went")
	}
}

// A launch record of a newer format is another client's (a newer version
// on the same profile): it is not this client's to judge, and stays.
func TestPruneLeavesARecordOfANewerFormat(t *testing.T) {
	dir := agentsDir(t)
	path := filepath.Join(dir, agentID+launchSuffix)
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"format":%d,"agent":%q}`, FormatVersion+1, agentID)), 0o600); err != nil {
		t.Fatal(err)
	}
	age(t, dir, agentID, t0)
	if err := Prune(dir, t0.Add(365*24*time.Hour), live(4242, 7)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("a newer client's agent was removed: %v", err)
	}
}

// One leftover that cannot be removed does not stop the cleanup: the rest
// goes, and its error is told after.
func TestOneStubbornLeftoverDoesNotStopTheCleanup(t *testing.T) {
	dir := agentsDir(t)
	stubborn := filepath.Join(dir, tempPrefix+"0dir")
	if err := os.MkdirAll(filepath.Join(stubborn, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	launchAgent(t, dir, agentID, t0, SessionEnd, live(4242, 7))
	age(t, dir, "", t0)
	if err := Prune(dir, t0.Add(40*24*time.Hour), live(4242, 7)); err == nil {
		t.Fatal("the stubborn leftover was not told")
	}
	if len(filesOf(t, dir, agentID)) != 0 {
		t.Fatal("the old ended agent stayed behind the stubborn leftover")
	}
}

// Leftovers go once they are an hour old: a temporary file a hook left,
// the files of an agent without its launch record (removed by d while a
// hook still ran), an agent whose record fails its check. Younger ones
// may still be written, and stay; names not of this form are left alone.
func TestPruneTakesLeftoversAfterAnHour(t *testing.T) {
	dir := agentsDir(t)
	now := t0.Add(2 * time.Hour)
	old, young := t0, now.Add(-time.Minute)
	write := func(name string, data string, at time.Time) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write(tempPrefix+"old", "{", old)
	write(tempPrefix+"young", "{", young)
	write(agentNumbered(1)+stateSuffix, "{}", old) // no record, old
	write(agentNumbered(1)+lockSuffix, "", old)
	write(agentNumbered(2)+stateSuffix, "{}", young) // no record, young
	write(agentNumbered(3)+launchSuffix, "broken", old)
	write(agentNumbered(4)+launchSuffix, "broken", young)
	write("notes.txt", "", old)
	write("0f8fad5b.state.json", "", old) // not an agent's id
	if err := Prune(dir, now, live(4242, 7)); err != nil {
		t.Fatal(err)
	}
	// Deciding under an agent's lock makes its lock file where there was
	// none: an agent that stays keeps it, as every agent with a hook has.
	want := []string{tempPrefix + "young", "0f8fad5b.state.json", agentNumbered(2) + stateSuffix, agentNumbered(2) + lockSuffix, agentNumbered(4) + launchSuffix, agentNumbered(4) + lockSuffix, "notes.txt"}
	got := names(t, dir)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("left %v, want %v", got, want)
	}
}

// A directory too full for Read (maxWalk) is still pruned, so that the
// next run shows its agents again; one beyond Prune's own bound is
// refused whole, and a missing one has nothing to prune.
func TestPruneReachesADirectoryReadRefuses(t *testing.T) {
	dir := agentsDir(t)
	for i := range maxWalk {
		path := filepath.Join(dir, fmt.Sprintf("%s%d", tempPrefix, i))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	age(t, dir, tempPrefix, t0)
	launchAgent(t, dir, agentID, t0, Stop, live(4242, 7))
	if _, err := Read(dir, live(4242, 7)); err == nil {
		t.Fatal("set-up: Read took the full directory")
	}
	if err := Prune(dir, t0.Add(2*time.Hour), live(4242, 7)); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir, live(4242, 7))
	if err != nil || len(got) != 1 {
		t.Fatalf("after the prune: %v, %v", got, err)
	}
	if err := Prune(filepath.Join(dir, "missing"), t0, nil); err != nil {
		t.Fatalf("missing: %v", err)
	}
	if err := Prune("relative", t0, nil); err != nil {
		t.Fatalf("relative: %v", err)
	}
}

// The status line gets the reason of a failed file operation, not its
// path: a long path would push the reason out of a narrow line.
func TestTheWatchersErrorsLeaveThePathOut(t *testing.T) {
	err := &os.PathError{Op: "remove", Path: "/a/long/path/" + agentID + launchSuffix, Err: os.ErrPermission}
	if got := withoutPath(err); !errors.Is(got, os.ErrPermission) || strings.Contains(got.Error(), "/a/long/path") {
		t.Fatalf("%v", got)
	}
	if withoutPath(nil) != nil {
		t.Fatal("nil became an error")
	}
	locked := fmt.Errorf("lock file /a/long/path/%s.lock: %w", agentID, privatefs.ErrLocked)
	if got := withoutPath(locked); !errors.Is(got, privatefs.ErrLocked) || got.Error() != privatefs.ErrLocked.Error() {
		t.Fatalf("the lock error kept its path: %v", got)
	}
	other := fmt.Errorf("no agent id")
	if got := withoutPath(other); got.Error() != other.Error() {
		t.Fatal("another error changed")
	}
}

// Remove decides again as the agent is when its lock is held: one that
// works or waits, or started less than an hour ago, stays, with
// ErrRunning, though the interface took it for removable.
func TestRemoveLeavesAnAgentThatRunsNow(t *testing.T) {
	dir := agentsDir(t)
	launchAgent(t, dir, agentNumbered(1), t0, UserPromptSubmit, live(4242, 7))
	launchAgent(t, dir, agentNumbered(2), t0, "", nil) // starting since t0
	for id, now := range map[string]time.Time{agentNumbered(1): t0.Add(2 * time.Hour), agentNumbered(2): t0.Add(59 * time.Minute)} {
		if err := Remove(dir, id, now, live(4242, 7)); !errors.Is(err, ErrRunning) {
			t.Fatalf("%s: %v", id, err)
		}
		if len(filesOf(t, dir, id)) == 0 {
			t.Fatalf("%s went", id)
		}
	}
	if err := Remove(dir, agentNumbered(2), t0.Add(61*time.Minute), live(4242, 7)); err != nil {
		t.Fatalf("stuck starting: %v", err)
	}
}

// A hook that writes while d or the cleanup waits for the agent's lock
// decides: the agent works again, and stays (review of #41: the decision
// was made before, on what an earlier read or the walk had seen).
func TestARemovalWaitingForTheLockSeesTheHook(t *testing.T) {
	for _, name := range []string{"d", "prune"} {
		dir := agentsDir(t)
		launchAgent(t, dir, agentID, t0, SessionEnd, live(4242, 7))
		age(t, dir, agentID, t0)
		unlock, err := privatefs.Lock(filepath.Join(dir, agentID+lockSuffix))
		if err != nil {
			t.Fatal(err)
		}
		now := t0.Add(40 * 24 * time.Hour)
		done := make(chan error, 1)
		go func() {
			if name == "d" {
				done <- Remove(dir, agentID, now, live(4242, 7))
				return
			}
			done <- Prune(dir, now, live(4242, 7))
		}()
		time.Sleep(50 * time.Millisecond) // the removal waits for the lock
		// The hook, holding the lock: the session works again.
		rec := stateRecord{Format: FormatVersion, State: stateNames[Working], Changed: now, PID: 4242, Start: 7}
		if err := replace(dir, filepath.Join(dir, agentID+stateSuffix), rec); err != nil {
			t.Fatal(err)
		}
		unlock()
		select {
		case err = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the removal hangs")
		}
		if name == "d" && !errors.Is(err, ErrRunning) {
			t.Fatalf("d: %v", err)
		}
		if got, _ := Read(dir, live(4242, 7)); len(got) != 1 || got[0].State != Working {
			t.Fatalf("%s: the working agent is %v", name, got)
		}
	}
}

// A removal that fails part way leaves the agent listed, as its error
// says: the launch record goes last.
func TestAFailedRemovalLeavesTheAgentListed(t *testing.T) {
	dir := agentsDir(t)
	launchAgent(t, dir, agentID, t0, SessionEnd, live(4242, 7))
	// A seen mark that cannot be removed: a directory with something in
	// it. It reads as not seen, so the agent stays ended and removable.
	seen := filepath.Join(dir, agentID+seenSuffix)
	if err := os.MkdirAll(filepath.Join(seen, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dir, agentID, t0, live(4242, 7)); err == nil {
		t.Fatal("the removal reported no error")
	}
	if got, err := Read(dir, live(4242, 7)); err != nil || len(got) != 1 {
		t.Fatalf("the agent left though its removal failed: %v, %v", got, err)
	}
}

// Owner, 2026-10-28: besides the 30 days the cleanup keeps at most
// maxOver agents whose session is over (ended, lost, stuck starting); of
// more, the ones with the oldest last change go first, also before their
// 30 days. An agent that works or waits is never counted or taken.
func TestPruneKeepsAtMostMaxOverEndedAgents(t *testing.T) {
	dir := agentsDir(t)
	now := t0.Add(10 * 24 * time.Hour)
	for i := range maxOver + 3 {
		id := agentNumbered(i)
		launchAgent(t, dir, id, t0, SessionEnd, live(4242, 7))
		age(t, dir, id, t0.Add(time.Duration(i)*time.Minute)) // agent 0 the oldest
	}
	working := agentNumbered(9999)
	launchAgent(t, dir, working, t0, UserPromptSubmit, live(4242, 7))
	age(t, dir, working, t0.Add(-time.Hour)) // older than every ended one
	if err := Prune(dir, now, live(4242, 7)); err != nil {
		t.Fatal(err)
	}
	for i := range maxOver + 3 {
		if stays := len(filesOf(t, dir, agentNumbered(i))) > 0; stays != (i >= 3) {
			t.Fatalf("agent %d stays %v", i, stays)
		}
	}
	if len(filesOf(t, dir, working)) == 0 {
		t.Fatal("the working agent was counted and taken")
	}
}
