package features

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

func contractTools(t *testing.T) ([]transport.Tool, *contract.File) {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := file.Tools()
	if err != nil {
		t.Fatal(err)
	}
	tools := make([]transport.Tool, 0, len(recorded))
	for _, tool := range recorded {
		tools = append(tools, transport.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}
	return tools, file
}

var contractServer = transport.ServerInfo{Name: "lalabase", Version: "0.9.0"}

// On the contract the client was built against (MCP 0.9.0, server slice S1
// included), every feature is on.
func TestOnTheContractEveryFeatureIsOn(t *testing.T) {
	tools, _ := contractTools(t)
	report := Detect(contractServer, tools)
	for _, f := range All {
		if !report.Enabled[f.Name] {
			t.Errorf("%s: off (missing %v)", f.Name, report.Missing[f.Name])
		}
	}
	if report.BelowTested {
		t.Error("the contract's own version counts as below the tested one")
	}
}

// For every feature the contract has, its answers carry the fields the
// adapter needs: presence is checked, not only that the answer decodes.
func TestEveryEnabledFeatureFindsItsFieldsInTheRecordedAnswers(t *testing.T) {
	tools, file := contractTools(t)
	report := Detect(contractServer, tools)
	for _, f := range All {
		if !report.Enabled[f.Name] {
			continue
		}
		ex, ok := file.Contract.MCP.Calls[f.Spec.Tool]["ok"]
		if !ok {
			t.Errorf("%s: the contract records no ok answer of %s", f.Name, f.Spec.Tool)
			continue
		}
		var body struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(ex.Response.Body, &body); err != nil || len(body.Result.Content) == 0 {
			t.Fatalf("%s: %v", f.Name, err)
		}
		missing, err := api.Missing([]byte(body.Result.Content[0].Text), f.Spec.Required)
		if err != nil || len(missing) > 0 {
			t.Errorf("%s: the recorded answer lacks %v (%v)", f.Name, missing, err)
		}
	}
	for _, spec := range []api.Spec{api.MeSpec, api.OrganisationsSpec, api.ProjectsSpec} {
		ex := file.Contract.REST[spec.Name]["ok"]
		missing, err := api.Missing(ex.Response.Body, spec.Required)
		if err != nil || len(missing) > 0 {
			t.Errorf("%s: the recorded answer lacks %v (%v)", spec.Name, missing, err)
		}
	}
}

// A required field the recorded answers do not have would make the runtime
// check fail every real answer: the check above must be able to see it.
func TestTheFieldCheckSeesAFieldTheContractLacks(t *testing.T) {
	_, file := contractTools(t)
	ex := file.Contract.MCP.Calls["get_ticket"]["ok"]
	var body struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	_ = json.Unmarshal(ex.Response.Body, &body)
	missing, _ := api.Missing([]byte(body.Result.Content[0].Text), append(slices.Clone(api.TicketSpec.Required), "posts_cursor"))
	if !slices.Equal(missing, []string{"posts_cursor"}) {
		t.Fatalf("got %v", missing)
	}
}

// Removing one argument from one schema switches off exactly the features
// that send it.
func TestAMissingArgumentSwitchesOffOnlyItsFeature(t *testing.T) {
	tools, _ := contractTools(t)
	for i, tool := range tools {
		if tool.Name != "list_tickets" {
			continue
		}
		var schema map[string]any
		_ = json.Unmarshal(tool.InputSchema, &schema)
		delete(schema["properties"].(map[string]any), "q")
		tools[i].InputSchema, _ = json.Marshal(schema)
	}
	report := Detect(contractServer, tools)
	if report.Enabled["tickets_search"] {
		t.Error("the search stayed on without q")
	}
	for _, name := range []string{"inbox", "mark_read", "tickets", "ticket", "comment", "knowledge_search"} {
		if !report.Enabled[name] {
			t.Errorf("%s went off with list_tickets' q", name)
		}
	}
	if !slices.Contains(report.Missing["tickets_search"], "list_tickets: argument q") {
		t.Errorf("missing %v", report.Missing["tickets_search"])
	}
}

func TestAMissingToolSwitchesOffItsFeatures(t *testing.T) {
	tools, _ := contractTools(t)
	tools = slices.DeleteFunc(tools, func(tool transport.Tool) bool { return tool.Name == "post_comment" })
	report := Detect(contractServer, tools)
	if report.Enabled["comment"] || !slices.Contains(report.Missing["comment"], "tool post_comment") {
		t.Fatalf("comment: %v %v", report.Enabled["comment"], report.Missing["comment"])
	}
}

// A newer server with more tools is a notice, never a reason to switch
// something off.
func TestANewerServerWithMoreToolsIsANotice(t *testing.T) {
	tools, _ := contractTools(t)
	tools = append(tools, transport.Tool{Name: "update_ticket", InputSchema: json.RawMessage(`{"type":"object","properties":{"ticket_id":{}}}`)})
	report := Detect(transport.ServerInfo{Name: "lalabase", Version: "0.9.0"}, tools)
	for _, f := range All {
		if f.Since == "" && !report.Enabled[f.Name] {
			t.Errorf("%s went off on a newer server", f.Name)
		}
	}
	if !slices.Contains(report.Unused, "update_ticket") {
		t.Errorf("unused %v", report.Unused)
	}
}

// The version is shown, never trusted: a high number with a missing schema
// still switches the feature off, and a low number is a notice only.
func TestTheVersionDecidesNothing(t *testing.T) {
	tools, _ := contractTools(t)
	tools = slices.DeleteFunc(tools, func(tool transport.Tool) bool { return tool.Name == "get_ticket" })
	report := Detect(transport.ServerInfo{Name: "lalabase", Version: "9.0.0"}, tools)
	if report.Enabled["ticket"] {
		t.Error("a high version switched on a feature whose tool is missing")
	}
	tools, _ = contractTools(t)
	report = Detect(transport.ServerInfo{Name: "lalabase", Version: "0.6.0"}, tools)
	if !report.BelowTested || !report.Enabled["inbox"] {
		t.Errorf("0.6.0: below tested %v, inbox %v", report.BelowTested, report.Enabled["inbox"])
	}
	for _, v := range []string{"", "x", "0.7", "1.0.0-beta"} {
		r := Detect(transport.ServerInfo{Version: v}, tools)
		if v == "1.0.0-beta" && r.BelowTested {
			t.Errorf("%q counted as below", v)
		}
		if (v == "" || v == "x") && !r.BelowTested {
			t.Errorf("%q not flagged as unknown", v)
		}
	}
}

// Every feature's adapter sends only what its spec declares (internal/api
// tests that), so the spec's arguments are what detection must find.
func TestFeaturesNameTheArgumentsTheirAdaptersSend(t *testing.T) {
	for _, f := range All {
		if f.Spec.Tool == "" {
			t.Errorf("%s has no tool", f.Name)
		}
		for _, arg := range f.ExtraArgs {
			if slices.Contains(f.Spec.Args, arg) {
				t.Errorf("%s lists %s twice", f.Name, arg)
			}
		}
		if f.Since != "" && len(f.ExtraArgs) == 0 {
			t.Errorf("%s waits for %s but needs nothing new", f.Name, f.Since)
		}
	}
}

// Every argument a feature sends, removed from its tool's schema one at a
// time, switches that feature off: idempotency_key as much as ticket_id.
func TestEveryArgumentOfEveryFeatureIsChecked(t *testing.T) {
	for _, f := range All {
		for _, arg := range append(slices.Clone(f.Spec.Args), f.ExtraArgs...) {
			tools, _ := contractTools(t)
			for i, tool := range tools {
				if tool.Name != f.Spec.Tool {
					continue
				}
				var schema map[string]any
				_ = json.Unmarshal(tool.InputSchema, &schema)
				delete(schema["properties"].(map[string]any), arg)
				tools[i].InputSchema, _ = json.Marshal(schema)
			}
			if Detect(contractServer, tools).Enabled[f.Name] {
				t.Errorf("%s stayed on without %s", f.Name, arg)
			}
		}
	}
}

// An adapter's optional arguments are exactly the extra arguments of the
// features that run it: an argument no feature checks could be sent to a
// server that does not know it.
func TestEveryOptionalArgumentBelongsToAFeature(t *testing.T) {
	for _, spec := range api.Specs {
		var extra []string
		for _, f := range All {
			if f.Spec.Name == spec.Name {
				extra = append(extra, f.ExtraArgs...)
			}
		}
		slices.Sort(extra)
		optional := slices.Sorted(slices.Values(spec.Optional))
		if !slices.Equal(slices.Compact(extra), optional) {
			t.Errorf("%s: optional %v, features check %v", spec.Name, optional, extra)
		}
		for _, arg := range spec.Optional {
			if slices.Contains(spec.Args, arg) {
				t.Errorf("%s: %s is both required and optional", spec.Name, arg)
			}
		}
	}
}

// Removing one S1 argument switches off its feature and leaves the base
// feature of the same tool on: an older server keeps the inbox and the ticket.
func TestAnS1ArgumentSwitchesOffOnlyItsFeature(t *testing.T) {
	cases := []struct{ tool, arg, off, stays string }{
		{"list_notifications", "organisation_id", "inbox_by_organisation", "inbox"},
		{"get_ticket", "posts_page", "ticket_posts_paged", "ticket"},
		{"get_ticket", "attachments_page", "ticket_posts_paged", "ticket_full_text"},
		{"get_ticket", "post_id", "ticket_full_text", "ticket_posts_paged"},
	}
	for _, c := range cases {
		tools, _ := contractTools(t)
		for i, tool := range tools {
			if tool.Name != c.tool {
				continue
			}
			var schema map[string]any
			_ = json.Unmarshal(tool.InputSchema, &schema)
			delete(schema["properties"].(map[string]any), c.arg)
			tools[i].InputSchema, _ = json.Marshal(schema)
		}
		report := Detect(contractServer, tools)
		if report.Enabled[c.off] || !report.Enabled[c.stays] {
			t.Errorf("without %s: %s on %v, %s on %v", c.arg, c.off, report.Enabled[c.off], c.stays, report.Enabled[c.stays])
		}
	}
}

// The version the client says it was tested with is the version of its
// contract copy.
func TestTheTestedVersionIsTheContractCopys(t *testing.T) {
	source, err := contract.LoadSource()
	if err != nil {
		t.Fatal(err)
	}
	if source.MCPServerVersion != MinTestedServerVersion {
		t.Fatalf("contract copy %s, MinTestedServerVersion %s", source.MCPServerVersion, MinTestedServerVersion)
	}
}

// Which feature checks which optional argument, written out on its own: a
// feature that checks another feature's argument switches on against a server
// that lacks it.
func TestEachOptionalArgumentBelongsToItsFeature(t *testing.T) {
	want := map[string]string{
		"q":                "tickets_search",
		"organisation_id":  "inbox_by_organisation",
		"posts_page":       "ticket_posts_paged",
		"posts_per_page":   "ticket_posts_paged",
		"posts_order":      "ticket_posts_paged",
		"attachments_page": "ticket_posts_paged",
		"full_text":        "ticket_full_text",
		"post_id":          "ticket_full_text",
	}
	seen := map[string]bool{}
	for _, f := range All {
		for _, arg := range f.ExtraArgs {
			if want[arg] != f.Name {
				t.Errorf("%s checks %s, which belongs to %q", f.Name, arg, want[arg])
			}
			seen[arg] = true
		}
	}
	for arg := range want {
		if !seen[arg] {
			t.Errorf("no feature checks %s", arg)
		}
	}
}
