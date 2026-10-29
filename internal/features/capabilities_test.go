package features

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K0: a feature can name the REST capabilities (GET /api/v1/me, since the
// core's R0) its REST way needs. A server that lists them all gets the
// REST way; one without capabilities, or without one of them, the MCP
// way, as long as both exist. The production list names none yet (K1 on);
// the mechanics are tested with features of the tests' own.

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

// K0 switches no feature: the production list names no capability, and on
// the contract, capabilities and all, every feature takes the MCP way.
func TestNoFeatureTakesTheRESTWayYet(t *testing.T) {
	for _, f := range All {
		if len(f.Capabilities) > 0 {
			t.Errorf("%s names capabilities %v; K0 switches no feature", f.Name, f.Capabilities)
		}
	}
	tools, _ := contractTools(t)
	report := Detect(contractServer, tools, contractCapabilities(t))
	for _, f := range All {
		if !report.Enabled[f.Name] || report.Via[f.Name] != MCP {
			t.Errorf("%s: on %v via %v", f.Name, report.Enabled[f.Name], report.Via[f.Name])
		}
	}
}

// The capabilities of a server change nothing for a feature without them:
// the report with and without them is the same.
func TestCapabilitiesChangeNothingForTheProductionFeatures(t *testing.T) {
	tools, _ := contractTools(t)
	a := Detect(contractServer, tools, contractCapabilities(t))
	b := Detect(contractServer, tools, nil)
	for _, f := range All {
		if a.Enabled[f.Name] != b.Enabled[f.Name] || a.Via[f.Name] != b.Via[f.Name] || !slices.Equal(a.Missing[f.Name], b.Missing[f.Name]) {
			t.Errorf("%s differs: %v/%v/%v and %v/%v/%v", f.Name, a.Enabled[f.Name], a.Via[f.Name], a.Missing[f.Name], b.Enabled[f.Name], b.Via[f.Name], b.Missing[f.Name])
		}
	}
}
