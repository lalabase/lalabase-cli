package gateway

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// The meetings of a project (K7a): read over REST alone, a page of
// MeetingPageSize rows with a search or none, checked against the question
// before the model sees them. The memory keeps them, the disk never does
// (cache.WithMemoryOnly, set where the model builds its store).

// MeetingsResult is one waiter's page of a project's meetings.
type MeetingsResult struct {
	Ticket
	Page   api.MeetingPage
	Answer transport.Answer
	Flight queue.Flight
}

func (r MeetingsResult) Header() Result {
	return Result{Ticket: r.Ticket, Answer: r.Answer, Flight: r.Flight}
}

// MeetingsQuery is the request for a meetings key: everything sent comes
// from the key.
func MeetingsQuery(k cache.Key) api.MeetingQuery {
	return api.MeetingQuery{ProjectID: k.Project, Q: k.Query, Page: k.Page, PerPage: k.PerPage}
}

// Meetings reads the page of a project's meetings t names.
func (g *Gateway) Meetings(ctx context.Context, t Ticket) tea.Cmd {
	if err := CheckMeetingsTicket(t); err != nil {
		return func() tea.Msg { return MeetingsResult{Ticket: t, Answer: refusedTicket(err)} }
	}
	query := MeetingsQuery(t.Key)
	return shared(g, ctx, t, "gateway: meetings", func(shared context.Context) transport.Answer {
		return g.src.MeetingsRESTAnswer(shared, query)
	}, func(ans transport.Answer, flight queue.Flight) MeetingsResult {
		page, ans := api.DecodeMeetingsREST(ans)
		if ans.OK() {
			if err := CheckMeetingsPage(t.Key, page); err != nil {
				page, ans = api.MeetingPage{}, contradiction(ans, err)
			}
		}
		return MeetingsResult{Ticket: t, Page: page, Answer: ans, Flight: flight}
	})
}

// CheckMeetingsTicket: one project's meetings, a page above zero of
// MeetingPageSize rows, a search or none, in their own area.
func CheckMeetingsTicket(t Ticket) error {
	k := t.Key
	switch {
	case k.Operation != api.MeetingsRESTSpec.Name:
		return fmt.Errorf("meetings: a ticket of %q", k.Operation)
	case k.Project == "":
		return errors.New("meetings: no project")
	case k.Page <= 0:
		return errors.New("meetings: a page below 1")
	case k.PerPage != MeetingPageSize:
		return fmt.Errorf("meetings: a page size of %d, not %d", k.PerPage, MeetingPageSize)
	case k.Organisation != "" || k.Global || k.Status != "" || k.Ticket != "" ||
		k.PostsPage != 0 || k.PostsPerPage != 0 || k.Newest || k.AttachmentsPage != 0 || k.FullText || k.Post != "":
		return errors.New("meetings: an option the read does not have")
	case t.Area != Meetings:
		return errors.New("meetings: another area")
	}
	return nil
}

// CheckMeetingsPage: the page is the one asked for, of the size asked
// for, every row a meeting of the project asked about (api.CheckMeetingPage
// for the rest).
func CheckMeetingsPage(k cache.Key, p api.MeetingPage) error {
	if err := api.CheckMeetingPage(p); err != nil {
		return err
	}
	if p.Meta.Page != k.Page {
		return fmt.Errorf("meetings: page %d answered for page %d", p.Meta.Page, k.Page)
	}
	if p.Meta.PerPage != k.PerPage {
		return fmt.Errorf("meetings: a page size of %d answered for %d", p.Meta.PerPage, k.PerPage)
	}
	for i, row := range p.Data {
		if row.ProjectID != k.Project {
			return fmt.Errorf("meetings: data[%d] is a meeting of another project", i)
		}
	}
	return nil
}
