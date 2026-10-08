package ui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// message runs a watch whose outcome is already settled, so it returns at
// once (the interface's tests start no goroutines).
func message(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	return cmd()
}

func TestTheFatalMessageQuits(t *testing.T) {
	m := New()
	m.width, m.height = 120, 24
	next, cmd := step(t, m, worker.Fatal{})
	if next != m || cmd == nil {
		t.Fatal("no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("the fatal message did not quit")
	}
}

// The model learns of a fatal error through its supervisor, also when it
// notices the ended lifetime first; a lifetime that just ended says nothing.
func TestTheModelWatchesItsSupervisor(t *testing.T) {
	if New().Init() != nil {
		t.Fatal("a model without a supervisor watches something")
	}
	failing := worker.New(context.Background())
	watch := New(WithSupervisor(failing)).Init()
	worker.Run(failing, "gateway: inbox", func() int { panic("x") })
	if _, ok := message(t, watch).(worker.Fatal); !ok {
		t.Fatal("no fatal message")
	}
	if _, ok := message(t, watchFatal(failing.Failed(), failing.Context().Done())).(worker.Fatal); !ok {
		t.Fatal("an ended lifetime hid the fatal error")
	}
	stopped := worker.New(context.Background())
	watch = New(WithSupervisor(stopped)).Init()
	stopped.Stop(time.Second)
	if msg := message(t, watch); msg != nil {
		t.Fatalf("a stopped lifetime sent %#v", msg)
	}
}
