package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// R3: an organisation counted before, then left the directory and omitted
// by a newer global snapshot, is unknown when the directory lists it again.
func TestR3OmittedKnownOrganisationIsUnknownOnReturn(t *testing.T) {
	h := newHarness(t)
	h.start()
	if !h.m.app.countOf(orgA).known {
		t.Fatal("setup: A counted")
	}
	h.f.orgs = func() ([]api.Ref, transport.Answer) {
		return []api.Ref{{ID: orgB, Name: "B"}}, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(q.Page, q.PerPage, 3, 3, h.d.rows(orgB, 101, 3), map[string]int{orgB: 3})
	})
	h.m.focus = 0
	h.drive(h.press("R")) // directory without A, snapshot without A
	h.f.orgs = func() ([]api.Ref, transport.Answer) {
		return h.d.orgs, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	refresh := h.press("R")().(tea.BatchMsg)
	h.drive(refresh[0]) // directory with A again, before the next snapshot
	if h.m.app.organisation(orgA) == nil {
		t.Fatal("setup: A listed again")
	}
	if c := h.m.app.countOf(orgA); c.known {
		t.Fatalf("A shows a count the newer snapshot omitted: %+v", c)
	}
}
