package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K3a: a comment over REST (the core's R3a), always plain text. Every answer
// is replayed from the contract copy; the transport's contract test holds
// the class of each.

const postGroup = "POST /api/v1/tickets/:ticket_id/posts"

// Every recorded answer reaches the caller sent: a comment of the ticket,
// with the spec's fields, from a 201 alone. A refusal says why in errors.
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
		if ans.Status != ex.Response.Status || !ans.Sent || ans.OK() != wrote || len(*sent) != 1 || (comment.ID != "") != wrote || (wrote && comment.TicketID != login) {
			t.Errorf("%s: %v %d sent %v, %d requests, %+v", name, ans.Class, ans.Status, ans.Sent, len(*sent), comment)
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

// Plain on the wire as the core reads it: the format, the body as it is,
// nothing else in the post. What the encoder escapes arrives byte for byte:
// the core makes the markup, the client none.
func TestACommentGoesAsPlainText(t *testing.T) {
	api, sent := restWriteCase(t, postGroup, "plain")
	if _, ans := api.PostCommentREST(context.Background(), login, "Zeile 1\nZeile 2", "key-0003"); !ans.OK() {
		t.Fatalf("%v %s", ans.Class, ans.Message)
	}
	want := recordedWrite{method: http.MethodPost, path: "/api/v1/tickets/" + login + "/posts", key: "key-0003", body: `{"post":{"format":"plain","body":"Zeile 1\nZeile 2"}}`}
	if len(*sent) != 1 || (*sent)[0] != want {
		t.Fatalf("sent %+v, want %+v", *sent, want)
	}
	api, sent = restWriteCase(t, postGroup, "plain")
	api.PostCommentREST(context.Background(), login, "<b>& \u2028", "key-0004")
	var body struct {
		Post map[string]string `json:"post"`
	}
	if err := json.Unmarshal([]byte((*sent)[0].body), &body); err != nil || body.Post["body"] != "<b>& \u2028" {
		t.Fatalf("arrived as %q (%v)", body.Post, err)
	}
}

// A comment without a usable key, or on an id that is no path segment, is
// refused before anything is sent.
func TestACommentIsRefusedBeforeSending(t *testing.T) {
	c, _ := transport.New("http://127.0.0.1:1", "lalab_pat_test")
	for _, in := range [][2]string{{login, ""}, {login, "short"}, {"", "key-0005"}, {"..", "key-0005"}, {"a/b", "key-0005"}} {
		if _, ans := New(c).PostCommentREST(context.Background(), in[0], "Hallo", in[1]); ans.Class != transport.InvalidInput || ans.Sent {
			t.Errorf("%q: %v sent %v", in, ans.Class, ans.Sent)
		}
	}
}
