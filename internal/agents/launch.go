package agents

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
)

// Launch is what the launch records of a new agent: the ticket's address
// as the server sent it (checked), the session id Claude Code started with,
// the iTerm pane's unique id ("" when unknown, or without iTerm) and the
// time.
type Launch struct {
	Agent, Organisation, Project, Ticket string
	Number                               int
	Session, Pane                        string
	At                                   time.Time
}

// maxNumber bounds a ticket's number: a positive 32-bit integer.
const maxNumber = 1<<31 - 1

func (l Launch) valid() bool {
	return ValidID(l.Agent) && ValidID(l.Session) &&
		serverID.MatchString(l.Organisation) && serverID.MatchString(l.Project) && serverID.MatchString(l.Ticket) &&
		TicketNumber(l.Number) && (l.Pane == "" || serverID.MatchString(l.Pane)) && !l.At.IsZero()
}

// WriteLaunch records a new agent in dir, which must exist (Prepare). The
// file is created exclusively, synced and checked; one that cannot be
// written whole is removed.
func WriteLaunch(dir string, l Launch) error {
	if !l.valid() {
		return errors.New("the agent's address did not pass its check")
	}
	if err := existing(dir); err != nil {
		return err
	}
	data, err := json.Marshal(launchRecord{
		Format: FormatVersion, Agent: l.Agent, Organisation: l.Organisation, Project: l.Project,
		Ticket: l.Ticket, Number: l.Number, Session: l.Session, Pane: l.Pane, At: l.At.UTC(),
	})
	if err != nil {
		return err
	}
	return createOwn(filepath.Join(dir, l.Agent+launchSuffix), data)
}

// createOwn writes a new file of the client's own in the agents
// directory (privatefs.CreateExclusive): the launch record and the seen
// mark.
func createOwn(path string, data []byte) error {
	return privatefs.CreateExclusive(path, data, 0o600)
}
