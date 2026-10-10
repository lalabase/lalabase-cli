package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// The start, step by step:
//
//	/me
//	 ├─ initialize → tools/list → capabilities → the global unread page 1 (counters)
//	 └─ /organisations
//
// Only a successful /me releases the rest; initialize and the organisations
// overlap; the tools wait for initialize; the counters for the capabilities.

// gatewayStep maps a startup index to the gateway's step.
func gatewayStep(i stepIndex) (gateway.Step, bool) {
	switch i {
	case stepMe:
		return gateway.StepMe, true
	case stepInitialize:
		return gateway.StepInitialize, true
	case stepTools:
		return gateway.StepTools, true
	case stepOrganisations:
		return gateway.StepOrganisations, true
	}
	return 0, false
}

func stepOf(s gateway.Step) (stepIndex, bool) {
	for i := stepMe; i < stepCounters; i++ {
		if gs, ok := gatewayStep(i); ok && gs == s {
			return i, true
		}
	}
	return 0, false
}

// startStep issues the next attempt of one of the four gateway steps.
func (a *app) startStep(i stepIndex) tea.Cmd {
	gs, ok := gatewayStep(i)
	if !ok || a.expired || a.deps.Startup == nil {
		return nil
	}
	s := &a.steps[i]
	if s.cancel != nil {
		s.cancel()
	}
	s.attempt++
	s.state, s.err = loading, present.Inline{}
	ctx, cancel := context.WithCancel(a.base)
	s.cancel = cancel
	return a.deps.Startup.Run(ctx, gateway.StartupTicket{Session: a.session, Step: gs, Attempt: s.attempt})
}

// receiveStartup accepts a startup result for the attempt it waits for and
// moves the start on.
func (m Model) receiveStartup(r gateway.StartupResult) (Model, tea.Cmd) {
	a := m.app
	if a == nil || a.expired || r.Session != a.session {
		return m, nil
	}
	i, ok := stepOf(r.Step)
	if !ok {
		return m, nil
	}
	s := &a.steps[i]
	if s.state != loading || r.Attempt != s.attempt {
		return m, nil
	}
	if a.revoked() || r.Answer.Class == transport.Unauthenticated {
		m.expire()
		return m, nil
	}
	if !r.Answer.OK() {
		s.state, s.err = failed, failureText(i, r.Answer)
		return m, nil
	}
	s.state, s.cancel = ready, nil
	switch i {
	case stepMe:
		return m.confirmIdentity(r.Me)
	case stepInitialize:
		a.server = r.Server
		return m, a.startStep(stepTools)
	case stepTools:
		report := features.Detect(a.server, r.Tools)
		a.report = &report
		if !a.inboxOn() {
			a.steps[stepCounters].state = unsupported
			return m, nil
		}
		return m, a.issueCounters()
	case stepOrganisations:
		a.directory = r.Organisations
		if a.highlight != "" && a.organisation(a.highlight) == nil {
			// The entry for all organisations has an inbox alone.
			a.highlight, a.highlightSection = "", sectionInbox
		}
	}
	return m, nil
}

// confirmIdentity opens the namespace with the token id the server
// confirmed, never with anything the profile says about itself, and
// releases the dependent steps.
func (m Model) confirmIdentity(me api.Me) (Model, tea.Cmd) {
	a := m.app
	ns, ok := cache.Confirmed(a.deps.Profile, a.deps.Server, me.Token.ID)
	if me.Token.ID == "" || !ok {
		a.steps[stepMe].state = failed
		a.steps[stepMe].err = uitext.Fill(uitext.UnexpectedResponse, api.MeSpec.Name+": no usable token id")
		return m, nil
	}
	a.me, a.ns = me, ns
	a.scope, a.store = gateway.NewScope(ns), cache.NewStore(cache.WithJournal(a.disk.journal))
	// The disk cache opens now and not before: nothing of it is read or
	// made for an identity the server did not confirm (C1e).
	return m, tea.Batch(a.startStep(stepInitialize), a.startStep(stepOrganisations), a.openDisk())
}

// organisation finds a directory entry by id.
func (a *app) organisation(id string) *api.Ref {
	for i := range a.directory {
		if a.directory[i].ID == id {
			return &a.directory[i]
		}
	}
	return nil
}

// failureText is the error shown for a failed step: the step's own line
// for a temporary failure, the class and a bounded detail for the rest.
func failureText(i stepIndex, ans transport.Answer) present.Inline {
	base := map[stepIndex]uitext.ID{
		stepMe: uitext.IdentityFailed, stepInitialize: uitext.CapabilitiesFailed, stepTools: uitext.CapabilitiesFailed,
		stepOrganisations: uitext.OrganisationsFailed, stepCounters: uitext.CountsFailed,
	}[i]
	switch ans.Class {
	case transport.Temporary, transport.Canceled:
		return uitext.Inline(base)
	case transport.Protocol:
		return uitext.Fill(uitext.UnexpectedResponse, detail(ans))
	}
	return uitext.Fill(uitext.RefusedWith, detail(ans))
}

// maxDetail bounds a server's text in a notice: one line, not a page.
const maxDetail = 120

func detail(ans transport.Answer) string {
	text := ans.Class.String()
	if ans.Message != "" {
		text += ": " + bounded(ans.Message, maxDetail)
	}
	return text
}

// bounded cuts a text to n runes, never inside one.
func bounded(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + uitext.Inline(uitext.Ellipsis).String()
}

// expire ends the session for good: pending work is canceled, the scope
// ended and the namespace emptied, every piece of state cleared. Nothing
// is asked of the server again until the program is restarted.
func (m *Model) expire() {
	a := m.app
	if a == nil || a.expired {
		return
	}
	a.expired = true
	m.mode = Navigate
	for i := range a.steps {
		if a.steps[i].cancel != nil {
			a.steps[i].cancel()
		}
	}
	for _, w := range a.waiters {
		w.cancel()
	}
	for _, op := range a.marks {
		// The session's writes end with it: a late answer fills nothing.
		op.cancel()
	}
	if a.scope != nil {
		// The store tells the journal: the namespace leaves the disk too.
		a.scope.EndSession(a.store)
	}
	a.endDiskSession()
	a.reset()
}
