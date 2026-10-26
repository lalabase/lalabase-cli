//go:build unix

package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// PermissionRequest has no tool_use_id. The name and input must identify
// the request even when other tools of the same agent finish beside it.
func toolPayload(t *testing.T, agent, tool, input string, id ...string) string {
	t.Helper()
	fields := map[string]any{
		"session_id": sessionID, "agent_id": agent, "tool_name": tool,
		"tool_input": json.RawMessage(input),
	}
	if len(id) > 0 {
		fields["tool_use_id"] = id[0]
	}
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestIdenticalParallelCallsCannotAnswerEachOthersPrompt(t *testing.T) {
	for _, secondBefore := range []bool{true, false} {
		dir := agentsDir(t)
		args := `{"command":"curl example.com"}`
		first := toolPayload(t, "", "Bash", args, "toolu_first")
		second := toolPayload(t, "", "Bash", args, "toolu_second")
		hook(t, dir, PreToolUse, first, live(4242, 7))
		if secondBefore {
			hook(t, dir, PreToolUse, second, live(4242, 7))
		}
		hook(t, dir, PermissionRequest, toolPayload(t, "", "Bash", args), live(4242, 7))
		if !secondBefore {
			hook(t, dir, PreToolUse, second, live(4242, 7))
		}
		hook(t, dir, PostToolUse, second, live(4242, 7))
		if got := stateOf(t, dir); got.State != "approval" {
			t.Fatalf("identical parallel command hid the prompt (second before request: %v): %+v", secondBefore, got)
		}
		hook(t, dir, PostToolUse, first, live(4242, 7))
		// Two candidates at PermissionRequest cannot be disambiguated;
		// a uniquely identified call can resume immediately.
		if got := stateOf(t, dir); !secondBefore && got.State != "working" {
			t.Fatalf("the identified tool completed: %+v", got)
		}
		hook(t, dir, Stop, `{}`, live(4242, 7))
		if got := stateOf(t, dir); got.State != "waiting" {
			t.Fatalf("the turn ended: %+v", got)
		}
	}
}

func TestAnApprovedCallMayHaveEditedArguments(t *testing.T) {
	dir := agentsDir(t)
	before := toolPayload(t, "", "Bash", `{"command":"original"}`, "toolu_first")
	hook(t, dir, PreToolUse, before, live(4242, 7))
	hook(t, dir, PermissionRequest, toolPayload(t, "", "Bash", `{"command":"original"}`), live(4242, 7))
	hook(t, dir, PostToolUse, toolPayload(t, "", "Bash", `{"command":"edited"}`, "toolu_first"), live(4242, 7))
	if got := stateOf(t, dir); got.State != "working" {
		t.Fatalf("completion of the same call still waits: %+v", got)
	}
}

func TestRewrittenArgumentsCannotSelectAnotherParallelCall(t *testing.T) {
	dir := agentsDir(t)
	first := toolPayload(t, "", "Bash", `{"command":"npm test"}`, "toolu_first")
	second := toolPayload(t, "", "Bash", `{"command":"npm run lint"}`, "toolu_second")
	hook(t, dir, PreToolUse, first, live(4242, 7))
	hook(t, dir, PreToolUse, second, live(4242, 7))
	// Another PreToolUse hook rewrites the first call before its prompt.
	hook(t, dir, PermissionRequest, toolPayload(t, "", "Bash", `{"command":"npm run lint"}`), live(4242, 7))
	hook(t, dir, PostToolUse, second, live(4242, 7))
	if got := stateOf(t, dir); got.State != "approval" {
		t.Fatalf("rewritten input selected the wrong call: %+v", got)
	}
}

func TestFailedToolsResolveOnlyTheirOwnApproval(t *testing.T) {
	for _, agent := range []string{"", "a123"} {
		t.Run(agent, func(t *testing.T) {
			dir := agentsDir(t)
			asked := toolPayload(t, agent, "Bash", `{"command":"npm test"}`)
			call := toolPayload(t, agent, "Bash", `{"command":"npm test"}`, "toolu_first")
			hook(t, dir, PreToolUse, call, live(4242, 7))
			hook(t, dir, PermissionRequest, asked, live(4242, 7))
			hook(t, dir, "PostToolUseFailure", toolPayload(t, agent, "Read", `{}`, "toolu_read"), live(4242, 7))
			if got := stateOf(t, dir); got.State != "approval" {
				t.Fatalf("unrelated failure hid the approval: %+v", got)
			}
			hook(t, dir, "PostToolUseFailure", call, live(4242, 7))
			if got := stateOf(t, dir); got.State != "working" {
				t.Fatalf("the approved tool ran and failed: %+v", got)
			}
			// Failure must also remove the active call, or the next one
			// with the same tool name would appear ambiguous forever.
			next := toolPayload(t, agent, "Bash", `{"command":"npm test"}`, "toolu_next")
			hook(t, dir, PreToolUse, next, live(4242, 7))
			hook(t, dir, PermissionRequest, asked, live(4242, 7))
			hook(t, dir, PostToolUse, next, live(4242, 7))
			if got := stateOf(t, dir); got.State != "working" {
				t.Fatalf("failed call left stale tracking: %+v", got)
			}
		})
	}
}

func TestParallelToolsKeepTheAwaitedApproval(t *testing.T) {
	for _, agent := range []string{"", "a06151abf02cc81f9"} {
		for _, event := range []string{PreToolUse, PostToolUse} {
			for _, other := range []struct{ name, agent, tool, input string }{
				{"other tool", agent, "Read", `{"file_path":"/tmp/file"}`},
				{"other command", agent, "Bash", `{"command":"pwd"}`},
				{"other agent", "a11fbd18b849cf8c5", "Bash", `{"command":"curl example.com","timeout":1000}`},
			} {
				t.Run(agent+"/"+event+"/"+other.name, func(t *testing.T) {
					dir := agentsDir(t)
					asked := toolPayload(t, agent, "Bash", `{"command":"curl example.com","timeout":1000}`)
					hook(t, dir, PermissionRequest, asked, live(4242, 7))
					c := HookCall{Dir: dir, Agent: agentID, Event: event, Now: t0.Add(time.Minute),
						Input: strings.NewReader(toolPayload(t, other.agent, other.tool, other.input))}
					if err := Hook(c); err != nil {
						t.Fatal(err)
					}
					if got := stateOf(t, dir); got.State != "approval" || !got.Changed.Equal(t0) {
						t.Fatalf("unrelated tool hid or restarted the wait: %+v", got)
					}
					// JSON key order and whitespace are not a new tool call.
					hook(t, dir, PostToolUse, toolPayload(t, agent, "Bash", `{ "timeout":1000, "command":"curl example.com" }`), live(4242, 7))
					if got := stateOf(t, dir); got.State != "working" {
						t.Fatalf("the awaited tool completed: %+v", got)
					}
				})
			}
		}
	}
}

func TestStartingTheAwaitedToolDoesNotAnswerItsPrompt(t *testing.T) {
	dir := agentsDir(t)
	asked := toolPayload(t, "", "Bash", `{"command":"curl example.com"}`)
	hook(t, dir, PermissionRequest, asked, live(4242, 7))
	hook(t, dir, PreToolUse, asked, live(4242, 7))
	if got := stateOf(t, dir); got.State != "approval" {
		t.Fatalf("PreToolUse is before permission is granted: %+v", got)
	}
}

func TestParallelApprovalsAndQuestionsResolveSeparately(t *testing.T) {
	for _, agent := range []string{"", "a06151abf02cc81f9"} {
		t.Run(agent, func(t *testing.T) {
			dir := agentsDir(t)
			first := toolPayload(t, "", "Bash", `{"command":"first"}`)
			second := toolPayload(t, agent, "Bash", `{"command":"second"}`)
			question := toolPayload(t, agent, "AskUserQuestion", `{"questions":[]}`)
			for _, input := range []string{first, second, question} {
				hook(t, dir, PermissionRequest, input, live(4242, 7))
			}
			for _, c := range []struct{ input, want string }{
				{first, "approval"}, {second, "waiting"}, {question, "working"},
			} {
				hook(t, dir, PostToolUse, c.input, live(4242, 7))
				if got := stateOf(t, dir); got.State != c.want {
					t.Fatalf("after %s: %+v, want %s", c.input, got, c.want)
				}
			}
		})
	}
}

func TestPermissionNotificationsWaitForATurnBoundary(t *testing.T) {
	for _, end := range []struct{ event, input, want string }{
		{Stop, `{}`, "waiting"},
		{UserPromptSubmit, `{}`, "working"},
		{SessionStart, `{"source":"clear"}`, "waiting"},
		{SessionEnd, `{}`, "ended"},
	} {
		t.Run(end.event, func(t *testing.T) {
			dir := agentsDir(t)
			hook(t, dir, UserPromptSubmit, `{}`, live(4242, 7))
			hook(t, dir, "Notification", `{"notification_type":"permission_prompt","message":"Allow network access?"}`, live(4242, 7))
			for _, event := range []string{PreToolUse, PostToolUse} {
				hook(t, dir, event, toolPayload(t, "", "Read", `{"file_path":"/tmp/file"}`), live(4242, 7))
				if got := stateOf(t, dir); got.State != "approval" {
					t.Fatalf("a notification has no tool identity: %+v", got)
				}
			}
			hook(t, dir, SessionStart, `{"source":"compact"}`, live(4242, 7))
			hook(t, dir, Stop, `{"background_tasks":[{"status":"running"}]}`, live(4242, 7))
			if got := stateOf(t, dir); got.State != "approval" {
				t.Fatalf("compaction or background work lost the prompt: %+v", got)
			}
			hook(t, dir, end.event, end.input, live(4242, 7))
			if got := stateOf(t, dir); got.State != end.want {
				t.Fatalf("turn boundary: %+v, want %s", got, end.want)
			}
		})
	}
}

func TestOtherNotificationsLeaveTheStateFileUntouched(t *testing.T) {
	for _, input := range []string{
		`{"notification_type":"idle_prompt"}`, `{"notification_type":"auth_success"}`,
		`{"notification_type":"elicitation_dialog"}`, `{"notification_type":"Permission_Prompt"}`,
		`{}`, `null`, `{`, `{"notification_type":42}`, "",
	} {
		t.Run(input, func(t *testing.T) {
			dir := agentsDir(t)
			hook(t, dir, PermissionRequest, toolPayload(t, "", "Bash", `{"command":"private"}`), live(4242, 7))
			path := filepath.Join(dir, agentID+stateSuffix)
			before, err := os.ReadFile(path) //nolint:gosec // the test's file
			if err != nil {
				t.Fatal(err)
			}
			hook(t, dir, "Notification", input, live(4242, 7))
			after, err := os.ReadFile(path) //nolint:gosec // the test's file
			if err != nil || string(before) != string(after) {
				t.Fatalf("unrelated notification changed the state: %s, %v", after, err)
			}
		})
	}
}

func TestNotificationBesideAKnownRequestKeepsItsOwnWait(t *testing.T) {
	dir := agentsDir(t)
	asked := toolPayload(t, "", "Bash", `{"command":"first"}`)
	hook(t, dir, PermissionRequest, asked, live(4242, 7))
	hook(t, dir, Notification, `{"notification_type":"permission_prompt"}`, live(4242, 7))
	hook(t, dir, PostToolUse, asked, live(4242, 7))
	if got := stateOf(t, dir); got.State != "approval" {
		t.Fatalf("a separate network prompt may still be open: %+v", got)
	}
}

func TestCompactionKeepsTheAwaitedCall(t *testing.T) {
	dir := agentsDir(t)
	asked := toolPayload(t, "a123", "Bash", `{"command":"first"}`)
	call := toolPayload(t, "a123", "Bash", `{"command":"first"}`, "toolu_first")
	hook(t, dir, PreToolUse, call, live(4242, 7))
	hook(t, dir, PermissionRequest, asked, live(4242, 7))
	hook(t, dir, SessionStart, `{"source":"compact"}`, live(4242, 7))
	hook(t, dir, PostToolUse, toolPayload(t, "a123", "Read", `{}`, "toolu_read"), live(4242, 7))
	if got := stateOf(t, dir); got.State != "approval" {
		t.Fatalf("compaction lost the request: %+v", got)
	}
	hook(t, dir, PostToolUse, call, live(4242, 7))
	if got := stateOf(t, dir); got.State != "working" {
		t.Fatalf("compaction lost the call's identity: %+v", got)
	}
}

func TestUnknownAndLegacyRequestsStayUntilTheTurnEnds(t *testing.T) {
	for _, input := range []string{
		`{}`, `{"tool_name":"Bash"}`, `{"tool_name":"Bash","tool_input":null}`,
		`{"tool_name":"Bash","tool_input":[]}`, `{"agent_id":"bad id","tool_name":"Bash","tool_input":{}}`,
	} {
		dir := agentsDir(t)
		hook(t, dir, PermissionRequest, input, live(4242, 7))
		hook(t, dir, PostToolUse, toolPayload(t, "", "Bash", `{}`), live(4242, 7))
		if got := stateOf(t, dir); got.State != "approval" {
			t.Fatalf("unknown request %s disappeared: %+v", input, got)
		}
	}
	for _, state := range []string{"approval", "waiting"} {
		dir := agentsDir(t)
		old := stateRecord{Format: FormatVersion, State: state, Changed: t0, Waiter: "a123"}
		if err := replace(dir, filepath.Join(dir, agentID+stateSuffix), old); err != nil {
			t.Fatal(err)
		}
		hook(t, dir, PostToolUse, toolPayload(t, "a123", "Bash", `{}`), live(4242, 7))
		if got := stateOf(t, dir); got.State != state {
			t.Fatalf("legacy request disappeared: %+v", got)
		}
		hook(t, dir, UserPromptSubmit, `{}`, live(4242, 7))
		if got := stateOf(t, dir); got.State != "working" {
			t.Fatalf("new turn kept the legacy wait: %+v", got)
		}
	}
}

func TestToolArgumentsKeepLargeIntegersDistinct(t *testing.T) {
	dir := agentsDir(t)
	asked := toolPayload(t, "", "MCP", `{"id":9007199254740992}`)
	hook(t, dir, PermissionRequest, asked, live(4242, 7))
	hook(t, dir, PostToolUse, toolPayload(t, "", "MCP", `{"id":9007199254740993}`), live(4242, 7))
	if got := stateOf(t, dir); got.State != "approval" {
		t.Fatalf("different integer arguments matched: %+v", got)
	}
	hook(t, dir, PostToolUse, asked, live(4242, 7))
	if got := stateOf(t, dir); got.State != "working" {
		t.Fatalf("matching integer arguments did not resolve: %+v", got)
	}
}

func TestTrackingIsBoundedWithoutDroppingApprovals(t *testing.T) {
	for _, count := range []int{maxPending - 1, maxPending, maxPending + 1} {
		dir := agentsDir(t)
		// Fill the active-call table as well as the pending requests.
		for i := range maxTools {
			hook(t, dir, PreToolUse, toolPayload(t, "", "Bash", `{"command":"active`+strconv.Itoa(i)+`"}`, "toolu_"+strconv.Itoa(i)), live(4242, 7))
		}
		for i := range count {
			hook(t, dir, PermissionRequest, toolPayload(t, "", "Bash", `{"command":"pending`+strconv.Itoa(i)+`"}`), live(4242, 7))
		}
		data, err := os.ReadFile(filepath.Join(dir, agentID+stateSuffix)) //nolint:gosec // the test's file
		if err != nil || len(data) > maxFile {
			t.Fatalf("%d requests produced %d bytes, limit %d: %v", count, len(data), maxFile, err)
		}
		if got := stateOf(t, dir); got.State != "approval" {
			t.Fatalf("request limit hid a prompt: %+v", got)
		}
	}
	for _, count := range []int{maxTools - 1, maxTools, maxTools + 1} {
		dir := agentsDir(t)
		for i := range count {
			hook(t, dir, PreToolUse, toolPayload(t, "", "Bash", `{"command":"same"}`, "toolu_"+strconv.Itoa(i)), live(4242, 7))
		}
		hook(t, dir, PermissionRequest, toolPayload(t, "", "Bash", `{"command":"same"}`), live(4242, 7))
		for i := range count {
			hook(t, dir, PostToolUse, toolPayload(t, "", "Bash", `{"command":"same"}`, "toolu_"+strconv.Itoa(i)), live(4242, 7))
		}
		if got := stateOf(t, dir); got.State != "approval" {
			t.Fatalf("ambiguous or untracked tool cleared a prompt: %+v", got)
		}
		hook(t, dir, Stop, `{}`, live(4242, 7))
		if got := stateOf(t, dir); got.State != "waiting" {
			t.Fatalf("bounded tracking did not recover at turn end: %+v", got)
		}
	}
}

func TestTrackedToolsNeverStoreTheirArguments(t *testing.T) {
	dir := agentsDir(t)
	const args = `{"command":"SECRET-COMMAND","file_path":"/private/SECRET-PATH","question":"SECRET-QUESTION"}`
	for _, event := range []string{PreToolUse, PermissionRequest} {
		hook(t, dir, event, toolPayload(t, "", "Bash", args, "toolu_secret"), live(4242, 7))
		data, err := os.ReadFile(filepath.Join(dir, agentID+stateSuffix)) //nolint:gosec // the test's file
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"SECRET", "/private", "toolu_secret", "Bash"} {
			if strings.Contains(string(data), secret) {
				t.Fatalf("%s persisted %q: %s", event, secret, data)
			}
		}
	}
}
