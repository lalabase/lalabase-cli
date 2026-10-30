package ui

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// K3a: a comment on a ticket from the terminal. What it shares with a booking
// (write.go) has the booking's tests; these pin the comment's own.

// commentCall is one attempt the fakes saw, on either way.
type commentCall struct {
	way, ticket, body, key string
	plain                  bool
}

func (f *fakes) comment(c commentCall) (api.Comment, transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commentLog = append(f.commentLog, c)
	if f.commentAnswer != nil {
		return f.commentAnswer(c)
	}
	return postedOn(c.ticket)
}

func (f *fakes) PostCommentREST(_ context.Context, ticket, body, key string) (api.Comment, transport.Answer) {
	return f.comment(commentCall{"rest", ticket, body, key, true})
}

func (f *fakes) PostComment(_ context.Context, _, ticket, body string, o api.CommentOptions, key string) (api.Comment, transport.Answer) {
	return f.comment(commentCall{"mcp", ticket, body, key, o.Plain})
}

func postedOn(ticket string) (api.Comment, transport.Answer) {
	return api.Comment{ID: posted, TicketID: ticket}, transport.Answer{Class: transport.Object, Status: 201, Sent: true}
}

func (f *fakes) commentCalls() []commentCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.commentLog)
}

// commentFails answers every attempt from now on with ans, a success for none.
func (f *fakes) commentFails(ans *transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commentAnswer = nil
	if ans != nil {
		f.commentAnswer = func(commentCall) (api.Comment, transport.Answer) { return api.Comment{}, *ans }
	}
}

var (
	// posted is the id of the comment the fakes write.
	posted      = idOf(800001)
	commentLost = transport.Answer{Class: transport.Temporary, Message: "connection lost", Sent: true}
)

// recordedPost is a recorded refusal, as the transport hands it on.
func recordedPost(t *testing.T, name string, class transport.Class) transport.Answer {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	ex := file.Contract.REST["POST /api/v1/tickets/:ticket_id/posts"][name]
	return transport.Answer{Class: class, Message: string(ex.Response.Body), Status: ex.Response.Status, Sent: true}
}

// fillComment opens the box and pastes text.
func (h *harness) fillComment(text string) {
	h.t.Helper()
	if h.press("k"); h.m.dialog == nil {
		h.t.Fatalf("k opened no dialog: %s", h.helpText())
	}
	h.feed(tea.PasteMsg{Content: text})
}

// sendComment fills the box and presses ctrl+s: the write waits in the command.
func (h *harness) sendComment(text string) tea.Cmd {
	h.t.Helper()
	h.fillComment(text)
	return h.press("ctrl+s")
}

// k needs the feature on a way of its own, a token that says it may write and
// the known ticket in the focused pane; a search line has the keys.
func TestKIsOfferedOnAKnownTicketWithTheFeatureAndAWritingToken(t *testing.T) {
	t.Parallel()

	writes := boolPtr(false)
	noFormat := func(h *harness) {
		h.setToken(writes, "posts.plain")
		h.f.tools = func() ([]transport.Tool, transport.Answer) {
			return withoutArgument(h.d.tools, "post_comment", "format"), transport.Answer{Class: transport.Object, Sent: true}
		}
	}
	for name, tc := range map[string]struct {
		token func(h *harness)
		want  bool
	}{
		"posts.plain":          {func(h *harness) { h.setToken(writes) }, true},
		"the tool with format": {func(h *harness) { h.setToken(writes, "posts.plain") }, true},
		"neither":              {noFormat, false},
		"a read-only token":    {func(h *harness) { h.setToken(boolPtr(true)) }, false},
	} {
		t.Run(name, func(t *testing.T) {
			h := newRESTHarness(t)
			tc.token(h)
			h.openSix()
			if strings.Contains(h.helpText(), "k add comment") != tc.want || h.offers(AddComment) != tc.want {
				t.Fatalf("offered %v: %s", h.offers(AddComment), h.helpText())
			}
			if h.press("k"); (h.m.dialog != nil) != tc.want {
				t.Fatalf("a dialog: %v", h.m.dialog != nil)
			}
		})
	}
	h := bookingHarness(t)
	if h.m.focus = view.List; h.offers(AddComment) {
		t.Fatalf("k from the list: %s", h.helpText())
	}
	h.m.focus, h.m.app.detail.known = view.Object, false
	if h.offers(AddComment) {
		t.Fatal("k on a ticket that is not known")
	}
	h = newRESTHarness(t)
	h.openTickets(website)
	h.press("/")
	if h.press("k"); h.m.mode != TextEntry || h.m.app.tickets.entry != "k" {
		t.Fatalf("mode %d, entry %q", h.m.mode, h.m.app.tickets.entry)
	}
}

