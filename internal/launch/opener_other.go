//go:build !darwin

package launch

import "path/filepath"

// findOpener is the program that hands an address to the browser, from the
// PATH on these systems, its path absolute.
func (l *Launcher) findOpener() (string, bool) {
	path, err := l.lookPath("xdg-open")
	if err != nil || !filepath.IsAbs(path) {
		return "", false
	}
	return path, true
}
