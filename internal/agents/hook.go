package agents

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
	"github.com/gboyke/lalabase-cli/internal/safefile"
)

// The events the launch registers a hook for, as Claude Code names them.
// SubagentStop and PreCompact mean nothing for the state and get none.
const (
	SessionStart       = "SessionStart"
	UserPromptSubmit   = "UserPromptSubmit"
	PreToolUse         = "PreToolUse"
	PostToolUse        = "PostToolUse"
	PostToolUseFailure = "PostToolUseFailure"
	PermissionRequest  = "PermissionRequest"
	Notification       = "Notification"
	Stop               = "Stop"
	SessionEnd         = "SessionEnd"
)

// Events are the hooks the settings file registers.
var Events = []string{SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest, Notification, Stop, SessionEnd}

// MaxInput bounds a hook's input: a larger one is not read at all.
const MaxInput = 1 << 20

// lockFor is how long a hook tries for its agent's lock (a test that is
// about the lock, not the time, makes it longer); lockStep how long it
// waits between tries. A hook has 5 s in all and must end well before.
var lockFor = 300 * time.Millisecond

const lockStep = 5 * time.Millisecond

// The values a hook takes of its foreign input, compared to these.
const (
	toolAskQuestion  = "AskUserQuestion"
	taskRunning      = "running"
	sourceCompact    = "compact"
	permissionPrompt = "permission_prompt"
)

// Keep the state below maxFile even with many simultaneous requests.
// Overflow becomes an unidentified approval, never a lost prompt.
const maxPending = 16
const maxTools = 8

var toolHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var toolUseID = regexp.MustCompile(`^[0-9A-Za-z_-]{1,128}$`)

// restarts are what replaces the session in the same process (/clear,
// /resume, /branch): a SessionEnd with one of them as its reason changes
// nothing, a SessionStart with one of them as its source waits for the
// person, since no Stop follows it.
var restarts = []string{"clear", "resume", "fork"}

func restart(s string) bool { return slices.Contains(restarts, s) }

// pidForm is CLAUDE_PID: a positive decimal number without a sign or a
// leading zero.
var pidForm = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

// maxPID bounds a pid: a positive 32-bit integer (pid_t).
const maxPID = 1<<31 - 1

// HookCall is one call of `lalabase hook`: the agents directory and the
// event from its command line, the agent's id, the input on stdin (nil when
// there is none to read) and CLAUDE_PID as the environment has it.
type HookCall struct {
	Dir, Event, Agent string
	Input             io.Reader
	PID               string
	Now               time.Time
	Procs             Procs
}

// input is all a hook decodes of its foreign input. Every other field is
// left unread by the decoder.
type input struct {
	SessionID        string          `json:"session_id"`
	AgentID          string          `json:"agent_id"`
	Source           string          `json:"source"`
	Reason           string          `json:"reason"`
	ToolName         string          `json:"tool_name"`
	ToolInput        json.RawMessage `json:"tool_input"`
	ToolUseID        string          `json:"tool_use_id"`
	NotificationType string          `json:"notification_type"`
	BackgroundTasks  []struct {
		Status string `json:"status"`
	} `json:"background_tasks"`
}

// facts are the values taken of the input, each checked; known is
// false when the input was missing, too large or no JSON object of these
// fields, and then only the event decides.
type facts struct {
	known   bool
	session string
	// subagent: the event comes from a subagent (an agent_id that is not
	// empty), which runs beside the session; agent is its id when it has
	// subagentID's form, else "".
	subagent   bool
	agent      string
	tool       string
	name       string
	call       string
	permission bool
	// restarted: SessionStart after /clear, /resume or /branch, which no
	// Stop follows; compacted: SessionStart after a compaction, by hand
	// or in the middle of a turn, which changes no state; restartEnd: a
	// SessionEnd that one of the restarts follows.
	restarted, compacted, restartEnd bool
	ask                              bool
	running                          int
}

func readFacts(r io.Reader) facts {
	if r == nil {
		return facts{}
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxInput+1))
	if err != nil || len(data) > MaxInput {
		return facts{}
	}
	var in input
	if err := json.Unmarshal(data, &in); err != nil {
		return facts{}
	}
	f := facts{
		known: true, subagent: in.AgentID != "", ask: in.ToolName == toolAskQuestion,
		restarted: restart(in.Source), compacted: in.Source == sourceCompact, restartEnd: restart(in.Reason),
		permission: in.NotificationType == permissionPrompt,
	}
	if serverID.MatchString(in.SessionID) {
		f.session = in.SessionID
	}
	if subagentID.MatchString(in.AgentID) {
		f.agent = in.AgentID
	}
	f.tool = toolIdentity(in, f)
	if in.ToolName != "" && (!f.subagent || f.agent != "") {
		f.name = identity([]string{f.session, f.agent, in.ToolName})
	}
	if toolUseID.MatchString(in.ToolUseID) && (!f.subagent || f.agent != "") {
		f.call = identity([]string{f.session, f.agent, in.ToolUseID})
	}
	for _, t := range in.BackgroundTasks {
		if t.Status == taskRunning {
			f.running++
		}
	}
	return f
}

