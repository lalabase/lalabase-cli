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
	forProjects
	forTickets
	forDetail
	forExpansion
	// forTitle: the title of an agent's ticket (C9b).
	forTitle
	// forTime: the time the person booked on the ticket in the pane (S6).
	forTime
	// forGroup: a page of one status of the ticket list (C11).
	forGroup
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

// section is what an organisation's entry of the chooser opens.
type section uint8

const (
	sectionInbox section = iota
	sectionTickets
)

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
	status
	selected, opened     string // notification ids
	offset, detailOffset int
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
	deps Session
	now  func() time.Time
	// loc is the zone times are shown in: the person's.
	loc     *time.Location
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
	// snapshot is the newest global page applied: it spoke for every
	// organisation, so no older filtered answer may set a count.
	snapshot uint64

	waiters map[gateway.Waiter]*waiter
	// filed is, per key, the newest request whose answer was filed in the
	// cache: an older request's answer that arrives later is not filed
	// over it. The scope orders by generation and epoch; within one epoch
	// the requests are ordered by their flight.
	filed map[cache.Key]uint64

	// highlight and highlightSection name the entry the cursor is on in
	// the chooser: an organisation ("" for all) and its section.
	highlight        string
	highlightSection section
	// active is the open inbox; projects, tickets and detail the open
	// tickets section (C1c). One of active and projects is set at most.
	active     *inboxView
	remembered map[string]position
	projects   *projectsView
	tickets    *ticketsView
	detail     *detailView
	// rememberedProject is the selected project per organisation,
	// rememberedTickets the page, row and search per project.
	rememberedProject map[string]string
	rememberedTickets map[string]ticketPosition
	// previewToken names the timer of the row the cursor rests on: only the
	// wake-up with the current token reads a preview.
	previewToken uint64
	// titles are the reads of the titles of the agents' tickets the
	// session did not hold (C9b), by ticket id: each is read once.
	titles map[string]*title

	// marks are the operations of marking as read, by notification id, each
	// the newest for its target (C1f); nextOp numbers them. origin is the
	// notification the ticket in the pane was opened from, nil when the
	// pane shows no such ticket.
	marks  map[string]*markOp
	nextOp uint64
	origin *origin
	// bookings are the bookings of time out or not cleared up, by ticket id,
	// each the newest for its ticket (K6); bookNotices say what became of
	// one where its dialog cannot.
	bookings    map[string]*bookOp
	bookNotices []bookNotice
	// groupNotice says why g did not group (C11); it stands as long as a
	// hint.
	groupNotice bookNotice

	expired bool
	pause   time.Time

	// disk is the disk cache (C1e), outside reset: an ended session still
	// has its namespace to remove from it.
	disk *diskState

	// launch is the launch of Claude Code beside a ticket (C7), outside
	// reset: its numbering goes on, so a late answer frees nothing.
	launch launchState
	// viewing is the agent the viewer was last asked to show (agents in
	// tmux), "" for none.
	viewing string

	// agents is what the sessions x started do (C9a), outside reset: they
	// are the profile's, and an expired session still has them.
	agents agentsState
}

func newApp(s Session) *app {
	a := &app{deps: s, now: time.Now, loc: time.Local, session: gateway.NewSession(), disk: newDiskState(s.Disk)}
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
	a.directory, a.counters, a.global, a.countersSeq, a.snapshot = nil, map[string]count{}, count{}, 0, 0
	a.waiters, a.filed = map[gateway.Waiter]*waiter{}, map[cache.Key]uint64{}
	a.highlight, a.highlightSection, a.active, a.remembered = "", sectionInbox, nil, map[string]position{}
	a.projects, a.tickets, a.detail = nil, nil, nil
	a.rememberedProject, a.rememberedTickets, a.previewToken = map[string]string{}, map[string]ticketPosition{}, 0
	a.titles = map[string]*title{}
	a.marks, a.origin = map[string]*markOp{}, nil
	a.bookings, a.bookNotices, a.groupNotice = map[string]*bookOp{}, nil, bookNotice{}
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

func (a *app) on(feature string) bool { return a.report != nil && a.report.Enabled[feature] }
func (a *app) inboxOn() bool          { return a.on("inbox") }
func (a *app) byOrgOn() bool          { return a.on("inbox_by_organisation") }

// The capabilities of the tickets section: the list, the search on it, the
// ticket, and the paged comments and attachments of the ticket.
func (a *app) ticketsOn() bool { return a.on("tickets") }
func (a *app) searchOn() bool  { return a.on("tickets_search") }
func (a *app) ticketOn() bool  { return a.on("ticket") }
func (a *app) pagedOn() bool   { return a.on("ticket_posts_paged") }

// fullTextOn: the description or one comment can be read whole (C1d).
func (a *app) fullTextOn() bool { return a.on("ticket_full_text") }

// canOpen says whether the entry of org ("" for all) and section can be
// opened: the capabilities are known and allow it. Without the organisation
// filter only the inbox of all organisations opens; the tickets open per
// organisation only, with the list tool.
func (a *app) canOpen(org string, s section) bool {
	if a.expired || a.scope == nil {
		return false
	}
	if s == sectionTickets {
		return org != "" && a.ticketsOn()
	}
	return a.inboxOn() && (org == "" || a.byOrgOn())
}

// pagingOn: server pages are read only with the organisation filter;
// without it the global inbox shows page 1 only.
func (a *app) pagingOn() bool { return a.byOrgOn() }
