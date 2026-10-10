// Package privatefs holds the one threat model of the files the client
// keeps for itself: the config and its lock file (internal/profile) and the
// disk cache (internal/diskcache, C1e). It was extracted from the profile
// package so that the cache does not grow a second model beside it.
//
// The attacker is another local user without root, who can write only where
// it is writable for them and replace only entries they own. What the person
// or an admin set up is a misconfiguration, refused where it makes a file
// readable or replaceable by others and can be seen without cgo. Rules, from
// / down, links resolved as the kernel resolves them (CheckDirs):
//
//   - a directory passed through belongs to the user or root and is not
//     writable by group or others, unless sticky (as /tmp: others may add
//     entries there, but only replace their own);
//   - a link passed through belongs to the user or root: in a sticky
//     directory its owner may point it elsewhere at any time;
//   - the file's own directory belongs to the user and is writable by nobody
//     else, sticky or not (others could add a lock file of theirs);
//   - the file itself and its lock file belong to the user and give others
//     no right at all (OwnFile; reading is enough to hold a lock);
//   - on macOS access control lists are read with getattrlist (CheckACL):
//     an allow entry on a private file or directory is refused, further up
//     those that let others add, delete or rename; deny entries are fine.
//     On Linux an access list shows in the mode bits through its mask.
//
// Stop criterion, from the config's review rounds: a change takes only a
// finding that breaks this model with a concrete scenario. Root, network
// file systems that map user ids, firmlinks and the user's own processes
// between a check and an open are known limits, not exceptions.
package privatefs

// Zone says how strictly an access list is read.
type Zone int

const (
	// Passage: a directory on the way. An entry that lets others add,
	// delete or rename there, or change its rights, is refused; one that
	// only lets them look in is not.
	Passage Zone = iota
	// Private: the file's own directory, the file, its lock file. Any entry
	// that allows something to others is refused.
	Private
)
