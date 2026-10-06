//go:build unix && !darwin

package profile

// checkACL has nothing to add on Linux and the BSDs: there an access list
// shows in the mode bits (the group bits hold its mask, the most any named
// entry may grant), and the mode checks see it.
func checkACL(string, aclZone) error { return nil }
