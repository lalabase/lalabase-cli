package cache

// The journal (C1e) is what the disk cache replays: every change of the
// memory store that is a decision about data, in the order it was taken,
// and nothing that is only the memory's own housekeeping (an eviction). The
// disk is a replay of the memory's decisions, so it takes no decision of its
// own.
//
// It is bounded and coalesces: a later put of a key supersedes an earlier
// one, a forget or a removal drops the puts it would undo, and the removal
// of a namespace drops everything of it. A removal is never dropped for
// room; when the bound is reached with removals alone, the namespace is
// removed instead, in one op: a lost put is a miss later, a lost removal
// would be stale data shown as fresh.

// ChangeKind is one kind of change.
type ChangeKind uint8

const (
	// ChangePut writes the entry under the key.
	ChangePut ChangeKind = iota
	// ChangeForget removes the one key.
	ChangeForget
	// ChangeRemoveResource removes every key of the resource (Key is the
	// resource).
	ChangeRemoveResource
	// ChangeRemoveNamespace removes every key of the namespace (Key carries
	// the namespace alone).
	ChangeRemoveNamespace
)

func (k ChangeKind) String() string {
	switch k {
	case ChangePut:
		return "put"
	case ChangeForget:
		return "forget"
	case ChangeRemoveResource:
		return "remove_resource"
	case ChangeRemoveNamespace:
		return "remove_namespace"
	}
	return "unknown"
}

// Change is one change.
type Change struct {
	Kind  ChangeKind
	Key   Key
	Entry Entry // for ChangePut
	// afterForget: this put took the place of a forget of its key. Dropped
	// for room, it leaves that forget behind: the file the forget was for
	// must not stay on disk as if the put had been applied.
	afterForget bool
}

// MaxPending bounds the journal.
const MaxPending = 256

// Journal is the bounded list of changes not yet replayed. Like the store it
// has no lock: its owner changes it in the update step alone.
type Journal struct{ ops []Change }

func NewJournal() *Journal { return &Journal{} }

// Len is the number of pending changes.
func (j *Journal) Len() int { return len(j.ops) }

// Add appends a change, dropping the earlier ones it supersedes, and keeps
// the bound.
func (j *Journal) Add(op Change) {
	keep := j.ops[:0]
	for _, old := range j.ops {
		if !supersedes(op, old) {
			keep = append(keep, old)
			continue
		}
		if op.Kind == ChangePut && (old.Kind == ChangeForget || old.afterForget) {
			op.afterForget = true
		}
	}
	j.ops = append(keep, op)
	j.bound()
}

// supersedes says whether an older change is pointless once op follows it.
func supersedes(op, old Change) bool {
	if old.Key.Namespace() != op.Key.Namespace() {
		return false
	}
	switch op.Kind {
	case ChangePut, ChangeForget:
		return (old.Kind == ChangePut || old.Kind == ChangeForget) && old.Key == op.Key
	case ChangeRemoveResource:
		return old.Kind != ChangeRemoveNamespace && old.Key.Resource() == op.Key.Resource()
	case ChangeRemoveNamespace:
		return true
	}
	return false
}

// bound keeps at most MaxPending changes: the oldest puts go first (one
// that took a forget's place becomes that forget again, so a file the
// forget was for does not stay); when none is left to drop, every
// namespace still pending is removed as a whole.
func (j *Journal) bound() {
	for len(j.ops) > MaxPending {
		dropped := false
		for i, op := range j.ops {
			if op.Kind != ChangePut {
				continue
			}
			if op.afterForget {
				j.ops[i] = Change{Kind: ChangeForget, Key: op.Key}
				continue // no room gained; the next put is looked at
			}
			j.ops = append(j.ops[:i], j.ops[i+1:]...)
			dropped = true
			break
		}
		if !dropped {
			j.collapse()
			return
		}
	}
}

// collapse replaces every pending change by one removal per namespace.
func (j *Journal) collapse() {
	var namespaces []Namespace
	for _, op := range j.ops {
		ns := op.Key.Namespace()
		seen := false
		for _, n := range namespaces {
			if n == ns {
				seen = true
			}
		}
		if !seen {
			namespaces = append(namespaces, ns)
		}
	}
	j.ops = j.ops[:0]
	for _, ns := range namespaces {
		j.ops = append(j.ops, Change{Kind: ChangeRemoveNamespace, Key: Key{ns: ns}})
	}
}

// Take removes and returns up to n changes, the oldest first.
func (j *Journal) Take(n int) []Change {
	n = min(n, len(j.ops))
	out := make([]Change, n)
	copy(out, j.ops[:n])
	j.ops = append(j.ops[:0], j.ops[n:]...)
	return out
}
