package agents

// Procs tells whether a process lives and when it started: a pid alone may
// name another process later (after kill -9 the system may reuse it), the
// pid with its start time names one. start is in the platform's own unit,
// only ever compared; err means the question could not be asked, which
// decides nothing.
type Procs interface {
	Start(pid int) (start int64, alive bool, err error)
}

// System is the processes of this machine.
var System Procs = systemProcs{}

type systemProcs struct{}
