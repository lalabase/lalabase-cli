package launch

// Kind is the outcome of one launch, or why there was none.
type Kind uint8

const (
	// OK is no outcome yet: a step that found what it looked for.
	OK Kind = iota
	// Opened: iTerm split the session and typed the command into the pane.
	Opened
	// Ended: Claude Code ran in this terminal and ended (the fallback).
	Ended
	// EndedWithError: it ended with an error, or could not be run.
	EndedWithError
	// NotHandedOver: the ticket's address did not pass its check.
	NotHandedOver
	// NoFolder, NotADirectory, BadFolder: the folder Claude Code would
	// start in is missing, no directory, or has a path no shell takes as
	// typed (detail: the path).
	NoFolder
	NotADirectory
	BadFolder
	// NoFiles: the launch's files (the server file, the script) could not
	// be written (detail: why).
	NoFiles
	// NoClaude: claude is not on the PATH (the fallback).
	NoClaude
	// NoPermission: macOS refused to let this terminal control iTerm
	// (error -1743).
	NoPermission
	// SessionGone: iTerm did not find the client's session.
	SessionGone
	// NoAnswer: iTerm did not answer within the deadline.
	NoAnswer
	// ITermFailed: iTerm refused for another reason.
	ITermFailed
	// Canceled: the session ended before the launch ran.
	Canceled
	// TmuxFailed: tmux could not start the agent's session.
	TmuxFailed
	// Jumped: iTerm selected the agent's pane (C9c).
	Jumped
	// PaneGone: the jump found the agent's pane in no window or tab of
	// iTerm (the script's own error 4201): it was closed.
	PaneGone
	// NoPane: there is no pane to jump to: the client runs without iTerm
	// (or in a multiplexer), or the agent's pane is not known.
	NoPane
	// FileOpened: the system took a file's address to the browser (C10).
	FileOpened
	// NoOpener: this machine cannot show a browser to the person: no
	// program for it, or the client runs over SSH.
	NoOpener
	// OpenFailed: the program refused, or did not end in time.
	OpenFailed
)

// Action is what was asked for: of x (C9c) a first start, the jump to a
// running agent's pane, or a start again of one whose session is over; of
// Enter on a file (C10) its opening.
type Action uint8

const (
	StartAction Action = iota
	JumpAction
	AgainAction
	OpenFileAction
)

// Done is the message of one launch: the caller's reference, the ticket it
// was for, what came of it and the value the status line names (a path, an
// error). Detail may hold a path from the person's config or an error of
// the file system: it is cleaned where it is shown.
type Done struct {
	Ref    uint64
	Ticket Ticket
	Kind   Kind
	Detail string
	// Untracked says why Claude Code runs without the client knowing its
	// state (C9a): no hooks could be handed over, or the agent could not be
	// recorded; "" when it is known. It may name a path: cleaned where it
	// is shown.
	Untracked string
	// Action is what was asked for. Agent is the agent a start recorded
	// ("" when it recorded none), or the one the jump was for; Replaces
	// the agent a start again replaces (C9c), whose files the launch does
	// not touch: the interface settles it (agents.Settle).
	Action          Action
	Agent, Replaces string
}