// The large box on the ticket of the key press, whatever moves behind it. What
// is typed and pasted goes as it is, without blank lines around it, plain, with
// a key, on the way the server has.
func TestACommentGoesAsWrittenOnTheWayOfItsServer(t *testing.T) {
	t.Parallel()

	for way, without := range map[string][]string{"rest": nil, "mcp": {"posts.plain"}} {
		t.Run(way, func(t *testing.T) {
			h := newRESTHarness(t)
			h.setToken(boolPtr(false), without...)
			h.openSix()
			if h.press("k"); h.m.mode != DialogLines || !strings.Contains(boxed(h.m), "Comment · #6 Ticket 6") {
				t.Fatalf("mode %d:\n%s", h.m.mode, boxed(h.m))
			}
			h.press("enter")
			h.typeText("Zeile")
			h.press("enter")
			h.feed(tea.PasteMsg{Content: "  <b>rechts</b> & so  \r\n\r\n\t\n \n"})
			h.m.app.ensureDetail(website, ticketID(website, 7))
			h.drive(h.press("ctrl+s"))
			calls := h.f.commentCalls()
			want := commentCall{way, six(), "Zeile\n  <b>rechts</b> & so  ", calls[0].key, true}
			if len(calls) != 1 || calls[0] != want || !strings.HasPrefix(want.key, "lalabase-comment-") || !transport.ValidKey(want.key) {
				t.Fatalf("calls %+v", calls)
			}
		})
	}
}

// Confirmed: the box and the draft go, the line says so, and what the post
// changed is read again: the ticket at the head of its newest page with the
// comment selected, the list, the counters, unknown until then.
func TestAConfirmedCommentShowsAtTheHeadOfItsTicket(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	a := h.m.app
	h.press("tab")
	h.drive(h.press("o"))
	h.drive(h.press("]"))
	a.drafts[six()] = "ein alter Entwurf"
	reads := len(h.f.restCalls())
	inboxes := []cache.Key{a.inboxKey(orgA, 1), a.inboxKey("", 1)}
	for _, k := range inboxes {
		a.store.Apply(k, cache.Replace, []byte(`{}`), h.now)
	}
	h.f.setRESTTicket(func(id string, o api.TicketOptions) transport.Answer {
		var body map[string]any
		_ = json.Unmarshal(h.d.defaultRESTTicket(id, o).Payload, &body)
		data, first := body["data"].(map[string]any), map[string]any{"id": posted, "author": nil, "body_text": "x", "is_private": false, "created_at": "2026-01-05T10:30:00Z"}
		data["posts"] = append([]any{first}, data["posts"].([]any)[1:]...)
		return restData(data, true)
	})
	cmds := h.feed(messages(t, h.sendComment("Erledigt"))...)
	_, kept := a.store.Get(inboxes[0])
	_, keptAll := a.store.Get(inboxes[1])
	if a.countOf(orgA).known || a.countOf("").known || kept || keptAll {
		t.Fatalf("counters %v %v, inboxes kept %v %v", a.countOf(orgA).known, a.countOf("").known, kept, keptAll)
	}
	h.drive(cmds...)
	d := a.detail
	if h.m.dialog != nil || len(a.comments) != 0 || len(a.drafts) != 0 || !strings.Contains(h.status(), "Comment posted on #6") || !a.countOf("").known {
		t.Fatalf("dialog %v, comments %d, drafts %q, status %q", h.m.dialog != nil, len(a.comments), a.drafts, h.status())
	}
	if d.key.PostsPage != 1 || !d.key.Newest || d.area != areaComments || d.follow != anchorPost || d.selectedPost() == nil || d.post != posted {
		t.Fatalf("key %+v, area %d, post %q, follow %d", d.key, d.area, d.post, d.follow)
	}
	if !slices.ContainsFunc(h.f.restCalls()[reads:], func(r restRead) bool { return r.route == "list" }) {
		t.Fatalf("reads %+v", h.f.restCalls()[reads:])
	}
}

