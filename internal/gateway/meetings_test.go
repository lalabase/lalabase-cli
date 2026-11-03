package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// K7a: a project's meetings, read over REST alone, checked against the
// question before the model sees them, filed only through Scope.Receive.

const meetingsGroup = "GET /api/v1/projects/:project_id/meetings"

// meetingsServer serves one recorded case of the meeting list, edited
// when edit is set, and records what it was sent.
type meetingsServer struct {
	mu   sync.Mutex
	sent []string
	api  *api.API
}

func serveMeetings(t *testing.T, name string, edit func(body map[string]any)) *meetingsServer {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	ex, ok := file.Contract.REST[meetingsGroup][name]
	if !ok {
		t.Fatalf("the contract has no %s", name)
	}
	s := &meetingsServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.sent = append(s.sent, r.Method+" "+r.URL.RequestURI())
		s.mu.Unlock()
		out := []byte(ex.Response.Body)
		if edit != nil {
			var body map[string]any
			_ = json.Unmarshal(out, &body)
			edit(body)
			out, _ = json.Marshal(body)
		}
		w.WriteHeader(ex.Response.Status)
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_synthetic")
	if err != nil {
		t.Fatal(err)
	}
	s.api = api.New(c)
	return s
}

func (s *meetingsServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.sent...)
}

func meetingsKey(ns cache.Namespace, project, q string, page int) cache.Key {
	k := ns.Key(api.MeetingsRESTSpec.Name)
	k.Project, k.Query, k.Page, k.PerPage = project, q, page, MeetingPageSize
	return k
}

func meetingsGateway(t *testing.T, srv *meetingsServer) (*Gateway, *Scope, cache.Namespace) {
	t.Helper()
	life := worker.New(context.Background())
	t.Cleanup(func() { life.Stop(wall) })
	ns := confirmed(t)
	return New(life, queue.New(queue.WithLifetime(life)), srv.api), NewScope(ns), ns
}

