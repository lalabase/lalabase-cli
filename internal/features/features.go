// Package features decides which features a server supports, from its
// tools/list: a feature is on when its tool is there and the tool's input
// schema declares every argument the feature sends. The server's version is
// shown and compared with the version the client was tested against, but it
// never switches anything on or off: S1 extends existing tools, so a tool name
// or a version number alone says nothing.
//
// Since K0 a feature may name the REST capabilities its REST way needs, as
// GET /api/v1/me lists them (the core's R0). A server that lists them all
// gets the REST way; any other, the MCP way, while both exist. A server
// without capabilities (before R0) keeps every feature it had. K1 names
// them for the ticket view (the core's R1). K6 adds a feature with the REST
// way alone, the booking of time: it has no tool, so a server that does not
// list its capability has no such feature. Every other feature has the MCP
// way alone.
package features

import (
	"slices"
	"strconv"
	"strings"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// MinTestedServerVersion is the MCP surface the client was tested against:
// the version of the contract copy in internal/contract, server slices S1,
// S2, S2b and S6 included (0.13.0 adds the people tools, which the client
// does not use, and 0.13.1 the bound of an idempotency key, far above this
// client's keys).
const MinTestedServerVersion = "0.13.1"

// Feature is one thing the client offers, and what it needs from the server.
type Feature struct {
	Name string
	// Spec is the adapter the feature runs; its Args must be declared. A
	// spec without a tool is a feature with the REST way alone.
	Spec api.Spec
	// ExtraArgs are arguments the feature needs beyond the adapter's base set.
	ExtraArgs []string
	// Since names the server slice that brings the feature, through an
	// argument of a tool the contract had (ExtraArgs) or a tool of its own;
	// empty for one the contract has had from the start.
	Since string
	// Capabilities are the REST capabilities its REST way needs, every
	// one of them ("posts.create", "idempotency"); empty for a feature
	// that has the MCP way alone.
	Capabilities []string
}

// Way is how a feature that is on reaches the server.
type Way int

const (
	// MCP: the feature's tool (Feature.Spec).
	MCP Way = iota
	// REST: the endpoints its capabilities name.
	REST
)

func (w Way) String() string {
	if w == REST {
		return "rest"
	}
	return "mcp"
}

// The REST capabilities of the ticket view (K1). tickets.list and
// tickets.show came with the core's R0; tickets.paging is R1's, which
// brought the list's meta.available_statuses and the ticket as a page: a
// server with R0 alone keeps the tools, its list has no status catalogue
// and its ticket no counts. A list and its search are one read, as are a
// ticket and its pages: each pair needs the same. The texts whole need
// one comment's route beside the pages.
var (
	listREST   = []string{"tickets.list", "tickets.paging"}
	ticketREST = []string{"tickets.show", "tickets.paging"}
	wholeREST  = []string{"tickets.show", "tickets.paging", "posts.show"}
)

// All features, in the order the interface offers them.
var All = []Feature{
	{Name: "inbox", Spec: api.NotificationsSpec},
	{Name: "mark_read", Spec: api.MarkReadSpec},
	{Name: "tickets", Spec: api.TicketsSpec, Capabilities: listREST},
	{Name: "tickets_search", Spec: api.TicketsSpec, ExtraArgs: []string{"q"}, Capabilities: listREST},
	{Name: "ticket", Spec: api.TicketSpec, Capabilities: ticketREST},
	{Name: "comment", Spec: api.CommentSpec},
	{Name: "knowledge_search", Spec: api.KnowledgeSpec},
	{Name: "inbox_by_organisation", Spec: api.NotificationsSpec, ExtraArgs: []string{"organisation_id"}, Since: "S1"},
	{Name: "ticket_posts_paged", Spec: api.TicketSpec, ExtraArgs: []string{"posts_page", "posts_per_page", "posts_order", "attachments_page"}, Since: "S1", Capabilities: ticketREST},
	{Name: "ticket_full_text", Spec: api.TicketSpec, ExtraArgs: []string{"full_text", "post_id"}, Since: "S1", Capabilities: wholeREST},
	// C2: the writes on a ticket. ticket_update needs the whole tool, every
	// argument its spec declares (a server with half of it keeps it off);
	// comment_plain needs format on top of the base feature comment.
	{Name: "ticket_update", Spec: api.UpdateTicketSpec, Since: "S2"},
	{Name: "comment_plain", Spec: api.CommentSpec, ExtraArgs: []string{"format"}, Since: "S2b"},
	// S6: the time the person booked on a ticket, in its meta bar.
	{Name: "booked_time", Spec: api.BookedTimeSpec, Since: "S6"},
	// K6: booking time on a ticket, the first write over REST.
	{Name: "time_book", Spec: api.BookTimeSpec, Since: "R6", Capabilities: []string{"time_sessions.book"}},
}

// Report is what one server supports.
type Report struct {
	Server transport.ServerInfo
	// Enabled says, per feature name, whether it is on.
	Enabled map[string]bool
	// Via says, per feature that is on, the way it takes.
	Via map[string]Way
	// Missing says, per feature that is off, what is missing
	// ("tool post_comment", "list_tickets: argument q").
	Missing map[string][]string
	// Unused are tools no feature uses: a notice that the server is newer,
	// never a reason to switch something off.
	Unused []string
	// BelowTested: the server's version is older than MinTestedServerVersion,
	// or it names none the client can read. A notice; the schemas decide.
	BelowTested bool
}

// Detect checks every feature against the tools a server lists and the
// REST capabilities its /me names (nil for a server that names none).
func Detect(server transport.ServerInfo, tools []transport.Tool, capabilities []string) Report {
	return detect(All, server, tools, capabilities)
}

func detect(all []Feature, server transport.ServerInfo, tools []transport.Tool, capabilities []string) Report {
	byName := map[string]transport.Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	report := Report{Server: server, Enabled: map[string]bool{}, Via: map[string]Way{}, Missing: map[string][]string{}}
	used := map[string]bool{}
	for _, f := range all {
		used[f.Spec.Tool] = true
		restMissing := lacking(f.Capabilities, capabilities)
		if len(f.Capabilities) > 0 && len(restMissing) == 0 {
			report.Enabled[f.Name], report.Via[f.Name] = true, REST
			continue
		}
		if f.Spec.Tool == "" {
			// The REST way alone (K6): no tool stands in for it.
			report.Missing[f.Name] = restMissing
			continue
		}
		missing := toolMissing(f, byName)
		if len(missing) == 0 {
			report.Enabled[f.Name], report.Via[f.Name] = true, MCP
			continue
		}
		report.Missing[f.Name] = append(missing, restMissing...)
	}
	for _, tool := range tools {
		if !used[tool.Name] {
			report.Unused = append(report.Unused, tool.Name)
		}
	}
	report.BelowTested = olderThan(server.Version, MinTestedServerVersion)
	return report
}

// toolMissing says what the feature's MCP way lacks: its tool, or an
// argument the tool's schema does not declare.
func toolMissing(f Feature, byName map[string]transport.Tool) []string {
	tool, ok := byName[f.Spec.Tool]
	if !ok {
		return []string{"tool " + f.Spec.Tool}
	}
	props := tool.Properties()
	var missing []string
	for _, arg := range append(slices.Clone(f.Spec.Args), f.ExtraArgs...) {
		if _, ok := props[arg]; !ok {
			missing = append(missing, f.Spec.Tool+": argument "+arg)
		}
	}
	return missing
}

// lacking names the capabilities of need the server does not list.
func lacking(need, listed []string) []string {
	var out []string
	for _, c := range need {
		if !slices.Contains(listed, c) {
			out = append(out, "capability "+c)
		}
	}
	return out
}

// olderThan compares "major.minor.patch" versions; a pre-release suffix is
// ignored, a missing patch counts as 0. A version that cannot be read counts
// as older: the client cannot say it was tested against it.
func olderThan(version, minimum string) bool {
	v, ok := parse(version)
	if !ok {
		return true
	}
	m, _ := parse(minimum)
	return slices.Compare(v, m) < 0
}

func parse(version string) ([]int, bool) {
	core, _, _ := strings.Cut(version, "-")
	parts := strings.Split(core, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return nil, false
	}
	out := make([]int, 3)
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}
