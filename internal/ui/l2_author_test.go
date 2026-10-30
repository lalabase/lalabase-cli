package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/gboyke/lalabase-cli/internal/api"
)

// openLogin opens the recorded ticket #1, whose comments are by Ben Berger
// and by Anna Albers, the token's own person.
func (h *harness) openLogin() {
	h.t.Helper()
	h.openTickets(website)
	h.m.app.tickets.selected = login
	h.drive(h.press("enter"))
	if d := h.m.app.detail; d == nil || !d.known || len(d.record.Posts) != 2 {
		h.t.Fatalf("ticket #1 is not open with its two comments: %+v", d)
	}
}

// authorLine is the line of the screen as written that holds a comment's
// head by the author (the name, its mark if any, two spaces and the time),
// or "".
func authorLine(h *harness, name string) string {
	h.t.Helper()
	head := regexp.MustCompile(regexp.QuoteMeta(name) + `( \(you\))?  [0-9]`)
	for _, line := range strings.Split(h.m.View().Content, "\n") {
		if head.MatchString(ansi.Strip(line)) {
			return line
		}
	}
	return ""
}

// The contract's /me and the recorded ticket name the person by the same
// kind of id: the user's UUID, so an author is the token's own person
// exactly when the ids are equal.
func TestTheContractNamesTheOwnAuthorByTheUsersID(t *testing.T) {
	t.Parallel()

	d := recorded(t)
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if !uuid.MatchString(d.me.User.ID) {
		t.Fatalf("/me user id %q is no UUID", d.me.User.ID)
	}
	own := 0
	for _, p := range d.recordedTicket.Posts {
		if p.Author == nil || !uuid.MatchString(p.Author.ID) {
			t.Fatalf("author %+v has no UUID", p.Author)
		}
		if p.Author.ID == d.me.User.ID {
			own++
		}
	}
	if own != 1 {
		t.Fatalf("%d recorded comments by the token's person, want 1", own)
	}
}

// L2: the token's own person writes in blue and bold, with "(you)" after
// the name, so the mark stays without colour; everyone else is bold, no
// colour, no mark.
func TestTheOwnAuthorIsBlueAndMarked(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.openLogin()
	if got := authorLine(h, "Anna Albers"); !strings.Contains(got, "\x1b[1;38;2;121;194;250mAnna Albers (you)\x1b[m") {
		t.Fatalf("own author: %q", got)
	}
	other := authorLine(h, "Ben Berger")
	if !strings.Contains(other, "\x1b[1;38;2;191;148;228mBen Berger\x1b[m") || strings.Contains(other, "(you)") {
		t.Fatalf("other author: %q", other)
	}
	// The name's own sequence is bold alone: a colour would join it
	// ("\x1b[1;38;2;121;194;250m").
	if strings.Contains(other, "34mBen Berger") {
		t.Fatalf("other author drawn in a colour: %q", other)
	}
}

// Only the id decides: a foreign author whose name ends in "(you)" gets
// no blue and no second mark.
func TestANameEndingInYouIsNoOwnAuthor(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.openLogin()
	d := h.m.app.detail
	for i, p := range d.record.Posts {
		if p.Author.ID != h.m.app.me.User.ID {
			author := *p.Author
			author.Name = "Ben Berger (you)"
			d.record.Posts[i].Author = &author
		}
	}
	line := authorLine(h, "Ben Berger")
	if !strings.Contains(line, "\x1b[1;38;2;191;148;228mBen Berger (you)\x1b[m") || strings.Contains(line, "(you) (you)") || strings.Contains(line, "121;194;250mBen") {
		t.Fatalf("foreign author: %q", line)
	}
}

// Without the token's person known (/me not loaded, failed, or without an
// id), nobody is marked; an author without an id is never the own one.
func TestWithoutMeNobodyIsMarked(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.openLogin()
	a := h.m.app
	check := func(label string) {
		t.Helper()
		if strings.Contains(ansi.Strip(h.m.View().Content), "(you)") {
			t.Fatalf("%s: someone is marked", label)
		}
		if line := authorLine(h, "Anna Albers"); !strings.Contains(line, "\x1b[1;38;2;191;148;228mAnna Albers\x1b[m") {
			t.Fatalf("%s: %q", label, line)
		}
	}
	a.steps[stepMe].state = loading
	check("/me loading")
	a.steps[stepMe].state = failed
	check("/me failed")
	a.steps[stepMe].state = ready
	me := a.me.User.ID
	a.me.User.ID = ""
	for i, p := range a.detail.record.Posts {
		if p.Author.ID == me {
			a.detail.record.Posts[i].Author = &api.Ref{ID: "", Name: p.Author.Name}
		}
	}
	check("no ids")
}