// From a notification its inbox is read again too, under its organisation.
func TestACommentFromANotificationReadsItsInbox(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.start()
	h.openLinked(orgA, 5)
	inbox := len(h.f.inboxCalls())
	h.drive(h.sendComment("Erledigt"))
	if !slices.ContainsFunc(h.f.inboxCalls()[inbox:], func(q api.NotificationQuery) bool { return q.OrganisationID == orgA }) {
		t.Fatalf("reads %+v", h.f.inboxCalls()[inbox:])
	}
}

// Refused or not sent: the box stays and says why, cleaned, with the text as it
// was; nothing is read, the ticket stays in the cache, the next press has a
// new key. The tool's refusal has its text in the message.
func TestARefusedCommentStaysInTheBox(t *testing.T) {
	t.Parallel()

	hostile, _ := json.Marshal(map[string]any{"error": "validation_failed", "code": "invalid_input", "errors": []string{"SAFE" + dialogHostile}})
	for _, tc := range []struct {
		name    string
		ans     transport.Answer
		want    string
		without string
	}{
		{"invalid", recordedPost(t, "invalid_input_whitespace", transport.InvalidInput), "Bitte geben Sie einen Kommentartext (post[body]) an.", ""},
		{"hostile", transport.Answer{Class: transport.InvalidInput, Message: string(hostile), Status: 422, Sent: true}, "SAFE", ""},
		{"forbidden", recordedPost(t, "forbidden", transport.NoAccess), "You may not comment on this ticket", ""},
		{"not found", recordedPost(t, "not_found", transport.NotFound), "This ticket is gone, or no longer yours to see", ""},
		{"read-only", recordedPost(t, "read_only", transport.ReadOnly), "Read-only token", ""},
		{"not sent", transport.Answer{Class: transport.Temporary, Message: "refused"}, "Not posted; the server was not reached", ""},
		{"the tool", transport.Answer{Class: transport.NoAccess, Message: "Not authorized.", Sent: true}, "Not authorized.", "posts.plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRESTHarness(t)
			h.setToken(boolPtr(false), tc.without)
			h.openSix()
			a := h.m.app
			h.f.commentFails(&tc.ans)
			reads := len(h.f.viewReads())
			h.drive(h.sendComment("Hallo\nWelt"))
			if h.m.dialog == nil || h.m.dialog.locked() || !strings.HasPrefix(h.dialogLine(), tc.want) || dialogValues(h.m)[0] != "Hallo\nWelt" {
				t.Fatalf("dialog %+v, line %q", h.m.dialog, h.dialogLine())
			}
			assertViewSafe(t, h.m.View().Content)
			if _, ok := a.store.Get(a.detail.key); !ok || len(h.f.viewReads()) != reads || h.status() != "" {
				t.Fatalf("cached %v, %d reads, status %q", ok, len(h.f.viewReads())-reads, h.status())
			}
			h.f.commentFails(nil)
			h.drive(h.press("ctrl+s"))
			if calls := h.f.commentCalls(); calls[0].key == calls[len(calls)-1].key || h.m.dialog != nil {
				t.Fatalf("calls %+v", calls)
			}
		})
	}
}

