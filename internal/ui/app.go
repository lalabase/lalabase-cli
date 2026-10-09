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
// sequence of the request that observed it, so a newer observation wins
// whatever order the answers arrive in.
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

// op is one logical read in flight, keyed by its share key: its sequence,
// and its waiters (an additional consumer joins the operation and its
// sequence). When every waiter was canceled and a new read of the same
// data is asked for, the read is issued after the canceled result arrived,
// so a joined old request is never taken for newly issued work.
type op struct {
	key     cache.Key
	share   string
	seq     uint64
	waiters map[gateway.Waiter]*waiter
	reissue *issue
}

type waiter struct {
	ticket   gateway.Ticket
	cancel   context.CancelFunc
	canceled bool
	purpose  purpose
}

type issue struct {
	area    gateway.Area
	key     cache.Key
	purpose purpose
}

func (o *op) allCanceled() bool {
	for _, w := range o.waiters {
		if !w.canceled {
			return false
		}
	}
	return true
}

// position is where the person was in an organisation's inbox: remembered
// per organisation, restored on return.
type position struct {
	page     int
	selected string
}

// inboxView is the active inbox: one organisation (or all), one page.
type inboxView struct {
	org            string // "" for all organisations
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
	// appliedSeq is the sequence of the last result applied to this view: a
	// late copy of an older result changes nothing.
	appliedSeq uint64
	// corrected: the one page correction after a shrink was done.
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
	// countersSeq is the sequence of the last result that settled the
	// counters step.
	countersSeq uint64

	seq     uint64
	ops     map[string]*op
	waiters map[gateway.Waiter]*op

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
	a.ops, a.waiters = map[string]*op{}, map[gateway.Waiter]*op{}
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
// is acted on.
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
