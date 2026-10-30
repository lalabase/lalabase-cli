package queue

import (
	"context"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

func TestReviewDefaultPauseReachesObserver(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(time.Second)
	var told time.Time
	q := New(WithLifetime(life), WithOnRateLimit(func(until time.Time) { told = until }))
	before := time.Now()
	q.Do(context.Background(), Job{Priority: User, Kind: Read, Run: func(context.Context) transport.Answer {
		return transport.Answer{Class: transport.Protocol, Status: 429}
	}})
	if told.Before(before.Add(defaultPause)) {
		t.Fatalf("observer got %v, queue actually paused until %v", told, q.pausedUntil)
	}
}
