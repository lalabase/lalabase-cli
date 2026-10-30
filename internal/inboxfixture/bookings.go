package inboxfixture

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// A booking of time (K6): POST /api/v1/tickets/:ticket_id/time_sessions, the
// core's R6 (TimeSessions::Booking, Api::V1::TimeSessionsController and the
// duration of the TimeSession model), the first write the client sends over
// REST. The fixture books with state: the sum (bookedTime) reads it
// afterwards, and the ticket's updated_at moves; a booking writes no activity
// and no notification, so nothing else does. It answers in the core's order:
// the token, a key seen before, the ticket, what the input lacks, the right,
// then the duration. So an empty duration without the right is a 422, and a
// bad one a 403. Refusals: #4 is forbidden, #5 has vanished.

// The texts of the core: the two recorded in the contract copy, the others
// from its locale (a duration that is no time, the model's rule).
const (
	textDurationMissing = "Bitte geben Sie eine Dauer an (duration_string oder duration_minutes)."
	textDurationOverDay = "Dauer darf höchstens 1440 Minuten (24 Stunden) betragen"
	textDurationZero    = "Dauer muss größer als 0 sein"
	textDurationFormat  = "Dauer muss ein gültiges Zeitformat sein (z.B. \"1:30\" oder \"90\")"
	// maxDuration is TimeSession::MAX_DURATION_MINUTES.
	maxDuration = 24 * 60
)

// durationForm is TimeSession#validate_duration_format.
var durationForm = regexp.MustCompile(`(?m)^\s*(\d+(:?\d{0,2})?([.,]\d+)?|\d+:\d{1,2})\s*$`)

// recordedBooking is the row of the recorded booking, the template of every
// answer.
func recordedBooking(file contractFile) (map[string]any, error) {
	var body struct {
		Data map[string]any `json:"data"`
	}
	ex := file.Contract.REST["POST /api/v1/tickets/:ticket_id/time_sessions"]["ok_minutes_text"]
	if err := json.Unmarshal(ex.Response.Body, &body); err != nil || len(body.Data) == 0 {
		return nil, errors.New("inbox fixture: the contract copy records no booking")
	}
	return body.Data, nil
}

// bookingOf is the ticket id of a booking's path, "" for any other.
func bookingOf(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if len(parts) == 3 && parts[0] == "tickets" && parts[2] == "time_sessions" {
		return parts[1]
	}
	return ""
}

// Ruby's to_i and to_f read the number a text starts with, sign included,
// and stop at the first character that is none of it. That is enough for
// what the form lets through and for a typo beside it; odd text the form
// refuses anyway may get another first message here (underscores, a blank
// after the colon, more digits than an int holds).
var (
	leadingInt   = regexp.MustCompile(`^[+-]?\d+`)
	leadingFloat = regexp.MustCompile(`^[+-]?\d*(\.\d+)?`)
)

func rubyInt(s string) int {
	n, _ := strconv.Atoi(leadingInt.FindString(s))
	return n
}

func rubyFloat(s string) float64 {
	n, _ := strconv.ParseFloat(leadingFloat.FindString(s), 64)
	return n
}

// durationMinutes reads a duration as TimeSession#parse_duration_string does:
// a comma is a point, HH:MM with a missing half as zero, decimal hours,
// else minutes. The form is judged apart (durationForm).
func durationMinutes(typed string) int {
	cleaned := strings.ReplaceAll(strings.TrimSpace(typed), ",", ".")
	if hours, minutes, ok := strings.Cut(cleaned, ":"); ok {
		return rubyInt(hours)*60 + rubyInt(minutes)
	}
	if strings.Contains(cleaned, ".") {
		return int(rubyFloat(cleaned) * 60)
	}
	return rubyInt(cleaned)
}

// checkDuration is what the model says of a duration: the messages of its
// validations, in their order; none for one that may be booked.
func checkDuration(typed string, minutes int) []string {
	var refusals []string
	switch {
	case minutes <= 0:
		refusals = append(refusals, textDurationZero)
	case minutes > maxDuration:
		refusals = append(refusals, textDurationOverDay)
	}
	if !durationForm.MatchString(typed) {
		refusals = append(refusals, textDurationFormat)
	}
	return refusals
}

