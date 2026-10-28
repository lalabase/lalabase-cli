package agents

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// hookEvent is one call of a hook: its event and its input.
type hookEvent struct{ event, input string }

// The hook's decision is step, apart from its files: every order of
// events that Hook writes to disk, step reaches without a directory, so
// a test of the decision needs none. The records are compared after the
// trip through JSON a file makes. This guards the split (what Hook does
// around step); the decision itself is held by the other hook tests.
func TestStepDecidesWhatHookWrites(t *testing.T) {
	bash := func(cmd, id string) string {
		return toolPayload(t, "", "Bash", `{"command":"`+cmd+`"}`, id)
	}
	const sub = "a06151abf02cc81f9"
	subTool := toolPayload(t, sub, "Read", `{"file_path":"/tmp/file"}`, "toolu_sub")
	for name, events := range map[string][]hookEvent{
		"a turn":                    {{SessionStart, `{"session_id":"` + sessionID + `","source":"startup"}`}, {UserPromptSubmit, `{}`}, {PreToolUse, bash("ls", "toolu_1")}, {PostToolUse, bash("ls", "toolu_1")}, {Stop, `{}`}},
		"an approval answered":      {{UserPromptSubmit, `{}`}, {PreToolUse, bash("rm x", "toolu_1")}, {PermissionRequest, bash("rm x", "")}, {PostToolUse, bash("rm x", "toolu_1")}},
		"a question":                {{UserPromptSubmit, `{}`}, {PermissionRequest, toolPayload(t, "", toolAskQuestion, `{"q":"?"}`)}},
		"a compaction keeps":        {{UserPromptSubmit, `{}`}, {PermissionRequest, bash("rm x", "")}, {SessionStart, `{"source":"compact"}`}},
		"a subagent after the turn": {{UserPromptSubmit, `{}`}, {Stop, `{}`}, {PreToolUse, subTool}, {PostToolUse, subTool}},
		"two parallel calls":        {{PreToolUse, bash("same", "toolu_1")}, {PreToolUse, bash("same", "toolu_2")}, {PermissionRequest, bash("same", "")}, {PostToolUse, bash("same", "toolu_1")}},
		"a notification":            {{UserPromptSubmit, `{}`}, {Notification, `{"notification_type":"permission_prompt"}`}, {Stop, `{}`}},
		"the end":                   {{UserPromptSubmit, `{}`}, {SessionEnd, `{"reason":"other"}`}},
		"a clear":                   {{UserPromptSubmit, `{}`}, {SessionEnd, `{"reason":"clear"}`}, {SessionStart, `{"source":"clear"}`}},
	} {
		dir := agentsDir(t)
		var stepped stateRecord
		for _, e := range events {
			hook(t, dir, e.event, e.input, nil)
			f := readFacts(strings.NewReader(e.input))
			if _, does := next(e.event, f); does != nothing {
				stepped = throughJSON(t, step(e.event, f, stepped, t0))
			}
		}
		if got := stateOf(t, dir); !reflect.DeepEqual(got, stepped) {
			t.Errorf("%s:\nHook %+v\nstep %+v", name, got, stepped)
		}
	}
}

// throughJSON is r as a state file holds it.
func throughJSON(t *testing.T, r stateRecord) stateRecord {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back stateRecord
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	return back
}
