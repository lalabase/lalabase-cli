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

// lockFor is how long a hook tries for its agent's lock (HookCall.LockFor
// changes it for a test about the lock, not the time); lockStep how long it
// waits between tries. A hook has 5 s in all and must end well before.
const (
	lockFor  = 300 * time.Millisecond
	lockStep = 5 * time.Millisecond
)

// The values a hook takes of its foreign input, compared to these.
const (
	toolAskQuestion  = "AskUserQuestion"
	taskRunning      = "running"
	sourceCompact    = "compact"
	permissionPrompt = "permission_prompt"
)

// maxPending and maxTools keep the state file below maxFile (4 KiB) with
// many simultaneous requests: a pending request is about 100 bytes of
// JSON, a running tool about 220 (three 64-digit hashes), the rest about
// 250, so 16 and 8 make about 3.6 KiB. Raising either can push the file
// past maxFile, and readState then reads it as no state at all, which
// loses the open requests (TestTrackingIsBoundedWithoutDroppingApprovals
// holds the sum). Overflow becomes one unidentified approval, never a lost prompt.
const (
	maxPending = 16
	maxTools   = 8
)

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
	// Procs reads a process's start time, recorded with PID so the
	// reader can tell a lost agent from a live one; nil keeps the pid the
	// state had.
	Procs Procs
	// LockFor is how long to try for the agent's lock; 0 is lockFor.
	LockFor time.Duration
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

// facts are the values taken of the input, each checked. Input that is
// missing, too large or no JSON object of these fields gives the zero
// facts, and then the event alone decides.
type facts struct {
	session string
	// subagent: the event comes from a subagent (an agent_id that is not
	// empty), which runs beside the session; agent is its id when it has
	// subagentID's form, else "".
	subagent bool
	agent    string
	// The hashes of the tool's input, name and call id (identity), each
	// scoped to the session and the subagent; "" when the event has none,
	// or comes from a subagent whose id does not pass its check (then
	// nothing of it can be told apart from another subagent's).
	inputHash, nameHash, callHash string
	permission                    bool
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
	var f facts
	f.subagent = in.AgentID != ""
	f.ask = in.ToolName == toolAskQuestion
	f.restarted = restart(in.Source)
	f.compacted = in.Source == sourceCompact
	f.restartEnd = restart(in.Reason)
	f.permission = in.NotificationType == permissionPrompt
	if serverID.MatchString(in.SessionID) {
		f.session = in.SessionID
	}
	if subagentID.MatchString(in.AgentID) {
		f.agent = in.AgentID
	}
	// A subagent whose id fails its check cannot be told apart from
	// another: none of its tools gets a hash.
	if identified := !f.subagent || f.agent != ""; identified {
		f.inputHash = inputIdentity(in, f.session, f.agent)
		if in.ToolName != "" {
			f.nameHash = identity([]string{f.session, f.agent, in.ToolName})
		}
		if toolUseID.MatchString(in.ToolUseID) {
			f.callHash = identity([]string{f.session, f.agent, in.ToolUseID})
		}
	}
	for _, t := range in.BackgroundTasks {
		if t.Status == taskRunning {
			f.running++
		}
	}
	return f
}

// inputIdentity is the hash of a tool's call by its input, for a
// PermissionRequest, which has no tool_use_id. Canonical JSON lets its
// input match PostToolUse despite key order or whitespace; UseNumber
// avoids rounding distinct integer arguments into the same identity.
func inputIdentity(in input, session, agent string) string {
	if in.ToolName == "" {
		return ""
	}
	var args map[string]any
	d := json.NewDecoder(bytes.NewReader(in.ToolInput))
	d.UseNumber()
	if err := d.Decode(&args); err != nil || args == nil {
		return ""
	}
	return identity([]any{session, agent, in.ToolName, args})
}

