package queue

import (
	"context"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// Previews (the ticket the cursor rests on) have a budget of their own,
// PreviewBudget per rolling BudgetWindow: browsing a list of tickets is
// the commonest thing a person does, and six reads a minute, shared with
// refreshes and titles, left the pane "Loading…" for up to a minute. The
// other background work keeps BackgroundBudget; together with the
// person's actions they stay below the server's 60 a minute per token.

func spendPreviews(t *testing.T, q *Queue, c *fakeClock, n int) {
	t.Helper()
	for i := range n {
		if i > 0 {
			c.Advance(time.Second)
		}
		g := newGate()
		r := do(q, context.Background(), Job{Priority: Preview, Run: g.run})
		waitStarted(t, g)
		g.release <- ok()
		if a := answer(t, r); a.Class != transport.Object {
			t.Fatalf("preview %d: %v", i, a.Class)
		}
	}
}

func TestThePreviewBudgetIsTheDocumentedOne(t *testing.T) {
	if PreviewBudget != 30 || PreviewBudget+BackgroundBudget >= 60 {
		t.Fatalf("preview budget %d beside %d", PreviewBudget, BackgroundBudget)
	}
}

// Thirty previews start within a minute; the thirty-first waits for the
// window, and the background budget is untouched by them.
func TestPreviewsHaveABudgetOfTheirOwn(t *testing.T) {
	c := newClock()
	q := New(WithClock(c))
	spendPreviews(t, q, c, PreviewBudget)
	held := newGate()
	do(q, context.Background(), Job{Priority: Preview, Run: held.run})
	notStarted(t, held)
	// The other background work still has all of its own.
	spend(t, q, c, BackgroundBudget)
	notStarted(t, held)
	c.Advance(BudgetWindow)
	waitStarted(t, held)
}

// A spent background budget (refreshes, titles) holds no preview.
func TestASpentBackgroundBudgetHoldsNoPreview(t *testing.T) {
	c := newClock()
	q := New(WithClock(c))
	spend(t, q, c, BackgroundBudget)
	g := newGate()
	do(q, context.Background(), Job{Priority: Preview, Run: g.run})
	waitStarted(t, g)
}

// Of a preview and other background work waiting for the one background
// place, the preview starts first; a person still goes before both.
func TestAPreviewGoesBeforeOtherBackgroundWork(t *testing.T) {
	q := New(WithClock(newClock()))
	busy := newGate()
	do(q, context.Background(), Job{Priority: Background, Run: busy.run})
	waitStarted(t, busy)
	refresh, preview := newGate(), newGate()
	do(q, context.Background(), Job{Priority: Background, Run: refresh.run})
	waitWaiting(t, q, 1)
	do(q, context.Background(), Job{Priority: Preview, Run: preview.run})
	waitWaiting(t, q, 2)
	busy.release <- ok()
	waitStarted(t, preview)
	notStarted(t, refresh)
	preview.release <- ok()
	waitStarted(t, refresh)
}
