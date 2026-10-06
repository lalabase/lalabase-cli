//go:build darwin

package profile

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// On macOS an access control list can grant what the mode bits deny, and
// the mode bits do not show it (ls -le does). It is read with getattrlist,
// without cgo. Layout (sys/kauth.h, sys/attr.h), in the machine's byte
// order: the attribute buffer's length, an attrreference (offset from
// itself, length; length 0: no list), then a kauth_filesec: magic, owner
// and group GUIDs, entry count, flags, and per entry a GUID, flags and
// rights.
const (
	attrCmnExtendedSecurity = 0x400000
	fsoptNoFollow           = 0x1
	filesecMagic            = 0x012cc16d
	noACL                   = 0xffffffff
	aceKindMask             = 0xf
	acePermit               = 1
	aceSize                 = 24

	// Rights that let others change a directory on the way: add a file or
	// a directory, delete or rename entries, delete it, change its rights
	// or owner, or all of it.
	rightsToChange = 1<<2 | 1<<4 | 1<<5 | 1<<6 | 1<<12 | 1<<13 | 1<<21 | 1<<23
)

type attrlist struct {
	bitmapcount uint16
	reserved    uint16
	commonattr  uint32
	volattr     uint32
	dirattr     uint32
	fileattr    uint32
	forkattr    uint32
}

// checkACL refuses an access list that lets others in, per zone. An entry
// that denies is always fine (every home carries "everyone deny delete").
func checkACL(path string, zone aclZone) error {
	entries, err := readACL(path)
	if err != nil {
		return fmt.Errorf("%s: reading its access list: %w", path, err)
	}
	for _, e := range entries {
		if e.flags&aceKindMask != acePermit {
			continue
		}
		if zone == private || e.rights&rightsToChange != 0 {
			return fmt.Errorf("%s carries an access list that lets others in (see: ls -led %s); remove that entry with chmod -a", path, path)
		}
	}
	return nil
}

type ace struct{ flags, rights uint32 }

func readACL(path string) ([]ace, error) {
	p, err := unix.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	al := attrlist{bitmapcount: 5, commonattr: attrCmnExtendedSecurity}
	buf := make([]byte, 64<<10)
	// x/sys has no libSystem wrapper for getattrlist, so it is called
	// directly. Should a later macOS refuse that, the call fails and the
	// config is refused: closed, not open.
	_, _, errno := unix.Syscall6(unix.SYS_GETATTRLIST, //nolint:staticcheck // no libSystem wrapper exists, see above
		uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&al)), //nolint:gosec // the layout getattrlist expects
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), fsoptNoFollow, 0) //nolint:gosec // the layout getattrlist expects
	if errno != 0 {
		return nil, errno
	}
	return parseFilesec(buf)
}

// parseFilesec reads the attribute buffer of getattrlist.
func parseFilesec(buf []byte) ([]ace, error) {
	le := binary.NativeEndian
	if len(buf) < 12 {
		return nil, fmt.Errorf("short attribute buffer")
	}
	total := int(le.Uint32(buf[0:]))
	offset := int(int32(le.Uint32(buf[4:]))) //nolint:gosec // an offset from getattrlist
	length := int(le.Uint32(buf[8:]))
	if length == 0 {
		return nil, nil
	}
	start := 4 + offset
	if total > len(buf) || start < 12 || start+length > total || length < 44 {
		return nil, fmt.Errorf("unexpected access list layout")
	}
	sec := buf[start : start+length]
	if le.Uint32(sec[0:]) != filesecMagic {
		return nil, fmt.Errorf("unexpected access list magic")
	}
	count := le.Uint32(sec[36:])
	if count == noACL {
		return nil, nil
	}
	if int(count) > (len(sec)-44)/aceSize {
		return nil, fmt.Errorf("access list longer than its buffer")
	}
	entries := make([]ace, 0, count)
	for i := range int(count) {
		e := sec[44+i*aceSize:]
		entries = append(entries, ace{flags: le.Uint32(e[16:]), rights: le.Uint32(e[20:])})
	}
	return entries, nil
}
