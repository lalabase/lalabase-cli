package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

func TestR1DirectoryRemovalDoesNotRelabelActiveInbox(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.press("esc")
	h.f.orgs = func() ([]api.Ref, transport.Answer) {
		return []api.Ref{{ID: orgB, Name: "Only B remains"}}, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.drive(h.press("R"))
	h.press("esc")
	text := ansi.Strip(h.m.View().Content)
	if h.m.app.active != nil && h.m.app.active.org == orgA && len(h.m.app.active.rows) > 0 && strings.Contains(text, "All organisations › Inbox") {
		t.Fatalf("filtered A rows relabelled as global after directory removal: active.org=%q first.row.org=%q\n%s", h.m.app.active.org, h.m.app.active.rows[0].Organisation.ID, text)
	}
}

func TestR1HelpNamesOnlyActivePageDirections(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	text := ansi.Strip(h.m.View().Content)
	before := text
	calls := len(h.f.inboxCalls())
	cmd := h.press("[")
	after := ansi.Strip(h.m.View().Content)
	if strings.Contains(text, "[/] page") && cmd == nil && before == after && len(h.f.inboxCalls()) == calls {
		t.Fatalf("help advertises inactive '[' at page %d of %d (command nil, screen unchanged, no request):\n%s", h.m.app.active.page, lastPage(h.m.app.active.total, h.m.app.active.perPage), text)
	}
}

func TestR1MissingInboxBaseArgumentsAndRetry(t *testing.T) {
	for _, arg := range []string{"status", "page", "per_page"} {
		t.Run(arg, func(t *testing.T) {
			h := newHarness(t)
			tools := withoutArgument(h.d.tools, "list_notifications", arg)
			h.f.tools = func() ([]transport.Tool, transport.Answer) {
				return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
			}
			h.start()
			h.drive(h.press("R"))
			for _, org := range []string{"", orgA, orgB} {
				h.drive(h.choose(org))
			}
			if len(h.f.inboxCalls()) != 0 || h.m.app.inboxOn() || h.m.app.active != nil {
				t.Fatalf("missing %s still issued an inbox request", arg)
			}
		})
	}
}
