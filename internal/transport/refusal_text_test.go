package transport

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/contract"
)

// K6: the words of a refusal, for a person. The class decides what the
// client does (rule 2); this is only what it shows, raw until then.

// Each recorded refusal of a booking gives its text, and the recorded
// answers without one give none: the caller then says it from the class.
func TestARecordedRefusalNamesItsText(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	const group = "POST /api/v1/tickets/:ticket_id/time_sessions"
	for name, want := range map[string]string{
		"invalid_input_no_duration": "Bitte geben Sie eine Dauer an (duration_string oder duration_minutes).",
		"invalid_input_over_a_day":  "Dauer darf höchstens 1440 Minuten (24 Stunden) betragen",
		"idempotency_mismatch":      "idempotency_key already used with different parameters",
		"forbidden":                 "",
		"read_only":                 "",
		"not_found":                 "",
		"hidden":                    "",
	} {
		ex, ok := file.Contract.REST[group][name]
		if !ok {
			t.Fatalf("the contract has no %s/%s", group, name)
		}
		req := recordedRequest(t, ex)
		srv := replayREST(t, req, keyOf(req), ex.Response.Status, ex.Response.Body)
		ans := sendRecorded(t, newClient(t, srv.URL), req)
		if ans.OK() {
			t.Fatalf("%s: a success", name)
		}
		if got := ans.RefusalText(); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

// What the contract cannot hold: bodies in other forms. Only the API's own
// form counts, as for its code: a proxy's body is nobody's refusal. The
// first text that says something wins; a part of another type takes nothing
// else with it; and the text stays as it came, cleaned where it is shown.
func TestARefusalsTextComesFromTheAPIsOwnFormAlone(t *testing.T) {
	for _, tc := range []struct {
		name, message, want string
	}{
		{"the first of several", `{"error":"validation_failed","errors":["one","two"]}`, "one"},
		{"errors before message", `{"error":"x","errors":["one"],"message":"other"}`, "one"},
		{"message alone", `{"error":"x","message":"said"}`, "said"},
		{"an empty list", `{"error":"x","errors":[],"message":"said"}`, "said"},
		{"a blank first", `{"error":"x","errors":["","  ","two"]}`, "two"},
		{"blanks alone", `{"error":"x","errors":["  "],"message":" "}`, ""},
		{"a number first", `{"error":"x","errors":[1,"two"]}`, "two"},
		{"errors of another type", `{"error":"x","errors":"text","message":"said"}`, "said"},
		{"errors of objects", `{"error":"x","errors":[{"a":1}],"message":"said"}`, "said"},
		{"a message of another type", `{"error":"x","errors":["one"],"message":7}`, "one"},
		{"nothing but the error word", `{"error":"forbidden","code":"forbidden"}`, ""},
		{"no error word", `{"errors":["from a proxy"],"message":"from a proxy"}`, ""},
		{"an error of another type", `{"error":1,"errors":["from a proxy"]}`, ""},
		{"no object", `["one"]`, ""},
		{"no JSON", `<html>502</html>`, ""},
		{"the text of a tool", `Not authorized.`, ""},
		{"empty", ``, ""},
		{"raw", `{"error":"x","errors":["` + `\u001b[31mred"]}`, "\x1b[31mred"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Answer{Class: InvalidInput, Message: tc.message}).RefusalText(); got != tc.want {
				t.Fatalf("%q, want %q", got, tc.want)
			}
		})
	}
}
