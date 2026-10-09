package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// stepIndex names the five reads of the start. The first four are the
// gateway's startup steps, in its order; the counters are an inbox read
// through the gateway's scope.
type stepIndex uint8

const (
	stepMe stepIndex = iota
	stepInitialize
	stepTools
	stepOrganisations
	stepCounters
	stepCount
)

type stepState uint8

const (
	blocked stepState = iota
	loading
	ready
	failed
	unsupported
)

// stage is one read of the start: its state, the attempt it waits for, how
// to cancel it, and what went wrong.
type stage struct {
	state   stepState
	attempt uint64
	cancel  context.CancelFunc
	err     present.Inline
}

// count is one unread counter: unknown until observed, and stamped with the
// request that observed it, so a newer request's observation wins whatever
// order the answers arrive in.
type count struct {
	known bool
	value int
	seq   uint64
}

// purpose says what a waiter's result is for.
type purpose uint8

const (
	forCounters purpose = iota
	forInbox
)

// waiter is one consumer of a read in flight: its ticket, how to cancel it,
// and what it was issued for. The queue shares the request between the
// waiters of one key; the model does not need to.
type waiter struct {
	ticket   gateway.Ticket
	cancel   context.CancelFunc
	canceled bool
	purpose  purpose
}

// position is where the person was in an organisation's inbox: remembered
// per organisation, restored on return.
type position struct {
	page     int
	selected string
}

// inboxView is the active inbox: one organisation (or all), one page.
type inboxView struct {
	org string // "" for all organisations
	// name is the organisation's name when the inbox was opened: the
	// heading keeps it when the directory no longer lists the organisation.
	name           string
	page           int
	key            cache.Key
	rows           []api.Notification
	total, perPage int
	// known: a page was shown at least once, from the cache or the server.
	known                bool
	selected, opened     string // notification ids
	offset, detailOffset int
	loading              bool
	cached, stale        bool
	err                  present.Inline
	loadedAt             time.Time
	// corrected: the one page correction after a shrink was done; an
	// explicit R frees it again.
	corrected bool
}

func (v *inboxView) index(id string) int {
	if id == "" {
		return -1
	}
	for i, row := range v.rows {
		if row.ID == id {
			return i
		}
	}
	return -1
}

// reselect keeps the selected row by its id when it is still there, else
// takes the first; opened details whose row is gone are closed.
func (v *inboxView) reselect() {
	if v.index(v.selected) < 0 {
		v.selected = ""
		if len(v.rows) > 0 {
			v.selected = v.rows[0].ID
		}
	}
	if v.opened != "" && v.index(v.opened) < 0 {
		v.opened = ""
	}
}

// app is the state of one session.
type app struct {
	deps    Session
	now     func() time.Time
	base    context.Context
	session gateway.Session

	steps  [stepCount]stage
	me     api.Me
	server transport.ServerInfo
	ns     cache.Namespace
	scope  *gateway.Scope
	store  *cache.Store
	report *features.Report

	directory []api.Ref
	counters  map[string]count
	global    count
	// countersSeq is the request that settled the counters step last.
	countersSeq uint64

	waiters map[gateway.Waiter]*waiter
	// filed is, per key, the newest request whose answer was filed in the
	// cache: an older request's answer that arrives later is not filed
	// over it. The scope orders by generation and epoch; within one epoch
	// the requests are ordered by their flight.
	filed map[cache.Key]uint64

	// highlight is the id of the organisation the cursor is on in the
	// chooser; "" is the entry for all organisations.
	highlight  string
	active     *inboxView
	remembered map[string]position

	expired bool
	pause   time.Time
}

func newApp(s Session) *app {
	a := &app{deps: s, now: time.Now, session: gateway.NewSession()}
	if s.Events != nil {
		a.session = s.Events.Session()
	}
	a.reset()
	return a
}

// reset empties every piece of state the session owned.
func (a *app) reset() {
	a.steps = [stepCount]stage{}
	a.me, a.server, a.report = api.Me{}, transport.ServerInfo{}, nil
	a.ns, a.scope, a.store = cache.Namespace{}, nil, nil
	a.directory, a.counters, a.global, a.countersSeq = nil, map[string]count{}, count{}, 0
	a.waiters, a.filed = map[gateway.Waiter]*waiter{}, map[cache.Key]uint64{}
	a.highlight, a.active, a.remembered = "", nil, map[string]position{}
	a.pause = time.Time{}
}

func (a *app) wait() tea.Cmd {
	if a.deps.Events == nil {
		return nil
	}
	return a.deps.Events.Wait()
}

// revoked is the durable state of the mailbox: checked before any result
// is acted on and before any key does anything.
func (a *app) revoked() bool {
	return a.deps.Events != nil && a.deps.Events.State().Revoked
}

func (a *app) inboxOn() bool { return a.report != nil && a.report.Enabled["inbox"] }
func (a *app) byOrgOn() bool { return a.report != nil && a.report.Enabled["inbox_by_organisation"] }

// canOpen says whether the inbox of org (or of all, "") can be read: the
// capabilities are known and allow it. Without the organisation filter only
// the entry for all organisations opens.
func (a *app) canOpen(org string) bool {
	return !a.expired && a.scope != nil && a.inboxOn() && (org == "" || a.byOrgOn())
}

// pagingOn: server pages are read only with the organisation filter;
// without it the global inbox shows page 1 only.
func (a *app) pagingOn() bool { return a.byOrgOn() }

// lastPage is the last page of total rows at perPage each: 1 for an empty
// inbox, never a division by zero.
func lastPage(total, perPage int) int {
	if perPage <= 0 || total <= 0 {
		return 1
	}
	n := total / perPage
	if total%perPage != 0 {
		n++
	}
	return max(1, n)
}
