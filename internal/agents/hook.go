package agents

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
	"github.com/gboyke/lalabase-cli/internal/safefile"
)

// The events the launch registers a hook for, as Claude Code names them.
// SubagentStop, PreCompact and Notification mean nothing for the state and
// get none.
const (
	SessionStart      = "SessionStart"
	UserPromptSubmit  = "UserPromptSubmit"
	PreToolUse        = "PreToolUse"
	PostToolUse       = "PostToolUse"
	PermissionRequest = "PermissionRequest"
	Stop              = "Stop"
	SessionEnd        = "SessionEnd"
)

// Events are the seven, in the order a settings file lists them.
var Events = []string{SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, PermissionRequest, Stop, SessionEnd}

// MaxInput bounds a hook's input: a larger one is not read at all.
const MaxInput = 1 << 20

// lockFor is how long a hook tries for its agent's lock (a test that is
// about the lock, not the time, makes it longer); lockStep how long it
// waits between tries. A hook has 5 s in all and must end well before.
var lockFor = 300 * time.Millisecond

const lockStep = 5 * time.Millisecond

// The values a hook takes of its foreign input, compared to these.
const (
	reasonClear     = "clear"
	toolAskQuestion = "AskUserQuestion"
	taskRunning     = "running"
)

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
	SessionID       string `json:"session_id"`
	Reason          string `json:"reason"`
	ToolName        string `json:"tool_name"`
	BackgroundTasks []struct {
		Status string `json:"status"`
	} `json:"background_tasks"`
}

// facts are the four values taken of the input, each checked; known is
// false when the input was missing, too large or no JSON object of these
// fields, and then only the event decides.
type facts struct {
	known   bool
	session string
	clear   bool
	ask     bool
	running int
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
	f := facts{known: true, clear: in.Reason == reasonClear, ask: in.ToolName == toolAskQuestion}
	if serverID.MatchString(in.SessionID) {
		f.session = in.SessionID
	}
	for _, t := range in.BackgroundTasks {
		if t.Status == taskRunning {
			f.running++
		}
	}
	return f
}

// next is the state an event leads to, and false when it changes nothing
// (a SessionEnd of /clear, which a SessionStart follows). Without the
// input's facts a Stop waits for the person, a PermissionRequest for an
// approval, and a SessionEnd ends.
func next(event string, f facts) (State, bool) {
	switch event {
	case SessionStart, UserPromptSubmit, PreToolUse, PostToolUse:
		return Working, true
	case PermissionRequest:
		if f.ask {
			return WaitingForYou, true
		}
		return WaitingForApproval, true
	case Stop:
		if f.running > 0 {
			return Working, true
		}
		return WaitingForYou, true
	case SessionEnd:
		if f.known && f.clear {
			return 0, false
		}
		return Ended, true
	}
	return 0, false
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
	state, change := next(c.Event, f)
	if !change {
		return nil
	}
	unlock, err := lock(filepath.Join(c.Dir, c.Agent+lockSuffix))
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(c.Dir, c.Agent+stateSuffix)
	old, _ := readState(path)
	rec := stateRecord{Format: FormatVersion, State: stateNames[state], Changed: c.Now.UTC(), Session: old.Session, PID: old.PID, Start: old.Start}
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
