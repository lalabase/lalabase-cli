package inboxfixture

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// Review of #16, finding 1: a read serialises the rows outside the lock
// while a write (a comment, a mark) changes them. The queue runs a read
// beside the one write place, so an inbox page loading while a comment is
// posted is an ordinary run; under the race detector the old fixture
// reports the data race, without it the runtime may stop the process.
func TestReadsAndWritesOfTheRowsDoNotRace(t *testing.T) {
	_, a, c := serve(t, Normal)
	ctx := context.Background()
	const pairs = 40
	var wg sync.WaitGroup
	errs := make(chan error, 3*pairs)
	for i := range pairs {
		wg.Add(3)
		go func() {
			defer wg.Done()
			if _, ans := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: OrgA}); !ans.OK() {
				errs <- fmt.Errorf("read %d: %v %s", i, ans.Class, ans.Message)
			}
		}()
		go func() {
			defer wg.Done()
			// Ticket #(i%30+1) of Website: row i of A names it.
			ticket := TicketID(ProjectWebsite, i%RowsA+1)
			ans := c.CallTool(ctx, transport.Call{Tool: "post_comment", OneRecord: true, Args: map[string]any{"project_id": ProjectWebsite, "ticket_id": ticket, "body": "x", "format": "plain", "idempotency_key": fmt.Sprintf("key-comment-%03d", i)}})
			if !ans.OK() {
				errs <- fmt.Errorf("comment %d: %v %s", i, ans.Class, ans.Message)
			}
		}()
		go func() {
			defer wg.Done()
			row := fmt.Sprintf("00000000-0000-4000-8000-%012d", 100000+RowsA-i%RowsA)
			ans := c.CallTool(ctx, transport.Call{Tool: "mark_notification_read", OneRecord: true, Args: map[string]any{"notification_id": row, "idempotency_key": fmt.Sprintf("key-mark-%03d", i)}})
			if !ans.OK() {
				errs <- fmt.Errorf("mark %d: %v %s", i, ans.Class, ans.Message)
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the reads and writes did not finish")
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// Review of #16, finding 3 b: the server replays a key only with the
// arguments it was first used with (Mcp::IdempotencyStore compares a
// fingerprint of the arguments without the key); the same key with other
// arguments is an invalid input, before any validation of the new
// arguments, and the store is one per token, shared by every write tool.
func TestAKeySeenBeforeWithOtherArgumentsIsRefused(t *testing.T) {
	f, a, c := serve(t, Normal)
	ctx := context.Background()
	six := TicketID(ProjectWebsite, 6)
	const mismatch = "idempotency_key already used with different parameters"
	first, ans := a.UpdateTicket(ctx, ProjectWebsite, six, api.TicketChange{Title: "first"}, "key-00000001")
	if !ans.OK() || first.Title != "first" {
		t.Fatalf("first: %v %+v", ans.Class, first)
	}
	if _, ans := a.UpdateTicket(ctx, ProjectWebsite, six, api.TicketChange{Title: "second"}, "key-00000001"); ans.Class != transport.InvalidInput || ans.Message != mismatch || !ans.Sent {
		t.Fatalf("other title, same key: %v %q", ans.Class, ans.Message)
	}
	if again, ans := a.UpdateTicket(ctx, ProjectWebsite, six, api.TicketChange{Title: "first"}, "key-00000001"); !ans.OK() || again.Title != "first" {
		t.Fatalf("same arguments, same key: %v %+v", ans.Class, again)
	}
	// Before the new arguments are read: a title that is no string answers
	// the mismatch, not the title's refusal.
	if ans := rawUpdate(c, map[string]any{"project_id": ProjectWebsite, "ticket_id": six, "title": 1, "idempotency_key": "key-00000001"}); ans.Class != transport.InvalidInput || ans.Message != mismatch {
		t.Fatalf("invalid arguments, same key: %v %q", ans.Class, ans.Message)
	}
	// One store per token: the key is known to the other write tools.
	if _, ans := a.PostComment(ctx, ProjectWebsite, six, "x", api.CommentOptions{Plain: true}, "key-00000001"); ans.Class != transport.InvalidInput || ans.Message != mismatch {
		t.Fatalf("a comment with the key: %v %q", ans.Class, ans.Message)
	}
	row := fmt.Sprintf("00000000-0000-4000-8000-%012d", 100000+6)
	if _, ans := a.MarkNotificationRead(ctx, row, "key-00000001"); ans.Class != transport.InvalidInput || ans.Message != mismatch {
		t.Fatalf("a mark with the key: %v %q", ans.Class, ans.Message)
	}
	// The mark's own key, reused for another row.
	if _, ans := a.MarkNotificationRead(ctx, row, "key-00000002"); !ans.OK() {
		t.Fatalf("mark: %v", ans.Class)
	}
	if _, ans := a.MarkNotificationRead(ctx, fmt.Sprintf("00000000-0000-4000-8000-%012d", 100000+7), "key-00000002"); ans.Class != transport.InvalidInput || ans.Message != mismatch {
		t.Fatalf("another row, same key: %v %q", ans.Class, ans.Message)
	}
	if got := f.Mutations(); len(got) != 2 || got[0].Kind != MutationTitle || got[1].Kind != MutationMarkRead {
		t.Fatalf("mutations %+v", got)
	}
	// A refused key is logged as seen, like every attempt.
	if got := f.Keys(); len(got) != 8 {
		t.Fatalf("keys %v", got)
	}
}

// Review of #16, finding 3 a: a plain body is stored as paragraphs
// (Posts::PlainTextBody) and read back through ActionText, so a run of
// blank lines, spaces and tabs included, is one paragraph break, and a
// paragraph of whitespace only (Unicode included) is dropped. Spaces at
// the edges of a line stay.
func TestAPlainBodyBecomesParagraphsAsOnTheServer(t *testing.T) {
	_, a, _ := serve(t, Normal)
	ctx := context.Background()
	six := TicketID(ProjectWebsite, 6)
	for i, tc := range []struct{ body, text string }{
		{"a\n\n\n\nb", "a\n\nb"},
		{"a\n \nb", "a\n\nb"},
		{"a\n\t\n \n\nb", "a\n\nb"},
		{"a\n\n \n\nb", "a\n\nb"},
		{"a\r\n\r\nb\rc", "a\n\nb\nc"},
		{" a \n b ", " a \n b "},
		{"\n\n x\n\n", " x"},
		{"a\n b\n\n c", "a\n b\n\n c"},
	} {
		comment, ans := a.PostComment(ctx, ProjectWebsite, six, tc.body, api.CommentOptions{Plain: true}, fmt.Sprintf("key-plain-%02d", i))
		if !ans.OK() || comment.BodyText != tc.text {
			t.Errorf("%q: %v %q, want %q", tc.body, ans.Class, comment.BodyText, tc.text)
		}
	}
}

// Review of #16, finding 3 a (the HTML side): without format the body is
// ActionText HTML, and body_text is what ActionText makes of it: a
// paragraph ends in two line breaks, a div in one, a br is one, a list
// item has its bullet, a comment and a script leave nothing, entities
// come back as characters, an open tag at the end is dropped, and
// trailing line breaks fall. Pinned against the core's ActionText 8.1.4.
func TestAnHTMLBodyBecomesTextAsActionTextDoes(t *testing.T) {
	_, a, _ := serve(t, Normal)
	ctx := context.Background()
	six := TicketID(ProjectWebsite, 6)
	for i, tc := range []struct{ html, text string }{
		{"<p>a</p><p>b</p>", "a\n\nb"},
		{"<p>a</p>\n<p>b</p>", "a\n\nb"},
		{"<p>a<br></p><p>b</p>", "a\n\nb"},
		{"<p>a</p><p></p><p>b</p>", "a\n\n\n\nb"},
		{"<p>a</p>b", "a\n\nb"},
		{"a\n<p>b</p>", "ab"},
		{"<P>A</P>", "A"},
		{"<h1>t</h1>p", "t\n\np"},
		{"a<br>b", "a\nb"},
		{"a<br/>b", "a\nb"},
		{"a<br>", "a"},
		{"<div>a</div><div>b</div>", "a\nb"},
		{"<ul><li>one</li><li>two</li></ul>", "• one\n• two"},
		{"<ol><li>x</li></ol>", "1. x"},
		{"<blockquote>q</blockquote>", "“q”"},
		{"&lt;x&gt; &amp;lt;", "<x> &lt;"},
		{"a<b", "a"},
		{"a < b", "a < b"},
		{"a <b>x</b>", "a x"},
		{"<script>x</script>y", "y"},
		{"a<!-- c -->b", "ab"},
		{"  a  ", "a"},
		{"<p> b</p>", " b"},
		{"<p>line one<br>line two</p><p>see &lt;file.txt&gt; &amp;lt;tag&amp;gt;</p>", "line one\nline two\n\nsee <file.txt> &lt;tag&gt;"},
	} {
		comment, ans := a.PostComment(ctx, ProjectWebsite, six, tc.html, api.CommentOptions{}, fmt.Sprintf("key-html-%02d", i))
		if !ans.OK() || comment.BodyText != tc.text {
			t.Errorf("%q: %v %q, want %q", tc.html, ans.Class, comment.BodyText, tc.text)
		}
	}
}

// Review of #16, finding 3 c: the server trims with String#strip, which
// knows ASCII whitespace and NUL only; a title is the exception
// (TicketTitle trims every Unicode space). A non-breaking space at the
// edge of a status, a ticket id or a user id stays and the value is
// refused as sent.
func TestTheEdgesAreStrippedAsRubyDoes(t *testing.T) {
	_, _, c := serve(t, Normal)
	six := TicketID(ProjectWebsite, 6)
	with := func(kv ...any) map[string]any {
		m := map[string]any{"project_id": ProjectWebsite, "ticket_id": six}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	if ans := rawUpdate(c, with("status", " new")); ans.Class != transport.Refused || ans.Reason != "value_not_allowed" {
		t.Errorf("a status behind a no-break space: %v %q", ans.Class, ans.Reason)
	}
	if ans := rawUpdate(c, with("status", "\x00\t new \x00")); !ans.OK() {
		t.Errorf("a status between NUL and tabs: %v %q", ans.Class, ans.Message)
	}
	if ans := rawUpdate(c, with("ticket_id", " "+six, "status", "new")); ans.Class != transport.InvalidInput || ans.Message != "invalid ticket_id format" {
		t.Errorf("a ticket id behind a no-break space: %v %q", ans.Class, ans.Message)
	}
	if ans := rawUpdate(c, with("add_assignee_ids", []any{" me"})); ans.Class != transport.InvalidInput || ans.Message != "invalid user id in add_assignee_ids" {
		t.Errorf("a user id behind a no-break space: %v %q", ans.Class, ans.Message)
	}
	if ans := rawUpdate(c, with("add_assignee_ids", []any{"\t me \n"})); !ans.OK() {
		t.Errorf("a user id between tabs: %v %q", ans.Class, ans.Message)
	}
	if ans := rawComment(c, with("body", "x", "format", "plain", "ticket_id", six+" ")); ans.Class != transport.InvalidInput || ans.Message != "invalid ticket_id format" {
		t.Errorf("a comment's ticket id before a no-break space: %v %q", ans.Class, ans.Message)
	}
}

// Review of #16, finding 3 d: a title needs a character that draws.
// Unicode files the Hangul fillers and the blank Braille pattern as
// letters and symbols; TicketTitle::VISIBLE leaves them out.
func TestATitleNeedsACharacterThatDraws(t *testing.T) {
	_, _, c := serve(t, Normal)
	six := TicketID(ProjectWebsite, 6)
	for _, title := range []string{"ㅤ", "⠀", "ᅟᅠﾠ", " ㅤ ⠀ "} {
		if ans := rawUpdate(c, map[string]any{"project_id": ProjectWebsite, "ticket_id": six, "title": title}); ans.Class != transport.InvalidInput || ans.Message != "title is required" {
			t.Errorf("%q: %v %q", title, ans.Class, ans.Message)
		}
	}
	if ans := rawUpdate(c, map[string]any{"project_id": ProjectWebsite, "ticket_id": six, "title": "ㅤa"}); !ans.OK() {
		t.Errorf("a filler beside a letter: %v %q", ans.Class, ans.Message)
	}
}

// Review of #16, finding 3 (stricter than the server): tag characters
// are refused in a title, except inside the three subdivision flags
// TicketTitle::FLAG allows (England, Scotland, Wales); a made-up region
// is hidden text all the same.
func TestTheSubdivisionFlagsAreAllowedInATitle(t *testing.T) {
	_, _, c := serve(t, Normal)
	six := TicketID(ProjectWebsite, 6)
	const base, cancel = "\U0001F3F4", "\U000E007F"
	tags := func(s string) string {
		out := ""
		for _, r := range s {
			out += string(0xE0000 + r)
		}
		return out
	}
	for _, region := range []string{"gbeng", "gbsct", "gbwls"} {
		if ans := rawUpdate(c, map[string]any{"project_id": ProjectWebsite, "ticket_id": six, "title": "Flag " + base + tags(region) + cancel}); !ans.OK() {
			t.Errorf("%s: %v %q", region, ans.Class, ans.Message)
		}
	}
	for _, title := range []string{"Flag " + base + tags("gbxyz") + cancel, "x" + tags("a"), "Flag " + base + tags("gbeng")} {
		if ans := rawUpdate(c, map[string]any{"project_id": ProjectWebsite, "ticket_id": six, "title": title}); ans.Class != transport.InvalidInput || ans.Message != "title must be one line without control characters" {
			t.Errorf("%q: %v %q", title, ans.Class, ans.Message)
		}
	}
}

// Review of #16, finding 3 (stricter than the server): a user id in the
// 32-hex form, or in capitals, names the same person (Lalabase::Uuid.dashed);
// the summary carries the one spelling the server stores.
func TestAnUndashedUserIDNamesThePerson(t *testing.T) {
	f, _, c := serve(t, Normal)
	six := TicketID(ProjectWebsite, 6)
	undashed := ""
	for _, r := range f.meID {
		if r != '-' {
			undashed += string(r)
		}
	}
	for _, spelling := range []string{undashed, strings.ToUpper(undashed), strings.ToUpper(f.meID)} {
		ans := rawUpdate(c, map[string]any{"project_id": ProjectWebsite, "ticket_id": six, "add_assignee_ids": []any{spelling}})
		if !ans.OK() || !strings.Contains(string(ans.Payload), `"id":"`+f.meID+`"`) {
			t.Errorf("%s: %v %q %s", spelling, ans.Class, ans.Message, ans.Payload)
		}
	}
	if list, _ := f.currentAssigneesOf(six); len(list) != 1 {
		t.Fatalf("assigned %d times", len(list))
	}
}

// Review of #16, finding 3 e: a scenario that stands for an older server
// names that server's version in initialize, so the notice "older than
// the tested version" shows against it as against the real thing.
func TestTheScenariosNameTheirVersion(t *testing.T) {
	ctx := context.Background()
	for scenario, want := range map[Scenario]string{Older: "0.7.0", S1Only: "0.9.0", S2Only: "0.10.0", Normal: "0.12.0", Refusals: "0.12.0"} {
		_, _, c := serve(t, scenario)
		info, ans := c.Initialize(ctx)
		if !ans.OK() || info.Name != "lalabase" || info.Version != want {
			t.Errorf("%s: %v %+v, want %s", scenario, ans.Class, info, want)
		}
		tools, _ := c.ListTools(ctx)
		if below := features.Detect(info, tools).BelowTested; below != (want != "0.12.0") {
			t.Errorf("%s: below tested %v", scenario, below)
		}
		// A server before S6 has no booked time (review round 2 of #42).
		if on := features.Detect(info, tools).Enabled["booked_time"]; on != (want == "0.12.0") {
			t.Errorf("%s: booked time %v", scenario, on)
		}
	}
}

// Review of #16, finding 4 b: the server reads the arguments in one
// order: the project (the dispatcher), then unknown keys, then the ticket
// id. A request wrong in two of them answers the first.
func TestTheArgumentsAreReadInTheServersOrder(t *testing.T) {
	_, _, c := serve(t, Normal)
	six := TicketID(ProjectWebsite, 6)
	if ans := rawUpdate(c, map[string]any{"project_id": ProjectWebsite, "ticket_id": "t-1", "priority": "high", "status": "new"}); ans.Class != transport.InvalidInput || ans.Message != "unknown arguments: priority" {
		t.Errorf("unknown argument and bad ticket id: %v %q", ans.Class, ans.Message)
	}
	unknown := "00000000-0000-4000-8000-000000000034"
	if ans := rawUpdate(c, map[string]any{"project_id": unknown, "ticket_id": six, "priority": "high", "status": "new"}); ans.Class != transport.NotFound {
		t.Errorf("unknown project and unknown argument: %v %q", ans.Class, ans.Message)
	}
	if ans := rawComment(c, map[string]any{"project_id": unknown, "ticket_id": "t-1", "body": "x"}); ans.Class != transport.NotFound {
		t.Errorf("unknown project and bad ticket id on a comment: %v %q", ans.Class, ans.Message)
	}
	// The SDK's checks come before the dispatcher's: a format it does not
	// know is refused before the project is looked up.
	if ans := rawComment(c, map[string]any{"project_id": unknown, "ticket_id": six, "body": "x", "format": "markdown"}); ans.Class != transport.InvalidInput || ans.Message != textInvalidFormat {
		t.Errorf("unknown project and bad format: %v %q", ans.Class, ans.Message)
	}
}

// currentAssigneesOf reads a ticket's assignees as they are now, for a test.
func (f *Fixture) currentAssigneesOf(id string) ([]any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.currentAssignees(ProjectWebsite, ticketNumber(ProjectWebsite, id))
}
