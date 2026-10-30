package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K3a: a comment through the one write path takes the way its ticket names,
// which the model takes from the feature report.

// commentServer answers every attempt on the REST route with the recorded
// case, or with a body as a 201, and keeps them.
func commentServer(t *testing.T, answer string) (*api.API, *[]bookAttempt) {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	var attempts []bookAttempt
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		attempts = append(attempts, bookAttempt{method: r.Method, path: r.URL.RequestURI(), key: r.Header.Get("Idempotency-Key"), body: string(body)})
		if ex, ok := file.Contract.REST["POST /api/v1/tickets/:ticket_id/posts"][answer]; ok {
			w.WriteHeader(ex.Response.Status)
			_, _ = w.Write(ex.Response.Body)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_synthetic")
	if err != nil {
		t.Fatal(err)
	}
	return api.New(c), &attempts
}

// The report's way is the route's: REST with the key on its header and the
// text plain, confirmed by the recorded 201. A report without the feature
// keeps the zero value, the tool (TestWriteConfirmsTheRecordedSuccesses).
func TestACommentTakesTheWayOfItsTicket(t *testing.T) {
	a, attempts := commentServer(t, "plain")
	way := RoutesOf(features.Report{Via: map[string]features.Way{"comment_plain": features.REST}}).Comment
	w := WriteTicket{Session: 7, Op: 1, Kind: Comment, Key: "lalabase-comment-7-1-1", Project: recordedProject, Ticket: recordedLogin, Value: "line one\nline two", Way: way}
	r := writeResult(t, writeGateway(t, a).Write(context.Background(), w))
	if !r.Answer.OK() || r.Answer.Status != http.StatusCreated || r.WriteTicket != w || r.Comment.TicketID != recordedLogin {
		t.Fatalf("%v %d %s, %+v", r.Answer.Class, r.Answer.Status, r.Answer.Message, r.Comment)
	}
	want := bookAttempt{method: http.MethodPost, path: "/api/v1/tickets/" + recordedLogin + "/posts", key: w.Key, body: `{"post":{"format":"plain","body":"line one\nline two"}}`}
	if len(*attempts) != 1 || (*attempts)[0] != want {
		t.Fatalf("sent %+v", *attempts)
	}
	if way := RoutesOf(features.Report{}).Comment; way != features.MCP {
		t.Fatalf("a report without the feature takes %v", way)
	}
}

// A success counts only when the comment is the ticket's and has an id, and
// the body is not compared: anything else is a protocol answer kept sent,
// which the model reloads and never resends. A refusal keeps its class.
func TestACommentOverRESTConfirmsItsTicketAlone(t *testing.T) {
	comment := func(id, ticket string) string {
		out, _ := json.Marshal(map[string]any{"data": map[string]any{
			"id": id, "ticket_id": ticket, "author": nil, "body_text": "another text", "is_private": false, "created_at": "2026-01-05T09:36:00Z",
		}})
		return string(out)
	}
	for name, tc := range map[string]struct {
		answer string
		class  transport.Class
	}{
		"its ticket":     {comment("x", recordedLogin), transport.Object},
		"another ticket": {comment("x", recordedExport), transport.Protocol},
		"no id":          {comment("", recordedLogin), transport.Protocol},
		"refused":        {"invalid_input_whitespace", transport.InvalidInput},
	} {
		t.Run(name, func(t *testing.T) {
			a, attempts := commentServer(t, tc.answer)
			w := WriteTicket{Session: 7, Op: 1, Kind: Comment, Key: "lalabase-comment-7-1-2", Project: recordedProject, Ticket: recordedLogin, Value: "x", Way: features.REST}
			r := writeResult(t, writeGateway(t, a).Write(context.Background(), w))
			if r.Answer.Class != tc.class || !r.Answer.Sent || len(*attempts) != 1 || (tc.class == transport.Object) != (r.Comment.ID != "") {
				t.Fatalf("%v sent %v, %d attempts, %+v", r.Answer.Class, r.Answer.Sent, len(*attempts), r.Comment)
			}
		})
	}
}
