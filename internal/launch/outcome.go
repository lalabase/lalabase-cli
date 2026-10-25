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
}