// bookingRefusal answers a booking the input or the model refuses.
func (f *Fixture) bookingRefusal(w http.ResponseWriter, req Request, messages ...string) {
	req.Status = http.StatusUnprocessableEntity
	f.record(req)
	out, _ := json.Marshal(map[string]any{"error": "validation_failed", "code": "invalid_input", "errors": messages})
	w.WriteHeader(http.StatusUnprocessableEntity)
	_, _ = w.Write(out)
}

// bookTime answers a POST under /api/v1/tickets/.
func (f *Fixture) bookTime(w http.ResponseWriter, r *http.Request, req Request) {
	id := bookingOf(r.URL.Path)
	if id == "" || !f.r1 {
		restError(w, req, f, http.StatusNotFound, "not_found", "")
		return
	}
	var body struct {
		Session struct {
			Duration    *string `json:"duration_string"`
			Description string  `json:"description"`
		} `json:"time_session"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&body); err != nil {
		restError(w, req, f, http.StatusBadRequest, "invalid_input", "the inbox fixture reads no such booking")
		return
	}
	req.Route = "time_sessions.book"
	req.Args = map[string]any{"ticket_id": id}
	if body.Session.Duration != nil {
		req.Args["duration_string"] = *body.Session.Duration
	}
	if body.Session.Description != "" {
		req.Args["description"] = body.Session.Description
	}
	key := r.Header.Get("Idempotency-Key")
	f.mu.Lock()
	f.keys = append(f.keys, key)
	f.mu.Unlock()
	// The token: a read-only one, a revoked one, the rate limit.
	switch f.scenario {
	case ReadOnly:
		req.Status = http.StatusForbidden
		f.record(req)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden","code":"read_only"}`))
		return
	case Unauthorized, RateLimited:
		if f.scenarioOn(w, "book:"+id, req) {
			return
		}
	}
	// A key seen before: the first answer again, with other arguments the
	// mismatch.
	f.mu.Lock()
	stored, seen := f.results[key]
	f.mu.Unlock()
	if key != "" && seen {
		if stored.fingerprint != fingerprint(req.Args) {
			req.Status = http.StatusUnprocessableEntity
			f.record(req)
			out, _ := json.Marshal(map[string]any{"error": "idempotency_key_reused", "code": "invalid_input", "message": textKeyMismatch})
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write(out)
			return
		}
		f.bookingAnswer(w, req, stored.result)
		return
	}
	// The read gate: a missing, a hidden and a vanished ticket answer alike.
	project, n := ticketByID(id)
	if n == 0 || (f.scenario == Refusals && project == ProjectWebsite && n == refusedVanished) {
		restError(w, req, f, http.StatusNotFound, "not_found", "")
		return
	}
	typed := ""
	if body.Session.Duration != nil {
		typed = *body.Session.Duration
	}
	if strings.TrimSpace(typed) == "" {
		f.bookingRefusal(w, req, textDurationMissing)
		return
	}
	if f.scenario == Refusals && project == ProjectWebsite && n == refusedWrite {
		req.Status = http.StatusForbidden
		f.record(req)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden","code":"forbidden"}`))
		return
	}
	minutes := durationMinutes(typed)
	if refusals := checkDuration(typed, minutes); len(refusals) > 0 {
		f.bookingRefusal(w, req, refusals...)
		return
	}
	// The booking, and its record: a description of blanks is none.
	f.mu.Lock()
	f.nextBook++
	f.booked[id] += minutes
	f.edit(id).row["updated_at"] = writeStamp
	row := maps.Clone(f.bookRow)
	row["id"], row["ticket_id"], row["duration_minutes"], row["description"] = fmt.Sprintf("00000000-0000-4000-8000-%012d", 950000+f.nextBook), id, minutes, nil
	if strings.TrimSpace(body.Session.Description) != "" {
		row["description"] = body.Session.Description
	}
	f.mutations = append(f.mutations, Mutation{Kind: MutationBook, Ticket: id, Key: key})
	f.remember(key, req.Args, row)
	f.mu.Unlock()
	if f.answerDue(w, r, req) {
		f.bookingAnswer(w, req, row)
	}
}

// bookingAnswer answers 201 with the row under data.
func (f *Fixture) bookingAnswer(w http.ResponseWriter, req Request, row map[string]any) {
	req.Status = http.StatusCreated
	f.record(req)
	out, _ := json.Marshal(map[string]any{"data": row})
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(out)
}