// Nobody knows: the box holds, nothing goes out again, the ticket is read, the
// lock holds until the last of those reads has landed; then k is a new action
// on the draft, with a new key.
func TestAnUnclearCommentIsReadBackNeverSentAgain(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.commentFails(&commentLost)
	reads := len(h.f.restCalls())
	cmds := h.feed(messages(t, h.sendComment("Hallo"))...)
	if h.m.dialog == nil || h.m.dialog.held == nil || !strings.Contains(h.dialogLine(), "reading the ticket") {
		t.Fatalf("dialog %+v, line %q", h.m.dialog, h.dialogLine())
	}
	if cmd := h.press("ctrl+s"); cmd != nil || len(h.f.commentCalls()) != 1 {
		t.Fatalf("ctrl+s acted: %+v", h.f.commentCalls())
	}
	h.press("esc")
	var msgs []tea.Msg
	for _, cmd := range cmds {
		msgs = append(msgs, messages(t, cmd)...)
	}
	for _, msg := range msgs {
		if h.press("k"); h.offers(AddComment) || h.m.dialog != nil {
			t.Fatal("k before the last read landed")
		}
		h.drive(h.feed(msg)...)
	}
	if !slices.ContainsFunc(h.f.restCalls()[reads:], func(r restRead) bool { return r.route == "ticket" && r.o.PostsPage == 1 && r.o.Newest }) {
		t.Fatalf("reads %+v", h.f.restCalls()[reads:])
	}
	if h.press("k"); h.m.dialog == nil || dialogValues(h.m)[0] != "Hallo" {
		t.Fatalf("no draft: %+v", h.m.dialog)
	}
	h.f.commentFails(nil)
	h.drive(h.press("ctrl+s"))
	if calls := h.f.commentCalls(); len(calls) != 2 || calls[0].key == calls[1].key {
		t.Fatalf("calls %+v", calls)
	}
}

// Ctrl-S twice is one comment; the lock is the ticket's, not the box's, and a
// comment's alone: a booking runs beside it, a comment on another ticket too.
func TestTheLockIsOfTheTicketAndTheKind(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	first := h.sendComment("Eins")
	if second := h.press("ctrl+s"); second != nil {
		t.Fatal("the second ctrl+s made a command")
	}
	spec := h.m.dialog.spec
	h.drive(spec.Send(Submission{Dialog: h.m.dialog.id, Target: spec.Target, Values: []string{"Zwei"}}))
	h.press("esc")
	if h.offers(AddComment) || !h.offers(BookTime) {
		t.Fatalf("while a comment runs: %s", h.helpText())
	}
	h.sendBooking("30", "")()
	h.reopen(7)
	h.drive(h.sendComment("Sieben"))
	h.drive(first)
	if calls := h.f.commentCalls(); len(calls) != 2 || calls[0].ticket == calls[1].ticket || len(h.f.bookCalls()) != 1 {
		t.Fatalf("calls %+v, bookings %+v", calls, h.f.bookCalls())
	}
}

// Esc leaves a send under way: its end is told on the line wherever the person
// is, in its tone, and the draft follows the end.
func TestTheEndOfACommentLeftWithEscIsTold(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		ans   transport.Answer
		want  string
		tone  present.Style
		draft string
	}{
		{"confirmed", transport.Answer{}, "Comment posted on #6", present.Plain, ""},
		{"refused", transport.Answer{Class: transport.NoAccess, Sent: true}, "Not posted on #6: You may not comment", present.NoticeProblem, "Hallo"},
		{"unclear", commentLost, "Not clear whether it was posted on #6", present.NoticeProblem, "Hallo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := bookingHarness(t)
			if tc.ans.Sent {
				h.f.commentFails(&tc.ans)
			}
			send := h.sendComment("Hallo")
			h.press("esc")
			h.reopen(7)
			pane := h.paneText()
			h.drive(send)
			got, tone := h.m.status()
			if !strings.Contains(got.String(), tc.want) || tone != tc.tone || h.m.app.drafts[six()] != tc.draft || h.paneText() != pane {
				t.Fatalf("status %q, tone %v, drafts %q", got.String(), tone, h.m.app.drafts)
			}
		})
	}
}

