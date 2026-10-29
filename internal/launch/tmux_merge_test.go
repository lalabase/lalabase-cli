package launch

import (
	"context"
	"errors"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/agents"
)

// A tmux start must name the recorded agent too: C9c uses it to settle
// a restart after the new agent's first hook, keeping the old one until then.
func TestTmuxStartNamesTheAgentForSettlement(t *testing.T) {
	for _, again := range []bool{false, true} {
		for _, recordFails := range []bool{false, true} {
			r := newRig(t, true, nil)
			r.l.tmux = "/opt/bin/tmux"
			r.l.runTmux = func(context.Context, []string) (string, string, error) {
				return "", "", nil
			}
			if recordFails {
				r.l.writeLaunch = func(string, agents.Launch) error { return errors.New("disk full") }
			}
			var d Done
			if again {
				d = r.again(shown, Again{Agent: oldAgent, Session: cleared}).(Done)
				if d.Action != AgainAction || d.Replaces != oldAgent {
					t.Fatalf("restart: %+v", d)
				}
			} else {
				d = r.done(shown)
				if d.Action != StartAction || d.Replaces != "" {
					t.Fatalf("start: %+v", d)
				}
			}
			if d.Kind != Opened {
				t.Fatalf("launch: %+v", d)
			}
			if recordFails {
				if d.Agent != "" || d.Untracked == "" {
					t.Fatalf("failed record: %+v", d)
				}
			} else if d.Agent != agentID || d.Untracked != "" {
				t.Fatalf("recorded agent missing from outcome: %+v", d)
			}
		}
	}
}
