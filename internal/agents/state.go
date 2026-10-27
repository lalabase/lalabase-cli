package agents

import "time"

// State is what an agent does, in order of urgency: the first is the one a
// person most needs to know of, so of two agents at one ticket it wins.
type State uint8

const (
	// WaitingForApproval: Claude Code asks to use a tool.
	WaitingForApproval State = iota
	// WaitingForYou: the turn ended, or Claude Code asks a question.
	WaitingForYou
	// Lost: the process is gone without saying so, or its pid now belongs
	// to another process.
	Lost
	// Working: a turn runs.
	Working
	// Starting: launched, no hook yet (the folder's trust dialog comes
	// first).
	Starting
	// Ended: the session ended.
	Ended
)

// Urgent says whether s is more urgent than t.
func (s State) Urgent(t State) bool { return s < t }

// stateNames are the states a state file holds: the hook's four. Starting
// and Lost are the reader's alone.
var stateNames = map[State]string{
	WaitingForApproval: "approval",
	WaitingForYou:      "waiting",
	Working:            "working",
	Ended:              "ended",
}

func stateNamed(name string) (State, bool) {
	for s, n := range stateNames {
		if n == name {
			return s, true
		}
	}
	return 0, false
}

// launchRecord is <agent id>.launch.json.
type launchRecord struct {
	Format       int       `json:"format"`
	Agent        string    `json:"agent"`
	Organisation string    `json:"organisation"`
	Project      string    `json:"project"`
	Ticket       string    `json:"ticket"`
	Number       int       `json:"number"`
	Session      string    `json:"session"`
	Pane         string    `json:"pane"`
	At           time.Time `json:"at"`
}

// stateRecord is <agent id>.state.json. Start is the process's start time
// in the platform's own unit, compared, never shown; 0 when unknown (the
// reader compares it to what the same platform reads now, so 0 meets 0).
// Waiter is read for records a client before pending tools were tracked
// (#35) wrote, which an agent may still have on disk; it is never written
// and can go once no such record is left. FormatVersion stays 1: every
// field added since is optional and an older reader ignores it.
type stateRecord struct {
	Format    int           `json:"format"`
	State     string        `json:"state"`
	Changed   time.Time     `json:"changed"`
	Session   string        `json:"session"`
	PID       int           `json:"pid"`
	Start     int64         `json:"start"`
	Waiter    string        `json:"waiter,omitempty"`
	Pending   []pendingWait `json:"pending,omitempty"`
	Tools     []toolCall    `json:"tools,omitempty"`
	Untracked bool          `json:"untracked,omitempty"`
}

// pendingWait is one open request (an approval or a question). Key is a
// hash, never the text: the call's hash (callHash) when ByID, else the
// hash of its session, subagent, tool name and input (inputHash). No
// command, path or question is kept. An empty Key cannot be matched:
// notifications carry no tool, and older or unreadable requests may have
// none either; such a request waits for a turn boundary. The JSON names
// are the file's format and stay.
type pendingWait struct {
	Key      string `json:"tool,omitempty"`
	ByID     bool   `json:"by_id,omitempty"`
	Question bool   `json:"question,omitempty"`
}

// toolCall is one tool running since its PreToolUse, by three hashes: of
// its call id (CallHash), of its input (InputHash) and of its name
// (NameHash). A PermissionRequest carries no call id; through these it
// finds the one running call it belongs to (pendingAfter).
type toolCall struct {
	CallHash  string `json:"id"`
	InputHash string `json:"tool"`
	NameHash  string `json:"name"`
}