// The ends of two tickets and of two kinds stand side by side; a press takes
// away its own kind's on its ticket alone.
func TestEndsStandSideBySide(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.commentFails(&transport.Answer{Class: transport.NoAccess, Sent: true})
	send := h.sendComment("Sechs")
	h.press("esc")
	h.drive(send)
	h.sendBooking("30", "")()
	h.reopen(7)
	h.f.commentFails(nil)
	send = h.sendComment("Sieben")
	h.press("esc")
	h.drive(send)
	for _, want := range []string{"Not posted on #6", "0:30 booked on #6", "Comment posted on #7"} {
		if !strings.Contains(h.status(), want) {
			t.Fatalf("%q is not on the line: %q", want, h.status())
		}
	}
	h.reopen(6)
	h.press("k")
	if h.press("ctrl+s"); strings.Contains(h.status(), "Not posted on #6") || !strings.Contains(h.status(), "0:30 booked on #6") {
		t.Fatalf("status %q", h.status())
	}
}

// Esc keeps the text per ticket, k brings it back, an emptied text leaves no
// draft, and a 401 takes the drafts with the session.
func TestADraftStaysWithItsTicketAndItsSession(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	a := h.m.app
	h.fillComment("Ent\nwurf")
	h.press("esc")
	h.reopen(7)
	h.fillComment("Sieben")
	h.press("esc")
	h.reopen(6)
	if h.press("k"); dialogValues(h.m)[0] != "Ent\nwurf" {
		t.Fatalf("draft %q", dialogValues(h.m))
	}
	for range len("Ent\nwurf") {
		h.press("backspace")
	}
	h.press("esc")
	if _, ok := a.drafts[six()]; ok || a.drafts[ticketID(website, 7)] != "Sieben" {
		t.Fatalf("drafts %q", a.drafts)
	}
	h.f.commentFails(&transport.Answer{Class: transport.Unauthenticated, Status: 401, Sent: true})
	h.drive(h.sendComment("mehr"))
	if !a.expired || h.m.dialog != nil || len(a.drafts) != 0 || len(a.comments) != 0 {
		t.Fatalf("expired %v, dialog %v, drafts %q", a.expired, h.m.dialog != nil, a.drafts)
	}
}

// Blanks send nothing and name the field; a paste stops before the first
// grapheme that does not fit, and says so; what no terminal draws stays out.
func TestWhatTheFieldTakes(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.press("k")
	for _, blanks := range []string{"", "  \n \n", "\u3000", "\ufffe\uffff"} {
		h.feed(tea.PasteMsg{Content: blanks})
		if cmd := h.press("ctrl+s"); cmd != nil || h.dialogLine() != "Text is required" {
			t.Fatalf("%q: line %q", blanks, h.dialogLine())
		}
	}
	if got := dialogValues(h.m)[0]; got != "  \n \n\u3000" {
		t.Fatalf("value %q", got)
	}
	h = bookingHarness(t)
	h.fillComment(strings.Repeat("ä", commentLimit-1) + "e\u0301x")
	if got := dialogValues(h.m)[0]; got != strings.Repeat("ä", commentLimit-1) || h.dialogLine() != "Text cut at 10000 characters" {
		t.Fatalf("%d runes, line %q", len([]rune(got)), h.dialogLine())
	}
}

// A read of the ticket begun before the post does not count: canceling reaches
// the network's reads alone, so the epoch stops this one, from the disk.
func TestAPageOnItsWayFromTheDiskDoesNotCount(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	a := h.m.app
	late := a.scope.Begin(gateway.Detail, a.detail.key, 0)
	old := h.d.defaultRESTTicket(six(), api.TicketOptions{})
	h.f.setRESTTicket(func(string, api.TicketOptions) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h.drive(h.sendComment("x"))
	if a.scope.Promote(a.store, late, cache.Entry{Payload: old.Payload, LoadedAt: h.now}) {
		t.Fatal("a page from before the comment was filed")
	}
}
