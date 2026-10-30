package agents

import (
	"errors"
	"testing"
)

// d on a waiting agent: its session ends and its files go, under its lock.
func TestEndEndsAWaitingAgentAndTakesItsFiles(t *testing.T) {
	dir := agentsDir(t)
	one := agentNumbered(1)
	launchAgent(t, dir, one, t0, Stop, live(4242, 7))
	calls := 0
	err := End(dir, one, t0, live(4242, 7), func() error { calls++; return nil })
	if err != nil || calls != 1 {
		t.Fatalf("err %v, %d kills", err, calls)
	}
	if got := filesOf(t, dir, one); len(got) != 0 {
		t.Fatalf("left: %v", got)
	}
}

// A hook that wrote since the screen showed it decides: an agent at work
// is not ended.
func TestEndLeavesAnAgentThatWorksNow(t *testing.T) {
	dir := agentsDir(t)
	one := agentNumbered(1)
	launchAgent(t, dir, one, t0, UserPromptSubmit, live(4242, 7))
	err := End(dir, one, t0, live(4242, 7), func() error {
		t.Error("ended an agent at work")
		return nil
	})
	if !errors.Is(err, ErrRunning) {
		t.Fatalf("err %v", err)
	}
	if len(filesOf(t, dir, one)) == 0 {
		t.Fatal("its files went")
	}
}

// A session that cannot be ended keeps the agent, and says why.
func TestEndKeepsAnAgentWhoseSessionDidNotEnd(t *testing.T) {
	dir := agentsDir(t)
	one := agentNumbered(1)
	launchAgent(t, dir, one, t0, Stop, live(4242, 7))
	boom := errors.New("boom")
	if err := End(dir, one, t0, live(4242, 7), func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if len(filesOf(t, dir, one)) == 0 {
		t.Fatal("its files went")
	}
}

// One that ended since then has no session left to end: its files go.
func TestEndTakesAnAgentThatEndedMeanwhile(t *testing.T) {
	dir := agentsDir(t)
	one := agentNumbered(1)
	launchAgent(t, dir, one, t0, SessionEnd, live(4242, 7))
	err := End(dir, one, t0, live(4242, 7), func() error {
		t.Error("ended an agent that is over")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := filesOf(t, dir, one); len(got) != 0 {
		t.Fatalf("left: %v", got)
	}
}
