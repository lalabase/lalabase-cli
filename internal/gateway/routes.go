package gateway

import (
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/features"
)

// Routes say which way each read of the ticket view takes (K1): the way
// the session's feature report gives the feature whose read it is. The
// zero value takes the tools, as every read did before K1; every read
// outside the ticket view does.
type Routes struct {
	// List is the ticket list and its search (feature tickets; its
	// search, tickets_search, needs the same capabilities).
	List features.Way
	// Ticket is a page of a ticket (feature ticket; its pages,
	// ticket_posts_paged, need the same capabilities).
	Ticket features.Way
	// Whole is a text read whole: the description or one comment
	// (feature ticket_full_text).
	Whole features.Way
}

// RoutesOf reads the routes from a report. A feature that is off has no
// way and keeps the tools: its reads are not offered.
func RoutesOf(r features.Report) Routes {
	return Routes{List: r.Via["tickets"], Ticket: r.Via["ticket"], Whole: r.Via["ticket_full_text"]}
}

// way is the way the read of a key takes.
func (r Routes) way(k cache.Key) features.Way {
	switch k.Operation {
	case api.TicketsSpec.Tool:
		return r.List
	case api.TicketSpec.Tool:
		if k.FullText || k.Post != "" {
			return r.Whole
		}
		return r.Ticket
	}
	return features.MCP
}

// Route sets the routes of the reads issued from now on: the model calls
// it once the feature report is known, before the first read of the
// ticket view. A ticket keeps the way it was issued with.
func (s *Scope) Route(r Routes) { s.routes = r }
