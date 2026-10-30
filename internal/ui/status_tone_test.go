package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// A status is drawn in the tone of what it means in its project, read from
// the server's status functions, never from its name. The last function
// the lifecycle knows wins; a function the client does not know, or none,
// leaves the status as the line around it.
func TestAStatusIsDrawnInTheToneOfItsMeaning(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		functions []string
		want      present.Style
	}{
		{nil, present.Strong},
		{[]string{}, present.Strong},
		{[]string{"is_backlog"}, present.Faint},
		{[]string{"is_new"}, present.StatusOpen},
		{[]string{"is_open"}, present.StatusOpen},
		{[]string{"is_in_progress"}, present.StatusActive},
		{[]string{"is_completed"}, present.StatusDone},
		{[]string{"is_closed"}, present.StatusDone},
		{[]string{"is_completed", "is_closed"}, present.StatusDone},
		{[]string{"is_new", "is_in_progress"}, present.StatusActive},
		{[]string{"is_in_progress", "is_new"}, present.StatusActive},
		{[]string{"is_waiting"}, present.Strong},
		{[]string{"is_waiting", "is_open"}, present.StatusOpen},
	} {
		if got := statusStyle(c.functions, present.Strong); got != c.want {
			t.Errorf("%q: %v, want %v", c.functions, got, c.want)
		}
	}
}

// statusRows gives the rows of Website the meaning a project could give
// them: #1 in progress, #2 new, #3 done, #4 no status, #5 in the backlog,
// #6 a status the project gives no function.
func statusRows(h *harness) {
	meanings := map[int][]string{1: {"is_in_progress"}, 2: {"is_new"}, 3: {"is_completed", "is_closed"}, 5: {"is_backlog"}}
	h.f.setTickets(func(q api.TicketQuery) transport.Answer {
		if q.ProjectID != website {
			return h.d.defaultTickets(q)
		}
		rows := h.d.ticketRows(website, 30)
		for i := range rows {
			rows[i].StatusFunctions = meanings[rows[i].Number]
		}
		return ticketPageAnswer(1, 25, len(rows), rows[:25])
	})
}

// The list draws each row's status in its tone; a row without a meaning,
// as every row of a server before the field, stays plain. A done row is
// quiet, whatever else its status means; no other row is.
func TestTheTicketListDrawsTheStatusInItsTone(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	statusRows(h)
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(website))
	want := map[string]present.Style{"#1 ": present.StatusActive, "#2 ": present.StatusOpen, "#3 ": present.StatusDone, "#4 ": present.Plain, "#5 ": present.Faint, "#6 ": present.Plain}
	seen := 0
	for _, r := range h.m.ticketsColumn().Rows {
		for prefix, style := range want {
			if strings.HasPrefix(strings.TrimSpace(r.Primary.String()), prefix) {
				seen++
				if r.TrailingStyle != style {
					t.Errorf("%s (%s): %v, want %v", r.Primary.String(), r.Trailing.String(), r.TrailingStyle, style)
				}
				if r.Quiet != (style == present.StatusDone) {
					t.Errorf("%s (%s): quiet %v", r.Primary.String(), r.Trailing.String(), r.Quiet)
				}
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("%d of %d rows seen", seen, len(want))
	}
}

// The ticket's head draws its status in its tone at the start of the faint
// line of facts; without a meaning the status is as faint as the rest.
func TestTheTicketHeadDrawsTheStatusInItsTone(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		functions []string
		want      present.Style
	}{
		{[]string{"is_completed"}, present.StatusDone},
		{nil, present.Faint},
	} {
		h := newHarness(t)
		h.openSix()
		d := h.m.app.detail
		d.record.StatusFunctions = c.functions
		tl := ticketText(d, ticketContext{paged: true, when: h.m.app.when})
		status := statusOf(d.record.Status)
		st := tl.styles[1]
		if !strings.HasPrefix(tl.lines[1], status+" · ") || st.Lead != len(status) || st.LeadStyle != c.want || st.RestStyle != present.Faint {
			t.Fatalf("%q: the facts %q %+v", c.functions, tl.lines[1], st)
		}
	}
}

// Every status function the core sends in its recorded answers is one the
// client gives a tone: a function renamed in the core
// (SystemFunctions::Registry) turns red here, not grey on a screen.
func TestTheClientKnowsEveryStatusFunctionTheCoreSends(t *testing.T) {
	t.Parallel()

	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, tone := range statusTones {
		known[tone.function] = true
	}
	var walk func(v any)
	seen := 0
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				if k == "status_functions" {
					list, ok := child.([]any)
					if !ok {
						t.Errorf("status_functions is %T, not a list", child)
						continue
					}
					seen++
					for _, f := range list {
						if s, _ := f.(string); !known[s] {
							t.Errorf("the core sends status function %v, the client gives it no tone", f)
						}
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	for tool, examples := range file.Contract.MCP.Calls {
		for name, ex := range examples {
			var body struct {
				Result struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"result"`
			}
			_ = json.Unmarshal(ex.Response.Body, &body)
			for _, c := range body.Result.Content {
				var payload any
				if json.Unmarshal([]byte(c.Text), &payload) == nil {
					walk(payload)
				} else if strings.Contains(c.Text, "status_functions") {
					t.Errorf("%s/%s: status_functions in a text that is no JSON", tool, name)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("no recorded answer carries status_functions")
	}
}
