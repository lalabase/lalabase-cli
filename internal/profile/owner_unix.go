//go:build unix

package profile

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// checkOwner refuses a config file that belongs to another user.
func checkOwner(info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != os.Getuid() {
		return errors.New("belongs to another user")
	}
	return nil
}
