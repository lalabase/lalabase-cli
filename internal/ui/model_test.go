package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// step captures commands without running timers or goroutines. C1a has no
// timed behaviour; C1b can add a fake clock when it adds the first timer.
func step(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	result, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected root type %T", next)
	}
	return result, cmd
}

func TestTerminalWidthReportPreservesState(t *testing.T) {
	for _, value := range []ansi.ModeSetting{ansi.ModeReset, ansi.ModeSet, ansi.ModePermanentlySet} {
		m := New()
		m.width, m.height, m.focus = 120, 24, view.Object
		want := m
		want.widthMethod = ansi.GraphemeWidth
		next, cmd := step(t, m, tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: value})
		if next != want || cmd != nil {
			t.Fatalf("mode report %d: %+v", value, next)
		}
	}
	for _, value := range []ansi.ModeSetting{ansi.ModeNotRecognized, ansi.ModePermanentlyReset} {
		m := New()
		next, cmd := step(t, m, tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: value})
		if next != m || cmd != nil {
			t.Fatalf("unsupported mode report %d changed state", value)
		}
	}
}

func TestResizePreservesRootState(t *testing.T) {
	m := New()
	if m.Init() != nil {
		t.Fatal("shell started work")
	}
	m.focus, m.mode, m.widthMethod = view.Object, TextEntry, ansi.GraphemeWidth
	m.content = presentation("kept", "list sentinel", "object sentinel")
	want := m
	for _, width := range []int{120, 119, 80, 79, 23, 0, 120} {
		var cmd tea.Cmd
		m, cmd = step(t, m, tea.WindowSizeMsg{Width: width, Height: 24})
		want.width, want.height = width, 24
		if m != want || cmd != nil {
			t.Fatalf("resize %d reset state or started work: %+v", width, m)
		}
	}
}
