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
//
// Every change carries the number of the oldest change it stands for
// (Mark, Settled): a read of the disk issued after a change is sent only
// once that change, or the one that took its place, has been applied.
//
// A change the journal lets go is cleared from its backing array: the
// slots would otherwise keep its payload alive until written again.

import "slices"

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
	// seq is the number of the oldest change this one stands for: its
	// own, or that of an earlier change it superseded.
	seq uint64
}

// MaxPending bounds the journal.
const MaxPending = 256

// Journal is the bounded list of changes not yet replayed. Like the store it
// has no lock: its owner changes it in the update step alone.
type Journal struct {
	ops []Change
	// last is the number of the newest change added.
	last uint64
}

func NewJournal() *Journal { return &Journal{} }

// Len is the number of pending changes.
func (j *Journal) Len() int { return len(j.ops) }

// Mark is the number of the newest change added: a read issued now waits
// for every change up to it (Settled).
func (j *Journal) Mark() uint64 { return j.last }

// Settled says whether every change up to mark has left the journal: none
// pending stands for one of them.
func (j *Journal) Settled(mark uint64) bool {
	for _, op := range j.ops {
		if op.seq <= mark {
			return false
		}
	}
	return true
}

// Add appends a change, dropping the earlier ones it supersedes, and keeps
// the bound. A change that supersedes others stands for the oldest of
// them.
func (j *Journal) Add(op Change) {
	j.last++
	op.seq = j.last
	keep := j.ops[:0]
	for _, old := range j.ops {
		if !supersedes(op, old) {
			keep = append(keep, old)
			continue
		}
		op.seq = min(op.seq, old.seq)
		if op.Kind == ChangePut && (old.Kind == ChangeForget || old.afterForget) {
			op.afterForget = true
		}
	}
	clear(j.ops[len(keep):])
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
				j.ops[i] = Change{Kind: ChangeForget, Key: op.Key, seq: op.seq}
				continue // no room gained; the next put is looked at
			}
			j.ops = slices.Delete(j.ops, i, i+1)
			dropped = true
			break
		}
		if !dropped {
			j.collapse()
			return
		}
	}
}

// collapse replaces every pending change by one removal per namespace,
// which stands for the oldest change of its namespace.
func (j *Journal) collapse() {
	var removals []Change
	for _, op := range j.ops {
		ns := op.Key.Namespace()
		seen := false
		for i, r := range removals {
			if r.Key.Namespace() == ns {
				removals[i].seq = min(r.seq, op.seq)
				seen = true
			}
		}
		if !seen {
			removals = append(removals, Change{Kind: ChangeRemoveNamespace, Key: Key{ns: ns}, seq: op.seq})
		}
	}
	clear(j.ops)
	j.ops = append(j.ops[:0], removals...)
}

// Take removes and returns up to n changes, the oldest first.
func (j *Journal) Take(n int) []Change {
	n = min(n, len(j.ops))
	out := make([]Change, n)
	copy(out, j.ops[:n])
	j.ops = slices.Delete(j.ops, 0, n)
	return out
}
