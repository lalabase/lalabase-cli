package launch

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// w opens the ticket or the meeting the pane shows in the browser: its
// page in the core, at an address built here from the profile's server
// and three checked ids, the word of the path the page's kind, the
// client's own. As for a file, no string of an answer becomes an address
// (the meeting's answer carries a path; it is not read).

const (
	pageProject = "00000000-0000-4000-8000-000000000003"
	pageID      = "00000000-0000-4000-8000-000000000004"
)

func TestPageAddressIsBuiltFromTheServerAndTheIDs(t *testing.T) {
	for _, tc := range []struct {
		server string
		kind   PageKind
		want   string
	}{
		{"https://app.example.com", TicketPage, "https://app.example.com/org/" + fileOrg + "/projects/" + pageProject + "/tickets/" + pageID},
		{"https://app.example.com/", MeetingPage, "https://app.example.com/org/" + fileOrg + "/projects/" + pageProject + "/meetings/" + pageID},
		{"https://app.example.com:8443/base/", TicketPage, "https://app.example.com:8443/base/org/" + fileOrg + "/projects/" + pageProject + "/tickets/" + pageID},
		{"http://localhost:3000", MeetingPage, "http://localhost:3000/org/" + fileOrg + "/projects/" + pageProject + "/meetings/" + pageID},
	} {
		got, ok := pageAddress(tc.server, Page{Kind: tc.kind, Org: fileOrg, Project: pageProject, ID: pageID})
		if !ok || got != tc.want {
			t.Fatalf("%s %d: %q, %v", tc.server, tc.kind, got, ok)
		}
	}
}

func TestPageAddressRefuses(t *testing.T) {
	good := Page{Kind: TicketPage, Org: fileOrg, Project: pageProject, ID: pageID}
	bad := []string{"", "a", "..", "a/b", "a%2fb", "a\nb", "a b", "a?b", "a#b", pageID + "/", " " + pageID}
	for _, id := range bad {
		for _, p := range []Page{
			{Kind: good.Kind, Org: id, Project: good.Project, ID: good.ID},
			{Kind: good.Kind, Org: good.Org, Project: id, ID: good.ID},
			{Kind: good.Kind, Org: good.Org, Project: good.Project, ID: id},
		} {
			if got, ok := pageAddress("https://app.example.com", p); ok {
				t.Fatalf("%+v gave %q", p, got)
			}
		}
	}
	for _, kind := range []PageKind{0, MeetingPage + 1, 255} {
		if got, ok := pageAddress("https://app.example.com", Page{Kind: kind, Org: fileOrg, Project: pageProject, ID: pageID}); ok {
			t.Fatalf("kind %d gave %q", kind, got)
		}
	}
	for _, server := range []string{"", "file:///etc", "javascript:alert(1)", "ftp://example.com", "example.com", "https://", "http://"} {
		if got, ok := pageAddress(server, good); ok {
			t.Fatalf("server %q gave %q", server, got)
		}
	}
}

func openPageDone(t *testing.T, l *Launcher, p Page) Done {
	t.Helper()
	done := make(chan Done, 1)
	go func() {
		d, _ := l.OpenPage(9, p)().(Done)
		done <- d
	}()
	select {
	case d := <-done:
		return d
	case <-time.After(10 * time.Second):
		t.Fatal("no answer")
	}
	return Done{}
}

func TestOpenPageStartsTheOpenerWithOneArgument(t *testing.T) {
	l, calls := openLauncher(t, Config{}, nil)
	d := openPageDone(t, l, Page{Kind: MeetingPage, Org: fileOrg, Project: pageProject, ID: pageID})
	if d.Kind != FileOpened || d.Action != OpenPageAction || d.Ref != 9 || d.Ticket.ID != pageID {
		t.Fatalf("done = %+v", d)
	}
	want := "https://app.example.com/org/" + fileOrg + "/projects/" + pageProject + "/meetings/" + pageID
	if len(*calls) != 1 || (*calls)[0].path != "/bin/opener" || len((*calls)[0].args) != 1 || (*calls)[0].args[0] != want {
		t.Fatalf("calls = %+v", *calls)
	}
	if strings.Contains((*calls)[0].args[0], "secret-token") {
		t.Fatal("the token is in the address")
	}
}

func TestOpenPageWithABadIDStartsNothing(t *testing.T) {
	l, calls := openLauncher(t, Config{}, nil)
	if d := openPageDone(t, l, Page{Kind: TicketPage, Org: fileOrg, Project: "../x", ID: pageID}); d.Kind != NotHandedOver || d.Action != OpenPageAction {
		t.Fatalf("done = %+v", d)
	}
	if len(*calls) != 0 {
		t.Fatalf("calls = %+v", *calls)
	}
}

func TestOpenPageOverSSHStartsNothing(t *testing.T) {
	l, calls := openLauncher(t, Config{Remote: true}, nil)
	if d := openPageDone(t, l, Page{Kind: TicketPage, Org: fileOrg, Project: pageProject, ID: pageID}); d.Kind != NoOpener {
		t.Fatalf("done = %+v", d)
	}
	if len(*calls) != 0 {
		t.Fatalf("calls = %+v", *calls)
	}
}

// A ticket's number goes to its notice through the Done, never into the
// address: the address of ticket #12 is the one without a number.
func TestOpenPageGivesTheNumberToTheNoticeAlone(t *testing.T) {
	l, calls := openLauncher(t, Config{}, nil)
	p := Page{Kind: TicketPage, Org: fileOrg, Project: pageProject, ID: pageID, Number: 12}
	d := openPageDone(t, l, p)
	if d.Ticket != (Ticket{Org: fileOrg, Project: pageProject, ID: pageID, Number: 12}) || d.Kind != FileOpened {
		t.Fatalf("done = %+v", d)
	}
	want := "https://app.example.com/org/" + fileOrg + "/projects/" + pageProject + "/tickets/" + pageID
	if len(*calls) != 1 || (*calls)[0].args[0] != want {
		t.Fatalf("calls = %+v", *calls)
	}
}

// The opener's refusal is the page's own outcome, not a file's.
func TestOpenPageWhoseOpenerFails(t *testing.T) {
	l, _ := openLauncher(t, Config{}, errors.New("refused"))
	d := openPageDone(t, l, Page{Kind: MeetingPage, Org: fileOrg, Project: pageProject, ID: pageID})
	if d.Kind != OpenFailed || d.Action != OpenPageAction {
		t.Fatalf("done = %+v", d)
	}
}
