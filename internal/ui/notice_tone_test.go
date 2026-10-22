package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// A launch's notice takes its tone from the outcome, never from its text:
// waiting while it runs, plain when Claude Code opened or ended, a problem
// for every outcome that opened nothing or ended in an error.
func TestALaunchsNoticeTakesTheToneOfItsOutcome(t *testing.T) {
	plain := map[launch.Kind]bool{launch.Opened: true, launch.Ended: true, launch.Canceled: true}
	for kind := launch.Opened; kind <= launch.Canceled; kind++ {
		h := newHarness(t)
		f := h.withLauncher()
		f.kind = kind
		h.openSix()
		cmd := h.press("x")
		if got := h.m.screen().StatusStyle; got != present.NoticeWaiting {
			t.Errorf("kind %v: tone while launching %v, want waiting", kind, got)
		}
		h.drive(cmd)
		want := present.NoticeProblem
		if plain[kind] {
			want = present.Plain
		}
		if got := h.m.screen().StatusStyle; got != want {
			t.Errorf("kind %v: tone %v, want %v", kind, got, want)
		}
	}
}

// The notices before a launch's take their tones from the state too: an
// expired session and the disk cache are problems, a rate-limit pause is
// waiting, and no notice has no tone.
func TestTheSessionsNoticesTakeTheToneOfTheirState(t *testing.T) {
	h := newHarness(t)
	h.start()
	if s := h.m.screen(); s.Status.String() != "" || s.StatusStyle != present.Plain {
		t.Fatalf("no notice: %q in %v", s.Status.String(), s.StatusStyle)
	}
	a := h.m.app
	a.disk = &diskState{notice: uitext.DiskBusy}
	if got := h.m.screen().StatusStyle; got != present.NoticeProblem {
		t.Fatalf("the disk cache in use: %v, want a problem", got)
	}
	a.pause = a.now().Add(time.Minute)
	if got := h.m.screen().StatusStyle; got != present.NoticeWaiting {
		t.Fatalf("a rate-limit pause: %v, want waiting", got)
	}
	a.expired = true
	if got := h.m.screen().StatusStyle; got != present.NoticeProblem {
		t.Fatalf("an expired session: %v, want a problem", got)
	}
	a.disk.clearFailed = true
	if s := h.m.screen(); s.Status != uitext.Inline(uitext.SessionExpiredDiskKept) || s.StatusStyle != present.NoticeProblem {
		t.Fatalf("an expired session, the disk kept: %q in %v", s.Status.String(), s.StatusStyle)
	}
}

// On the screen the notice is drawn in its tone, beside the context and on
// a line of its own, and nothing else of its line is.
func TestAFailedLaunchIsDrawnInItsTone(t *testing.T) {
	const notice = "No MCP server of Claude Code's for https://lalabase.com"
	h := newHarness(t)
	f := h.withLauncher()
	f.kind = launch.NoServer
	h.openSix()
	msg := h.press("x")().(launch.Done)
	msg.Detail = "https://lalabase.com"
	h.feed(msg)
	problem := "\x1b[1;31m" + notice + "\x1b[m"
	seen := map[int]bool{}
	for _, width := range []int{len(notice), 200} {
		h.m.width, h.m.height = width, 24
		lines := strings.Split(h.m.View().Content, "\n")
		at := 1
		if h.m.geometry().NoticeHeight > 0 {
			at = 2
		}
		seen[at] = true
		if !strings.HasSuffix(strings.TrimRight(lines[at], " "), problem) {
			t.Fatalf("width %d: line %d %q does not end in the notice drawn as a problem", width, at, lines[at])
		}
		if rest := strings.TrimSuffix(strings.TrimRight(lines[at], " "), problem); strings.Contains(rest, "\x1b") {
			t.Fatalf("width %d: the context carries a style: %q", width, rest)
		}
		if got := ansi.Strip(lines[at]); !strings.Contains(got, notice) {
			t.Fatalf("width %d: the notice's text %q", width, got)
		}
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("the notice was not drawn both beside the context and on its own line: %v", seen)
	}
}

// A notice wrapped over the lines under the context keeps its tone on
// every one of them.
func TestAWrappedNoticeKeepsItsToneOnEveryLine(t *testing.T) {
	h := newHarness(t)
	f := h.withLauncher()
	f.kind = launch.NoPermission
	h.openSix()
	h.drive(h.press("x"))
	h.m.width, h.m.height = 40, 24
	g := h.m.geometry()
	if g.NoticeHeight < 2 {
		t.Fatalf("the notice takes %d lines at 40 columns, want it wrapped", g.NoticeHeight)
	}
	lines := strings.Split(h.m.View().Content, "\n")
	for i := 2; i < 2+g.NoticeHeight; i++ {
		if !strings.HasPrefix(lines[i], "\x1b[1;31m") {
			t.Fatalf("notice line %d is not drawn as a problem: %q", i-1, lines[i])
		}
	}
}
