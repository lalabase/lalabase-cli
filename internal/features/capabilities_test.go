package features

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K0: a feature can name the REST capabilities (GET /api/v1/me, since the
// core's R0) its REST way needs. A server that lists them all gets the
// REST way; one without capabilities, or without one of them, the MCP
// way, as long as both exist. The mechanics are tested with features of
// the tests' own; K1 names them for the ticket view.

// contractCapabilities are the capabilities of the recorded /me.
func contractCapabilities(t *testing.T) []string {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Data struct {
			Capabilities []string `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(file.Contract.REST["GET /api/v1/me"]["ok"].Response.Body, &body); err != nil || len(body.Data.Capabilities) == 0 {
		t.Fatalf("the recorded /me names no capabilities: %v", err)
	}
	return body.Data.Capabilities
}

// testFeatures stand for features K1 switches: a comment, which writes,
// and the inbox, which reads.
var testFeatures = []Feature{
	{Name: "comment", Spec: api.CommentSpec, Capabilities: []string{"posts.create", "idempotency", "error_codes"}},
	{Name: "inbox", Spec: api.NotificationsSpec, Capabilities: []string{"notifications.list", "error_codes"}},
	{Name: "knowledge_search", Spec: api.KnowledgeSpec},
}

func TestAFeatureTakesTheRESTWayWhenTheServerListsItsCapabilities(t *testing.T) {
	tools, _ := contractTools(t)
	caps := contractCapabilities(t)
	without := func(name string) []string {
		return slices.DeleteFunc(slices.Clone(caps), func(c string) bool { return c == name })
	}
	withoutTool := func(name string) []transport.Tool {
		return slices.DeleteFunc(slices.Clone(tools), func(tool transport.Tool) bool { return tool.Name == name })
	}
	cases := []struct {
		name  string
		tools []transport.Tool
		caps  []string
		want  map[string]Way // a feature not named here is off
	}{
		{"every capability", tools, caps, map[string]Way{"comment": REST, "inbox": REST, "knowledge_search": MCP}},
		{"a server before R0 (no capabilities)", tools, nil, map[string]Way{"comment": MCP, "inbox": MCP, "knowledge_search": MCP}},
		{"an empty list", tools, []string{}, map[string]Way{"comment": MCP, "inbox": MCP, "knowledge_search": MCP}},
		{"one capability missing", tools, without("posts.create"), map[string]Way{"comment": MCP, "inbox": REST, "knowledge_search": MCP}},
		{"a feature of the API missing", tools, without("error_codes"), map[string]Way{"comment": MCP, "inbox": MCP, "knowledge_search": MCP}},
		{"the tool missing, REST there", withoutTool("post_comment"), caps, map[string]Way{"comment": REST, "inbox": REST, "knowledge_search": MCP}},
		{"the tool missing, no capabilities", withoutTool("post_comment"), nil, map[string]Way{"inbox": MCP, "knowledge_search": MCP}},
	}
	for _, tc := range cases {
		report := detect(testFeatures, contractServer, tc.tools, tc.caps)
		for _, f := range testFeatures {
			want, on := tc.want[f.Name]
			if report.Enabled[f.Name] != on {
				t.Errorf("%s: %s on %v, want %v (missing %v)", tc.name, f.Name, report.Enabled[f.Name], on, report.Missing[f.Name])
				continue
			}
			if on && report.Via[f.Name] != want {
				t.Errorf("%s: %s via %v, want %v", tc.name, f.Name, report.Via[f.Name], want)
			}
			if _, has := report.Via[f.Name]; !on && has {
				t.Errorf("%s: %s is off and has a way", tc.name, f.Name)
			}
		}
	}
	// A feature off on both ways names what each lacks.
	report := detect(testFeatures, contractServer, withoutTool("post_comment"), without("posts.create"))
	if missing := report.Missing["comment"]; !slices.Contains(missing, "tool post_comment") || !slices.Contains(missing, "capability posts.create") {
		t.Fatalf("missing %v", missing)
	}
}

// meCapabilities reads the capabilities of a recorded /me kept beside the
// tests.
func meCapabilities(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Body struct {
			Data struct {
				APIVersion   string   `json:"api_version"`
				Capabilities []string `json:"capabilities"`
			} `json:"data"`
		} `json:"body"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil || len(recorded.Body.Data.Capabilities) == 0 {
		t.Fatalf("%s: %v", path, err)
	}
	return recorded.Body.Data.Capabilities
}

// ticketView are the five features K1 switches: the list, its search, the
// ticket, its pages and its texts whole.
var ticketView = []string{"tickets", "tickets_search", "ticket", "ticket_posts_paged", "ticket_full_text"}

// K1: a server that lists the capabilities of the core's R1 gets the
// ticket view over REST, feature by feature; one before R1 (its /me of
// API 1.2.0 recorded beside the tests) or before R0 (no capabilities)
// keeps the tools. Every other feature takes the tools whatever the
// server lists.
func TestTheTicketViewTakesTheRESTWayWithTheCapabilitiesOfR1(t *testing.T) {
	tools, _ := contractTools(t)
	caps := contractCapabilities(t)
	without := func(names ...string) []string {
		return slices.DeleteFunc(slices.Clone(caps), func(c string) bool { return slices.Contains(names, c) })
	}
	viewTools := func(tools []transport.Tool) []transport.Tool {
		return slices.DeleteFunc(slices.Clone(tools), func(tool transport.Tool) bool {
			return tool.Name == "list_tickets" || tool.Name == "get_ticket"
		})
	}
	rest := func(names ...string) map[string]Way {
		out := map[string]Way{}
		for _, name := range names {
			out[name] = REST
		}
		return out
	}
	cases := []struct {
		name  string
		tools []transport.Tool
		caps  []string
		rest  map[string]Way // the features over REST; every other one is on over MCP
		off   []string
	}{
		{"the contract's /me (API 1.5.0)", tools, caps, rest(ticketView...), nil},
		{"a /me of API 1.2.0, before R1", tools, meCapabilities(t, "testdata/me_1.2.0.json"), nil, nil},
		{"no capabilities, before R0", tools, nil, nil, nil},
		{"without tickets.paging", tools, without("tickets.paging"), nil, nil},
		{"without posts.show", tools, without("posts.show"), rest("tickets", "tickets_search", "ticket", "ticket_posts_paged"), nil},
		{"without tickets.list", tools, without("tickets.list"), rest("ticket", "ticket_posts_paged", "ticket_full_text"), nil},
		{"without tickets.show", tools, without("tickets.show"), rest("tickets", "tickets_search"), nil},
		// R1's number route is not needed: the client opens tickets by id.
		{"without tickets.by_number", tools, without("tickets.by_number"), rest(ticketView...), nil},
		{"the tools gone, the capabilities there", viewTools(tools), caps, rest(ticketView...), nil},
		{"the tools gone, no capabilities", viewTools(tools), nil, nil, ticketView},
	}
	for _, tc := range cases {
		report := Detect(contractServer, tc.tools, tc.caps)
		for _, f := range All {
			off := slices.Contains(tc.off, f.Name)
			if report.Enabled[f.Name] == off {
				t.Errorf("%s: %s on %v (missing %v)", tc.name, f.Name, report.Enabled[f.Name], report.Missing[f.Name])
				continue
			}
			want := MCP
			if w, ok := tc.rest[f.Name]; ok {
				want = w
			}
			if !off && report.Via[f.Name] != want {
				t.Errorf("%s: %s via %v, want %v", tc.name, f.Name, report.Via[f.Name], want)
			}
		}
	}
}

// A list and its search are one read: they need the same capabilities,
// or a search would go over REST while the list stays on the tools.
// Likewise a ticket and its pages.
func TestTheReadsOfOneRequestNeedTheSameCapabilities(t *testing.T) {
	need := map[string][]string{}
	for _, f := range All {
		need[f.Name] = slices.Sorted(slices.Values(f.Capabilities))
	}
	for _, pair := range [][2]string{{"tickets", "tickets_search"}, {"ticket", "ticket_posts_paged"}} {
		if !slices.Equal(need[pair[0]], need[pair[1]]) {
			t.Errorf("%s needs %v, %s %v", pair[0], need[pair[0]], pair[1], need[pair[1]])
		}
	}
	// The texts whole need what the ticket needs, and one comment's route.
	for _, c := range need["ticket"] {
		if !slices.Contains(need["ticket_full_text"], c) {
			t.Errorf("ticket_full_text lacks %s", c)
		}
	}
	if !slices.Contains(need["ticket_full_text"], "posts.show") {
		t.Error("ticket_full_text does not need posts.show")
	}
}

// Every capability a feature names is one the contract's /me lists: a
// misspelt one would keep the feature on the tools for good.
func TestEveryCapabilityAFeatureNamesIsRecorded(t *testing.T) {
	caps := contractCapabilities(t)
	for _, f := range All {
		for _, c := range f.Capabilities {
			if !slices.Contains(caps, c) {
				t.Errorf("%s names %s, which the contract's /me does not list", f.Name, c)
			}
		}
	}
}

// The capabilities of a server change nothing for a feature without them:
// the report with and without them is the same.
func TestCapabilitiesChangeNothingForTheOtherFeatures(t *testing.T) {
	tools, _ := contractTools(t)
	a := Detect(contractServer, tools, contractCapabilities(t))
	b := Detect(contractServer, tools, nil)
	for _, f := range All {
		if slices.Contains(ticketView, f.Name) {
			if len(f.Capabilities) == 0 {
				t.Errorf("%s names no capability", f.Name)
			}
			continue
		}
		if len(f.Capabilities) > 0 {
			t.Errorf("%s names capabilities %v; K1 switches the ticket view alone", f.Name, f.Capabilities)
		}
		if a.Enabled[f.Name] != b.Enabled[f.Name] || a.Via[f.Name] != b.Via[f.Name] || !slices.Equal(a.Missing[f.Name], b.Missing[f.Name]) {
			t.Errorf("%s differs: %v/%v/%v and %v/%v/%v", f.Name, a.Enabled[f.Name], a.Via[f.Name], a.Missing[f.Name], b.Enabled[f.Name], b.Via[f.Name], b.Missing[f.Name])
		}
	}
}
