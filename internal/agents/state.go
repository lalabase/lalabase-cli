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
// in the platform's own unit, compared, never shown; 0 when unknown.
// Waiter is read for records from before pending tools were tracked.
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

// pendingWait identifies a request by its scoped call-id hash (ByID) or
// by a hash of its session, subagent, tool name and input. No command,
// path or question is kept. An empty
// Tool cannot be matched: notifications carry no tool, and older or
// unreadable requests may have none either. They wait for a turn boundary.
type pendingWait struct {
	Tool     string `json:"tool,omitempty"`
	ByID     bool   `json:"by_id,omitempty"`
	Question bool   `json:"question,omitempty"`
}

// toolCall ties a PreToolUse's scoped call id to its tool/input hash so
// a PermissionRequest (which has no id) can find a unique active call.
type toolCall struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Name string `json:"name"`
}
