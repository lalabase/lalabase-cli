//go:build darwin || linux

package agents

import (
	"context"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// The real process table: a child lives with a start time that stays the
// same while it lives, and is gone once it was killed and reaped.
func TestTheSystemKnowsALiveAndAGoneProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, "sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	pid := child.Process.Pid
	start, alive, err := System.Start(pid)
	if err != nil || !alive || start <= 0 {
		t.Fatalf("live child: %d %v %v", start, alive, err)
	}
	again, alive, err := System.Start(pid)
	if err != nil || !alive || again != start {
		t.Fatalf("the start time moved: %d then %d (%v %v)", start, again, alive, err)
	}
	if own, alive, err := System.Start(1); err != nil || !alive || own == start {
		t.Fatalf("pid 1: %d %v %v", own, alive, err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if _, alive, err := System.Start(pid); err != nil || alive {
		t.Fatalf("killed child: alive %v, %v", alive, err)
	}
}

// A state whose process was killed reads as lost; through the real table.
func TestAKilledSessionIsLost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, "sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	c := HookCall{Dir: dir, Event: PreToolUse, Agent: agentID, PID: itoa(child.Process.Pid), Now: t0, Procs: System}
	if err := Hook(c); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(dir, System); err != nil || len(got) != 1 || got[0].State != Working {
		t.Fatalf("%+v %v", got, err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if got, err := Read(dir, System); err != nil || len(got) != 1 || got[0].State != Lost {
		t.Fatalf("%+v %v", got, err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
