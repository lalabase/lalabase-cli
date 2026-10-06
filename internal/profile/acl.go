package profile

// aclZone says how strictly an access list is read.
type aclZone int

const (
	// passage: a directory on the way. An entry that lets others add,
	// delete or rename there, or change its rights, is refused; one that
	// only lets them look in is not.
	passage aclZone = iota
	// private: the config's directory, the config, its lock file. Any entry
	// that allows something to others is refused.
	private
)