// The read sends the key's question, decodes the page, and the scope files
// it as it came; the ticket names REST, the meetings' one way.
func TestAMeetingPageIsReadOverRESTAndFiledThroughTheScope(t *testing.T) {
	srv := serveMeetings(t, "ok", func(body map[string]any) {
		// As the client asks: page 1 of MeetingPageSize rows.
		body["meta"].(map[string]any)["per_page"] = MeetingPageSize
	})
	gw, scope, ns := meetingsGateway(t, srv)
	key := meetingsKey(ns, website, "März", 1)
	ticket := scope.Begin(Meetings, key, queue.User)
	if ticket.Way != features.REST {
		t.Fatalf("a meetings read via %v", ticket.Way)
	}
	r := result(t, run(gw.Read(context.Background(), ticket))).(MeetingsResult)
	if !r.Answer.OK() || len(r.Page.Data) != 2 || r.Page.Data[0].Title != "Kickoff Relaunch" {
		t.Fatalf("%v %q %+v", r.Answer.Class, r.Answer.Message, r.Page)
	}
	want := "GET /api/v1/projects/" + website + "/meetings?page=1&per_page=" + strconv.Itoa(MeetingPageSize) + "&q=M%C3%A4rz"
	if got := srv.requests(); len(got) != 1 || got[0] != want {
		t.Fatalf("sent %q, want %q", got, want)
	}
	store := cache.NewStore()
	if d, ok := scope.Receive(store, r, time.Now()); !ok || d != cache.Replace {
		t.Fatalf("%v %v", d, ok)
	}
	e, ok := store.Get(key)
	if !ok {
		t.Fatal("nothing filed")
	}
	page, ans := api.DecodeMeetingsREST(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
	if !ans.OK() || CheckMeetingsPage(key, page) != nil || page.Data[1].ID != r.Page.Data[1].ID {
		t.Fatalf("the filed page: %v %q", ans.Class, ans.Message)
	}
}

// A page that is not the answer to the question is a contradiction the
// model never sees as rows: another page, another size, a meeting of
// another project, counts that disagree.
func TestAMeetingPageThatAnswersAnotherQuestionIsRefused(t *testing.T) {
	for name, edit := range map[string]func(body map[string]any){
		"another page":    func(b map[string]any) { b["meta"].(map[string]any)["page"] = 2 },
		"another size":    func(b map[string]any) { b["meta"].(map[string]any)["per_page"] = MeetingPageSize + 1 },
		"another project": func(b map[string]any) { b["data"].([]any)[1].(map[string]any)["project_id"] = orgA },
		"a short total":   func(b map[string]any) { b["meta"].(map[string]any)["total"] = 1 },
	} {
		srv := serveMeetings(t, "ok", func(body map[string]any) {
			body["meta"].(map[string]any)["per_page"] = MeetingPageSize
			edit(body)
		})
		gw, scope, ns := meetingsGateway(t, srv)
		r := result(t, run(gw.Read(context.Background(), scope.Begin(Meetings, meetingsKey(ns, website, "", 1), queue.User)))).(MeetingsResult)
		if r.Answer.Class != transport.Protocol || len(r.Page.Data) != 0 {
			t.Errorf("%s: %v %q, %d rows", name, r.Answer.Class, r.Answer.Message, len(r.Page.Data))
		}
	}
}

// A ticket that is no meetings read of one project is refused before
// anything is sent.
func TestAMeetingsTicketOutsideItsShapeSendsNothing(t *testing.T) {
	srv := serveMeetings(t, "ok", nil)
	gw, scope, ns := meetingsGateway(t, srv)
	good := meetingsKey(ns, website, "", 1)
	for name, c := range map[string]struct {
		area Area
		edit func(k *cache.Key)
	}{
		"no project":      {Meetings, func(k *cache.Key) { k.Project = "" }},
		"page 0":          {Meetings, func(k *cache.Key) { k.Page = 0 }},
		"another size":    {Meetings, func(k *cache.Key) { k.PerPage = MeetingPageSize + 1 }},
		"an organisation": {Meetings, func(k *cache.Key) { k.Organisation = orgA }},
		"a status":        {Meetings, func(k *cache.Key) { k.Status = "new" }},
		"a ticket":        {Meetings, func(k *cache.Key) { k.Ticket = login }},
		"another area":    {Tickets, func(*cache.Key) {}},
	} {
		k := good
		c.edit(&k)
		r := result(t, run(gw.Read(context.Background(), scope.Begin(c.area, k, queue.User))))
		if h := r.(Received).Header(); h.Answer.Class != transport.InvalidInput {
			t.Errorf("%s: %v %q", name, h.Answer.Class, h.Answer.Message)
		}
	}
	if got := srv.requests(); len(got) != 0 {
		t.Fatalf("sent %q", got)
	}
	if err := CheckMeetingsTicket(scope.Begin(Meetings, good, queue.User)); err != nil {
		t.Fatal(err)
	}
}

// A project the reader cannot reach is a removal: the scope takes every
// page of the project's meetings, the search's too.
func TestAMeetingListOutOfReachIsRemoved(t *testing.T) {
	srv := serveMeetings(t, "not_found", nil)
	gw, scope, ns := meetingsGateway(t, srv)
	store := cache.NewStore()
	search := meetingsKey(ns, website, "März", 1)
	store.Apply(search, cache.Replace, []byte(`{"data":[],"meta":{"page":1,"per_page":10,"total":0}}`), time.Now())
	r := result(t, run(gw.Read(context.Background(), scope.Begin(Meetings, meetingsKey(ns, website, "", 1), queue.User)))).(MeetingsResult)
	if d, ok := scope.Receive(store, r, time.Now()); !ok || d != cache.Remove {
		t.Fatalf("%v %v", d, ok)
	}
	if _, ok := store.Get(search); ok {
		t.Fatal("the search of a project out of reach stayed")
	}
}

// K7b: one meeting, whole, in the object pane.

const (
	meetingGroup = "GET /api/v1/meetings/:id"
	kickoff      = "00000000-0000-4000-8000-000000000101"
)

func serveMeeting(t *testing.T, name string, edit func(data map[string]any), opts ...transport.Option) *meetingsServer {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	ex, ok := file.Contract.REST[meetingGroup][name]
	if !ok {
		t.Fatalf("the contract has no %s", name)
	}
	s := &meetingsServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.sent = append(s.sent, r.Method+" "+r.URL.RequestURI())
		s.mu.Unlock()
		out := []byte(ex.Response.Body)
		if edit != nil {
			var body map[string]any
			_ = json.Unmarshal(out, &body)
			edit(body["data"].(map[string]any))
			out, _ = json.Marshal(body)
		}
		w.WriteHeader(ex.Response.Status)
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_synthetic", opts...)
	if err != nil {
		t.Fatal(err)
	}
	s.api = api.New(c)
	return s
}

func meetingKey(ns cache.Namespace, project, id string) cache.Key {
	k := ns.Key(api.MeetingRESTSpec.Name)
	k.Project, k.Ticket = project, id
	return k
}

func TestAMeetingIsReadOverRESTAndFiledThroughTheScope(t *testing.T) {
	srv := serveMeeting(t, "ok", nil) // a meeting of website
	gw, scope, ns := meetingsGateway(t, srv)
	key := meetingKey(ns, website, kickoff)
	ticket := scope.Begin(Meeting, key, queue.User)
	if ticket.Way != features.REST {
		t.Fatalf("a meeting read via %v", ticket.Way)
	}
	r := result(t, run(gw.Read(context.Background(), ticket))).(MeetingResult)
	if !r.Answer.OK() || r.Meeting.Title != "Kickoff Relaunch" || *r.Meeting.Transcript != "Anna: Wir starten im März." {
		t.Fatalf("%v %q %+v", r.Answer.Class, r.Answer.Message, r.Meeting)
	}
	if got := srv.requests(); len(got) != 1 || got[0] != "GET /api/v1/meetings/"+kickoff {
		t.Fatalf("sent %q", got)
	}
	store := cache.NewStore()
	if d, ok := scope.Receive(store, r, time.Now()); !ok || d != cache.Replace {
		t.Fatalf("%v %v", d, ok)
	}
	if _, ok := store.Get(key); !ok {
		t.Fatal("nothing filed")
	}
}

// Another meeting, or one of another project, is a contradiction.
func TestAMeetingThatIsNotTheOneAskedForIsRefused(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"another meeting": func(d map[string]any) { d["id"] = login },
		"another project": func(d map[string]any) { d["project_id"] = orgA },
		"a negative time": func(d map[string]any) { d["duration_minutes"] = -5 },
	} {
		srv := serveMeeting(t, "ok", edit)
		gw, scope, ns := meetingsGateway(t, srv)
		r := result(t, run(gw.Read(context.Background(), scope.Begin(Meeting, meetingKey(ns, website, kickoff), queue.User)))).(MeetingResult)
		if r.Answer.Class != transport.Protocol || r.Meeting.ID != "" {
			t.Errorf("%s: %v %q", name, r.Answer.Class, r.Answer.Message)
		}
	}
}

