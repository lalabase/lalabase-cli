package api

import (
	"context"
	"strings"
	"testing"
)

// C1b-2: what the spec cannot say about an inbox page. A page or page size
// of zero would divide the paging, a negative total or count is no count,
// a row without an id cannot be selected and an id twice would be selected
// twice. Each one is a protocol error, never a page that looks fine.
func TestCheckNotificationPageRefusesWhatTheSpecCannotSee(t *testing.T) {
	api, _ := replayCase(t, "list_notifications", "ok")
	valid, ans := api.Notifications(context.Background(), NotificationQuery{Status: "unread"})
	if !ans.OK() {
		t.Fatal(ans.Class)
	}
	if err := CheckNotificationPage(valid); err != nil {
		t.Fatalf("the recorded page was refused: %v", err)
	}
	for name, tc := range map[string]struct {
		edit func(*NotificationPage)
		want string
	}{
		"page zero":            {func(p *NotificationPage) { p.Meta.Page = 0 }, "page"},
		"page size zero":       {func(p *NotificationPage) { p.Meta.PerPage = 0 }, "per_page"},
		"negative total":       {func(p *NotificationPage) { p.Meta.Total = -1 }, "total"},
		"negative unread":      {func(p *NotificationPage) { p.Meta.UnreadTotal = -1 }, "unread_total"},
		"negative count":       {func(p *NotificationPage) { p.Meta.ByOrganisation[0].Unread = -1 }, "unread"},
		"count without id":     {func(p *NotificationPage) { p.Meta.ByOrganisation[0].ID = "" }, "by_organisation"},
		"row without id":       {func(p *NotificationPage) { p.Data[0].ID = "" }, "id"},
		"row without org id":   {func(p *NotificationPage) { p.Data[0].Organisation.ID = "" }, "organisation"},
		"id twice":             {func(p *NotificationPage) { p.Data = append(p.Data, p.Data[0]); p.Meta.Total = 2 }, "twice"},
		"more rows than total": {func(p *NotificationPage) { p.Meta.Total = 0 }, "total"},
		"more rows than size": {func(p *NotificationPage) {
			p.Meta.PerPage = 1
			p.Data = append(p.Data, p.Data[0])
			p.Data[1].ID = "other"
		}, "per_page"},
	} {
		page := valid
		page.Data = append([]Notification(nil), valid.Data...)
		page.Meta.ByOrganisation = append([]OrganisationCount(nil), valid.Meta.ByOrganisation...)
		tc.edit(&page)
		err := CheckNotificationPage(page)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error naming %q", name, err, tc.want)
		}
	}
	// An empty page within a larger total is fine: an offset page may be
	// empty after rows were read elsewhere.
	page := valid
	page.Data = nil
	page.Meta.Page, page.Meta.Total = 3, 40
	if err := CheckNotificationPage(page); err != nil {
		t.Fatalf("an empty offset page was refused: %v", err)
	}
}
