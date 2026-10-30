package inboxfixture

import (
	"encoding/json"
	"maps"
	"net/http"
)

// A comment over REST (K3a): POST /api/v1/tickets/:ticket_id/posts, the
// core's R3a (Posts::Comment and Api::V1::PostsController), with the state
// the tool keeps (addComment). It answers in the core's order: the token, a
// key seen before, the ticket, the input, the right. So a bad input on a
// hidden ticket is a 404, and a good one without the right a 403. Refusals:
// #2 and #4 are forbidden, as the tool's, #5 has vanished.

// The text of the core, as recorded in the contract copy, for a body of blanks
// and, since nothing here writes html, for any other format.
const (
	textCommentEmpty  = "Bitte geben Sie einen Kommentartext (post[body]) an."
	textCommentFormat = "Das Format (post[format]) muss html oder plain sein."
)

// createComment answers POST /api/v1/tickets/:ticket_id/posts.
func (f *Fixture) createComment(w http.ResponseWriter, r *http.Request, req Request) {
	id := ticketWrite(r.URL.Path, "posts")
	if !f.r1 {
		restError(w, req, f, http.StatusNotFound, "not_found", "")
		return
	}
	var body struct {
		Post struct {
			Format string `json:"format"`
			Body   string `json:"body"`
		} `json:"post"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&body); err != nil {
		restError(w, req, f, http.StatusBadRequest, "invalid_input", "the inbox fixture reads no such comment")
		return
	}
	req.Route = "posts.create"
	req.Args = map[string]any{"ticket_id": id, "format": body.Post.Format, "body": body.Post.Body}
	key := r.Header.Get("Idempotency-Key")
	if f.restWriteGate(w, req, key, "comment:"+id) {
		return
	}
	// The read gate: a missing, a hidden and a vanished ticket answer alike.
	project, n := ticketByID(id)
	refused := f.scenario == Refusals && project == ProjectWebsite
	if n == 0 || (refused && n == refusedVanished) {
		restError(w, req, f, http.StatusNotFound, "not_found", "")
		return
	}
	text := plainText(body.Post.Body)
	switch {
	case body.Post.Format != "plain":
		f.restRefusal(w, req, textCommentFormat)
		return
	case text == "":
		f.restRefusal(w, req, textCommentEmpty)
		return
	}
	if refused && (n == refusedValues || n == refusedWrite) {
		req.Status = http.StatusForbidden
		f.record(req)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden","code":"forbidden"}`))
		return
	}
	f.mu.Lock()
	row := maps.Clone(f.postRow)
	maps.Copy(row, f.addComment(id, key, text, body.Post.Body, false))
	row["updated_at"] = writeStamp
	f.remember(key, req.Args, row)
	f.mu.Unlock()
	if f.answerDue(w, r, req) {
		f.restCreated(w, req, row)
	}
}