func TestAMeetingTicketOutsideItsShapeSendsNothing(t *testing.T) {
	srv := serveMeeting(t, "ok", nil)
	gw, scope, ns := meetingsGateway(t, srv)
	good := meetingKey(ns, website, kickoff)
	for name, c := range map[string]struct {
		area Area
		edit func(k *cache.Key)
	}{
		"no meeting":   {Meeting, func(k *cache.Key) { k.Ticket = "" }},
		"a page":       {Meeting, func(k *cache.Key) { k.Page = 1 }},
		"a search":     {Meeting, func(k *cache.Key) { k.Query = "x" }},
		"another area": {Meetings, func(*cache.Key) {}},
	} {
		k := good
		c.edit(&k)
		r := result(t, run(gw.Read(context.Background(), scope.Begin(c.area, k, queue.User))))
		if h := r.(Received).Header(); h.Answer.Class != transport.InvalidInput {
			t.Errorf("%s: %v %q", name, h.Answer.Class, h.Answer.Message)
		}
	}
	if got := srv.requests(); len(got) != 0 {
		t.Fatalf("sent %q", got)
	}
}

// A meeting gone or out of reach is removed, and only the meeting: the
// list it was picked from stays.
func TestAMeetingOutOfReachIsRemovedAndTheListStays(t *testing.T) {
	srv := serveMeeting(t, "not_found", nil)
	gw, scope, ns := meetingsGateway(t, srv)
	store := cache.NewStore()
	list := meetingsKey(ns, website, "", 1)
	store.Apply(list, cache.Replace, []byte(`{"data":[],"meta":{"page":1,"per_page":25,"total":0}}`), time.Now())
	key := meetingKey(ns, website, kickoff)
	store.Apply(key, cache.Replace, []byte(`{}`), time.Now())
	r := result(t, run(gw.Read(context.Background(), scope.Begin(Meeting, key, queue.User)))).(MeetingResult)
	if d, ok := scope.Receive(store, r, time.Now()); !ok || d != cache.Remove {
		t.Fatalf("%v %v", d, ok)
	}
	if _, ok := store.Get(key); ok {
		t.Fatal("the meeting stayed")
	}
	if _, ok := store.Get(list); !ok {
		t.Fatal("the list went with the meeting")
	}
}

// An answer above the transport's bound is an error that says so, never a
// meeting with its text cut.
func TestAMeetingLargerThanTheBoundIsAnErrorNotACutText(t *testing.T) {
	srv := serveMeeting(t, "ok", func(d map[string]any) {
		d["transcript"] = strings.Repeat("Anna: Wir starten im März. ", 200)
	}, transport.WithMaxBody(2048))
	gw, scope, ns := meetingsGateway(t, srv)
	r := result(t, run(gw.Read(context.Background(), scope.Begin(Meeting, meetingKey(ns, website, kickoff), queue.User)))).(MeetingResult)
	if r.Answer.Class != transport.Protocol || !strings.Contains(r.Answer.Message, "larger than 2048 bytes") || r.Meeting.Transcript != nil {
		t.Fatalf("%v %q %+v", r.Answer.Class, r.Answer.Message, r.Meeting.Transcript)
	}
}
