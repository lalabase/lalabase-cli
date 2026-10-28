// Package features decides which features a server supports, from its
// tools/list: a feature is on when its tool is there and the tool's input
// schema declares every argument the feature sends. The server's version is
// shown and compared with the version the client was tested against, but it
// never switches anything on or off: S1 extends existing tools, so a tool name
// or a version number alone says nothing.
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
// S2, S2b and S6 included.
const MinTestedServerVersion = "0.12.0"

// Feature is one thing the client offers, and what it needs from the server.
type Feature struct {
	Name string
	// Spec is the adapter the feature runs; its Args must be declared.
	Spec api.Spec
	// ExtraArgs are arguments the feature needs beyond the adapter's base set.
	ExtraArgs []string
	// Since names the server slice that brings the feature, through an
	// argument of a tool the contract had (ExtraArgs) or a tool of its own;
	// empty for one the contract has had from the start.
	Since string
}

// All features, in the order the interface offers them.
var All = []Feature{
	{Name: "inbox", Spec: api.NotificationsSpec},
	{Name: "mark_read", Spec: api.MarkReadSpec},
	{Name: "tickets", Spec: api.TicketsSpec},
	{Name: "tickets_search", Spec: api.TicketsSpec, ExtraArgs: []string{"q"}},
	{Name: "ticket", Spec: api.TicketSpec},
	{Name: "comment", Spec: api.CommentSpec},
	{Name: "knowledge_search", Spec: api.KnowledgeSpec},
	{Name: "inbox_by_organisation", Spec: api.NotificationsSpec, ExtraArgs: []string{"organisation_id"}, Since: "S1"},
	{Name: "ticket_posts_paged", Spec: api.TicketSpec, ExtraArgs: []string{"posts_page", "posts_per_page", "posts_order", "attachments_page"}, Since: "S1"},
	{Name: "ticket_full_text", Spec: api.TicketSpec, ExtraArgs: []string{"full_text", "post_id"}, Since: "S1"},
	// C2: the writes on a ticket. ticket_update needs the whole tool, every
	// argument its spec declares (a server with half of it keeps it off);
	// comment_plain needs format on top of the base feature comment.
	{Name: "ticket_update", Spec: api.UpdateTicketSpec, Since: "S2"},
	{Name: "comment_plain", Spec: api.CommentSpec, ExtraArgs: []string{"format"}, Since: "S2b"},
	// S6: the time the person booked on a ticket, in its meta bar.
	{Name: "booked_time", Spec: api.BookedTimeSpec, Since: "S6"},
}

// Report is what one server supports.
type Report struct {
	Server transport.ServerInfo
	// Enabled says, per feature name, whether it is on.
	Enabled map[string]bool
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

// Detect checks every feature against the tools a server lists.
func Detect(server transport.ServerInfo, tools []transport.Tool) Report {
	byName := map[string]transport.Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	report := Report{Server: server, Enabled: map[string]bool{}, Missing: map[string][]string{}}
	used := map[string]bool{}
	for _, f := range All {
		used[f.Spec.Tool] = true
		tool, ok := byName[f.Spec.Tool]
		if !ok {
			report.Missing[f.Name] = []string{"tool " + f.Spec.Tool}
			continue
		}
		props := tool.Properties()
		var missing []string
		for _, arg := range append(slices.Clone(f.Spec.Args), f.ExtraArgs...) {
			if _, ok := props[arg]; !ok {
				missing = append(missing, f.Spec.Tool+": argument "+arg)
			}
		}
		if len(missing) > 0 {
			report.Missing[f.Name] = missing
			continue
		}
		report.Enabled[f.Name] = true
	}
	for _, tool := range tools {
		if !used[tool.Name] {
			report.Unused = append(report.Unused, tool.Name)
		}
	}
	report.BelowTested = olderThan(server.Version, MinTestedServerVersion)
	return report
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
