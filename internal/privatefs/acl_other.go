//go:build unix && !darwin

package privatefs

// CheckACL has nothing to add on Linux and the BSDs: there an access list
// shows in the mode bits (the group bits hold its mask, the most any named
// entry may grant), and the mode checks see it.
func CheckACL(string, Zone) error { return nil }

// ACE is one entry of an access list; none is read here.
type ACE struct{ Flags, Rights uint32 }

// ReadACL reads nothing here (tests of the darwin reader replace it there).
var ReadACL = func(string) ([]ACE, error) { return nil, nil }