// PermissionRequest has no tool_use_id. Canonical JSON lets its input
// match PostToolUse despite key order or whitespace. UseNumber avoids
// rounding distinct integer arguments into the same identity.
func toolIdentity(in input, f facts) string {
	if in.ToolName == "" || (f.subagent && f.agent == "") {
		return ""
	}
	var args map[string]any
	d := json.NewDecoder(bytes.NewReader(in.ToolInput))
	d.UseNumber()
	if err := d.Decode(&args); err != nil || args == nil {
		return ""
	}
	return identity([]any{f.session, f.agent, in.ToolName, args})
}

func identity(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// change is what an event does to the state.
type change uint8

const (
	// nothing: the state file stays as it is.
	nothing change = iota
	// set: the event's state.
	set
	// keep: the state and its time stay; the session id and the pid are
	// taken.
	keep
)

// next is the state an event leads to and what it does. A SessionEnd that
// /clear, /resume or /branch follows changes nothing; the SessionStart
// after it waits for the person (no Stop follows). A compaction keeps the
// state: by hand it follows a turn's end, by Claude Code it comes in the
// middle of one. Without the input's facts a SessionStart works, a Stop
// waits for the person, a PermissionRequest for an approval, and a
// SessionEnd ends.
func next(event string, f facts) (State, change) {
	switch event {
	case SessionStart:
		switch {
		case f.compacted:
			return Working, keep
		case f.restarted:
			return WaitingForYou, set
		}
		return Working, set
	case UserPromptSubmit, PreToolUse, PostToolUse, PostToolUseFailure:
		return Working, set
	case PermissionRequest:
		if f.ask {
			return WaitingForYou, set
		}
		return WaitingForApproval, set
	case Notification:
		if f.permission {
			return WaitingForApproval, set
		}
		return 0, nothing
	case Stop:
		if f.running > 0 {
			return Working, set
		}
		return WaitingForYou, set
	case SessionEnd:
		if f.restartEnd {
			return 0, nothing
		}
		return Ended, set
	}
	return 0, nothing
}

// Hook records the state an event leads to. It checks the call (the event
// one of Events, the agent's id, the directory there and private), takes
// the agent's lock, reads the state the last hook left, and replaces it
// whole. It writes nothing on any failure; the caller ignores the error.
func Hook(c HookCall) error {
	if !ValidID(c.Agent) {
		return errors.New("no agent id")
	}
	known := false
	for _, e := range Events {
		known = known || e == c.Event
	}
	if !known {
		return errors.New("an event the launch registers no hook for")
	}
	if err := existing(c.Dir); err != nil {
		return err
	}
	f := readFacts(c.Input)
	state, does := next(c.Event, f)
	if does == nothing {
		return nil
	}
	unlock, err := lock(filepath.Join(c.Dir, c.Agent+lockSuffix))
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(c.Dir, c.Agent+stateSuffix)
	old, _ := readState(path)
	pending := pendingAfter(c.Event, f, old)
	if does == keep && old.State != "" {
		// A compaction keeps both the state and its pending requests.
		state, _ = stateNamed(old.State)
	}
	if len(pending) > 0 {
		state = WaitingForYou
		for _, wait := range pending {
			if !wait.Question {
				state = WaitingForApproval
				break
			}
		}
	} else if subagentTool(c.Event, f) && old.State == stateNames[WaitingForYou] && len(old.Pending) == 0 {
		// A background tool cannot answer the main session's ended turn.
		state = WaitingForYou
	}
	tools, untracked := toolsAfter(c.Event, f, old)
	rec := stateRecord{Format: FormatVersion, State: stateNames[state], Changed: c.Now.UTC(), Session: old.Session, PID: old.PID, Start: old.Start, Pending: pending, Tools: tools, Untracked: untracked}
	if old.State == rec.State && !old.Changed.IsZero() {
		// The same state goes on: its age counts from when it began.
		rec.Changed = old.Changed
	}
	if f.session != "" {
		rec.Session = f.session
	}
	if pid, ok := parsePID(c.PID); ok && c.Procs != nil {
		// The start time is taken while the process surely lives: it
		// called this hook. A pid whose process cannot be read keeps what
		// the state had.
		if start, alive, err := c.Procs.Start(pid); err == nil && alive {
			rec.PID, rec.Start = pid, start
		}
	}
	return replace(c.Dir, path, rec)
}

func pendingAfter(event string, f facts, old stateRecord) []pendingWait {
	pending := old.Pending
	if len(pending) == 0 && (old.State == stateNames[WaitingForApproval] || old.Waiter != "") {
		// Old records have no tool identity. Treat them conservatively.
		pending = []pendingWait{{Question: old.State == stateNames[WaitingForYou]}}
	}
	switch event {
	case PermissionRequest:
		if len(pending) >= maxPending {
			return []pendingWait{{}}
		}
		wait := pendingWait{Question: f.ask}
		if !old.Untracked && f.tool != "" {
			wait.Tool = f.tool
			matches, candidates := 0, 0
			for _, call := range old.Tools {
				if call.Name == f.name {
					candidates++
				}
				if call.Tool == f.tool {
					matches++
					wait.Tool, wait.ByID = call.ID, true
				}
			}
			if candidates > 1 || (candidates == 1 && matches == 0) {
				// Other PreToolUse hooks can rewrite arguments before
				// permission checks. Parallel calls of the same tool
				// cannot safely be distinguished by their inputs alone.
				wait.Tool, wait.ByID = "", false
			}
		}
		return append(pending, wait)
	case Notification:
		// A notification may repeat a tool prompt or describe a separate
		// network prompt. Without an identity neither can be ruled out.
		if !slices.Contains(pending, pendingWait{}) {
			if len(pending) >= maxPending {
				return []pendingWait{{}}
			}
			pending = append(pending, pendingWait{})
		}
	case PostToolUse, PostToolUseFailure:
		for i, wait := range pending {
			key := f.tool
			if wait.ByID {
				key = f.call
			}
			if key != "" && wait.Tool == key {
				return slices.Delete(pending, i, i+1)
			}
		}
	case PreToolUse:
		// PreToolUse precedes permission checks, so it answers no prompt.
		for i, wait := range pending {
			if !wait.ByID && (f.tool == "" || wait.Tool == f.tool) {
				// A request observed without its PreToolUse can no longer
				// be matched by arguments if an identical call starts.
				pending[i].Tool = ""
			}
		}
	case SessionStart:
		if !f.compacted {
			return nil
		}
	case Stop:
		if f.running == 0 {
			return nil
		}
	default:
		return nil
	}
	return pending
}

func toolsAfter(event string, f facts, old stateRecord) ([]toolCall, bool) {
	tools, untracked := old.Tools, old.Untracked
	switch event {
	case PreToolUse:
		if f.call == "" || f.tool == "" {
			return tools, true
		}
		for _, call := range tools {
			if call.ID == f.call {
				return tools, untracked
			}
		}
		if len(tools) >= maxTools {
			return tools, true
		}
		tools = append(tools, toolCall{ID: f.call, Tool: f.tool, Name: f.name})
	case PostToolUse, PostToolUseFailure:
		for i, call := range tools {
			if call.ID == f.call {
				tools = slices.Delete(tools, i, i+1)
				break
			}
		}
	case PermissionRequest, Notification:
	case SessionStart:
		if !f.compacted {
			return nil, false
		}
	case Stop:
		if f.running == 0 {
			return nil, false
		}
	default:
		return nil, false
	}
	return tools, untracked
}

// subagentTool: a tool event of a subagent, not of the session.
func subagentTool(event string, f facts) bool {
	return f.subagent && (event == PreToolUse || event == PostToolUse || event == PostToolUseFailure)
}

func parsePID(s string) (int, bool) {
	if !pidForm.MatchString(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > maxPID {
		return 0, false
	}
	return n, true
}

// lock takes the agent's lock, trying for lockFor: a hook of the same
// agent may hold it a moment.
func lock(path string) (func(), error) {
	deadline := time.Now().Add(lockFor)
	for {
		unlock, err := privatefs.TryLock(path)
		if !errors.Is(err, privatefs.ErrLocked) || time.Now().After(deadline) {
			return unlock, err
		}
		time.Sleep(lockStep)
	}
}

// readState reads a state file as a private file of at most maxFile bytes;
// a missing or failing one is the zero record.
func readState(path string) (stateRecord, error) {
	data, info, err := safefile.ReadRegularNoLink(path, maxFile)
	if err != nil {
		return stateRecord{}, err
	}
	if err := privatefs.OwnFile(path, info); err != nil {
		return stateRecord{}, err
	}
	var r stateRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return stateRecord{}, err
	}
	if _, ok := stateNamed(r.State); r.Format != FormatVersion || !ok || r.Changed.IsZero() {
		return stateRecord{}, errors.New("not a state of this format")
	}
	if r.Session != "" && !serverID.MatchString(r.Session) {
		r.Session = ""
	}
	if r.Waiter != "" && !subagentID.MatchString(r.Waiter) {
		r.Waiter = ""
	}
	if len(r.Pending) > maxPending {
		return stateRecord{}, errors.New("too many pending requests")
	}
	for i := range r.Pending {
		if !toolHash.MatchString(r.Pending[i].Tool) {
			r.Pending[i].Tool = ""
		}
	}
	if len(r.Tools) > maxTools {
		return stateRecord{}, errors.New("too many active tools")
	}
	for _, call := range r.Tools {
		if !toolHash.MatchString(call.Tool) || !toolHash.MatchString(call.ID) || !toolHash.MatchString(call.Name) {
			r.Tools, r.Untracked = nil, true
			break
		}
	}
	if r.PID < 0 || r.PID > maxPID {
		r.PID, r.Start = 0, 0
	}
	return r, nil
}

// replace writes the record to a new temporary file in the directory,
// syncs and closes it and renames it over the state file: a reader sees
// the old state or the new one, never half of either.
func replace(dir, path string, r stateRecord) (err error) {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, tempPrefix+"*") // O_EXCL, mode 0600
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
