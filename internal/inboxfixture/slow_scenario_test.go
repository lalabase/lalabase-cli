package inboxfixture

import (
	"context"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// Review round 2 of C1f, probe S14: the Slow scenario carries the change out at once and only answers
// late: a read during the pause already shows the row gone.
func TestTheSlowScenarioActsBeforeItAnswers(t *testing.T) {
	f, a, _ := serve(t, Slow)
	ctx := context.Background()
	a1 := api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: OrgA}
	page, ans := a.Notifications(ctx, a1)
	if !ans.OK() {
		t.Fatalf("page 1: %v", ans.Class)
	}
	row := page.Data[0].ID
	done := make(chan transport.Answer, 1)
	go func() {
		_, ans := a.MarkNotificationRead(ctx, row, "key-00000001")
		done <- ans
	}()
	deadline := time.Now().Add(slowAnswer / 2)
	for len(f.Mutations()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the change waits for the answer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if during, _ := a.Notifications(ctx, a1); during.Meta.Total != 29 {
		t.Fatalf("during the pause: %+v", during.Meta)
	}
	select {
	case ans := <-done:
		if !ans.OK() {
			t.Fatalf("mark: %v", ans.Class)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
	}
}
