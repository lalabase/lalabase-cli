package ui

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

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
			for _, param := range params {
				if n := param.Param(0); n != 0 && n != 1 && n != 22 {
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
	m.content = presentation("INLINE_START"+hostile, "BODY_START"+hostile+"\n\tEND", "OBJECT_START"+hostile)
	for _, field := range []string{m.content.Organisation.String(), m.content.List.String(), m.content.Object.String()} {
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
	if !strings.Contains(ansi.Strip(m.View().Content), "Organisation > List > Object") {
		t.Fatal("missing narrow breadcrumb")
	}
}
