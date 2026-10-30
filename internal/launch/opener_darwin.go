package launch

// findOpener is the program that hands an address to the browser: a fixed
// path on macOS, so that nothing on the PATH takes its place.
func (l *Launcher) findOpener() (string, bool) { return "/usr/bin/open", true }
