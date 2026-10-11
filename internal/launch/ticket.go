package launch

// Ticket is the address of the ticket shown when the key was pressed: its
// project, its id and its number, as the server sent them. They are checked
// before they go anywhere (checkTicket); nothing else of the ticket is
// handed over.
type Ticket struct {
	Project, ID string
	Number      int
}
