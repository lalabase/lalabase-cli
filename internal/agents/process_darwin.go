package agents

import "golang.org/x/sys/unix"

// zombie is SZOMB of p_stat (sys/proc.h): the process ended and waits for
// its parent; it is gone for the state.
const zombie = 5

// Start asks the kernel with sysctl kern.proc.pid: no entry means no such
// process. The start time is microseconds since the epoch.
func (systemProcs) Start(pid int) (int64, bool, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return 0, false, err
	}
	if len(procs) != 1 || int(procs[0].Proc.P_pid) != pid || procs[0].Proc.P_stat == zombie {
		return 0, false, nil
	}
	t := procs[0].Proc.P_starttime
	return t.Sec*1_000_000 + int64(t.Usec), true, nil
}
