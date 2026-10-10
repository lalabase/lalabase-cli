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
