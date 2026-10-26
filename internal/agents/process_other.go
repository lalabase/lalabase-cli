//go:build !darwin && !linux

package agents

// Start cannot ask about a process here: alive, start unknown, so an agent
// is never taken for lost. The release builds are macOS and Linux.
func (systemProcs) Start(int) (int64, bool, error) { return 0, true, nil }
