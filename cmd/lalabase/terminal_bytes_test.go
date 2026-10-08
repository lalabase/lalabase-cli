//go:build linux || darwin

package main

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const terminalHostile = "\x1b[2J\x1b[31mCSI\x1b[0m\x1b]8;;https://evil.invalid\x07LINK\x1b]8;;\x1b\\\x1b]52;c;YQ==\x1b\\\x1bPpayload\x1b\\\x07\u202e\u2066\u2069\xff\u009b"

func terminalOutputError(output string) error {
	if !utf8.ValidString(output) {
		return fmt.Errorf("invalid UTF-8")
	}
	var failure error
	reject := func(kind string) {
		if failure == nil {
			failure = fmt.Errorf("forbidden terminal output: %s", kind)
		}
	}
	p := ansi.NewParser()
	p.SetHandler(ansi.Handler{
		Print: func(r rune) {
			if unicode.IsControl(r) || (unicode.Is(unicode.Cf, r) && r != '\u200c' && r != '\u200d' && r != '\u00ad') {
				reject("control rune")
			}
		},
		Execute: func(b byte) {
			if b != '\n' && b != '\r' && b != '\b' && b != '\t' {
				reject("C0")
			}
		},
		HandleCsi: func(cmd ansi.Cmd, params ansi.Params) {
			prefix, intermediate, final := cmd.Prefix(), cmd.Intermediate(), cmd.Final()
			values := make([]int, len(params))
			for i, param := range params {
				values[i] = param.Param(0)
				if param.HasMore() {
					reject("CSI subparameter")
					return
				}
			}
			one := func(n int) bool { return len(values) == 1 && values[0] == n }
			if prefix == '?' && intermediate == '$' && final == 'p' && (one(2026) || one(2027)) {
				return
			}
			if intermediate != 0 {
				reject("CSI intermediate")
				return
			}
			if prefix == '?' && (final == 'h' || final == 'l') && len(values) == 1 {
				switch values[0] {
				case 25, 1049, 2004, 2026, 2027:
					return
				}
			}
			if prefix == '?' && final == 'W' && one(5) {
				return
			}
			if prefix == '?' && final == 'u' && len(values) == 0 {
				return
			}
			if (prefix == '>' || prefix == '<') && final == 'u' && one(1) {
				return
			}
			if prefix == '>' && final == 'm' && ((len(values) == 2 && values[0] == 4 && values[1] == 2) || one(4)) {
				return
			}
			if prefix == 0 {
				switch final {
				case 'm':
					for _, n := range values {
						if n != 0 && n != 1 && n != 22 {
							reject("SGR")
						}
					}
					return
				case 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'X', 'd':
					if len(values) <= 1 && (len(values) == 0 || values[0] >= 0 && values[0] <= 4096) {
						return
					}
				case 'H', 'f':
					if len(values) <= 2 {
						valid := true
						for _, n := range values {
							valid = valid && n >= 0 && n <= 4096
						}
						if valid {
							return
						}
					}
				case 'J', 'K':
					if len(values) == 0 || one(0) || one(1) || one(2) {
						return
					}
				}
			}
			reject(fmt.Sprintf("CSI %c/%c/%c %v", prefix, intermediate, final, values))
		},
		HandleEsc: func(ansi.Cmd) { reject("ESC") }, HandleOsc: func(int, []byte) { reject("OSC") }, HandleDcs: func(ansi.Cmd, ansi.Params, []byte) { reject("DCS") },
		HandleApc: func([]byte) { reject("APC") }, HandlePm: func([]byte) { reject("PM") }, HandleSos: func([]byte) { reject("SOS") },
	})
	p.Parse([]byte(output))
	if p.StateName() != "GroundState" {
		reject("unfinished sequence")
	}
	return failure
}

func TestTerminalOutputAllowlist(t *testing.T) {
	for _, bad := range []string{terminalHostile, "\x1b[31m", "\x1b[8t", "\x1b]8;;https://evil.invalid\x07", "\x1b]52;c;YQ==\x1b\\", "\x1bPdata\x1b\\", "\x07", "\u202e", "\xff", "\x1b["} {
		if err := terminalOutputError(bad); err == nil {
			t.Errorf("accepted hostile stream %q", bad)
		}
	}
	if err := terminalOutputError("safe\r\n\x1b[1mBold\x1b[m\x1b[?1049h\x1b[?1049l"); err != nil {
		t.Fatal(err)
	}
}

