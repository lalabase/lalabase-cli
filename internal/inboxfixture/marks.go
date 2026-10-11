package inboxfixture

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The first write of the fixture (C1f): mark_notification_read; the
// writes of C2 are in writes.go, with the key store they share (replay,
// remember) and the scenario gate. A marked row is read from then on: it
// leaves the unread pages and the counts, as on the server. The fixture
// keeps three logs apart, for the tests: every HTTP attempt (Requests,
// with the key the attempt carried), the idempotency keys it saw (Keys)
// and the changes it carried out (Mutations). A key seen before answers
// the original result and changes nothing, as the server does; with other
// arguments it is refused.

// Mutation is one change the fixture carried out: its kind (the Mutation
// constants in writes.go), the notification or the ticket it changed, and
// the key the request carried.
type Mutation struct {
	Kind         string
	Notification string
	Ticket       string
	Key          string
}

// The rows of A the Refusals scenario answers with a refusal: #2 is
// archived, #3 is not found (deleted meanwhile), #4 is not allowed.
const (
	refusedArchived  = 2
	refusedMissing   = 3
	refusedForbidden = 4
)

// slowAnswer is how long the Slow scenario holds the answer of a write:
// long enough for a person or a test to move the selection meanwhile.
const slowAnswer = 1500 * time.Millisecond

// Mutations are the changes carried out so far, in order.
func (f *Fixture) Mutations() []Mutation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.mutations)
}

// Keys are the idempotency keys the write saw so far, in order, repeats
// included.
func (f *Fixture) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.keys)
}

// findRow is the row with the id, in any organisation, and its number
// (synthesise numbers the ids from 100001 on).
func (f *Fixture) findRow(id string) (map[string]any, int) {
	for _, org := range []string{OrgA, OrgB} {
		for _, row := range f.rows[org] {
			if row["id"] == id {
				n, _ := strconv.Atoi(strings.TrimLeft(id[strings.LastIndex(id, "-")+1:], "0"))
				return row, n - 100000
			}
		}
	}
	return nil, 0
}

// unread are the rows of org not marked as read, as copies: the caller
// serialises them after f.mu is released, while a mark (markRead) or a
// comment (postComment, core #989) changes the rows under the lock.
// Called with f.mu held.
func (f *Fixture) unread(org string) []map[string]any {
	var out []map[string]any
	for _, row := range f.rows[org] {
		if read, _ := row["read"].(bool); !read {
			out = append(out, copyMap(row))
		}
	}
	return out
}

// markRead answers mark_notification_read.
func (f *Fixture) markRead(w http.ResponseWriter, r *http.Request, args map[string]any, reply func(int, any, any), req Request) {
	id, _ := args["notification_id"].(string)
	key, raw := writeKey(args)
	f.mu.Lock()
	f.keys = append(f.keys, raw)
	f.mu.Unlock()
	if id == "" {
		reply(http.StatusOK, toolError("invalid_input", "invalid notification_id format"), nil)
		return
	}
	if f.writeGate(w, "mark:"+id, reply, req) {
		return
	}
	// The dispatcher: a key seen before (the tool is token-scoped, no
	// project).
	if answer := f.replay(key, args); answer != nil {
		reply(http.StatusOK, answer, nil)
		return
	}
	f.mu.Lock()
	row, number := f.findRow(id)
	if row == nil {
		f.mu.Unlock()
		reply(http.StatusOK, toolEmpty("No notification found for "+id+"."), nil)
		return
	}
	if f.scenario == Refusals && row["organisation"].(map[string]any)["id"] == OrgA {
		f.mu.Unlock()
		switch number {
		case refusedArchived:
			reply(http.StatusOK, toolRefused("archived", "notification is archived; it cannot be marked as read"), nil)
			return
		case refusedMissing:
			reply(http.StatusOK, toolEmpty("No notification found for "+id+"."), nil)
			return
		case refusedForbidden:
			reply(http.StatusOK, toolError("forbidden", "Not authorized."), nil)
			return
		}
		f.mu.Lock()
	}
	// The change itself, and its record.
	at := "2026-01-05T10:00:00Z"
	row["read"], row["read_at"], row["state"] = true, at, "read"
	result := map[string]any{"id": id, "state": "read", "read_at": at}
	f.remember(key, args, result)
	f.mutations = append(f.mutations, Mutation{Kind: MutationMarkRead, Notification: id, Key: key})
	f.mu.Unlock()
	f.answerWrite(w, r, reply, req, result)
}

// pause holds an answer for slowAnswer; false when the client left
// meanwhile (logged with status 0: no answer went out).
func (f *Fixture) pause(w http.ResponseWriter, r *http.Request, req Request) bool {
	_ = w
	select {
	case <-time.After(slowAnswer):
		return true
	case <-r.Context().Done():
		req.Status = 0
		f.record(req)
		return false
	}
}

// toolRefused is a refusal with a reason, as the server sends an archived
// notification.
func toolRefused(reason, text string) map[string]any {
	out := toolError("refused", text)
	out["_meta"].(map[string]any)[metaReasonKey] = reason
	return out
}

const metaReasonKey = "lalabase/reason"

// withoutReadOnly is the recorded /me without its read_only field: a
// server before MCP 0.7.0, whose token's permission the client cannot
// know.
func withoutReadOnly(me json.RawMessage) (json.RawMessage, error) {
	var body map[string]any
	if err := json.Unmarshal(me, &body); err != nil {
		return nil, err
	}
	data, _ := body["data"].(map[string]any)
	token, _ := data["token"].(map[string]any)
	if token == nil {
		return nil, errors.New("inbox fixture: the recorded /me has no token")
	}
	delete(token, "read_only")
	return json.Marshal(body)
}

// dropTool removes a tool from the tools/list answer.
func (f *Fixture) dropTool(tool string) error {
	for i, raw := range f.tools {
		var t map[string]any
		if err := json.Unmarshal(raw, &t); err != nil {
			return err
		}
		if t["name"] == tool {
			f.tools = slices.Delete(f.tools, i, i+1)
			f.dropped[tool] = true
			return nil
		}
	}
	return errors.New("inbox fixture: no tool " + tool + " in the contract copy")
}
