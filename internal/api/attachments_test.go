package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// C1d: the metadata of an attachment beside its name. Every field is
// optional: an older server sends none of them, and a value that does not
// read is unknown, never a reason to refuse a ticket that is valid without
// it.

func TestAttachmentMetadataIsReadWhenPresent(t *testing.T) {
	text := recordedText(t, "get_ticket", func(p map[string]any) {
		p["attachments"] = []any{
			map[string]any{"id": "d1", "filename": "design.pdf", "content_type": "application/pdf", "byte_size": 1536, "text": "ignored", "text_status": "available"},
			map[string]any{"filename": "empty.txt", "content_type": "text/plain", "byte_size": 0, "text_status": "empty"},
		}
		p["attachments_total"] = 2
	})
	ticket, ans := replayText(t, text).Ticket(context.Background(), website, login)
	if !ans.OK() {
		t.Fatalf("%v %s", ans.Class, ans.Message)
	}
	a, b := ticket.Attachments[0], ticket.Attachments[1]
	if a.Filename != "design.pdf" || a.ContentType == nil || *a.ContentType != "application/pdf" || a.ByteSize == nil || *a.ByteSize != 1536 || a.TextStatus == nil || *a.TextStatus != "available" {
		t.Fatalf("first: %+v", a)
	}
	// Zero bytes is a size, not a missing one.
	if b.ByteSize == nil || *b.ByteSize != 0 || b.TextStatus == nil || *b.TextStatus != "empty" {
		t.Fatalf("second: %+v", b)
	}
}

func TestMissingOrUnreadableAttachmentMetadataIsUnknownAndTheTicketStays(t *testing.T) {
	for name, attachment := range map[string]map[string]any{
		"absent (older server)": {"filename": "a.txt"},
		"null":                  {"filename": "a.txt", "content_type": nil, "byte_size": nil, "text_status": nil},
		"wrong types":           {"filename": "a.txt", "content_type": 7, "byte_size": "12", "text_status": []any{"x"}},
		"fraction":              {"filename": "a.txt", "byte_size": 1.5},
		"exponent":              {"filename": "a.txt", "byte_size": json.RawMessage("1e3")},
		"negative":              {"filename": "a.txt", "byte_size": -1},
		"object":                {"filename": "a.txt", "content_type": map[string]any{}, "byte_size": map[string]any{}, "text_status": true},
	} {
		text := recordedText(t, "get_ticket", func(p map[string]any) {
			p["attachments"] = []any{attachment}
			p["attachments_total"] = 1
		})
		ticket, ans := replayText(t, text).Ticket(context.Background(), website, login)
		if !ans.OK() {
			t.Errorf("%s: the ticket was refused: %v %s", name, ans.Class, ans.Message)
			continue
		}
		a := ticket.Attachments[0]
		if a.Filename != "a.txt" || a.ContentType != nil || a.ByteSize != nil || a.TextStatus != nil {
			t.Errorf("%s: %+v", name, a)
		}
	}
}

// The file name stays required: a ticket whose attachment has none is a
// protocol error, as before C1d.
func TestAnAttachmentWithoutAFileNameIsStillAProtocolError(t *testing.T) {
	text := recordedText(t, "get_ticket", func(p map[string]any) {
		p["attachments"] = []any{map[string]any{"content_type": "text/plain", "byte_size": 3}}
		p["attachments_total"] = 1
	})
	if _, ans := replayText(t, text).Ticket(context.Background(), website, login); ans.Class != transport.Protocol {
		t.Fatalf("got %v", ans.Class)
	}
}

// The server's marker of a shortened text (Tools::Base::TRUNCATION_MARKER).
func TestShortenedRecognisesTheServersMarkerAtTheEndOnly(t *testing.T) {
	if TruncationMarker != "\n\n…[truncated]" {
		t.Fatalf("the marker is %q", TruncationMarker)
	}
	for text, want := range map[string]bool{
		"long text\n\n…[truncated]":         true,
		"\n\n…[truncated]":                  true,
		"short":                             false,
		"":                                  false,
		"a\n\n…[truncated] and more":        false,
		"a\n…[truncated]":                   false,
		"a\n\n...[truncated]":               false,
		"a\n\n…[truncated]\n":               false,
		"a\n\n…[truncated]\n\n…[truncated]": true,
	} {
		body, short := Shortened(text)
		if short != want {
			t.Errorf("%q: shortened %v", text, short)
		}
		if short && body+TruncationMarker != text {
			t.Errorf("%q: body %q", text, body)
		}
		if !short && body != text {
			t.Errorf("%q: body %q changed", text, body)
		}
	}
}

// C12: a comment's files stand beside its count (core R7). A server before
// R7 sends the count alone, so the list is no required field: it is read
// as an attachment's metadata is, and what does not read never refuses a
// ticket that is valid without it.

const commentFile = "00000000-0000-4000-8000-000000000053"

func checkRecordedFile(t *testing.T, p Post) {
	t.Helper()
	if p.ID != comment || p.AttachmentsCount != 1 || len(p.Attachments) != 1 {
		t.Fatalf("comment %+v", p)
	}
	f := p.Attachments[0]
	if f.ID == nil || *f.ID != commentFile || f.Filename != "test_file.txt" || f.ContentType == nil || *f.ContentType != "text/plain" || f.ByteSize == nil || *f.ByteSize != 500 {
		t.Fatalf("file %+v", f)
	}
}

