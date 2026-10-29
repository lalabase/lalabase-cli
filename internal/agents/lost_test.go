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

// C9c: a starting agent whose pane is gone, or whose claude ended here
// before any hook, is lost. The client writes that as a state of its
// own, under the agent's lock, only while no hook wrote one.

func TestMarkLostOnlyWithoutAState(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	at := t0.Add(time.Minute)
	lost, err := MarkLost(dir, agentID, at)
	if err != nil || !lost {
		t.Fatalf("lost %v, %v", lost, err)
	}
	ag := readOne(t, dir)
	if ag.State != Lost || !ag.Since.Equal(at) || ag.Resumable || ag.Session != sessionID {
		t.Fatalf("after the mark %+v", ag)
	}
	if info, err := os.Lstat(filepath.Join(dir, agentID+stateSuffix)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the state file %v, %v", info, err)
	}
	// A second mark finds the state: nothing changes.
	if lost, err := MarkLost(dir, agentID, at.Add(time.Hour)); err != nil || lost {
		t.Fatalf("again: %v, %v", lost, err)
	}
	if ag := readOne(t, dir); !ag.Since.Equal(at) {
		t.Fatalf("the second mark wrote: %+v", ag)
	}

	// An agent a hook wrote a state for is never marked.
	for _, event := range []string{UserPromptSubmit, Stop, PermissionRequest, SessionEnd} {
		dir := agentsDir(t)
		launchAgent(t, dir, agentID, t0, event, live(4242, 7))
		before := stateOf(t, dir)
		if lost, err := MarkLost(dir, agentID, at); err != nil || lost {
			t.Fatalf("%s: %v, %v", event, lost, err)
		}
		if after := stateOf(t, dir); after.State != before.State || !after.Changed.Equal(before.Changed) {
			t.Fatalf("%s: the state changed to %+v", event, after)
		}
	}
}

// Without its launch record there is no agent to mark: no state file of
// its own is left behind as a stray.
func TestMarkLostWithoutTheAgent(t *testing.T) {
	dir := agentsDir(t)
	if lost, err := MarkLost(dir, agentID, t0); err != nil || lost {
		t.Fatalf("%v, %v", lost, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, agentID+stateSuffix)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a state without an agent: %v", err)
	}
	for _, c := range []struct {
		name string
		dir  string
		id   string
		at   time.Time
	}{
		{"id", dir, "../" + agentID, t0},
		{"time", dir, agentID, time.Time{}},
		{"directory", filepath.Join(dir, "missing"), agentID, t0},
	} {
		if _, err := MarkLost(c.dir, c.id, c.at); err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
}

// The mark takes the agent's lock: while a hook holds it, nothing is
// written; the bounded wait ends in ErrLocked.
func TestMarkLostTakesTheAgentsLock(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	unlock, err := privatefs.Lock(filepath.Join(dir, agentID+lockSuffix))
	if err != nil {
		t.Fatal(err)
	}
	_, err = MarkLost(dir, agentID, t0)
	unlock()
	if !errors.Is(err, privatefs.ErrLocked) {
		t.Fatalf("the mark bypassed the lock: %v", err)
	}
	if ag := readOne(t, dir); ag.State != Starting {
		t.Fatalf("a refused mark wrote: %+v", ag)
	}
}

// A hook that writes while the mark waits for the lock decides: the
// agent started after all, and is not lost.
func TestAHookWhileTheMarkWaitsWins(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	unlock, err := privatefs.Lock(filepath.Join(dir, agentID+lockSuffix))
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		lost bool
		err  error
	}
	done := make(chan result, 1)
	go func() {
		lost, err := MarkLost(dir, agentID, t0.Add(time.Minute))
		done <- result{lost, err}
	}()
	time.Sleep(50 * time.Millisecond) // the mark waits for the lock
	rec := stateRecord{Format: FormatVersion, State: stateNames[Working], Changed: t0, Session: sessionID}
	if err := replace(dir, filepath.Join(dir, agentID+stateSuffix), rec); err != nil {
		t.Fatal(err)
	}
	unlock()
	var r result
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the mark hangs")
	}
	if r.err != nil || r.lost {
		t.Fatalf("%+v", r)
	}
	if ag := readOne(t, dir); ag.State != Working {
		t.Fatalf("the hook's state was replaced: %+v", ag)
	}
}