func TestPTYHostilePlaceholderBytes(t *testing.T) {
	c := startTerminal(t, "hostile", 120)
	if err := c.ready(); err != nil {
		t.Fatal(err)
	}
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	output := c.bytes()
	for _, marker := range []string{"SAFE_INLINE", "SAFE_LIST", "SAFE_OBJECT"} {
		if !strings.Contains(output, marker) {
			t.Fatalf("fixture omitted %s", marker)
		}
	}
	if err := terminalOutputError(output); err != nil {
		t.Fatalf("%v\n%q", err, output)
	}
}

func TestTerminalOutputAllowsRendererGrammar(t *testing.T) {
	allowed := []string{"plain 界 e\u0301", "\r\n\b\t", "\u200c\u200d\u00ad"}
	for _, params := range []string{"", "0", "1", "22", "0;1;22"} {
		allowed = append(allowed, "\x1b["+params+"m")
	}
	for _, mode := range []int{25, 1049, 2004, 2026, 2027} {
		for _, final := range []byte{'h', 'l'} {
			allowed = append(allowed, fmt.Sprintf("\x1b[?%d%c", mode, final))
		}
	}
	allowed = append(allowed, "\x1b[?2026$p", "\x1b[?2027$p", "\x1b[?5W", "\x1b[?u", "\x1b[>1u", "\x1b[<1u", "\x1b[>4;2m", "\x1b[>4m")
	for _, final := range []byte{'A', 'B', 'C', 'D', 'E', 'F', 'G', 'X', 'd'} {
		for _, params := range []string{"", "0", "1", "4095", "4096"} {
			allowed = append(allowed, "\x1b["+params+string(final))
		}
	}
	for _, final := range []byte{'H', 'f'} {
		for _, params := range []string{"", "0", "1", "0;0", "1;1", "4095;4096", "4096;4095"} {
			allowed = append(allowed, "\x1b["+params+string(final))
		}
	}
	for _, final := range []byte{'J', 'K'} {
		for _, params := range []string{"", "0", "1", "2"} {
			allowed = append(allowed, "\x1b["+params+string(final))
		}
	}
	for _, sequence := range allowed {
		if err := terminalOutputError(sequence); err != nil {
			t.Errorf("renderer sequence %q rejected: %v", sequence, err)
		}
	}
}

func TestTerminalOutputRejectsControlFamilies(t *testing.T) {
	// Keep cases separate so an early UTF-8 error cannot mask a missing handler.
	forbidden := []string{
		"\x1b_payload\x1b\\", "\x1b^payload\x1b\\", "\x1bXpayload\x1b\\",
		// A following valid CSI must not hide a string command's rejection.
		"\x1b_payload\x1b[0m", "\x1b^payload\x1b[0m", "\x1bXpayload\x1b[0m",
		"\x1bPdata\x1b\\", "\x1b]52;c;YQ==\x1b\\", "\x1bc",
		"\x00", "\x07", "\u009b", "\u202e", "\u2066", "\u2069", "\xff",
		"\x1b[1:2m", "\x1b[1 m", "\x1b[?25$p", "\x1b[?2026;2027$p",
		"\x1b[?1048h", "\x1b[?25;1049h", "\x1b[?4W", "\x1b[?1u",
		"\x1b[>2u", "\x1b[<2u", "\x1b[>4;3m", "\x1b[2m", "\x1b[31m",
		"\x1b[", "\x1b]52;c;YQ==", "\x1bPdata", "\x1b_payload", "\x1b^payload", "\x1bXpayload",
	}
	for _, final := range []byte{'A', 'B', 'C', 'D', 'E', 'F', 'G', 'X', 'd'} {
		for _, params := range []string{"4097", "1;2"} {
			forbidden = append(forbidden, "\x1b["+params+string(final))
		}
	}
	for _, final := range []byte{'H', 'f'} {
		for _, params := range []string{"4097;1", "1;4097", "1;2;3"} {
			forbidden = append(forbidden, "\x1b["+params+string(final))
		}
	}
	for _, final := range []byte{'J', 'K'} {
		for _, params := range []string{"3", "1;2"} {
			forbidden = append(forbidden, "\x1b["+params+string(final))
		}
	}
	for _, sequence := range forbidden {
		if err := terminalOutputError(sequence); err == nil {
			t.Errorf("forbidden sequence %q accepted", sequence)
		}
	}
}
