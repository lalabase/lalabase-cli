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

// File is a file of a ticket: its document's id as the server sent it, and
// its name, which only the notice shows. The name is foreign and never
// part of an address (fileAddress).
type File struct {
	ID, Name string
}