func TestACommentsFilesAreReadOnBothWays(t *testing.T) {
	ctx := context.Background()
	o := TicketOptions{PostsPerPage: 1}
	tools, _ := replayCase(t, "comment_file", "page")
	ticket, ans := tools.TicketWith(ctx, website, login, o)
	if !ans.OK() || len(ticket.Posts) != 1 {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, ticket)
	}
	checkRecordedFile(t, ticket.Posts[0])

	rest, _ := restRecorded(t, "ticket_view", "show_paged_comment_file")
	ticket, ans = DecodeTicketREST(rest.TicketRESTAnswer(ctx, login, o), o)
	if !ans.OK() || len(ticket.Posts) != 1 {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, ticket)
	}
	checkRecordedFile(t, ticket.Posts[0])

	// What REST files reads the same from the cache.
	kept, ok := DecodeTicket(ans, o)
	if !ok.OK() || len(kept.Posts) != 1 {
		t.Fatalf("%v %q %+v", ok.Class, ok.Message, kept)
	}
	checkRecordedFile(t, kept.Posts[0])
}

func TestOneCommentBringsItsFiles(t *testing.T) {
	api, _ := replayCase(t, "comment_file", "one_post")
	ticket, ans := api.TicketWith(context.Background(), website, login, TicketOptions{PostID: comment})
	if !ans.OK() || len(ticket.Posts) != 1 {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, ticket)
	}
	checkRecordedFile(t, ticket.Posts[0])
}

// A list with nothing in it is a list: the server has said what there is.
func TestACommentWithoutFilesHasAnEmptyList(t *testing.T) {
	api, _ := replayCase(t, "comment_file", "no_file")
	ticket, ans := api.TicketWith(context.Background(), website, login, TicketOptions{PostID: "00000000-0000-4000-8000-000000000052"})
	if !ans.OK() || len(ticket.Posts) != 1 {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, ticket)
	}
	if p := ticket.Posts[0]; p.Attachments == nil || len(p.Attachments) != 0 || p.AttachmentsCount != 0 {
		t.Fatalf("comment %+v", p)
	}
}

func TestACommentsListAndCountAreReadLeniently(t *testing.T) {
	file := map[string]any{"id": commentFile, "filename": "shot.png"}
	for name, c := range map[string]struct {
		list  any // nil: the list is absent
		count any
		names []string // nil: no list at all
		want  int
	}{
		"before R7, the count alone": {count: 2, want: 2},
		"a list of null":             {list: json.RawMessage("null"), count: 2, want: 2},
		"a list of another type":     {list: "shot.png", count: 2, want: 2},
		"an object for a list":       {list: file, count: 1, want: 1},
		"the blob of a file is gone": {list: []any{}, count: 1, names: []string{}, want: 1},
		"an entry of another type":   {list: []any{5, "x", nil, file}, count: 4, names: []string{"shot.png"}, want: 4},
		"a name of another type":     {list: []any{map[string]any{"filename": 5}, file}, count: 2, names: []string{"shot.png"}, want: 2},
		"an entry without a name":    {list: []any{map[string]any{"id": commentFile}}, count: 1, names: []string{}, want: 1},
		"no count":                   {list: []any{file}, names: []string{"shot.png"}, want: 0},
		"a count of another type":    {list: []any{file}, count: "1", names: []string{"shot.png"}, want: 0},
		"a fraction for a count":     {list: []any{file}, count: 1.5, names: []string{"shot.png"}, want: 0},
		"a negative count":           {list: []any{file}, count: -1, names: []string{"shot.png"}, want: 0},
		"a count of null":            {list: []any{file}, count: json.RawMessage("null"), names: []string{"shot.png"}, want: 0},
		"a count with an exponent":   {list: []any{file}, count: json.RawMessage("1e3"), names: []string{"shot.png"}, want: 0},
		"a count no int holds":       {list: []any{file}, count: json.RawMessage("99999999999999999999"), names: []string{"shot.png"}, want: 0},
	} {
		text := recordedText(t, "get_ticket", func(p map[string]any) {
			post := p["posts"].([]any)[0].(map[string]any)
			delete(post, "attachments")
			delete(post, "attachments_count")
			if c.list != nil {
				post["attachments"] = c.list
			}
			if c.count != nil {
				post["attachments_count"] = c.count
			}
		})
		ticket, ans := replayText(t, text).Ticket(context.Background(), website, login)
		if !ans.OK() {
			t.Errorf("%s: the ticket was refused: %v %s", name, ans.Class, ans.Message)
			continue
		}
		post := ticket.Posts[0]
		if (post.Attachments == nil) != (c.names == nil) || len(post.Attachments) != len(c.names) || post.AttachmentsCount != c.want {
			t.Errorf("%s: %+v", name, post)
			continue
		}
		for i, want := range c.names {
			if post.Attachments[i].Filename != want {
				t.Errorf("%s: file %d is %+v", name, i, post.Attachments[i])
			}
		}
	}
}

// The fields the spec names stay strict beside a lenient list.
func TestACommentsOwnFieldsStayStrict(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"id":         func(p map[string]any) { p["id"] = 5 },
		"author":     func(p map[string]any) { p["author"] = "Ben" },
		"body_text":  func(p map[string]any) { p["body_text"] = 5 },
		"is_private": func(p map[string]any) { p["is_private"] = "no" },
		"created_at": func(p map[string]any) { p["created_at"] = 5 },
	} {
		text := recordedText(t, "get_ticket", func(p map[string]any) { edit(p["posts"].([]any)[0].(map[string]any)) })
		if _, ans := replayText(t, text).Ticket(context.Background(), website, login); ans.Class != transport.Protocol {
			t.Errorf("%s: got %v", name, ans.Class)
		}
	}
}
