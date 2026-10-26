package agents

import (
	"errors"
	"io/fs"
	"strconv"
	"strings"

	"github.com/gboyke/lalabase-cli/internal/safefile"
)

// Start reads /proc/<pid>/stat, a file the client does not control, through
// internal/safefile: no such file means no such process. The start time is
// the 22nd field, clock ticks since boot. The second field, the program's
// name in parentheses, may hold spaces and parentheses itself, so the
// fields are counted from the last ")".
func (systemProcs) Start(pid int) (int64, bool, error) {
	data, _, err := safefile.ReadRegular("/proc/"+strconv.Itoa(pid)+"/stat", maxFile)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return parseStat(string(data))
}

func parseStat(stat string) (int64, bool, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, false, errors.New("no program name in /proc stat")
	}
	// After the name: the state (field 3) ... the start time (field 22).
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 {
		return 0, false, errors.New("too few fields in /proc stat")
	}
	if fields[0] == "Z" || fields[0] == "X" {
		return 0, false, nil
	}
	start, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, false, err
	}
	return start, true, nil
}