// identity is the SHA-256 of a value's JSON. The three hashes cannot meet
// by accident: each is compared only with its own kind (an input hash with
// input hashes, and so on), and the input's has four parts, not three.
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
	if !slices.Contains(Events, c.Event) {
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
	wait := c.LockFor
	if wait <= 0 {
		wait = lockFor
	}
	unlock, err := lock(filepath.Join(c.Dir, c.Agent+lockSuffix), wait)
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(c.Dir, c.Agent+stateSuffix)
	old, _ := readState(path)
	// The state is decided in this order, each step over the one before:
	//  1. the event's own state (next);
	//  2. a compaction keeps the state there was (keep), which must come
	//     before 3, since a compaction keeps the open requests too;
	//  3. an open request outranks the event: a prompt stays open while a
	//     parallel tool ends, so the state is approval or, for questions
	//     alone, waiting;
	//  4. without one, a background subagent's tool cannot end the main
	//     session's waiting: its turn ended, the person still has it.
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

// pendingAfter is the open requests after event. It works on a copy:
// Hook reads the old ones afterwards.
func pendingAfter(event string, f facts, old stateRecord) []pendingWait {
	pending := slices.Clone(old.Pending)
	if len(pending) == 0 && (old.State == stateNames[WaitingForApproval] || old.Waiter != "") {
		// A record from before requests were tracked (Waiter, #35) has an
		// open request but no hash of it: it is kept unidentified, so it
		// waits for the next turn boundary instead of being dropped.
		pending = []pendingWait{{Question: old.State == stateNames[WaitingForYou]}}
	}
	switch event {
	case PermissionRequest:
		if len(pending) >= maxPending {
			return []pendingWait{{}}
		}
		wait := pendingWait{Question: f.ask}
		if !old.Untracked && f.inputHash != "" {
			wait.Key, wait.ByID = requestKey(old.Tools, f)
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
			key := f.inputHash
			if wait.ByID {
				key = f.callHash
			}
			if key != "" && wait.Key == key {
				return slices.Delete(pending, i, i+1)
			}
		}
	case PreToolUse:
		// PreToolUse precedes permission checks, so it answers no prompt.
		for i, wait := range pending {
			if !wait.ByID && (f.inputHash == "" || wait.Key == f.inputHash) {
				// A request observed without its PreToolUse can no longer
				// be matched by arguments if an identical call starts.
				pending[i].Key = ""
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

// requestKey is how a PermissionRequest is told by its tool's call:
// through the one running call of that tool (by its call hash), by its
// input when none of that tool runs, or not at all.
func requestKey(running []toolCall, f facts) (key string, byID bool) {
	var sameName []toolCall
	for _, call := range running {
		if call.NameHash == f.nameHash {
			sameName = append(sameName, call)
		}
	}
	switch {
	case len(sameName) == 0:
		// No PreToolUse of this tool seen: by its input.
		return f.inputHash, false
	case len(sameName) == 1 && sameName[0].InputHash == f.inputHash:
		// The one running call of this tool, with this input.
		return sameName[0].CallHash, true
	default:
		// Two calls of the same tool, or one whose input another
		// PreToolUse hook rewrote before the permission check: inputs
		// alone cannot tell them apart. Unidentified.
		return "", false
	}
}

// toolsAfter is the running tools after event, and whether one of them
// could not be tracked (then no request is matched by them). It works on a
// copy, as pendingAfter does.
func toolsAfter(event string, f facts, old stateRecord) ([]toolCall, bool) {
	tools, untracked := slices.Clone(old.Tools), old.Untracked
	switch event {
	case PreToolUse:
		if f.callHash == "" || f.inputHash == "" {
			return tools, true
		}
		for _, call := range tools {
			if call.CallHash == f.callHash {
				return tools, untracked
			}
		}
		if len(tools) >= maxTools {
			return tools, true
		}
		tools = append(tools, toolCall{CallHash: f.callHash, InputHash: f.inputHash, NameHash: f.nameHash})
	case PostToolUse, PostToolUseFailure:
		for i, call := range tools {
			if call.CallHash == f.callHash {
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

// lock takes the agent's lock, trying for wait: a hook of the same agent
// may hold it a moment.
func lock(path string, wait time.Duration) (func(), error) {
	deadline := time.Now().Add(wait)
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
	var r stateRecord
	if err := readOwn(path, &r); err != nil {
		return stateRecord{}, err
	}
	if _, ok := stateNamed(r.State); r.Format != FormatVersion || !ok || r.Changed.IsZero() {
		return stateRecord{}, errors.New("not a state of this format")
	}
	// Each field that fails its check is dropped in the way that loses
	// the least: a session id is forgotten (the next hook brings one); a
	// request keeps standing unidentified, so an open approval is never
	// lost; a running tool that fails throws away all of them and marks
	// tracking off, since matching on half of them could match wrongly.
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
		if !toolHash.MatchString(r.Pending[i].Key) {
			r.Pending[i].Key = ""
		}
	}
	if len(r.Tools) > maxTools {
		return stateRecord{}, errors.New("too many active tools")
	}
	for _, call := range r.Tools {
		if !toolHash.MatchString(call.InputHash) || !toolHash.MatchString(call.CallHash) || !toolHash.MatchString(call.NameHash) {
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
