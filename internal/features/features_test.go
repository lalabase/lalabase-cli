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

var contractServer = transport.ServerInfo{Name: "lalabase", Version: "0.11.0"}

// On the contract the client was built against (MCP 0.11.0, server slices
// S1, S2 and S2b included), every feature is on.
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
	// The recorded successes a feature's fields are checked against where
	// they are not the tool's one "ok": update_ticket records one per
	// kind, the plain comment lives in a group of its own.
	successes := map[string][][2]string{
		"ticket_update": {{"update_ticket", "ok_status"}, {"update_ticket", "ok_assign"}, {"update_ticket", "ok_title"}},
		"comment_plain": {{"post_comment_formats", "plain"}},
	}
	for _, f := range All {
		if !report.Enabled[f.Name] {
			continue
		}
		cases, ok := successes[f.Name]
		if !ok {
			cases = [][2]string{{f.Spec.Tool, "ok"}}
		}
		for _, c := range cases {
			ex, ok := file.Contract.MCP.Calls[c[0]][c[1]]
			if !ok {
				t.Errorf("%s: the contract records no %s/%s", f.Name, c[0], c[1])
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
				t.Errorf("%s: the recorded %s/%s lacks %v (%v)", f.Name, c[0], c[1], missing, err)
			}
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
	tools = append(tools, transport.Tool{Name: "delete_ticket", InputSchema: json.RawMessage(`{"type":"object","properties":{"ticket_id":{}}}`)})
	report := Detect(transport.ServerInfo{Name: "lalabase", Version: "0.11.0"}, tools)
	for _, f := range All {
		if !report.Enabled[f.Name] {
			t.Errorf("%s went off on a newer server", f.Name)
		}
	}
	if !slices.Contains(report.Unused, "delete_ticket") {
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
			// A slice brings an argument of a tool the contract had, or a
			// tool of its own: never a feature that an older server has.
			for _, g := range All {
				if g.Since == "" && g.Spec.Tool == f.Spec.Tool {
					t.Errorf("%s waits for %s but needs nothing %s lacks", f.Name, f.Since, g.Name)
				}
			}
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

// A feature that brings a whole tool (Since set, no extra arguments)
// declares every argument of the tool's recorded schema in its spec, so
// the feature needs the whole tool: a server with half of it keeps the
// feature off. The dispatcher's two arguments (project_id,
// idempotency_key) are in the recorded schema, so they count. Review of
// #16, finding 4 a: remove_assignee_ids is declared for this reason and
// never sent, and nothing else pins it.
func TestAWholeToolFeatureDeclaresEveryArgumentOfItsSchema(t *testing.T) {
	tools, _ := contractTools(t)
	for _, f := range All {
		if f.Since == "" || len(f.ExtraArgs) > 0 {
			continue
		}
		for _, tool := range tools {
			if tool.Name != f.Spec.Tool {
				continue
			}
			declared := slices.Concat(f.Spec.Args, f.Spec.Optional)
			for name := range tool.Properties() {
				if !slices.Contains(declared, name) {
					t.Errorf("%s: the schema of %s has %s, which the spec does not declare", f.Name, tool.Name, name)
				}
			}
			for _, name := range declared {
				if _, ok := tool.Properties()[name]; !ok {
					t.Errorf("%s: the spec declares %s, which the schema of %s lacks", f.Name, name, tool.Name)
				}
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
		// S2b: a 0.10.0 server has update_ticket and no plain comments.
		{"post_comment", "format", "comment_plain", "comment"},
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
		"format":           "comment_plain",
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

// toolsListOf reads a recorded tools/list kept beside the tests.
func toolsListOf(t *testing.T, path string) []transport.Tool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Result struct {
			Tools []transport.Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Result.Tools) == 0 {
		t.Fatalf("%s: %v", path, err)
	}
	return body.Result.Tools
}

// On the server before S2 (MCP 0.9.0, its tools/list kept in testdata) the
// two write features of C2 are off, with the reason named, and the ten
// features of C1 stay on: a 0.9.0 server keeps everything it had.
func TestOnTheServerBeforeS2TheWriteFeaturesAreOff(t *testing.T) {
	tools := toolsListOf(t, "testdata/tools_list_0.9.0.json")
	report := Detect(transport.ServerInfo{Name: "lalabase", Version: "0.9.0"}, tools)
	on := 0
	for _, f := range All {
		want := f.Since != "S2" && f.Since != "S2b"
		if report.Enabled[f.Name] != want {
			t.Errorf("%s on %v on a 0.9.0 server (missing %v)", f.Name, report.Enabled[f.Name], report.Missing[f.Name])
		}
		if report.Enabled[f.Name] {
			on++
		}
	}
	if on != 10 {
		t.Errorf("%d features on, want the ten of C1", on)
	}
	if !slices.Contains(report.Missing["ticket_update"], "tool update_ticket") || !slices.Contains(report.Missing["comment_plain"], "post_comment: argument format") {
		t.Errorf("missing %v / %v", report.Missing["ticket_update"], report.Missing["comment_plain"])
	}
	if !report.BelowTested {
		t.Error("0.9.0 is not below the tested 0.11.0")
	}
}

// A 0.10.0 server (S2 without S2b) has update_ticket and no plain
// comments: the status, assignment and title can be written, a comment
// cannot.
func TestOnAServerWithS2AloneOnlyThePlainCommentIsOff(t *testing.T) {
	tools, _ := contractTools(t)
	for i, tool := range tools {
		if tool.Name != "post_comment" {
			continue
		}
		var schema map[string]any
		_ = json.Unmarshal(tool.InputSchema, &schema)
		delete(schema["properties"].(map[string]any), "format")
		tools[i].InputSchema, _ = json.Marshal(schema)
	}
	report := Detect(transport.ServerInfo{Name: "lalabase", Version: "0.10.0"}, tools)
	if !report.Enabled["ticket_update"] || report.Enabled["comment_plain"] || !report.Enabled["comment"] || !report.BelowTested {
		t.Fatalf("ticket_update %v, comment_plain %v, comment %v, below %v", report.Enabled["ticket_update"], report.Enabled["comment_plain"], report.Enabled["comment"], report.BelowTested)
	}
}
