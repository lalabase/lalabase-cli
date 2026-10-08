package api

import (
	"context"
	"reflect"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// Every waiter of a shared read decodes the same answer itself: both get the
// whole page, and neither shares a list, a pointer or a byte with the other.
func TestDecodeNotificationsGivesEveryCallerItsOwnFullPage(t *testing.T) {
	api, _ := replayCase(t, "list_notifications", "ok")
	ans := api.NotificationsAnswer(context.Background(), NotificationQuery{Status: "unread"})
	first, a1 := DecodeNotifications(ans)
	second, a2 := DecodeNotifications(ans)
	if !a1.OK() || !a2.OK() || !reflect.DeepEqual(first, second) {
		t.Fatalf("%v %v: %+v / %+v", a1.Class, a2.Class, first, second)
	}
	if len(first.Data) != 1 || first.Data[0].ID != unread || first.Data[0].Ticket == nil || first.Data[0].Ticket.Number != 1 ||
		first.Data[0].Organisation.ID != org || first.Meta.UnreadTotal != 1 || len(first.Meta.ByOrganisation) != 1 {
		t.Fatalf("decoded %+v", first)
	}
	first.Data[0].ID = "changed"
	first.Data[0].Ticket.Title = "changed"
	first.Meta.ByOrganisation[0].Name = "changed"
	if second.Data[0].ID != unread || second.Data[0].Ticket.Title == "changed" || second.Meta.ByOrganisation[0].Name == "changed" {
		t.Fatal("the second decode shares data with the first")
	}
}

// The decode-only path checks the same Spec as the adapter: a field missing,
// null or of the wrong type is a protocol error with an empty page, never a
// half-filled one, and keeps what the transport knew.
func TestDecodeNotificationsRefusesWhatTheAdapterRefuses(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"missing":    func(p map[string]any) { delete(p["data"].([]any)[0].(map[string]any), "id") },
		"null":       func(p map[string]any) { p["meta"].(map[string]any)["total"] = nil },
		"wrong type": func(p map[string]any) { p["meta"].(map[string]any)["page"] = "one" },
		"nested": func(p map[string]any) {
			p["meta"].(map[string]any)["by_organisation"].([]any)[0].(map[string]any)["unread"] = nil
		},
	} {
		api := replayText(t, recordedText(t, "list_notifications", edit))
		ans := api.NotificationsAnswer(context.Background(), NotificationQuery{})
		page, got := DecodeNotifications(ans)
		if got.Class != transport.Protocol || got.Status != ans.Status || got.Sent != ans.Sent {
			t.Errorf("%s: got %v status %d sent %v", name, got.Class, got.Status, got.Sent)
		}
		if !reflect.DeepEqual(page, NotificationPage{}) {
			t.Errorf("%s: a refused answer gave a page: %+v", name, page)
		}
		// The adapter refuses it the same way.
		if _, viaAdapter := api.Notifications(context.Background(), NotificationQuery{}); viaAdapter.Class != transport.Protocol {
			t.Errorf("%s: the adapter accepted it: %v", name, viaAdapter.Class)
		}
	}
}

// An answer that is no object gives no page, even with a payload beside it,
// and passes on the transport's class, reason, retry time and sent flag.
func TestDecodeNotificationsGivesNoPageForAnError(t *testing.T) {
	api, _ := replayCase(t, "list_notifications", "ok")
	valid := api.NotificationsAnswer(context.Background(), NotificationQuery{}).Payload
	for _, ans := range []transport.Answer{
		{Class: transport.Temporary, Status: 429, Sent: true, Payload: valid},
		{Class: transport.Refused, Reason: "archived", Payload: valid},
		{Class: transport.Canceled},
	} {
		page, got := DecodeNotifications(ans)
		if !reflect.DeepEqual(page, NotificationPage{}) || !reflect.DeepEqual(got, ans) {
			t.Errorf("%v: page %+v, answer %+v", ans.Class, page, got)
		}
	}
	notFound, _ := replayCase(t, "list_notifications", "organisation_not_found")
	page, got := DecodeNotifications(notFound.NotificationsAnswer(context.Background(), NotificationQuery{OrganisationID: missing}))
	if got.Class != transport.NotFound || !reflect.DeepEqual(page, NotificationPage{}) {
		t.Fatalf("%v %+v", got.Class, page)
	}
}
