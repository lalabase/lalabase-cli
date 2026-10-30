package gateway

import (
	"context"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K3a: a comment through the one write path takes the way its ticket names.

// commentSource answers a comment on either way and says which it took.
type commentSource struct {
	*api.API
	way    string
	answer api.Comment
}

func (s *commentSource) PostCommentREST(context.Context, string, string, string) (api.Comment, transport.Answer) {
	s.way = "rest"
	return s.answer, transport.Answer{Class: transport.Object, Status: 201, Sent: true}
}

func (s *commentSource) PostComment(_ context.Context, _, _, _ string, o api.CommentOptions, _ string) (api.Comment, transport.Answer) {
	s.way = "mcp"
	if !o.Plain {
		s.way = "html"
	}
	return s.answer, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
}

// The report's way is the route's, the text plain on both, and a report
// without the feature keeps the tool. A success counts only when the comment
// is the ticket's and has an id: else a protocol answer kept sent, which the
// model reloads and never resends.
func TestACommentTakesTheWayOfItsTicketAndConfirmsItsTicketAlone(t *testing.T) {
	report := features.Report{Via: map[string]features.Way{"comment_plain": features.REST}}
	for name, tc := range map[string]struct {
		way     features.Way
		answer  api.Comment
		wire    string
		class   transport.Class
		carried bool
	}{
		"REST":           {RoutesOf(report).Comment, api.Comment{ID: "x", TicketID: recordedLogin}, "rest", transport.Object, true},
		"the tool":       {RoutesOf(features.Report{}).Comment, api.Comment{ID: "x", TicketID: recordedLogin}, "mcp", transport.Object, true},
		"another ticket": {features.REST, api.Comment{ID: "x", TicketID: recordedExport}, "rest", transport.Protocol, false},
		"no id":          {features.REST, api.Comment{TicketID: recordedLogin}, "rest", transport.Protocol, false},
	} {
		t.Run(name, func(t *testing.T) {
			src := &commentSource{answer: tc.answer}
			w := WriteTicket{Session: 7, Op: 1, Kind: Comment, Key: "lalabase-comment-7-1-1", Project: recordedProject, Ticket: recordedLogin, Value: "x", Way: tc.way}
			r := writeResult(t, writeGateway(t, src).Write(context.Background(), w))
			if src.way != tc.wire || r.Answer.Class != tc.class || !r.Answer.Sent || r.WriteTicket != w || (r.Comment.ID != "") != tc.carried {
				t.Fatalf("way %s, %v sent %v, %+v", src.way, r.Answer.Class, r.Answer.Sent, r.Comment)
			}
		})
	}
}
