package launch

// Ticket is the address of the ticket shown when the key was pressed: its
// project, its id and its number, as the server sent them. They are checked
// before they go anywhere (checkTicket); nothing else of the ticket is
// handed over. Org is its organisation, which only the agent's record
// keeps (C9a): without it the launch goes on without one.
type Ticket struct {
	Org, Project, ID string
	Number           int
}

// FileKind says what a file hangs on, and so which word its address holds
// (fileAddress). It is the client's own, set where a file is selected, never
// read from an answer. The zero value is no kind: a file without one opens
// nothing.
type FileKind uint8

const (
	// TicketFile hangs on the ticket (C10), CommentFile on a comment (C12).
	TicketFile FileKind = iota + 1
	CommentFile
)

// File is a file of a ticket or of one of its comments: its kind, its
// document's id as the server sent it, and its name, which only the notice
// shows. The name is foreign and never part of an address (fileAddress).
type File struct {
	Kind     FileKind
	ID, Name string
}

// PageKind says what a page in the core shows, and so which word its
// address holds (pageAddress). Like FileKind it is the client's own, set
// where w is pressed, never read from an answer; the zero value opens
// nothing.
type PageKind uint8

const (
	// TicketPage is a ticket's page, MeetingPage a meeting's.
	TicketPage PageKind = iota + 1
	MeetingPage
)

// Page is the ticket or the meeting the pane shows, for w: its kind, its
// organisation, its project and its id as the server sent them. They are
// checked before they become an address (pageAddress). Number is a
// ticket's, for the notice alone, 0 for a meeting; it is no part of the
// address.
type Page struct {
	Kind             PageKind
	Org, Project, ID string
	Number           int
}
