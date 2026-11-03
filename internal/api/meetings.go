package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// Meetings over REST (K7a, the core's R8): a project's meetings, newest
// first, page by page, with a search. There is no tool for them: the
// feature has the REST way alone. Every text of a meeting is foreign and
// cleaned where it is shown.

// MeetingsRESTSpec is the meeting list of a project. A meeting may have no
// duration, and its creator may be gone (no name).
var MeetingsRESTSpec = Spec{
	Name: "GET /api/v1/projects/:project_id/meetings", Path: "/api/v1/projects/:project_id/meetings",
	Required: []string{
		"data[].id", "data[].project_id", "data[].title", "data[].occurred_at", "data[].duration_minutes?",
		"data[].creator.id", "data[].creator.name?",
		"meta.page", "meta.per_page", "meta.total",
	},
}

// MeetingRow is one meeting of a list.
type MeetingRow struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	Title           string `json:"title"`
	OccurredAt      string `json:"occurred_at"`
	DurationMinutes *int   `json:"duration_minutes"`
	Creator         struct {
		ID   string  `json:"id"`
		Name *string `json:"name"`
	} `json:"creator"`
}

// MeetingPage is one page of a project's meetings.
type MeetingPage struct {
	Data []MeetingRow `json:"data"`
	Meta struct {
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
		Total   int `json:"total"`
	} `json:"meta"`
}

// MeetingQuery is the read of a page of a project's meetings: a search or
// none, a page and its size or the server's.
type MeetingQuery struct {
	ProjectID, Q  string
	Page, PerPage int
}

// MeetingsRESTAnswer sends the read of a meeting list and returns the
// answer undecoded (DecodeMeetingsREST). A blank search and an unset page
// are not sent.
func (a *API) MeetingsRESTAnswer(ctx context.Context, q MeetingQuery) transport.Answer {
	project, ok := segment(q.ProjectID)
	if !ok {
		return noSegment(MeetingsRESTSpec, "project")
	}
	query := url.Values{}
	if q.Q != "" {
		query.Set("q", q.Q)
	}
	if q.Page > 0 {
		query.Set("page", strconv.Itoa(q.Page))
	}
	if q.PerPage > 0 {
		query.Set("per_page", strconv.Itoa(q.PerPage))
	}
	return a.c.Get(ctx, withQuery("/api/v1/projects/"+project+"/meetings", query))
}

// DecodeMeetingsREST checks an answer of MeetingsRESTAnswer and decodes a
// new page from it; the payload stays as it came. An empty list is a page
// without rows, never nil.
func DecodeMeetingsREST(ans transport.Answer) (MeetingPage, transport.Answer) {
	var page MeetingPage
	if ans = decode(MeetingsRESTSpec, ans, &page); !ans.OK() {
		return MeetingPage{}, ans
	}
	if page.Data == nil {
		page.Data = []MeetingRow{}
	}
	return page, ans
}

// CheckMeetingPage checks what the spec cannot say about a page: its
// counts agree with its rows, every row's id comes once, no duration is
// below zero.
func CheckMeetingPage(p MeetingPage) error {
	m := p.Meta
	switch {
	case m.Page <= 0:
		return errors.New("meetings: meta.page is not above zero")
	case m.PerPage <= 0:
		return errors.New("meetings: meta.per_page is not above zero")
	case m.Total < 0:
		return errors.New("meetings: meta.total is negative")
	case len(p.Data) > m.PerPage:
		return errors.New("meetings: more rows than meta.per_page")
	case len(p.Data) > m.Total:
		return errors.New("meetings: more rows than meta.total")
	}
	for i, row := range p.Data {
		if row.DurationMinutes != nil && *row.DurationMinutes < 0 {
			return fmt.Errorf("meetings: data[%d] lasts less than nothing", i)
		}
	}
	return uniqueIDs("meetings: data", func(yield func(string) bool) {
		for _, row := range p.Data {
			if !yield(row.ID) {
				return
			}
		}
	})
}
