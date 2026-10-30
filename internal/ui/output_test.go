package ui

import (
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// viewSGR are the SGR parameters the view writes itself.
var viewSGR = map[int]bool{0: true, 1: true, 2: true, 7: true, 22: true, 32: true, 33: true, 34: true, 36: true, 39: true, 49: true, 100: true}

func TestShellBodyColumnsStayAligned(t *testing.T) {
	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		// The cells of the frames' sides and corners, the same on every line
		// of the body: each pane's first and last column; with the object
		// in focus three columns become two.
		for _, tc := range []struct {
			width          int
			frames, object []int
		}{
			{80, []int{0, 31, 32, 79}, nil},
			{119, []int{0, 46, 47, 118}, nil},
			{120, []int{0, 25, 26, 62, 63, 119}, []int{0, 36, 37, 119}},
			{121, []int{0, 25, 26, 63, 64, 120}, []int{0, 37, 38, 120}},
		} {
			for _, height := range []int{8, 24} {
				for _, focus := range []view.Pane{view.Navigation, view.List, view.Object} {
					m := NewWithContent("Büro", "Größe 👩\u200d👩\u200d👧\u200d👦\n界 e\u0301", "Änderung 🇩🇪\n👍🏽")
					m.width, m.height, m.focus, m.widthMethod = tc.width, height, focus, method
					lines := strings.Split(m.View().Content, "\n")
					if len(lines) != height {
						t.Fatalf("height %d: %d lines", height, len(lines))
					}
					if !strings.Contains(lines[2], "\x1b[1m") {
						t.Fatal("fixture omitted the styled focus title")
					}
					for row, line := range lines[2 : height-1] {
						if cells := method.StringWidth(line); cells != tc.width {
							t.Fatalf("method %d, width %d, focus %d, row %d: %d cells: %q", method, tc.width, focus, row, cells, line)
						}
						var frames []int
						plain := ansi.Strip(line)
						for offset, r := range plain {
							if strings.ContainsRune("\u2502\u256d\u256e\u2570\u256f", r) {
								frames = append(frames, method.StringWidth(plain[:offset]))
							}
						}
						want := tc.frames
						if tc.object != nil && focus == view.Object {
							want = tc.object
						}
						if !reflect.DeepEqual(frames, want) {
							t.Fatalf("width %d, focus %d, row %d: frames at %v, want %v", tc.width, focus, row, frames, want)
						}
					}
				}
			}
		}
	}
}

const hostile = "\x1b[2J\x1b[31mCSI\x1b[0m\x1b]8;;https://evil.invalid\x07LINK\x1b]8;;\x1b\\\x1b]52;c;YQ==\x1b\\\x1bPpayload\x1b\\\x07\u202e\u2066\u2069\xff\u009b"

func assertViewSafe(t *testing.T, text string) {
	t.Helper()
	if !utf8.ValidString(text) {
		t.Fatal("invalid UTF-8 view")
	}
	p := ansi.NewParser()
	forbidden := func(kind string) { t.Errorf("view emitted %s", kind) }
	p.SetHandler(ansi.Handler{
		Print: func(r rune) {
			if unicode.Is(unicode.Cf, r) && r != '\u200c' && r != '\u200d' && r != '\u00ad' {
				forbidden("format character")
			}
		},
		Execute: func(b byte) {
			if b != '\n' {
				forbidden("control")
			}
		},
		HandleCsi: func(cmd ansi.Cmd, params ansi.Params) {
			if cmd.Final() != 'm' || cmd.Prefix() != 0 || cmd.Intermediate() != 0 {
				forbidden("non-SGR CSI")
				return
			}
			// The view's own attributes: reset, bold, faint, reversed
			// (the cursor bar), the end of bold and faint, the three tones of a status (green, yellow,
			// cyan) and their end, and the focused pane's ground (48;5;234)
			// and its end. Foreign text brings none (it is cleaned).
			for i := 0; i < len(params); i++ {
				if params[i].Param(0) == 48 {
					if i+2 >= len(params) || params[i+1].Param(0) != 5 || params[i+2].Param(0) != 234 {
						forbidden("unexpected background")
					}
					i += 2
					continue
				}
				if !viewSGR[params[i].Param(0)] {
					forbidden("unexpected SGR")
				}
			}
		},
		HandleEsc: func(ansi.Cmd) { forbidden("ESC") },
		HandleOsc: func(int, []byte) { forbidden("OSC") },
		HandleDcs: func(ansi.Cmd, ansi.Params, []byte) { forbidden("DCS") },
		HandleApc: func([]byte) { forbidden("APC") }, HandlePm: func([]byte) { forbidden("PM") }, HandleSos: func([]byte) { forbidden("SOS") },
	})
	p.Parse([]byte(text))
	if p.StateName() != "GroundState" {
		t.Fatalf("unfinished view sequence: %s", p.StateName())
	}
}

func TestHostilePlaceholderViewBytes(t *testing.T) {
	m := New()
	m.width, m.height = 120, 24
	m.shell = [3]string{"INLINE_START" + hostile, "BODY_START" + hostile + "\n\tEND", "OBJECT_START" + hostile}
	s := m.screen()
	for _, field := range []string{s.Context.String(), s.List.Text.String(), s.Object.Text.String()} {
		if strings.ContainsAny(field, "\x1b\x07\u202e\u2066\u2069\u009b") || !utf8.ValidString(field) {
			t.Fatalf("mapping bypassed boundary: %q", field)
		}
	}
	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		m.widthMethod = method
		text := m.View().Content
		assertViewSafe(t, text)
		for _, marker := range []string{"INLINE_START", "BODY_START", "OBJECT_START"} {
			if !strings.Contains(ansi.Strip(text), marker) {
				t.Fatalf("view omitted %s: %q", marker, text)
			}
		}
		for _, width := range []int{0, 1, 23, 24, 79, 80, 119, 120} {
			for _, height := range []int{0, 1, 7, 8, 24} {
				m.width, m.height = width, height
				text = m.View().Content
				assertViewSafe(t, text)
				if width == 0 || height == 0 {
					if text != "" {
						t.Fatal("nonempty zero-sized view")
					}
					continue
				}
				lines := strings.Split(text, "\n")
				if len(lines) > height {
					t.Fatalf("height %d: %d lines", height, len(lines))
				}
				for _, line := range lines {
					if method.StringWidth(line) > width {
						t.Fatalf("width %d overflow: %q", width, line)
					}
				}
			}
		}
		m.width, m.height = 120, 24
	}
	m.width, m.height, m.focus = 79, 24, view.Object
	if !strings.Contains(ansi.Strip(m.View().Content), "Organisation › List › Object") {
		t.Fatal("missing narrow breadcrumb")
	}
}
