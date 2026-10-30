package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K3a: a comment over REST (the core's R3a), always plain text. Every answer
// is replayed from the contract copy; the transport's contract test holds
// the class of each.

const postGroup = "POST /api/v1/tickets/:ticket_id/posts"

// Every recorded answer reaches the caller sent, as it was: a comment of the
// ticket, with the spec's fields, from a 201 alone. The refusals say why in
// errors, the 403 and the 404 not at all.
func TestEveryRecordedAnswerToAComment(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(Specs, func(s Spec) bool { return s.Name == postGroup && s.Method == http.MethodPost }) {
		t.Error("the comment is not among Specs")
	}
	for name, ex := range file.Contract.REST[postGroup] {
		api, sent := restWriteCase(t, postGroup, name)
		comment, ans := api.PostCommentREST(context.Background(), login, "Hallo", "key-0001")
		wrote := ex.Response.Status == http.StatusCreated
		if ans.Status != ex.Response.Status || !ans.Sent || ans.OK() != wrote || len(*sent) != 1 || (comment.ID != "") != wrote {
			t.Errorf("%s: %v %d sent %v, %d requests, %+v", name, ans.Class, ans.Status, ans.Sent, len(*sent), comment)
		}
		if wrote && (comment.TicketID != login || comment.BodyText == "") {
			t.Errorf("%s: %+v", name, comment)
		}
		if missing, err := PostCommentSpec.Missing(ex.Response.Body); wrote && (err != nil || len(missing) > 0) {
			t.Errorf("%s lacks %v (%v)", name, missing, err)
		}
	}
	api, _ := restWriteCase(t, postGroup, "invalid_input_whitespace")
	if _, ans := api.PostCommentREST(context.Background(), login, " ", "key-0002"); ans.RefusalText() != "Bitte geben Sie einen Kommentartext (post[body]) an." {
		t.Errorf("refusal %q", ans.RefusalText())
	}
}

// A comment is plain text, on the wire as the core reads it: the format, the
// body as it is, nothing else in the post (no is_private). What the encoder
// escapes arrives byte for byte: the core makes the markup, the client none.
// Two sends of one action are the same bytes: the server compares them.
func TestACommentGoesAsPlainText(t *testing.T) {
	api, sent := restWriteCase(t, postGroup, "plain")
	for range 2 {
		if _, ans := api.PostCommentREST(context.Background(), login, "Zeile 1\nZeile 2", "key-0003"); !ans.OK() {
			t.Fatalf("%v %s", ans.Class, ans.Message)
		}
	}
	want := recordedWrite{method: http.MethodPost, path: "/api/v1/tickets/" + login + "/posts", key: "key-0003", body: `{"post":{"format":"plain","body":"Zeile 1\nZeile 2"}}`}
	if len(*sent) != 2 || (*sent)[0] != want || (*sent)[1] != want {
		t.Fatalf("sent %+v, want %+v", *sent, want)
	}
	for _, typed := range []string{"<b>&", "a b", " ", `\"`, "\U0001F600", "  rechts  "} {
		api, sent := restWriteCase(t, postGroup, "plain")
		api.PostCommentREST(context.Background(), login, typed, "key-0004")
		var body struct {
			Post map[string]string `json:"post"`
		}
		if err := json.Unmarshal([]byte((*sent)[0].body), &body); err != nil {
			t.Fatal(err)
		}
		if body.Post["body"] != typed || len(body.Post) != 2 {
			t.Fatalf("%q arrived as %q", typed, body.Post)
		}
	}
}

// A comment without a key of the right form, or on an id that is no path
// segment, is refused before anything is sent.
func TestACommentIsRefusedBeforeSending(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	for _, in := range [][2]string{{login, ""}, {login, "short"}, {login, strings.Repeat("k", 129)}, {"", "key-0005"}, {"..", "key-0005"}, {"a/b", "key-0005"}} {
		if _, ans := New(c).PostCommentREST(context.Background(), in[0], "Hallo", in[1]); ans.Class != transport.InvalidInput || ans.Sent {
			t.Errorf("%q: %v sent %v", in, ans.Class, ans.Sent)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests sent", hits)
	}
}

// An answer that lacks what confirms the comment, or holds it in the wrong
// type, is a protocol error kept sent: the server may have written it. What
// the contract cannot hold, so the only hand-written answers here.
func TestACommentAnswerWithoutItsFieldsIsAProtocolErrorKeptSent(t *testing.T) {
	rest := `"author":{"id":"a"},"body_text":"x","is_private":false,"created_at":"2026-01-05T09:36:00Z"`
	for _, body := range []string{
		`{}`, `{"data":null}`,
		`{"data":{"ticket_id":"t",` + rest + `}}`,
		`{"data":{"id":"x",` + rest + `}}`,
		`{"data":{"id":7,"ticket_id":"t",` + rest + `}}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(body))
		}))
		c, _ := transport.New(srv.URL, "lalab_pat_test")
		_, ans := New(c).PostCommentREST(context.Background(), login, "Hallo", "key-0006")
		srv.Close()
		if ans.Class != transport.Protocol || !ans.Sent {
			t.Errorf("%s: %v sent %v", body, ans.Class, ans.Sent)
		}
	}
}