// A hook that comes after the mark replaces it as any state: the
// session did start. A compaction keeps no lost state.
func TestAHookAfterTheMarkReplacesIt(t *testing.T) {
	for event, want := range map[string]State{SessionStart: Working, Stop: WaitingForYou, SessionEnd: Ended} {
		dir := agentsDir(t)
		if err := WriteLaunch(dir, launchOf(t0)); err != nil {
			t.Fatal(err)
		}
		if _, err := MarkLost(dir, agentID, t0); err != nil {
			t.Fatal(err)
		}
		hook(t, dir, event, `{"session_id":"`+cleared+`"}`, nil)
		if ag := readOne(t, dir); ag.State != want || !ag.Resumable || ag.Session != cleared {
			t.Fatalf("%s: %+v", event, ag)
		}
	}
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	if _, err := MarkLost(dir, agentID, t0); err != nil {
		t.Fatal(err)
	}
	hook(t, dir, SessionStart, `{"session_id":"`+sessionID+`","source":"compact"}`, nil)
	if ag := readOne(t, dir); ag.State != Working {
		t.Fatalf("a compaction kept lost: %+v", ag)
	}
}

// A lost state is lost for d and for the cleanup as a lost process is.
func TestALostMarkIsRemovableAndPruned(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	if _, err := MarkLost(dir, agentID, t0); err != nil {
		t.Fatal(err)
	}
	ag := readOne(t, dir)
	if !Removable(ag, t0) {
		t.Fatalf("a lost mark is not removable: %+v", ag)
	}
	age(t, dir, agentID, t0)
	if err := Prune(dir, t0.Add(29*24*time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	if len(filesOf(t, dir, agentID)) == 0 {
		t.Fatal("pruned before 30 days")
	}
	if err := Prune(dir, t0.Add(31*24*time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	if got := filesOf(t, dir, agentID); len(got) != 0 {
		t.Fatalf("a lost mark stayed: %v", got)
	}
}

// Resumable: only a session a hook recorded can be resumed; the launch's
// own id may never have been used (Esc on the trust dialog).
func TestResumableOnlyAfterAHook(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	if ag := readOne(t, dir); ag.Resumable {
		t.Fatalf("a starting agent is resumable: %+v", ag)
	}
	hook(t, dir, SessionEnd, `{"session_id":"`+sessionID+`"}`, nil)
	if ag := readOne(t, dir); !ag.Resumable || ag.Session != sessionID {
		t.Fatalf("an ended agent: %+v", ag)
	}
	// A hook without a session id in its input keeps the launch's, which
	// it then did use: the hook ran in that session.
	dir = agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	hook(t, dir, SessionEnd, "", nil)
	if ag := readOne(t, dir); !ag.Resumable || ag.Session != sessionID {
		t.Fatalf("a hook without input: %+v", ag)
	}
}

// Settle: what follows a start, decided under the agents' locks.
func TestSettle(t *testing.T) {
	newAgent, oldAgent := agentNumbered(1), agentNumbered(2)
	at := t0.Add(time.Minute)
	for _, c := range []struct {
		name      string
		after     After
		newHook   string // the new agent's hook before the settle
		lost      bool
		newStays  bool
		newState  State
		oldStays  bool
		oldExists bool
	}{
		{"here, no hook", After{Agent: newAgent}, "", true, true, Lost, true, false},
		{"here, a hook", After{Agent: newAgent}, SessionEnd, false, true, Ended, true, false},
		{"pane gone, starting", After{Agent: newAgent}, "", true, true, Lost, true, false},
		{"pane gone, working", After{Agent: newAgent}, UserPromptSubmit, false, true, Working, true, false},
		{"again here, no hook", After{Agent: newAgent, Replaces: oldAgent}, "", true, false, 0, true, true},
		{"again here, a hook", After{Agent: newAgent, Replaces: oldAgent}, SessionEnd, false, true, Ended, false, true},
		{"again opened", After{Agent: newAgent, Replaces: oldAgent, Opened: true}, "", false, true, Starting, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := agentsDir(t)
			launchAgent(t, dir, newAgent, t0, c.newHook, live(4242, 7))
			if c.oldExists {
				launchAgent(t, dir, oldAgent, t0, SessionEnd, live(4242, 7))
			}
			lost, err := Settle(dir, c.after, at, live(4242, 7))
			if err != nil || lost != c.lost {
				t.Fatalf("lost %v, %v", lost, err)
			}
			got, err := Read(dir, live(4242, 7))
			if err != nil {
				t.Fatal(err)
			}
			byID := map[string]Agent{}
			for _, ag := range got {
				byID[ag.ID] = ag
			}
			if ag, ok := byID[newAgent]; ok != c.newStays || (ok && ag.State != c.newState) {
				t.Fatalf("the new agent: %v %+v", ok, ag)
			}
			if !c.newStays && len(filesOf(t, dir, newAgent)) != 0 {
				t.Fatalf("the failed agent left %v", filesOf(t, dir, newAgent))
			}
			if c.oldExists {
				if _, ok := byID[oldAgent]; ok != c.oldStays {
					t.Fatalf("the old agent stays: %v", ok)
				}
			}
		})
	}
}

// An agent being replaced that runs again (a hook after its lost mark)
// is not removed: Settle says so, and both stay.
func TestSettleLeavesAReplacedAgentThatRunsAgain(t *testing.T) {
	dir := agentsDir(t)
	newAgent, oldAgent := agentNumbered(1), agentNumbered(2)
	launchAgent(t, dir, newAgent, t0, "", nil)
	launchAgent(t, dir, oldAgent, t0, UserPromptSubmit, live(4242, 7))
	_, err := Settle(dir, After{Agent: newAgent, Replaces: oldAgent, Opened: true}, t0, live(4242, 7))
	if !errors.Is(err, ErrRunning) {
		t.Fatalf("%v", err)
	}
	if got, _ := Read(dir, live(4242, 7)); len(got) != 2 {
		t.Fatalf("agents %+v", got)
	}
}

func TestTheWatcherSettles(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	w := NewWatcher(worker.New(context.Background()), dir)
	after := After{Agent: agentID}
	if msg := w.Settle(after, t0)(); msg != (Settled{After: after, Lost: true}) {
		t.Fatalf("%#v", msg)
	}
	if msg, ok := w.Settle(After{Agent: "x"}, t0)().(Settled); !ok || msg.Err == nil {
		t.Fatalf("%#v", msg)
	}
}

// Review round 1 of #43: an agent the reader finds lost (its pid gone)
// after its hooks recorded a session is resumable: that is the crash or
// the pane closed while it worked, whose conversation start again keeps.
func TestALostProcessIsResumable(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	hook(t, dir, UserPromptSubmit, `{"session_id":"`+cleared+`"}`, live(4242, 7))
	got, err := Read(dir, live(1, 1)) // 4242 is gone
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if ag := got[0]; ag.State != Lost || !ag.Resumable || ag.Session != cleared {
		t.Fatalf("%+v", ag)
	}
}

// Review round 1 of #43: a launch record that fails its check is no agent
// to mark: no state file is left beside it.
func TestMarkLostWithABrokenRecord(t *testing.T) {
	for name, record := range map[string]string{
		"another agent":  `{"format":1,"agent":"` + agentNumbered(9) + `","organisation":"` + org + `","project":"` + project + `","ticket":"` + ticket + `","number":42,"session":"` + sessionID + `","pane":"","at":"2026-10-26T09:00:00Z"}`,
		"another format": `{"format":2,"agent":"` + agentID + `","organisation":"` + org + `","project":"` + project + `","ticket":"` + ticket + `","number":42,"session":"` + sessionID + `","pane":"","at":"2026-10-26T09:00:00Z"}`,
		"no number":      `{"format":1,"agent":"` + agentID + `","organisation":"` + org + `","project":"` + project + `","ticket":"` + ticket + `","number":0,"session":"` + sessionID + `","pane":"","at":"2026-10-26T09:00:00Z"}`,
	} {
		dir := agentsDir(t)
		write(t, filepath.Join(dir, agentID+launchSuffix), record, 0o600)
		if lost, err := MarkLost(dir, agentID, t0); err != nil || lost {
			t.Fatalf("%s: %v, %v", name, lost, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, agentID+stateSuffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: a state beside a broken record: %v", name, err)
		}
	}
}
